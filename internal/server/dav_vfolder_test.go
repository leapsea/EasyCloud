package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A virtual folder is configured right on the endpoint: a name plus the
// original paths it gathers. Example from the spec: A holds /1/2/3 and
// /1/2/5 but not /1/2/6. It is read-only, also when the same folder is
// shared read/write elsewhere on the endpoint.
func TestWebDAVVirtualFolders(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	e.write("1/2/3/x.txt", "three")
	e.write("1/2/5/y.txt", "five")
	e.write("1/2/6/z.txt", "six")
	e.write("docs/readme.md", "readme")
	e.write("docs/private.txt", "not in A")
	e.write("notes/todo.txt", "todo")
	e.write("notes/secret.txt", "secret")

	create := func(slug string, dirs []map[string]string) (int, map[string]any) {
		return admin.do("POST", "/api/admin/webdav", map[string]any{"name": slug, "slug": slug, "username": slug + "-u", "dirs": dirs})
	}
	// Two entries showing the same name in one virtual folder need an alias.
	if c, b := create("dup", []map[string]string{{"path": "/1/2/3", "folder": "A"}, {"path": "/docs", "folder": "A", "alias": "3"}}); c != 400 || !strings.Contains(fmt.Sprint(b["detail"]), "alias") {
		t.Fatalf("duplicate entry name: %d %v", c, b)
	}
	// A virtual folder cannot share its name with a directory at the root.
	if c, b := create("clash", []map[string]string{{"path": "/docs", "alias": "A", "perm": "rw"}, {"path": "/1/2/3", "folder": "A"}}); c != 400 {
		t.Fatalf("root name clash: %d %v", c, b)
	}

	c, body := create("tv", []map[string]string{
		{"path": "/1/2/3", "folder": "A"},
		{"path": "/1/2/5", "folder": "A", "perm": "rw"}, // ignored: virtual folders are read-only
		{"path": "/docs/readme.md", "folder": "A"},
		{"path": "/notes/todo.txt", "perm": "rw"}, // a single file at the root: read-only regardless
		{"path": "/1/2/5", "perm": "rw"},          // the same folder shared read/write at the root
	})
	if c != 201 {
		t.Fatalf("create: %d %v", c, body)
	}
	d := dav{t, e.ts.URL + "/dav/tv", "tv-u", body["password"].(string)}

	_, root := d.do("PROPFIND", "/", "", map[string]string{"Depth": "1"})
	ia, it, i5 := strings.Index(root, "/dav/tv/A/"), strings.Index(root, "/dav/tv/todo.txt"), strings.Index(root, "/dav/tv/5/")
	if ia < 0 || it < 0 || i5 < 0 || !(ia < it && it < i5) {
		t.Fatalf("root (configured order A, todo.txt, 5): %s", root)
	}
	// The single root file: readable, read-only, its folder stays hidden.
	if c, b := d.do("GET", "/todo.txt", "", nil); c != 200 || b != "todo" {
		t.Fatalf("root file: %d %q", c, b)
	}
	if c, _ := d.do("PUT", "/todo.txt", "changed", nil); c < 400 {
		t.Fatalf("root file writable: %d", c)
	}
	if c, _ := d.do("GET", "/todo.txt/../secret.txt", "", nil); c == 200 {
		t.Fatal("root file exposed its folder")
	}
	_, a := d.do("PROPFIND", "/A/", "", map[string]string{"Depth": "1"})
	for _, want := range []string{"/dav/tv/A/3/", "/dav/tv/A/5/", "/dav/tv/A/readme.md"} {
		if !strings.Contains(a, want) {
			t.Fatalf("A lacks %s:\n%s", want, a)
		}
	}
	for _, leak := range []string{"/6", "private", "x.txt"} {
		if strings.Contains(a, leak) {
			t.Fatalf("A shows %s:\n%s", leak, a)
		}
	}
	if c, b := d.do("GET", "/A/3/x.txt", "", nil); c != 200 || b != "three" {
		t.Fatalf("read in A: %d %q", c, b)
	}
	if c, b := d.do("GET", "/A/readme.md", "", nil); c != 200 || b != "readme" {
		t.Fatalf("read file entry: %d %q", c, b)
	}
	if c, _ := d.do("GET", "/A/readme.md/../private.txt", "", nil); c == 200 {
		t.Fatal("file entry exposed its folder")
	}
	if c, _ := d.do("GET", "/A/6/z.txt", "", nil); c == 200 {
		t.Fatal("/1/2/6 reachable")
	}
	for _, w := range []struct{ m, p string }{
		{"PUT", "/A/new.txt"}, {"PUT", "/A/5/y.txt"}, {"PUT", "/A/5/new.txt"}, {"DELETE", "/A/3/x.txt"},
		{"DELETE", "/A/3"}, {"MKCOL", "/A/5/dir"}, {"MKCOL", "/A/new"},
	} {
		if c, _ := d.do(w.m, w.p, "data", nil); c < 400 {
			t.Errorf("%s %s: %d, want refused", w.m, w.p, c)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "1", "2", "5", "y.txt")); string(b) != "five" {
		t.Fatalf("write through A reached the file: %q", b)
	}
	if c, _ := d.do("PUT", "/5/new.txt", "ok", nil); c != 201 && c != 204 {
		t.Fatalf("write via the read/write directory: %d", c)
	}

	// The configuration round-trips, and entries follow moves.
	if c, _ := admin.do("POST", "/api/files/move", map[string]any{"sources": []string{"/1/2/3"}, "dest": "/docs", "onConflict": "fail"}); c != 200 {
		t.Fatalf("move: %d", c)
	}
	_, list := admin.do("GET", "/api/admin/webdav", nil)
	dirs := list["items"].([]any)[0].(map[string]any)["dirs"].([]any)
	first := dirs[0].(map[string]any)
	if first["folder"] != "A" || first["path"] != "/docs/3" || first["perm"] != "r" {
		t.Fatalf("entry after move: %v", first)
	}
	if c, b := d.do("GET", "/A/3/x.txt", "", nil); c != 200 || b != "three" {
		t.Fatalf("read after move: %d %q", c, b)
	}
}
