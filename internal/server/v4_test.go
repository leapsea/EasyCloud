package server_test

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/config"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func trustKey(pub ed25519.PublicKey) func(*config.Config) {
	return func(c *config.Config) { c.Plugins.TrustedKeys = base64.StdEncoding.EncodeToString(pub) }
}

func waitForLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func anonGet(t *testing.T, url string, hdr map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

func TestShares(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, ue := admin.do("POST", "/api/admin/users", map[string]any{"username": "erin", "password": "erinpass1"})
	e.write("user/erin/album/a.txt", "AAAA")
	e.write("user/erin/album/sub/b.txt", "BBBB")
	e.write("user/erin/solo.txt", "0123456789")
	e.write("user/erin/secret.txt", "TOP SECRET")
	erin := e.client()
	erin.login("erin", "erinpass1")

	// Folder share with a password.
	code, sh := erin.do("POST", "/api/shares", map[string]any{"path": "/user/erin/album", "password": "s3cret"})
	if code != 201 || len(sh["id"].(string)) != 22 {
		t.Fatalf("create share: %d %v", code, sh)
	}
	id := sh["id"].(string)
	base := e.ts.URL + "/api/s/" + id
	if c, b, _ := anonGet(t, base+"/", nil); c != 200 || !strings.Contains(string(b), `"unlocked":false`) {
		t.Fatalf("meta locked: %d %s", c, b)
	}
	if c, _, _ := anonGet(t, base+"/list", nil); c != 401 {
		t.Fatalf("list without password: %d", c)
	}
	visitor := e.client()
	if c, _ := visitor.do("POST", "/api/s/"+id+"/unlock", map[string]string{"password": "nope"}); c != 401 {
		t.Fatalf("bad password: %d", c)
	}
	c, un := visitor.do("POST", "/api/s/"+id+"/unlock", map[string]string{"password": "s3cret"})
	if c != 200 {
		t.Fatalf("unlock: %d", c)
	}
	_, l := visitor.do("GET", "/api/s/"+id+"/list?path=/", nil) // via cookie
	if strings.Join(names(l), ",") != "sub,a.txt" {
		t.Fatalf("share list: %v", l)
	}
	// Direct links for download tools use ?token=.
	tok := un["token"].(string)
	if c, b, _ := anonGet(t, base+"/download?path=/sub/b.txt&token="+tok, nil); c != 200 || string(b) != "BBBB" {
		t.Fatalf("token download: %d %s", c, b)
	}
	// No escaping the share root.
	for _, p := range []string{"/../secret.txt", "/../../erin/secret.txt", "..", "/sub/../../secret.txt"} {
		if c, _, _ := anonGet(t, base+"/download?path="+q(p)+"&token="+tok, nil); c != 400 && c != 404 {
			t.Errorf("escape %q: %d", p, c)
		}
	}
	// Zip of the folder.
	c, zb, _ := anonGet(t, base+"/zip?token="+tok, nil)
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if c != 200 || err != nil {
		t.Fatalf("zip: %d %v", c, err)
	}
	var got []string
	for _, f := range zr.File {
		got = append(got, f.Name)
	}
	if !strings.Contains(strings.Join(got, ","), "album/sub/b.txt") {
		t.Fatalf("zip entries %v", got)
	}

	// Single-file share with a download limit of 2; seeking does not count.
	_, fs := erin.do("POST", "/api/shares", map[string]any{"path": "/user/erin/solo.txt", "maxDownloads": 2})
	fbase := e.ts.URL + "/api/s/" + fs["id"].(string)
	if c, b, _ := anonGet(t, fbase+"/download", nil); c != 200 || string(b) != "0123456789" {
		t.Fatalf("file share: %d", c)
	}
	if c, b, _ := anonGet(t, fbase+"/download", map[string]string{"Range": "bytes=5-"}); c != 206 || string(b) != "56789" {
		t.Fatalf("range: %d %q", c, b)
	}
	anonGet(t, fbase+"/download", nil)
	if c, _, _ := anonGet(t, fbase+"/download", nil); c != 410 {
		t.Fatalf("download limit: %d", c)
	}
	if c, _, _ := anonGet(t, fbase+"/list", nil); c != 410 {
		t.Fatalf("exhausted share still listed: %d", c)
	}

	// The share follows a rename; and dies when the owner is disabled.
	erin.do("POST", "/api/files/rename", map[string]string{"path": "/user/erin/album", "name": "album-2026"})
	if c, b, _ := anonGet(t, base+"/download?path=/a.txt&token="+tok, nil); c != 200 || string(b) != "AAAA" {
		t.Fatalf("after rename: %d %s", c, b)
	}
	other := e.client()
	other.login("admin", "adminpass1")
	if c, l := other.do("GET", "/api/admin/shares", nil); c != 200 || len(l["items"].([]any)) != 2 {
		t.Fatalf("admin shares: %d %v", c, l)
	}
	admin.do("PATCH", "/api/admin/users/"+ue["id"].(string), map[string]any{"disabled": true})
	if c, _, _ := anonGet(t, base+"/", nil); c != 410 {
		t.Fatalf("share of disabled owner: %d", c)
	}
	// Unknown / malformed ids look identical.
	if c, _, _ := anonGet(t, e.ts.URL+"/api/s/AAAAAAAAAAAAAAAAAAAAAA/", nil); c != 404 {
		t.Fatalf("unknown share: %d", c)
	}
}

type dav struct {
	t          *testing.T
	base       string // e.g. http://host/dav/config-a
	user, pass string
}

func (d dav) do(method, path string, body string, hdr map[string]string) (int, string) {
	d.t.Helper()
	req, _ := http.NewRequest(method, d.base+path, strings.NewReader(body))
	req.SetBasicAuth(d.user, d.pass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// davConfig creates a WebDAV configuration as admin and returns a client for it.
func davConfig(t *testing.T, e *env, admin *client, slug string, dirs []map[string]string) (dav, map[string]any) {
	t.Helper()
	code, body := admin.do("POST", "/api/admin/webdav", map[string]any{"name": "Config " + slug, "slug": slug, "username": slug + "-user", "dirs": dirs})
	if code != 201 {
		t.Fatalf("create webdav config %s: %d %v", slug, code, body)
	}
	return dav{t, e.ts.URL + "/dav/" + slug, slug + "-user", body["password"].(string)}, body["item"].(map[string]any)
}

func TestWebDAVConfigs(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, u := admin.do("POST", "/api/admin/users", map[string]any{"username": "lily", "password": "lilypass1", "quotaBytes": 1000})
	_ = u
	e.write("data/1/2/3/movie.mkv", "MOVIE")
	e.write("data/4/doc.txt", "four")
	e.write("data/5/ro.txt", "readonly")
	e.write("data/6/b-only.txt", "six")
	e.write("data/7/secret.txt", "not shared")
	e.write("other/3/clash.txt", "x")

	// Name clash: /data/1/2/3 and /other/3 would both be "3" — an alias is required.
	if code, body := admin.do("POST", "/api/admin/webdav", map[string]any{"name": "Clash", "slug": "clash", "username": "clash",
		"dirs": []map[string]string{{"path": "/data/1/2/3", "perm": "rw"}, {"path": "/other/3", "perm": "r"}}}); code != 400 || !strings.Contains(fmt.Sprint(body["detail"]), "alias") {
		t.Fatalf("name clash: %d %v", code, body)
	}
	if code, _ := admin.do("POST", "/api/admin/webdav", map[string]any{"name": "Bad", "slug": "bad", "username": "bad",
		"dirs": []map[string]string{{"path": "/does/not/exist", "perm": "r"}}}); code != 400 {
		t.Fatalf("missing dir accepted: %d", code)
	}

	a, cfgA := davConfig(t, e, admin, "config-a", []map[string]string{
		{"path": "/data/1/2/3", "perm": "rw"}, {"path": "/data/4", "perm": "rw"}, {"path": "/data/5", "perm": "r"}})
	b, _ := davConfig(t, e, admin, "config-b", []map[string]string{{"path": "/data/6", "perm": "rw"}})

	// The root of each config lists exactly its directories.
	c, body := a.do("PROPFIND", "/", "", map[string]string{"Depth": "1"})
	if c != 207 {
		t.Fatalf("propfind A: %d %s", c, body)
	}
	for _, want := range []string{"/dav/config-a/3/", "/dav/config-a/4/", "/dav/config-a/5/"} {
		if !strings.Contains(body, want) {
			t.Errorf("config A root missing %s", want)
		}
	}
	for _, leak := range []string{"/6", "/7", "data", "user", ".easycloud"} {
		if strings.Contains(body, "/dav/config-a"+leak) {
			t.Errorf("config A root leaks %s", leak)
		}
	}
	if _, body := b.do("PROPFIND", "/", "", map[string]string{"Depth": "1"}); !strings.Contains(body, "/dav/config-b/6/") || strings.Contains(body, "config-b/3") {
		t.Errorf("config B root: %s", body)
	}

	// Reads, writes, read-only.
	if c, got := a.do("GET", "/3/movie.mkv", "", map[string]string{"Range": "bytes=1-3"}); c != 206 || got != "OVI" {
		t.Errorf("range read: %d %q", c, got)
	}
	if c, _ := a.do("PUT", "/4/new.txt", "hello", nil); c != 201 {
		t.Errorf("put rw: %d", c)
	}
	if b2, _ := os.ReadFile(filepath.Join(e.data, "data/4/new.txt")); string(b2) != "hello" {
		t.Error("put content")
	}
	if c, _ := a.do("PUT", "/5/new.txt", "x", nil); c != 403 && c != 404 { // x/net/webdav maps every PUT open error to 404
		t.Errorf("put read-only: %d", c)
	}
	if c, _ := a.do("MKCOL", "/4/sub", "", nil); c != 201 {
		t.Errorf("mkcol: %d", c)
	}
	// Moves between two shared directories go through the real paths.
	if c, _ := a.do("MOVE", "/4/new.txt", "", map[string]string{"Destination": a.base + "/3/moved.txt"}); c != 201 {
		t.Errorf("cross-dir move: %d", c)
	}
	if _, err := os.Stat(filepath.Join(e.data, "data/1/2/3/moved.txt")); err != nil {
		t.Error("moved file missing")
	}
	if c, _ := a.do("MOVE", "/3/moved.txt", "", map[string]string{"Destination": a.base + "/5/x.txt"}); c < 400 {
		t.Errorf("move into read-only dir: %d", c)
	}
	// The shared directories themselves and the root are fixed.
	for _, m := range []struct{ method, path string }{{"DELETE", "/3"}, {"MKCOL", "/newtop"}, {"PUT", "/top.txt"}} {
		if c, _ := a.do(m.method, m.path, "x", nil); c < 400 {
			t.Errorf("%s %s at root level: %d", m.method, m.path, c)
		}
	}
	if c, _ := a.do("MOVE", "/3", "", map[string]string{"Destination": a.base + "/renamed"}); c < 400 {
		t.Errorf("rename shared dir: %d", c)
	}
	// Deletes go to the creator's recycle bin.
	if c, _ := a.do("DELETE", "/4/doc.txt", "", nil); c != 204 {
		t.Errorf("delete: %d", c)
	}
	if _, tr := admin.do("GET", "/api/trash", nil); len(tr["items"].([]any)) != 1 {
		t.Errorf("deleted file not in the admin's recycle bin: %v", tr)
	}

	// Isolation: nothing outside the directories, no cross-config access.
	for _, p := range []string{"/7/secret.txt", "/../7/secret.txt", "/3/../../../7/secret.txt", "/6/b-only.txt", "/data/7/secret.txt", "/.easycloud/instance.json"} {
		if c, _ := a.do("GET", p, "", nil); c != 404 && c != 400 {
			t.Errorf("A GET %s: %d", p, c)
		}
	}
	if c, _ := (dav{t, e.ts.URL + "/dav/config-b", a.user, a.pass}).do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 401 {
		t.Errorf("A's account on B's endpoint: %d", c)
	}
	if c, _ := (dav{t, a.base, a.user, "wrong-password"}).do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 401 {
		t.Errorf("wrong password: %d", c)
	}
	// The old personal WebDAV endpoint is gone.
	if c, _ := (dav{t, e.ts.URL + "/dav", "admin", "adminpass1"}).do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 404 {
		t.Errorf("personal /dav/: %d", c)
	}

	// Quota of the folder owner applies: share lily's home (1000 bytes) through a config.
	e.write("user/lily/keep.txt", "1234")
	q, _ := davConfig(t, e, admin, "lily-home", []map[string]string{{"path": "/user/lily", "perm": "rw"}})
	waitFor(t, "usage indexed", func() bool {
		_, u := admin.do("GET", "/api/files/usage?path=/user/lily", nil)
		return u["bytes"] == float64(4)
	})
	if c, _ := q.do("PUT", "/lily/big.bin", strings.Repeat("x", 5000), nil); c < 400 {
		t.Errorf("over-quota put: %d", c)
	}
	if _, err := os.Stat(filepath.Join(e.data, "user/lily/big.bin")); err == nil {
		t.Error("partial over-quota file left behind")
	}

	// Disabling and password reset take effect immediately.
	id := cfgA["id"].(string)
	admin.do("PATCH", "/api/admin/webdav/"+id, map[string]any{"enabled": false})
	if c, _ := a.do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 401 {
		t.Errorf("disabled config: %d", c)
	}
	admin.do("PATCH", "/api/admin/webdav/"+id, map[string]any{"enabled": true})
	_, pw := admin.do("POST", "/api/admin/webdav/"+id+"/password", nil)
	if c, _ := a.do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 401 {
		t.Errorf("old password after reset: %d", c)
	}
	a.pass = pw["password"].(string)
	if c, _ := a.do("PROPFIND", "/", "", map[string]string{"Depth": "0"}); c != 207 {
		t.Errorf("new password: %d", c)
	}
	// Directories follow a rename made in the file manager.
	admin.do("POST", "/api/files/rename", map[string]string{"path": "/data/4", "name": "four"})
	if c, got := a.do("GET", "/four/sub/", "", nil); c >= 400 && c != 405 {
		t.Errorf("after rename: %d %s", c, got)
	}
	_, list := admin.do("GET", "/api/admin/webdav", nil)
	found := false
	for _, it := range list["items"].([]any) {
		for _, d := range it.(map[string]any)["dirs"].([]any) {
			if d.(map[string]any)["path"] == "/data/four" {
				found = true
			}
		}
	}
	if !found {
		t.Error("config directory did not follow the rename")
	}
	// Non-admins cannot manage configs.
	lily := e.client()
	lily.login("lily", "lilypass1")
	if c, _ := lily.do("GET", "/api/admin/webdav", nil); c != 404 {
		t.Errorf("non-admin list: %d", c)
	}
}

func TestSambaGatewaySync(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	pub, priv := testKey(t)
	e := newEnv(t, trustKey(pub))
	src := t.TempDir()
	bin := filepath.Join(src, "bin", runtime.GOOS+"-"+runtime.GOARCH, "samba-gateway")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/easycloud/easycloud/plugins/samba-gateway").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	man, _ := os.ReadFile("../../plugins/samba-gateway/manifest.json")
	os.WriteFile(filepath.Join(src, "manifest.json"), man, 0o644)
	admin := e.client()
	admin.login("admin", "adminpass1")
	if res, b := admin.raw("POST", "/api/admin/plugins", bytes.NewReader(signedPackage(t, src, priv)), "application/zip"); res.StatusCode != 201 {
		t.Fatalf("install: %d %s", res.StatusCode, b)
	}
	outDir := filepath.Join(e.app, "samba-out")
	if c, b := admin.do("PATCH", "/api/admin/plugins/samba-gateway", map[string]any{"config": map[string]any{"outputDir": outDir}}); c != 204 {
		t.Fatalf("configure: %d %v", c, b)
	}
	e.write("share/finance/q1.xlsx", "x")
	_, uh := admin.do("POST", "/api/admin/users", map[string]any{"username": "hana", "password": "hanapass1"})
	_, dept := admin.do("POST", "/api/admin/groups", map[string]any{"name": "财务"})
	admin.do("PATCH", "/api/admin/groups/"+dept["id"].(string), map[string]any{"memberIds": []string{uh["id"].(string)}})
	admin.do("POST", "/api/admin/grants", map[string]any{"subjectType": "group", "subjectId": dept["id"], "path": "/share/finance", "perm": "rw"})
	hana := e.client()
	hana.login("hana", "hanapass1")
	hana.do("POST", "/api/auth/app-passwords", map[string]string{"name": "windows"})

	waitForLong(t, "smb.conf with hana's shares", func() bool {
		conf, err := os.ReadFile(filepath.Join(outDir, "smb.conf"))
		pw, _ := os.ReadFile(filepath.Join(outDir, "smbpasswd"))
		return err == nil && strings.Contains(string(conf), "[finance]") && strings.Contains(string(conf), `write list = "hana"`) &&
			strings.Contains(string(conf), "[hana]") && strings.Contains(string(pw), "hana:")
	})
	// Disabling the user removes the account on the next sync.
	admin.do("PATCH", "/api/admin/users/"+uh["id"].(string), map[string]any{"disabled": true})
	waitForLong(t, "account removed", func() bool {
		pw, _ := os.ReadFile(filepath.Join(outDir, "smbpasswd"))
		return !strings.Contains(string(pw), "hana:")
	})
}
