package server_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string, symlink string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if symlink != "" {
		h := &zip.FileHeader{Name: symlink}
		h.SetMode(os.ModeSymlink | 0o777)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte("/etc/passwd"))
	}
	zw.Close()
	return buf.Bytes()
}

func (c *client) waitTask(id string) map[string]any {
	c.e.t.Helper()
	var task map[string]any
	waitForLong(c.e.t, "task "+id, func() bool {
		_, l := c.do("GET", "/api/tasks", nil)
		for _, x := range l["items"].([]any) {
			m := x.(map[string]any)
			if m["id"] == id && m["status"] != "queued" && m["status"] != "running" {
				task = m
				return true
			}
		}
		return false
	})
	return task
}

func TestExtractAndTasks(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, ui := admin.do("POST", "/api/admin/users", map[string]any{"username": "ivan", "password": "ivanpass1", "quotaBytes": 100000})
	admin.do("POST", "/api/admin/users", map[string]any{"username": "judy", "password": "judypass1"})
	ivan, judy := e.client(), e.client()
	ivan.login("ivan", "ivanpass1")
	judy.login("judy", "judypass1")

	// A benign archive extracts into a sibling folder.
	os.WriteFile(filepath.Join(e.data, "user/ivan/photos.zip"), makeZip(t, map[string]string{"a.txt": "A", "dir/b.txt": "B"}, "link"), 0o644)
	code, task := ivan.do("POST", "/api/tasks", map[string]any{"kind": "extract", "sources": []string{"/user/ivan/photos.zip"}})
	if code != 202 {
		t.Fatalf("create extract: %d %v", code, task)
	}
	done := ivan.waitTask(task["id"].(string))
	if done["status"] != "done" || done["result"].([]any)[0] != "/user/ivan/photos" {
		t.Fatalf("extract task: %v", done)
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "user/ivan/photos/dir/b.txt")); string(b) != "B" {
		t.Fatal("extracted content")
	}
	if _, err := os.Lstat(filepath.Join(e.data, "user/ivan/photos/link")); err == nil {
		t.Fatal("symlink entry was extracted")
	}
	// Extracting again renames the destination instead of overwriting.
	_, task = ivan.do("POST", "/api/tasks", map[string]any{"kind": "extract", "sources": []string{"/user/ivan/photos.zip"}})
	if d := ivan.waitTask(task["id"].(string)); d["result"].([]any)[0] != "/user/ivan/photos (1)" {
		t.Fatalf("second extract: %v", d)
	}

	// Zip Slip: every escaping entry is refused and nothing lands outside.
	for i, evil := range []string{"../escape.txt", "../../judy/pwned.txt", "/abs.txt", "a/../../../x.txt", ".easycloud/../../y.txt"} {
		name := filepath.Join(e.data, "user/ivan", "evil"+string(rune('0'+i))+".zip")
		os.WriteFile(name, makeZip(t, map[string]string{"ok.txt": "fine", evil: "EVIL"}, ""), 0o644)
		_, task := ivan.do("POST", "/api/tasks", map[string]any{"kind": "extract", "sources": []string{"/user/ivan/" + filepath.Base(name)}})
		d := ivan.waitTask(task["id"].(string))
		if d["status"] != "failed" || !strings.Contains(d["message"].(string), "unsafe entry") {
			t.Errorf("zip slip %q: %v", evil, d)
		}
	}
	for _, p := range []string{"user/escape.txt", "user/judy/pwned.txt", "abs.txt", "x.txt", "y.txt", "escape.txt"} {
		if _, err := os.Stat(filepath.Join(e.data, p)); err == nil {
			t.Errorf("zip slip wrote %s", p)
		}
	}
	// Same for tar.gz.
	var tbuf bytes.Buffer
	gz := gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../../tar-escape.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("EVIL"))
	tw.Close()
	gz.Close()
	os.WriteFile(filepath.Join(e.data, "user/ivan/evil.tar.gz"), tbuf.Bytes(), 0o644)
	_, task = ivan.do("POST", "/api/tasks", map[string]any{"kind": "extract", "sources": []string{"/user/ivan/evil.tar.gz"}})
	if d := ivan.waitTask(task["id"].(string)); d["status"] != "failed" {
		t.Errorf("tar slip: %v", d)
	}
	if _, err := os.Stat(filepath.Join(e.data, "tar-escape.txt")); err == nil {
		t.Error("tar slip escaped")
	}

	// Quota applies to extraction (100 KB quota, 200 KB archive).
	os.WriteFile(filepath.Join(e.data, "user/ivan/big.zip"), makeZip(t, map[string]string{"big.bin": strings.Repeat("x", 200_000)}, ""), 0o644)
	_, task = ivan.do("POST", "/api/tasks", map[string]any{"kind": "extract", "sources": []string{"/user/ivan/big.zip"}})
	if d := ivan.waitTask(task["id"].(string)); d["status"] != "failed" || !strings.Contains(d["message"].(string), "QUOTA_EXCEEDED") {
		t.Errorf("quota on extract: %v", d)
	}

	// Copy task with progress; other users never see it or can cancel it.
	e.write("share/big/data.bin", strings.Repeat("z", 3<<20))
	admin.do("POST", "/api/admin/grants", map[string]any{"subjectType": "user", "subjectId": ui["id"], "path": "/share/big", "perm": "r"})
	admin.do("PATCH", "/api/admin/users/"+ui["id"].(string), map[string]any{"quotaBytes": 0})
	_, task = ivan.do("POST", "/api/tasks", map[string]any{"kind": "copy", "sources": []string{"/share/big"}, "dest": "/user/ivan"})
	id := task["id"].(string)
	if c, _ := judy.do("POST", "/api/tasks/"+id+"/cancel", nil); c != 404 {
		t.Errorf("judy cancelling ivan's task: %d", c)
	}
	if _, l := judy.do("GET", "/api/tasks", nil); len(l["items"].([]any)) != 0 {
		t.Error("judy sees ivan's tasks")
	}
	d := ivan.waitTask(id)
	if d["status"] != "done" || d["doneBytes"] != float64(3<<20) {
		t.Fatalf("copy task: %v", d)
	}
	if fi, err := os.Stat(filepath.Join(e.data, "user/ivan/big/data.bin")); err != nil || fi.Size() != 3<<20 {
		t.Fatal("copied file")
	}
	// Tasks cannot reach paths the user cannot read.
	if c, _ := judy.do("POST", "/api/tasks", map[string]any{"kind": "copy", "sources": []string{"/user/ivan/big"}, "dest": "/user/judy"}); c != 404 {
		t.Errorf("judy copying ivan's files: %d", c)
	}
	if c, _ := ivan.do("DELETE", "/api/tasks", nil); c != 204 {
		t.Error("clear finished")
	}
}

func TestProtectedSystemDirectories(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	admin.do("POST", "/api/admin/users", map[string]any{"username": "kate", "password": "katepass1"})
	e.write("user/orphan/x.txt", "not an account") // a folder under /user that is not a personal space
	e.write("other/keep.txt", "x")

	_, l := admin.do("GET", "/api/files/list?path=/user", nil)
	flags := map[string]bool{}
	for _, x := range l["entries"].([]any) {
		m := x.(map[string]any)
		flags[m["name"].(string)] = m["protected"] == true
	}
	if !flags["kate"] || !flags["admin"] || flags["orphan"] {
		t.Fatalf("protected flags: %v", flags)
	}
	_, root := admin.do("GET", "/api/files/list?path=/", nil)
	for _, x := range root["entries"].([]any) {
		if m := x.(map[string]any); m["name"] == "user" && m["protected"] != true {
			t.Fatal("/user not flagged")
		}
	}
	// Even the administrator cannot delete, move or rename them — through any API.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/files/trash", map[string]any{"paths": []string{"/user"}}},
		{"POST", "/api/files/trash", map[string]any{"paths": []string{"/user/kate"}}},
		{"POST", "/api/files/rename", map[string]string{"path": "/user/kate", "name": "kate2"}},
		{"POST", "/api/files/rename", map[string]string{"path": "/user", "name": "users"}},
		{"POST", "/api/files/move", map[string]any{"sources": []string{"/user/kate"}, "dest": "/user/orphan"}},
		{"POST", "/api/tasks", map[string]any{"kind": "move", "sources": []string{"/user"}, "dest": "/other"}},
	} {
		code, body := admin.do(c.method, c.path, c.body)
		if c.path == "/api/tasks" {
			if code != 202 {
				t.Fatalf("task create: %d", code)
			}
			if d := admin.waitTask(body["id"].(string)); d["status"] != "failed" || !strings.Contains(d["message"].(string), "PROTECTED_PATH") {
				t.Errorf("move task on /user: %v", d)
			}
			continue
		}
		if code != 403 || body["code"] != "PROTECTED_PATH" {
			t.Errorf("%s %v: %d %v", c.path, c.body, code, body)
		}
	}
	// A WebDAV config sharing /user cannot remove or move a personal space inside it either.
	d, _ := davConfig(t, e, admin, "homes", []map[string]string{{"path": "/user", "perm": "rw"}})
	if c, _ := d.do("DELETE", "/user/kate", "", nil); c < 400 {
		t.Errorf("DAV delete personal space: %d", c)
	}
	if c, _ := d.do("MOVE", "/user/kate", "", map[string]string{"Destination": d.base + "/user/kate-moved"}); c < 400 {
		t.Errorf("DAV move personal space: %d", c)
	}
	for _, p := range []string{"user", "user/kate", "user/admin"} {
		if _, err := os.Stat(filepath.Join(e.data, p)); err != nil {
			t.Errorf("%s is gone", p)
		}
	}
	// Ordinary folders, including non-account ones under /user, still behave normally.
	if c, _ := admin.do("POST", "/api/files/trash", map[string]any{"paths": []string{"/user/orphan"}}); c != 200 {
		t.Errorf("trash /user/orphan: %d", c)
	}
	if c, _ := admin.do("POST", "/api/files/mkdir", map[string]string{"path": "/user/kate/docs"}); c != 201 {
		t.Errorf("content inside a personal space must stay editable: %d", c)
	}
	if c, _ := admin.do("POST", "/api/files/trash", map[string]any{"paths": []string{"/user/kate/docs"}}); c != 200 {
		t.Errorf("trash inside personal space: %d", c)
	}
}
