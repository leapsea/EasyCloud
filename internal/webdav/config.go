package webdav

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"golang.org/x/crypto/bcrypt"
)

// A Config is one WebDAV endpoint (/dav/<slug>/) with its own account. Its
// root lists the configured directories; nothing outside them is reachable.
type Config struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Username  string    `json:"username"`
	Enabled   bool      `json:"enabled"`
	Dirs      []Dir     `json:"dirs"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	hash      string
}

// Dir is one entry of a config. Without Folder it is a shared directory (or a
// single file, read-only) at the root, under Name (the alias, or its own
// name). With Folder it
// is a file or directory gathered into the virtual folder of that name: the
// virtual folder appears at the root and lists its entries under their names;
// it is always read-only, since a write there could not tell which real
// folder is meant.
type Dir struct {
	Path    string `json:"path"`
	Alias   string `json:"alias"`
	Perm    string `json:"perm"` // "r" | "rw"; always "r" inside a virtual folder
	Folder  string `json:"folder,omitempty"`
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Missing bool   `json:"missing,omitempty"`
}

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	userRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
)

const (
	maxDirs    = 50  // entries at the root (directories and virtual folders)
	maxEntries = 500 // all rows, including the entries of virtual folders
)

func invalid(detail string) error { return errs.ErrInvalidInput.WithDetail(detail) }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return sb.String()
}

type Service struct {
	db       *database.DB
	dataRoot *os.Root

	mu       sync.Mutex
	authOK   map[string]authEntry // credential hash -> config id (bcrypt is slow; clients auth every request)
	failures map[string][]time.Time
}

type authEntry struct {
	id string
	at time.Time
}

func NewService(db *database.DB, dataRoot *os.Root) *Service {
	return &Service{db: db, dataRoot: dataRoot, authOK: map[string]authEntry{}, failures: map[string][]time.Time{}}
}

func (s *Service) forget() {
	s.mu.Lock()
	s.authOK = map[string]authEntry{}
	s.mu.Unlock()
}

// ---------- persistence ----------

const cfgCols = "id, name, slug, username, password_hash, enabled, created_by, created_at, updated_at"

func scanCfg(r interface{ Scan(...any) error }) (Config, error) {
	var c Config
	var created, updated int64
	err := r.Scan(&c.ID, &c.Name, &c.Slug, &c.Username, &c.hash, &c.Enabled, &c.CreatedBy, &created, &updated)
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return c, err
}

func (s *Service) loadDirs(ctx context.Context, c *Config) error {
	rows, err := s.db.R.QueryContext(ctx, "SELECT path, alias, perm, folder FROM easycloud_dav_dirs WHERE config_id = ? ORDER BY position", c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	c.Dirs = []Dir{}
	for rows.Next() {
		var d Dir
		var perm int
		if err := rows.Scan(&d.Path, &d.Alias, &perm, &d.Folder); err != nil {
			return err
		}
		d.Perm = pathguard.Perm(perm).String()
		d.Name = effectiveName(d)
		if p, err := pathguard.Parse(d.Path); err != nil {
			d.Missing = true
		} else if fi, err := s.dataRoot.Stat(p.String()); err != nil {
			d.Missing = true
		} else {
			d.IsDir = fi.IsDir()
		}
		c.Dirs = append(c.Dirs, d)
	}
	return rows.Err()
}

// validName: a single, addressable path segment (aliases, virtual folder names).
func validName(n string) bool {
	if n == "" || strings.Contains(n, "/") || len([]rune(n)) > 255 {
		return false
	}
	p, err := pathguard.Parse(n)
	return err == nil && len(p.Segments()) == 1
}

func effectiveName(d Dir) string {
	if d.Alias != "" {
		return d.Alias
	}
	p, err := pathguard.Parse(d.Path)
	if err != nil || p.IsRoot() {
		return "data"
	}
	return p.Name()
}

func (s *Service) get(ctx context.Context, col, v string) (Config, error) {
	c, err := scanCfg(s.db.R.QueryRowContext(ctx, "SELECT "+cfgCols+" FROM easycloud_dav_configs WHERE "+col+" = ?", v))
	if errors.Is(err, sql.ErrNoRows) {
		return c, errs.ErrNotFound
	} else if err != nil {
		return c, err
	}
	return c, s.loadDirs(ctx, &c)
}

func (s *Service) Get(ctx context.Context, id string) (Config, error) { return s.get(ctx, "id", id) }

func (s *Service) List(ctx context.Context) ([]Config, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT "+cfgCols+" FROM easycloud_dav_configs ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []Config
	for rows.Next() {
		c, err := scanCfg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	for i := range out {
		if err := s.loadDirs(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Config{}
	}
	return out, nil
}

// Input is the editable part of a config. Password is optional on create
// (a random one is generated) and ignored on update (use ResetPassword).
type Input struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled"`
	Dirs     []Dir  `json:"dirs"`
}

// normalize validates input and returns the directories in canonical form.
func (s *Service) normalize(in *Input) ([]Dir, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(strings.ToLower(in.Slug))
	in.Username = strings.TrimSpace(in.Username)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return nil, invalid("name must be 1-64 characters")
	}
	if !slugRe.MatchString(in.Slug) {
		return nil, invalid("slug must match [a-z0-9][a-z0-9-]{0,31}")
	}
	if !userRe.MatchString(in.Username) {
		return nil, invalid("username must match [A-Za-z0-9][A-Za-z0-9_.-]{0,31}")
	}
	if len(in.Dirs) == 0 || len(in.Dirs) > maxEntries {
		return nil, invalid(fmt.Sprintf("1-%d entries are required", maxEntries))
	}
	seenPath := map[string]bool{}
	rootNames := map[string]string{}              // lower-case name at the root -> what it is
	folderNames := map[string]map[string]string{} // virtual folder -> lower-case entry name -> path
	out := make([]Dir, 0, len(in.Dirs))
	for _, d := range in.Dirs {
		folder := strings.TrimSpace(d.Folder)
		if folder != "" && !validName(folder) {
			return nil, invalid("virtual folder name " + folder + " is not a valid folder name")
		}
		p, err := pathguard.Parse(d.Path)
		if err != nil || (folder != "" && p.IsRoot()) {
			return nil, invalid("invalid path " + d.Path)
		}
		fi, err := s.dataRoot.Stat(p.String())
		if err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
			return nil, invalid(p.Display() + " does not exist")
		}
		key := folder + "\x00" + p.Display()
		if seenPath[key] {
			return nil, invalid(p.Display() + " is listed twice")
		}
		seenPath[key] = true
		alias := strings.TrimSpace(d.Alias)
		if alias != "" && !validName(alias) {
			return nil, invalid("alias " + alias + " is not a valid name")
		}
		perm := d.Perm
		if folder != "" || !fi.IsDir() {
			perm = "r" // virtual folders and single files are read-only
		} else if perm != "r" && perm != "rw" {
			return nil, invalid("perm must be r or rw")
		}
		nd := Dir{Path: p.Display(), Alias: alias, Perm: perm, Folder: folder, IsDir: fi.IsDir()}
		nd.Name = effectiveName(nd)
		if folder == "" {
			k := strings.ToLower(nd.Name)
			if other, dup := rootNames[k]; dup {
				return nil, invalid(fmt.Sprintf("%s and %s would both appear as \"%s\": set an alias", other, nd.Path, nd.Name))
			}
			rootNames[k] = nd.Path
		} else {
			if folderNames[folder] == nil {
				k := strings.ToLower(folder)
				if other, dup := rootNames[k]; dup {
					return nil, invalid(fmt.Sprintf("virtual folder %q and %s would both appear as %q", folder, other, folder))
				}
				rootNames[k] = "virtual folder " + folder
				folderNames[folder] = map[string]string{}
			}
			k := strings.ToLower(nd.Name)
			if other, dup := folderNames[folder][k]; dup {
				return nil, invalid(fmt.Sprintf("%s and %s would both appear as \"%s\" in %s: set an alias", other, nd.Path, nd.Name, folder))
			}
			folderNames[folder][k] = nd.Path
		}
		out = append(out, nd)
	}
	if len(rootNames) > maxDirs {
		return nil, invalid(fmt.Sprintf("at most %d entries at the root", maxDirs))
	}
	return out, nil
}

func isDup(err error) bool {
	m := strings.ToLower(fmt.Sprint(err))
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate")
}

func (s *Service) writeDirs(ctx context.Context, tx *sql.Tx, id string, dirs []Dir) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_dav_dirs WHERE config_id = ?", id); err != nil {
		return err
	}
	for i, d := range dirs {
		perm := pathguard.PermRead
		if d.Perm == "rw" {
			perm = pathguard.PermRW
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_dav_dirs (config_id, position, path, alias, perm, folder) VALUES (?,?,?,?,?,?)",
			id, i, d.Path, d.Alias, int(perm), d.Folder); err != nil {
			return err
		}
	}
	return nil
}

// Create adds a config and returns it with its plaintext password (shown once).
func (s *Service) Create(ctx context.Context, by string, in Input) (Config, string, error) {
	dirs, err := s.normalize(&in)
	if err != nil {
		return Config{}, "", err
	}
	pw := in.Password
	if pw == "" {
		pw = randomPassword()
	} else if len(pw) < 8 || len(pw) > 128 {
		return Config{}, "", invalid("password must be 8-128 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return Config{}, "", err
	}
	enabled := in.Enabled == nil || *in.Enabled
	id, now := newID(), time.Now().Unix()
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_dav_configs ("+cfgCols+") VALUES (?,?,?,?,?,?,?,?,?)",
		id, in.Name, in.Slug, in.Username, string(hash), enabled, by, now, now); err != nil {
		if isDup(err) {
			return Config{}, "", errs.ErrConflict.WithDetail("slug or username already in use")
		}
		return Config{}, "", err
	}
	if err := s.writeDirs(ctx, tx, id, dirs); err != nil {
		return Config{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Config{}, "", err
	}
	c, err := s.Get(ctx, id)
	return c, pw, err
}

func (s *Service) Update(ctx context.Context, id string, in Input) (Config, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Config{}, err
	}
	dirs, err := s.normalize(&in)
	if err != nil {
		return Config{}, err
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE easycloud_dav_configs SET name = ?, slug = ?, username = ?, enabled = ?, updated_at = ? WHERE id = ?",
		in.Name, in.Slug, in.Username, enabled, time.Now().Unix(), id); err != nil {
		if isDup(err) {
			return Config{}, errs.ErrConflict.WithDetail("slug or username already in use")
		}
		return Config{}, err
	}
	if err := s.writeDirs(ctx, tx, id, dirs); err != nil {
		return Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return Config{}, err
	}
	s.forget()
	return s.Get(ctx, id)
}

// SetEnabled toggles a config without touching anything else.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) (Config, error) {
	res, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_dav_configs SET enabled = ?, updated_at = ? WHERE id = ?", enabled, time.Now().Unix(), id)
	if err != nil {
		return Config{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Config{}, errs.ErrNotFound
	}
	s.forget()
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM easycloud_dav_configs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_dav_dirs WHERE config_id = ?", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.forget()
	return nil
}

// ResetPassword sets a new random password and returns it (shown once).
func (s *Service) ResetPassword(ctx context.Context, id string) (string, error) {
	pw := randomPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	res, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_dav_configs SET password_hash = ?, updated_at = ? WHERE id = ?", string(hash), time.Now().Unix(), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errs.ErrNotFound
	}
	s.forget()
	return pw, nil
}

// PathMoved keeps shared directories pointing at renamed or moved folders.
func (s *Service) PathMoved(ctx context.Context, from, to pathguard.RelPath) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT config_id, position, path FROM easycloud_dav_dirs")
	if err != nil {
		return
	}
	type upd struct {
		id   string
		pos  int
		path string
	}
	var ups []upd
	for rows.Next() {
		var u upd
		if rows.Scan(&u.id, &u.pos, &u.path) != nil {
			continue
		}
		p, err := pathguard.Parse(u.path)
		if err != nil || !p.HasPrefix(from) {
			continue
		}
		np := to
		for _, seg := range p.Segments()[len(from.Segments()):] {
			np, _ = np.Join(seg)
		}
		u.path = np.Display()
		ups = append(ups, u)
	}
	rows.Close()
	for _, u := range ups {
		_, _ = s.db.W.ExecContext(ctx, "UPDATE easycloud_dav_dirs SET path = ? WHERE config_id = ? AND position = ?", u.path, u.id, u.pos)
	}
}

// ---------- authentication ----------

var (
	errBadCredentials = errors.New("bad credentials")
	errRateLimited    = errors.New("too many attempts")
)

// Authenticate checks the account of the config addressed by slug. Disabled
// configs and wrong credentials look the same to the client.
func (s *Service) Authenticate(ctx context.Context, slug, ip, user, pass string) (Config, error) {
	sum := sha256.Sum256([]byte(slug + "\x00" + user + "\x00" + pass))
	key := hex.EncodeToString(sum[:])
	s.mu.Lock()
	e, cached := s.authOK[key]
	s.mu.Unlock()
	if cached && time.Since(e.at) < 5*time.Minute {
		if c, err := s.get(ctx, "id", e.id); err == nil && c.Enabled && c.Slug == slug {
			return c, nil
		}
	}
	lk := ip + "|" + slug
	if s.limited(lk) {
		return Config{}, errRateLimited
	}
	c, err := s.get(ctx, "slug", slug)
	if err != nil || !c.Enabled || c.Username != user || bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(pass)) != nil {
		s.fail(lk)
		return Config{}, errBadCredentials
	}
	s.mu.Lock()
	if len(s.authOK) > 10000 {
		s.authOK = map[string]authEntry{}
	}
	s.authOK[key] = authEntry{id: c.ID, at: time.Now()}
	s.mu.Unlock()
	return c, nil
}

func (s *Service) limited(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-time.Minute)
	kept := s.failures[k][:0]
	for _, t := range s.failures[k] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.failures[k] = kept
	return len(kept) >= 10
}

func (s *Service) fail(k string) {
	s.mu.Lock()
	s.failures[k] = append(s.failures[k], time.Now())
	s.mu.Unlock()
}
