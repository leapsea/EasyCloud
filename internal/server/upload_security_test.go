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

// Regression: one user must not be able to read, append to, terminate or
// reassemble another user's tus upload — neither directly, nor disguised as
// "POST + X-HTTP-Method-Override" (tusd rewrites the method after our owner
// check ran), nor through the concatenation extension (a "final" upload names
// other uploads by ID and tusd does not check their owner).
func TestUploadsAreOwnerOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	admin.do("POST", "/api/admin/users", map[string]any{"username": "bob", "password": "bobpass12"})
	alice, bob := e.client(), e.client()
	alice.login("alice", "alicepass1")
	bob.login("bob", "bobpass12")

	meta := func(kv ...string) string {
		var parts []string
		for i := 0; i < len(kv); i += 2 {
			parts = append(parts, kv[i]+" "+base64.StdEncoding.EncodeToString([]byte(kv[i+1])))
		}
		return strings.Join(parts, ",")
	}
	send := func(c *client, method, url string, hdr map[string]string, body []byte) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Tus-Resumable", "1.0.0")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	abs := func(loc string) string {
		if strings.HasPrefix(loc, "http") {
			return loc
		}
		return e.ts.URL + loc
	}
	base := e.ts.URL + "/api/upload/"
	create := func(name string, size string, conflict string) string {
		t.Helper()
		res := send(bob, "POST", base, map[string]string{"Upload-Length": size,
			"Upload-Metadata": meta("filename", name, "dir", "/user/bob", "onConflict", conflict)}, nil)
		if res.StatusCode != 201 {
			t.Fatalf("bob create %s: %d", name, res.StatusCode)
		}
		return abs(res.Header.Get("Location"))
	}
	patch := map[string]string{"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"}

	// A finished upload whose placement failed (name taken), so its data stays
	// in the upload area: the target of the concatenation attack.
	e.write("user/bob/secret.txt", "old")
	stuck := create("secret.txt", "6", "fail")
	if res := send(bob, "PATCH", stuck, patch, []byte("SECRET")); res.StatusCode != 409 {
		t.Fatalf("placement should fail with a conflict, got %d", res.StatusCode)
	}
	live := create("big.bin", "10", "rename") // in progress

	for _, m := range []string{"HEAD", "PATCH", "DELETE"} {
		if res := send(alice, m, live, patch, nil); res.StatusCode != 404 {
			t.Errorf("alice %s bob's upload: %d, want 404", m, res.StatusCode)
		}
		if res := send(alice, "POST", live, map[string]string{"X-HTTP-Method-Override": m}, nil); res.StatusCode != 400 {
			t.Errorf("alice POST+override %s: %d, want 400", m, res.StatusCode)
		}
	}
	if res := send(alice, "POST", live, nil, nil); res.StatusCode != 404 {
		t.Errorf("alice POST to bob's upload URL: %d, want 404", res.StatusCode)
	}
	// The owner cannot use the override either (the client never needs it).
	if res := send(bob, "POST", live, map[string]string{"X-HTTP-Method-Override": "HEAD"}, nil); res.StatusCode != 400 {
		t.Errorf("bob POST+override: %d, want 400", res.StatusCode)
	}

	// Concatenation is not offered and is refused.
	stuckPath := strings.TrimPrefix(stuck, e.ts.URL)
	res := send(alice, "POST", base, map[string]string{"Upload-Concat": "final;" + stuckPath,
		"Upload-Metadata": meta("filename", "stolen.txt", "dir", "/user/alice")}, nil)
	if res.StatusCode != 400 {
		t.Fatalf("concatenation: %d, want 400", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.data, "user", "alice", "stolen.txt")); err == nil {
		t.Fatal("alice assembled bob's upload into her space")
	}
	if res := send(alice, "POST", base, map[string]string{"Upload-Concat": "partial", "Upload-Length": "4",
		"Upload-Metadata": meta("filename", "p.bin", "dir", "/user/alice")}, nil); res.StatusCode != 400 {
		t.Fatalf("partial upload: %d, want 400", res.StatusCode)
	}
	opt := send(alice, "OPTIONS", base, nil, nil)
	if strings.Contains(opt.Header.Get("Tus-Extension"), "concatenation") {
		t.Fatalf("concatenation still advertised: %q", opt.Header.Get("Tus-Extension"))
	}

	// The owner's own uploads are unaffected.
	if res := send(bob, "HEAD", live, nil, nil); res.StatusCode != 200 || res.Header.Get("Upload-Offset") != "0" {
		t.Fatalf("bob HEAD own upload: %d offset %q", res.StatusCode, res.Header.Get("Upload-Offset"))
	}
	if res := send(bob, "PATCH", live, patch, []byte("0123456789")); res.StatusCode != 204 {
		t.Fatalf("bob finishes own upload: %d", res.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "user", "bob", "big.bin")); string(b) != "0123456789" {
		t.Fatalf("bob's file: %q", b)
	}
	other := create("gone.bin", "5", "rename")
	if res := send(bob, "DELETE", other, nil, nil); res.StatusCode != 204 {
		t.Fatalf("bob terminates own upload: %d", res.StatusCode)
	}
}
