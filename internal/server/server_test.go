package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database/dbtest"
	"github.com/easycloud/easycloud/internal/server"
)

type env struct {
	t    *testing.T
	ts   *httptest.Server
	data string
	app  string
}

func newEnv(t *testing.T, opts ...func(*config.Config)) *env {
	t.Helper()
	data, app := t.TempDir(), t.TempDir()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataRoot, cfg.Paths.AppData, cfg.Admin.Password = data, app, "adminpass1"
	cfg.Plugins.Repository = ""
	for _, o := range opts {
		o(cfg)
	}
	if mc, ok := dbtest.MariaDBConfig(); ok && os.Getenv("EASYCLOUD_TEST_SERVER_MARIADB") == "1" {
		cfg.Database = mc
		dbtest.Wipe(t, dbtest.OpenRaw(t, mc))
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(ctx, cfg, dbtest.Logger())
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(func() { ts.Close(); cancel(); srv.Close() })
	return &env{t: t, ts: ts, data: data, app: app}
}

type client struct {
	e *env
	c *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (c *client) login(user, pw string) {
	c.e.t.Helper()
	if code, body := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": pw}); code != 200 {
		c.e.t.Fatalf("login %s: %d %v", user, code, body)
	}
}

func q(p string) string { return url.QueryEscape(p) }

func names(body map[string]any) []string {
	var out []string
	for _, e := range body["entries"].([]any) {
		out = append(out, e.(map[string]any)["name"].(string))
	}
	return out
}

func (e *env) write(rel, content string) {
	p := filepath.Join(e.data, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(content), 0o644)
}

// tusCreate starts an upload and returns status + Location.
func (c *client) tusCreate(dir, name string, size int) (int, string) {
	req, _ := http.NewRequest("POST", c.e.ts.URL+"/api/upload/", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", fmt.Sprint(size))
	req.Header.Set("Upload-Metadata", "filename "+base64.StdEncoding.EncodeToString([]byte(name))+",dir "+base64.StdEncoding.EncodeToString([]byte(dir)))
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

func (c *client) tusPatch(loc string, data []byte) (int, string) {
	req, _ := http.NewRequest("PATCH", loc, bytes.NewReader(data))
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("X-Easycloud-Path")
}

func TestCrossUserMatrix(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")

	e.write("share/AA/doc.txt", "aa")
	e.write("share/AAB/secret.txt", "aab")
	e.write("share/BB/report.txt", "bb")

	_, ua := admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	_, ub := admin.do("POST", "/api/admin/users", map[string]any{"username": "bob", "password": "bobpass12"})
	_, dept := admin.do("POST", "/api/admin/groups", map[string]any{"name": "研发部"})
	_, sub := admin.do("POST", "/api/admin/groups", map[string]any{"name": "后端组", "parentId": dept["id"]})
	if code, _ := admin.do("PATCH", "/api/admin/users/"+ub["id"].(string), map[string]any{"groupIds": []string{sub["id"].(string)}}); code != 200 {
		t.Fatal("set groups")
	}
	for _, g := range []map[string]any{
		{"subjectType": "user", "subjectId": ua["id"], "path": "/share/AA", "perm": "rw"},
		{"subjectType": "group", "subjectId": dept["id"], "path": "/share/BB", "perm": "r"}, // inherited by 后端组
	} {
		if code, body := admin.do("POST", "/api/admin/grants", g); code != 201 {
			t.Fatalf("grant: %d %v", code, body)
		}
	}
	e.write("user/bob/private.txt", "bob's")

	alice, bob := e.client(), e.client()
	alice.login("alice", "alicepass1")
	bob.login("bob", "bobpass12")

	// Navigation-only ancestors list just the branches leading to grants.
	_, root := alice.do("GET", "/api/files/list?path=/", nil)
	if root["access"] != "nav" || strings.Join(names(root), ",") != "share,user" {
		t.Fatalf("alice root: %v", root)
	}
	_, users := alice.do("GET", "/api/files/list?path=/user", nil)
	if strings.Join(names(users), ",") != "alice" {
		t.Fatalf("alice /user: %v", names(users))
	}
	_, share := alice.do("GET", "/api/files/list?path=/share", nil)
	if strings.Join(names(share), ",") != "AA" {
		t.Fatalf("alice /share: %v", names(share))
	}
	// Department grant inherited through the parent department, read-only.
	_, bb := bob.do("GET", "/api/files/list?path=/share/BB", nil)
	if bb["access"] != "r" || strings.Join(names(bb), ",") != "report.txt" {
		t.Fatalf("bob /share/BB: %v", bb)
	}
	if code, _ := bob.do("POST", "/api/files/mkdir", map[string]string{"path": "/share/BB/new"}); code != 404 {
		t.Fatalf("read-only mkdir: %d", code)
	}

	// Alice against everything of Bob's and against non-granted siblings: all 404.
	targets := []string{"/user/bob", "/user/bob/private.txt", "/share/AAB", "/share/AAB/secret.txt", "/share/BB"}
	for _, p := range targets {
		checks := []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/files/list?path=" + q(p), nil},
			{"GET", "/api/files/stat?path=" + q(p), nil},
			{"GET", "/api/files/download?path=" + q(p), nil},
			{"POST", "/api/files/mkdir", map[string]string{"path": p + "/x"}},
			{"POST", "/api/files/rename", map[string]string{"path": p, "name": "pwned"}},
			{"POST", "/api/files/move", map[string]any{"sources": []string{p}, "dest": "/user/alice"}},
			{"POST", "/api/files/copy", map[string]any{"sources": []string{p}, "dest": "/user/alice"}},
			{"POST", "/api/files/move", map[string]any{"sources": []string{"/share/AA/doc.txt"}, "dest": p}},
			{"POST", "/api/files/trash", map[string]any{"paths": []string{p}}},
		}
		for _, c := range checks {
			if code, body := alice.do(c.method, c.path, c.body); code != 404 {
				t.Errorf("alice %s %s (%s): %d %v", c.method, c.path, p, code, body)
			}
		}
		if code, _ := alice.tusCreate(p, "x.txt", 3); code != 404 {
			t.Errorf("alice upload into %s: %d", p, code)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "user/bob/private.txt")); string(b) != "bob's" {
		t.Fatal("bob's file changed")
	}

	// Resource IDs: trash items and uploads are owner-scoped.
	_, tr := alice.do("POST", "/api/files/trash", map[string]any{"paths": []string{"/share/AA/doc.txt"}})
	trashID := tr["items"].([]any)[0].(map[string]any)["id"].(string)
	for _, c := range []struct{ m, p string }{{"POST", "/api/trash/" + trashID + "/restore"}, {"DELETE", "/api/trash/" + trashID}} {
		if code, _ := bob.do(c.m, c.p, nil); code != 404 {
			t.Errorf("bob %s %s: %d", c.m, c.p, code)
		}
	}
	if _, l := bob.do("GET", "/api/trash", nil); len(l["items"].([]any)) != 0 {
		t.Error("bob sees alice's trash")
	}
	code, loc := alice.tusCreate("/user/alice", "up.txt", 5)
	if code != 201 {
		t.Fatalf("alice upload create: %d", code)
	}
	if code, _ := bob.tusPatch(loc, []byte("hacky")); code != 404 {
		t.Errorf("bob patching alice's upload: %d", code)
	}
	if code, p := alice.tusPatch(loc, []byte("hello")); code != 204 || p != "/user/alice/up.txt" {
		t.Errorf("alice patch: %d %q", code, p)
	}
	if code, _ := alice.do("POST", "/api/trash/"+trashID+"/restore", nil); code != 200 {
		t.Error("alice restore own trash")
	}

	// Non-admins cannot see admin endpoints at all.
	for _, p := range []string{"/api/admin/users", "/api/admin/groups", "/api/admin/grants", "/api/admin/audit", "/api/settings"} {
		if code, _ := alice.do("GET", p, nil); code != 404 {
			t.Errorf("alice GET %s: %d", p, code)
		}
	}
	// A granted directory itself cannot be renamed, moved or deleted by its grantee.
	if code, _ := alice.do("POST", "/api/files/rename", map[string]string{"path": "/share/AA", "name": "AAX"}); code != 400 && code != 404 {
		t.Errorf("rename grant root: %d", code)
	}
	if code, _ := alice.do("POST", "/api/files/trash", map[string]any{"paths": []string{"/user/alice"}}); code != 400 {
		t.Errorf("trash home: %d", code)
	}

	// Admin renames a granted directory: the grant follows it.
	if code, body := admin.do("POST", "/api/files/rename", map[string]string{"path": "/share/AA", "name": "AA2"}); code != 200 {
		t.Fatalf("admin rename: %d %v", code, body)
	}
	if code, body := alice.do("GET", "/api/files/list?path=/share/AA2", nil); code != 200 || body["access"] != "rw" {
		t.Fatalf("grant did not follow rename: %d %v", code, body)
	}

	// Disabling a user ends their sessions immediately.
	admin.do("PATCH", "/api/admin/users/"+ua["id"].(string), map[string]any{"disabled": true})
	if code, _ := alice.do("GET", "/api/files/list?path=/user/alice", nil); code != 401 {
		t.Errorf("disabled user still signed in: %d", code)
	}
	// The last administrator is protected.
	if code, _ := admin.do("PATCH", "/api/admin/users/admin", map[string]any{"role": "user"}); code != 400 && code != 409 {
		t.Errorf("demote last admin: %d", code)
	}

	// Audit trail captured the actions.
	_, au := admin.do("GET", "/api/admin/audit?userId="+ua["id"].(string), nil)
	if len(au["items"].([]any)) < 3 {
		t.Errorf("audit entries: %v", au)
	}
}

func TestAttackPayloads(t *testing.T) {
	e := newEnv(t)
	e.write("ok.txt", "ok")
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "passwd"), []byte("root:x"), 0o644)
	os.Symlink(outside, filepath.Join(e.data, "link"))
	admin := e.client()
	admin.login("admin", "adminpass1")
	payloads := []string{
		"../", "/../../etc/passwd", "..%2f..%2fetc", "%2e%2e/%2e%2e/etc", "..\\..\\windows", "/ok.txt\x00.png",
		"/.easycloud/instance.json", "/.easycloud/../ok.txt", "/link/passwd", "\xc0\xae\xc0\xae/etc",
	}
	for _, p := range payloads {
		for _, path := range []string{"/api/files/list?path=", "/api/files/stat?path=", "/api/files/download?path="} {
			code, body := admin.do("GET", path+q(p), nil)
			if code != 400 && code != 404 {
				t.Errorf("GET %s%q: %d %v", path, p, code, body)
			}
		}
		if code, _ := admin.do("POST", "/api/files/mkdir", map[string]string{"path": p + "/x"}); code != 400 && code != 404 {
			t.Errorf("mkdir %q: %d", p, code)
		}
	}
	// Cross-site requests are rejected before reaching handlers.
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/files/mkdir", strings.NewReader(`{"path":"/evil"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	res, _ := admin.c.Do(req)
	if res.StatusCode != 403 {
		t.Errorf("cross-origin: %d", res.StatusCode)
	}
	res.Body.Close()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func TestSearchQuotaAndHostRename(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	e.write("share/team/plan-2026.md", "x")
	e.write("share/secret/plan-secret.md", "x")
	_, ua := admin.do("POST", "/api/admin/users", map[string]any{"username": "carol", "password": "carolpass1", "quotaBytes": 10})
	admin.do("POST", "/api/admin/grants", map[string]any{"subjectType": "user", "subjectId": ua["id"], "path": "/share/team", "perm": "rw"})
	carol := e.client()
	carol.login("carol", "carolpass1")

	waitFor(t, "index", func() bool {
		_, r := admin.do("GET", "/api/files/search?q=plan", nil)
		return len(r["items"].([]any)) == 2
	})
	_, r := carol.do("GET", "/api/files/search?q=plan", nil)
	if items := r["items"].([]any); len(items) != 1 || items[0].(map[string]any)["path"] != "/share/team/plan-2026.md" {
		t.Fatalf("carol search: %v", r)
	}
	if _, r := carol.do("GET", "/api/files/search?q=plan&path=/share/secret", nil); len(r["items"].([]any)) != 0 {
		t.Fatalf("scoped search leaked: %v", r)
	}
	if code, _ := carol.do("GET", "/api/files/usage?path=/share/secret", nil); code != 404 {
		t.Fatalf("usage outside grant: %d", code)
	}

	// Quota: 10 bytes in /user/carol.
	if code, _ := carol.tusCreate("/user/carol", "small.txt", 8); code != 201 {
		t.Fatalf("within quota: %d", code)
	}
	e.write("user/carol/existing.bin", "12345678")
	waitFor(t, "usage", func() bool {
		_, u := carol.do("GET", "/api/files/usage?path=/user/carol", nil)
		return u["bytes"] == float64(8)
	})
	if code, _ := carol.tusCreate("/user/carol", "big.txt", 5); code != 507 {
		t.Fatalf("over quota upload: %d", code)
	}
	if code, body := carol.do("POST", "/api/files/copy", map[string]any{"sources": []string{"/share/team/plan-2026.md"}, "dest": "/user/carol"}); code != 200 {
		t.Fatalf("1-byte copy within quota: %d %v", code, body)
	}
	if code, _ := carol.tusCreate("/share/team", "big.txt", 5000); code != 201 {
		t.Fatalf("quota must not apply outside home: %d", code)
	}

	// Host-side rename of a granted directory: the grant follows via inode tracking.
	if err := os.Rename(filepath.Join(e.data, "share/team"), filepath.Join(e.data, "share/team-renamed")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "grant follows host rename", func() bool {
		code, body := carol.do("GET", "/api/files/list?path=/share/team-renamed", nil)
		return code == 200 && body["access"] == "rw"
	})
}
