package server_test

import (
	"sort"
	"strings"
	"testing"
)

// Category views list files of a kind across everything the user may read,
// and nothing else.
func TestCategories(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, ua := admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	for _, f := range []string{
		"share/AA/a.jpg", "share/AA/sub/b.MKV", "share/AA/c.mp3", "share/AA/d.pdf", "share/AA/e.txt", "share/AA/f.bin", "share/AA/g.DOCX", "share/AA/h.epub",
		"share/AAB/secret.jpg", "share/BB/other.mp4", "user/alice/me.png",
	} {
		e.write(f, strings.Repeat("x", len(f)))
	}
	if code, body := admin.do("POST", "/api/admin/grants", map[string]any{"subjectType": "user", "subjectId": ua["id"], "path": "/share/AA", "perm": "r"}); code != 201 {
		t.Fatalf("grant: %d %v", code, body)
	}
	alice := e.client()
	alice.login("alice", "alicepass1")
	paths := func(c *client, q string) ([]string, float64) {
		t.Helper()
		code, body := c.do("GET", "/api/files/category?"+q, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", q, code, body)
		}
		var out []string
		for _, it := range body["items"].([]any) {
			out = append(out, it.(map[string]any)["path"].(string))
		}
		return out, body["total"].(float64)
	}
	waitFor(t, "index", func() bool { p, _ := paths(admin, "kind=image"); return len(p) == 3 })

	sorted := func(p []string) string { sort.Strings(p); return strings.Join(p, ",") }
	cases := []struct{ kind, want string }{
		{"image", "/share/AA/a.jpg,/user/alice/me.png"}, // not /share/AAB (a sibling prefix, no grant)
		{"video", "/share/AA/sub/b.MKV"},                // by extension, case-insensitive
		{"audio", "/share/AA/c.mp3"},
		{"doc", "/share/AA/d.pdf,/share/AA/e.txt,/share/AA/g.DOCX,/share/AA/h.epub"},
		{"doc&type=pdf", "/share/AA/d.pdf"},
		{"doc&type=word", "/share/AA/g.DOCX"},
		{"doc&type=excel", ""},
		{"doc&type=other", "/share/AA/h.epub"}, // in the category, outside every tag
		{"image&type=jpg", "/share/AA/a.jpg"},
		{"image&type=png", "/user/alice/me.png"},
		{"image&type=other", ""},
	}
	for _, c := range cases {
		if got, _ := paths(alice, "kind="+c.kind); sorted(got) != c.want {
			t.Errorf("alice %s = %v, want %s", c.kind, got, c.want)
		}
	}
	if got, _ := paths(admin, "kind=video"); sorted(got) != "/share/AA/sub/b.MKV,/share/BB/other.mp4" {
		t.Errorf("admin video = %v", got)
	}

	// Sorting and paging.
	if got, total := paths(admin, "kind=image&sort=name&order=asc&limit=2"); total != 3 || strings.Join(got, ",") != "/share/AA/a.jpg,/user/alice/me.png" {
		t.Errorf("page 1 = %v (total %v)", got, total)
	}
	if got, _ := paths(admin, "kind=image&sort=name&order=asc&limit=2&offset=2"); strings.Join(got, ",") != "/share/AAB/secret.jpg" {
		t.Errorf("page 2 = %v", got)
	}
	if code, _ := alice.do("GET", "/api/files/category?kind=doc&type=mkv", nil); code != 400 {
		t.Errorf("type of another kind: %d", code)
	}
	if code, _ := alice.do("GET", "/api/files/category?kind=exe", nil); code != 400 {
		t.Errorf("unknown kind: %d", code)
	}
}
