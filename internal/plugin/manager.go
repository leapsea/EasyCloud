package plugin

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/pkg/pluginsdk"
	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
)

var (
	ErrNotFound  = errors.New("plugin not found")
	ErrInvalid   = errors.New("invalid plugin")
	ErrUntrusted = errors.New("untrusted plugin")
)

// Installed is a plugin as stored plus its runtime status.
type Installed struct {
	Manifest    *Manifest      `json:"manifest"`
	Enabled     bool           `json:"enabled"`
	Config      map[string]any `json:"config"`
	SignedBy    string         `json:"signedBy"`
	InstalledAt time.Time      `json:"installedAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	Status      string         `json:"status"` // running | stopped | error | ready | unconfigured（启用但缺少 manifest 声明的必填配置）
	Error       string         `json:"error,omitempty"`
}

type Options struct {
	Dir           string // app_data/plugins
	HostVersion   string
	AllowUnsigned bool
	TrustedKeys   []ed25519.PublicKey
	Repository    string
}

// ParseKeys decodes comma-separated base64 ed25519 public keys.
func ParseKeys(s string) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	for _, k := range strings.Split(s, ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid trusted key %q", k)
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out, nil
}

type Manager struct {
	db   *database.DB
	opts Options
	log  *slog.Logger

	mu       sync.RWMutex
	plugins  map[string]*Installed
	procs    map[string]*proc
	ctx      context.Context
	accounts AccountSink
}

type proc struct {
	client *goplugin.Client
	ext    pluginsdk.Extension
	stop   chan struct{}
}

func NewManager(db *database.DB, opts Options, log *slog.Logger) *Manager {
	return &Manager{db: db, opts: opts, log: log, plugins: map[string]*Installed{}, procs: map[string]*proc{}, ctx: context.Background()}
}

func (m *Manager) versionDir(id, version string) string {
	return filepath.Join(m.opts.Dir, id, version)
}

// Load reads installed plugins from the database.
func (m *Manager) Load(ctx context.Context) error {
	rows, err := m.db.R.QueryContext(ctx, "SELECT version, enabled, config, manifest, signed_by, installed_at, updated_at FROM easycloud_plugins")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ver, cfg, man, signed string
		var enabled bool
		var inst, upd int64
		if err := rows.Scan(&ver, &enabled, &cfg, &man, &signed, &inst, &upd); err != nil {
			return err
		}
		mf, err := ParseManifest([]byte(man))
		if err != nil {
			m.log.Error("stored plugin manifest unreadable", "err", err)
			continue
		}
		p := &Installed{Manifest: mf, Enabled: enabled, SignedBy: signed, InstalledAt: time.Unix(inst, 0).UTC(), UpdatedAt: time.Unix(upd, 0).UTC(), Status: "stopped"}
		_ = json.Unmarshal([]byte(cfg), &p.Config)
		if p.Config == nil {
			p.Config = map[string]any{}
		}
		if err := mf.Validate(m.opts.HostVersion); err != nil {
			p.Status, p.Error = "error", err.Error()
		}
		m.plugins[mf.ID] = p
	}
	return rows.Err()
}

// Run starts enabled backend plugins and supervises them until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.reconcile(id)
	}
	<-ctx.Done()
	m.mu.Lock()
	for id := range m.procs {
		m.stopLocked(id)
	}
	m.mu.Unlock()
}

// reconcile brings one plugin's runtime in line with its stored state.
func (m *Manager) reconcile(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	m.stopLocked(id)
	if !ok {
		return
	}
	if !p.Enabled {
		p.Status, p.Error = "stopped", ""
		return
	}
	if p.Status == "error" && p.Error != "" && strings.HasPrefix(p.Error, "requires easycloud") {
		return
	}
	// 必填配置缺失时不视为可用：不启动后端进程，状态标为待配置，由管理员在插件页点「配置」补全。
	if missing := missingRequired(p.Manifest, p.Config); len(missing) > 0 {
		p.Status, p.Error = "unconfigured", ""
		return
	}
	if p.Manifest.Type != KindBackend {
		p.Status, p.Error = "ready", ""
		return
	}
	if err := m.startLocked(p); err != nil {
		p.Status, p.Error = "error", err.Error()
		m.log.Error("plugin failed to start", "id", id, "err", err)
		return
	}
	p.Status, p.Error = "running", ""
	if p.Manifest.Backend.Accounts {
		go m.SyncAccounts(m.ctx)
	}
}

func (m *Manager) stopLocked(id string) {
	if pr, ok := m.procs[id]; ok {
		close(pr.stop)
		pr.client.Kill()
		delete(m.procs, id)
	}
}

// pluginEnv passes only harmless variables: plugins never see database passwords.
func pluginEnv(cfg map[string]any) []string {
	env := []string{}
	for _, k := range []string{"PATH", "HOME", "LANG", "TZ", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	b, _ := json.Marshal(cfg)
	return append(env, pluginsdk.ConfigEnv+"="+string(b))
}

func (m *Manager) startLocked(p *Installed) error {
	rel, ok := p.Manifest.Executable()
	if !ok {
		return fmt.Errorf("no executable for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := m.versionDir(p.Manifest.ID, p.Manifest.Version)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	fi, err := root.Lstat(rel) // must be a regular file inside the plugin directory
	root.Close()
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("executable %s missing", rel)
	}
	cmd := exec.Command(filepath.Join(dir, filepath.FromSlash(rel)))
	cmd.Dir = dir
	cmd.Env = pluginEnv(withDefaults(p.Manifest, p.Config))
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  pluginsdk.Handshake,
		Plugins:          pluginsdk.PluginMap(nil),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           hclog.New(&hclog.LoggerOptions{Name: "plugin." + p.Manifest.ID, Output: slogWriter{m.log, p.Manifest.ID}, Level: hclog.Info, JSONFormat: true}),
		StartTimeout:     15 * time.Second,
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return err
	}
	raw, err := rpc.Dispense("extension")
	if err != nil {
		client.Kill()
		return err
	}
	pr := &proc{client: client, ext: raw.(pluginsdk.Extension), stop: make(chan struct{})}
	m.procs[p.Manifest.ID] = pr
	go m.supervise(p.Manifest.ID, pr)
	return nil
}

// supervise restarts a crashed plugin with backoff.
func (m *Manager) supervise(id string, pr *proc) {
	delay := time.Second
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-pr.stop:
			return
		case <-m.ctx.Done():
			return
		case <-t.C:
			if !pr.client.Exited() {
				continue
			}
			m.log.Warn("plugin exited; restarting", "id", id, "in", delay.String())
			select {
			case <-time.After(delay):
			case <-pr.stop:
				return
			}
			delay = min(delay*2, time.Minute)
			m.mu.Lock()
			if m.procs[id] != pr {
				m.mu.Unlock()
				return
			}
			delete(m.procs, id)
			p := m.plugins[id]
			if err := m.startLocked(p); err != nil {
				p.Status, p.Error = "error", err.Error()
				m.mu.Unlock()
				continue
			}
			p.Status, p.Error = "running", ""
			m.mu.Unlock()
			if p.Manifest.Backend.Accounts {
				go m.SyncAccounts(m.ctx)
			}
			return // the new supervisor takes over
		}
	}
}

type slogWriter struct {
	log *slog.Logger
	id  string
}

func (w slogWriter) Write(b []byte) (int, error) {
	w.log.Info("plugin log", "plugin", w.id, "line", strings.TrimSpace(string(b)))
	return len(b), nil
}

// missingRequired 按 manifest 声明的 required 字段返回缺失项的 key（默认值视为已填），
// 是“插件是否配置完整”的唯一判定来源，前端只读取由此得出的 status。
func missingRequired(m *Manifest, cfg map[string]any) []string {
	eff := withDefaults(m, cfg)
	var out []string
	for _, f := range m.Config {
		if f.Required && (eff[f.Key] == nil || eff[f.Key] == "") {
			out = append(out, f.Key)
		}
	}
	return out
}

func withDefaults(m *Manifest, cfg map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range m.Config {
		if f.Default != nil {
			out[f.Key] = f.Default
		}
	}
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

// ---------- queries ----------

// List returns installed plugins sorted by id; secret config values are masked.
func (m *Manager) List() []Installed {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Installed, 0, len(m.plugins))
	for _, p := range m.plugins {
		c := *p
		c.Config = masked(p.Manifest, p.Config)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

func masked(m *Manifest, cfg map[string]any) map[string]any {
	out := map[string]any{}
	secret := map[string]bool{}
	for _, f := range m.Config {
		secret[f.Key] = f.Type == "secret"
	}
	for k, v := range cfg {
		if secret[k] && v != "" && v != nil {
			out[k] = "********"
		} else {
			out[k] = v
		}
	}
	return out
}

// Viewer describes an enabled viewer plugin for the browser.
type Viewer struct {
	ID         string   `json:"id"`
	Version    string   `json:"version"`
	Label      string   `json:"label"`
	Entry      string   `json:"entry"` // URL of the ES module
	Mimes      []string `json:"mimes"`
	Extensions []string `json:"extensions"`
	Priority   int      `json:"priority"`
}

func (m *Manager) Viewers() []Viewer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []Viewer{}
	for _, p := range m.plugins {
		if !p.Enabled || p.Manifest.Type != KindViewer || p.Status == "error" || p.Status == "unconfigured" {
			continue
		}
		v := p.Manifest.Viewer
		label := v.Label
		if label == "" {
			label = p.Manifest.Name
		}
		out = append(out, Viewer{ID: p.Manifest.ID, Version: p.Manifest.Version, Label: label,
			Entry: "/plugins/" + p.Manifest.ID + "/" + p.Manifest.Version + "/" + v.Entry, Mimes: v.Mimes, Extensions: v.Extensions, Priority: v.Priority})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}

// ThumbnailProvider returns a running backend plugin that handles mime.
func (m *Manager) ThumbnailProvider(mime string) (pluginsdk.Extension, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := m.plugins[id]
		for _, pat := range p.Manifest.Backend.Thumbnail {
			if MatchMime(pat, mime) {
				return m.procs[id].ext, id, true
			}
		}
	}
	return nil, "", false
}

// Connector returns the configuration of an enabled connector of the given kind.
// 缺少必填配置的 connector 视为不可用（如未填 Document Server 地址的 OnlyOffice），依赖它的入口随之隐藏。
func (m *Manager) Connector(kind string) (map[string]any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.plugins {
		if p.Enabled && p.Manifest.Type == KindConnector && p.Manifest.Connector.Kind == kind && len(missingRequired(p.Manifest, p.Config)) == 0 {
			return withDefaults(p.Manifest, p.Config), true
		}
	}
	return nil, false
}

// Broadcast forwards a file event to backend plugins that asked for events.
func (m *Manager) Broadcast(ev pluginsdk.FileEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, pr := range m.procs {
		if !m.plugins[id].Manifest.Backend.Events {
			continue
		}
		go func(ext pluginsdk.Extension) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = ext.OnEvent(ctx, &ev)
		}(pr.ext)
	}
}

// AccountSink receives account/share snapshots for protocol gateways.
type AccountSink func(ctx context.Context) (*pluginsdk.SyncRequest, error)

// SetAccountSource registers how to build Sync snapshots; plugins with
// "accounts": true get one on start and whenever SyncAccounts is called.
func (m *Manager) SetAccountSource(src AccountSink) {
	m.mu.Lock()
	m.accounts = src
	m.mu.Unlock()
}

// SyncAccounts pushes the current snapshot to every running account plugin.
func (m *Manager) SyncAccounts(ctx context.Context) {
	m.mu.RLock()
	src := m.accounts
	var targets []string
	for id := range m.procs {
		if b := m.plugins[id].Manifest.Backend; b != nil && b.Accounts {
			targets = append(targets, id)
		}
	}
	m.mu.RUnlock()
	if src == nil || len(targets) == 0 {
		return
	}
	req, err := src(ctx)
	if err != nil {
		m.log.Error("build account snapshot", "err", err)
		return
	}
	for _, id := range targets {
		m.mu.RLock()
		pr, ok := m.procs[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := pr.ext.Sync(cctx, req); err != nil {
			m.log.Error("plugin account sync failed", "plugin", id, "err", err)
			m.mu.Lock()
			if p := m.plugins[id]; p != nil {
				p.Error = "同步失败：" + err.Error()
			}
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			if p := m.plugins[id]; p != nil && strings.HasPrefix(p.Error, "同步失败") {
				p.Error = ""
			}
			m.mu.Unlock()
		}
		cancel()
	}
}

// StaticDir returns the directory serving /plugins/<id>/<version>/ (viewer assets).
func (m *Manager) StaticDir(id, version string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.plugins[id]
	if !ok || !p.Enabled || p.Manifest.Version != version {
		return "", false
	}
	return m.versionDir(id, version), true
}

// ---------- mutations ----------

// Install verifies and installs (or upgrades) a package.
func (m *Manager) Install(ctx context.Context, data []byte) (*Installed, error) {
	pkg, err := Open(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	mf := pkg.Manifest
	if err := mf.Validate(m.opts.HostVersion); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	signedBy, verr := pkg.Verify(m.opts.TrustedKeys)
	if verr != nil {
		if !m.opts.AllowUnsigned {
			return nil, fmt.Errorf("%w: %v (set EASYCLOUD_PLUGINS_ALLOW_UNSIGNED=true to allow)", ErrUntrusted, verr)
		}
		signedBy = ""
		m.log.Warn("installing unverified plugin because unsigned plugins are allowed", "id", mf.ID, "reason", verr)
	}
	final := m.versionDir(mf.ID, mf.Version)
	tmp := final + ".tmp-" + fmt.Sprint(time.Now().UnixNano())
	if err := pkg.Extract(tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, fmt.Errorf("extract: %w", err)
	}
	if mf.Type == KindBackend {
		if rel, ok := mf.Executable(); !ok {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("%w: no executable for %s/%s", ErrInvalid, runtime.GOOS, runtime.GOARCH)
		} else if fi, err := os.Lstat(filepath.Join(tmp, filepath.FromSlash(rel))); err != nil || !fi.Mode().IsRegular() {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("%w: executable %s missing from package", ErrInvalid, rel)
		} else {
			_ = os.Chmod(filepath.Join(tmp, filepath.FromSlash(rel)), 0o755)
		}
	}
	_ = os.RemoveAll(final) // reinstall of the same version
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}

	m.mu.Lock()
	prev := m.plugins[mf.ID]
	cfg := map[string]any{}
	enabled := true
	installedAt := time.Now()
	if prev != nil {
		cfg, enabled, installedAt = prev.Config, prev.Enabled, prev.InstalledAt
	}
	m.mu.Unlock()

	manJSON, _ := json.Marshal(mf)
	cfgJSON, _ := json.Marshal(cfg)
	q := m.db.Upsert("easycloud_plugins", []string{"id", "version", "enabled", "config", "manifest", "signed_by", "installed_at", "updated_at"}, []string{"id"})
	if _, err := m.db.W.ExecContext(ctx, q, mf.ID, mf.Version, enabled, string(cfgJSON), string(manJSON), signedBy, installedAt.Unix(), time.Now().Unix()); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.plugins[mf.ID] = &Installed{Manifest: mf, Enabled: enabled, Config: cfg, SignedBy: signedBy, InstalledAt: installedAt.UTC(), UpdatedAt: time.Now().UTC(), Status: "stopped"}
	m.mu.Unlock()
	m.reconcile(mf.ID) // hot-restarts a running backend on the new version
	if prev != nil && prev.Manifest.Version != mf.Version {
		_ = os.RemoveAll(m.versionDir(mf.ID, prev.Manifest.Version))
	}
	m.mu.RLock()
	out := *m.plugins[mf.ID]
	m.mu.RUnlock()
	return &out, nil
}

// Update changes enabled state and/or configuration.
func (m *Manager) Update(ctx context.Context, id string, enabled *bool, cfg map[string]any) error {
	m.mu.Lock()
	p, ok := m.plugins[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	newCfg := map[string]any{}
	for k, v := range p.Config {
		newCfg[k] = v
	}
	if cfg != nil {
		fields := map[string]ConfigField{}
		for _, f := range p.Manifest.Config {
			fields[f.Key] = f
		}
		for k, v := range cfg {
			f, known := fields[k]
			if !known {
				m.mu.Unlock()
				return fmt.Errorf("%w: unknown config key %q", ErrInvalid, k)
			}
			if f.Type == "secret" && v == "********" {
				continue // unchanged masked secret
			}
			newCfg[k] = v
		}
		for _, f := range p.Manifest.Config {
			if f.Required && (newCfg[f.Key] == nil || newCfg[f.Key] == "") {
				m.mu.Unlock()
				return fmt.Errorf("%w: %s is required", ErrInvalid, f.Label)
			}
		}
	}
	en := p.Enabled
	if enabled != nil {
		en = *enabled
	}
	m.mu.Unlock()
	cfgJSON, _ := json.Marshal(newCfg)
	if _, err := m.db.W.ExecContext(ctx, "UPDATE easycloud_plugins SET enabled = ?, config = ?, updated_at = ? WHERE id = ?", en, string(cfgJSON), time.Now().Unix(), id); err != nil {
		return err
	}
	m.mu.Lock()
	p.Enabled, p.Config, p.UpdatedAt = en, newCfg, time.Now().UTC()
	m.mu.Unlock()
	m.reconcile(id)
	return nil
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	p, ok := m.plugins[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	m.stopLocked(id)
	delete(m.plugins, id)
	m.mu.Unlock()
	if _, err := m.db.W.ExecContext(ctx, "DELETE FROM easycloud_plugins WHERE id = ?", id); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(m.opts.Dir, p.Manifest.ID))
	return nil
}

// ---------- repository ----------

type RepoVersion struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Engine  string `json:"engine"`
}

type RepoPlugin struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Description string        `json:"description"`
	Versions    []RepoVersion `json:"versions"`
	// Filled in by the host:
	Latest    string `json:"latest,omitempty"`
	Installed string `json:"installed,omitempty"`
	Update    bool   `json:"update,omitempty"`
}

func (m *Manager) Repository() string { return m.opts.Repository }

// Fetcher downloads URLs; replaced in tests.
type Fetcher func(ctx context.Context, url string, limit int64) ([]byte, error)

// Available lists repository plugins compatible with this host.
func (m *Manager) Available(ctx context.Context, fetch Fetcher) ([]RepoPlugin, error) {
	if m.opts.Repository == "" {
		return nil, errors.New("no plugin repository configured")
	}
	b, err := fetch(ctx, m.opts.Repository, 8<<20)
	if err != nil {
		return nil, fmt.Errorf("fetch repository index: %w", err)
	}
	var idx struct {
		Plugins []RepoPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("repository index: %w", err)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range idx.Plugins {
		rp := &idx.Plugins[i]
		for _, v := range rp.Versions {
			if ok, _ := Satisfies(m.opts.HostVersion, v.Engine); ok && (rp.Latest == "" || semverLess(rp.Latest, v.Version)) {
				rp.Latest = v.Version
			}
		}
		if p, ok := m.plugins[rp.ID]; ok {
			rp.Installed = p.Manifest.Version
			rp.Update = rp.Latest != "" && semverLess(p.Manifest.Version, rp.Latest)
		}
	}
	return idx.Plugins, nil
}

// InstallFromRepo downloads, checks the hash, then installs like an upload.
func (m *Manager) InstallFromRepo(ctx context.Context, fetch Fetcher, id, version string) (*Installed, error) {
	avail, err := m.Available(ctx, fetch)
	if err != nil {
		return nil, err
	}
	for _, rp := range avail {
		if rp.ID != id {
			continue
		}
		if version == "" {
			version = rp.Latest
		}
		for _, v := range rp.Versions {
			if v.Version != version {
				continue
			}
			data, err := fetch(ctx, v.URL, maxPackageSize)
			if err != nil {
				return nil, fmt.Errorf("download: %w", err)
			}
			if sum := sha256Hex(data); !strings.EqualFold(sum, v.SHA256) {
				return nil, fmt.Errorf("%w: sha256 mismatch (got %s)", ErrInvalid, sum)
			}
			return m.Install(ctx, data)
		}
	}
	return nil, ErrNotFound
}

func semverLess(a, b string) bool { return compareSemver(a, b) < 0 }
