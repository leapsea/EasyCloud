package server_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An upload whose data is complete but whose placement failed (the folder was
// not writable at that moment) must not stay stuck: tus clients resume with a
// HEAD, see offset == length and report success without another request. The
// HEAD retries the placement; if that fails again the stale upload is dropped
// with 404 so the client starts over and gets the real error.
func TestStaleUploadIsPlacedOnResume(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission bits do not apply to root")
	}
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	e.write("share/drop/keep.txt", "x")
	dir := filepath.Join(e.data, "share", "drop")

	send := func(method, url string, hdr map[string]string, body []byte) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Tus-Resumable", "1.0.0")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	stuck := func(name string) string {
		t.Helper()
		res := send("POST", e.ts.URL+"/api/upload/", map[string]string{"Upload-Length": "5",
			"Upload-Metadata": "filename " + b64(name) + ",dir " + b64("/share/drop") + ",onConflict " + b64("rename")}, nil)
		if res.StatusCode != 201 {
			t.Fatalf("create: %d", res.StatusCode)
		}
		loc := res.Header.Get("Location")
		if !strings.HasPrefix(loc, "http") {
			loc = e.ts.URL + loc
		}
		os.Chmod(dir, 0o555) // the folder becomes unwritable before the data arrives
		res = send("PATCH", loc, map[string]string{"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"}, []byte("hello"))
		if res.StatusCode != 403 {
			t.Fatalf("placement into a read-only folder: %d, want 403", res.StatusCode)
		}
		return loc
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	// Fixed meanwhile: the resume HEAD places the file.
	loc := stuck("a.txt")
	os.Chmod(dir, 0o755)
	res := send("HEAD", loc, nil, nil)
	if res.StatusCode != 200 || res.Header.Get("Upload-Offset") != "5" || res.Header.Get("X-Easycloud-Path") != "/share/drop/a.txt" {
		t.Fatalf("resume HEAD: %d offset %q path %q", res.StatusCode, res.Header.Get("Upload-Offset"), res.Header.Get("X-Easycloud-Path"))
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "hello" {
		t.Fatalf("placed file: %q", b)
	}
	if res := send("HEAD", loc, nil, nil); res.StatusCode != 404 {
		t.Fatalf("HEAD after placement: %d, want 404", res.StatusCode)
	}

	// Still broken: the stale upload is removed and the client told to start over.
	loc = stuck("b.txt")
	if res := send("HEAD", loc, nil, nil); res.StatusCode != 404 {
		t.Fatalf("resume HEAD, folder still read-only: %d, want 404", res.StatusCode)
	}
	id := loc[strings.LastIndex(loc, "/")+1:]
	if _, err := os.Stat(filepath.Join(e.data, ".easycloud", "uploads", id)); !os.IsNotExist(err) {
		t.Fatalf("stale upload data left behind: %v", err)
	}
	// And a fresh upload is refused before any data is sent.
	res = send("POST", e.ts.URL+"/api/upload/", map[string]string{"Upload-Length": "5",
		"Upload-Metadata": "filename " + b64("c.txt") + ",dir " + b64("/share/drop")}, nil)
	if res.StatusCode != 403 {
		t.Fatalf("create into read-only folder: %d, want 403", res.StatusCode)
	}
}
