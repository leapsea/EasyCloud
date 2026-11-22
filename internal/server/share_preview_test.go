package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Online previews: offered only for shares without a download limit, never
// counted as downloads, and viewer plugins get a capability URL limited to
// one file of the share.
func TestSharePreview(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	admin.do("POST", "/api/admin/users", map[string]any{"username": "erin", "password": "erinpass1"})
	e.write("user/erin/album/a.txt", "AAAA")
	e.write("user/erin/secret.txt", "TOP SECRET")
	erin := e.client()
	erin.login("erin", "erinpass1")

	post := func(url string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		res, err := http.Post(url, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	meta := func(base string) map[string]any {
		t.Helper()
		_, b, _ := anonGet(t, base+"/", nil)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}

	// No download limit: previewable, inline reads are not downloads.
	_, open := erin.do("POST", "/api/shares", map[string]any{"path": "/user/erin/album"})
	base := e.ts.URL + "/api/s/" + open["id"].(string)
	if m := meta(base); m["preview"] != true {
		t.Fatalf("unlimited share: preview = %v", m["preview"])
	}
	for i := 0; i < 3; i++ {
		if c, b, _ := anonGet(t, base+"/download?path=/a.txt&inline=1", nil); c != 200 || string(b) != "AAAA" {
			t.Fatalf("inline read: %d %q", c, b)
		}
	}
	_, list := erin.do("GET", "/api/shares", nil)
	for _, it := range list["items"].([]any) {
		if m := it.(map[string]any); m["id"] == open["id"] && m["downloads"].(float64) != 0 {
			t.Fatalf("previews counted as downloads: %v", m["downloads"])
		}
	}

	// Viewer plugins: a raw URL for that one file, readable cross-origin.
	c, tok := post(base+"/token", map[string]string{"path": "/a.txt"})
	if c != 200 || !strings.HasPrefix(tok["url"].(string), "/api/raw/") {
		t.Fatalf("preview token: %d %v", c, tok)
	}
	if c, b, h := anonGet(t, e.ts.URL+tok["url"].(string), nil); c != 200 || string(b) != "AAAA" || h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("raw via share token: %d %q", c, b)
	}
	if c, _ := post(base+"/token", map[string]string{"path": "/../secret.txt"}); c == 200 {
		t.Fatal("token for a file outside the share")
	}

	// Download limit: download-only, no preview, no token, inline counts.
	_, lim := erin.do("POST", "/api/shares", map[string]any{"path": "/user/erin/album", "maxDownloads": 1})
	lbase := e.ts.URL + "/api/s/" + lim["id"].(string)
	if m := meta(lbase); m["preview"] != false {
		t.Fatalf("limited share: preview = %v", m["preview"])
	}
	if c, out := post(lbase+"/token", map[string]string{"path": "/a.txt"}); c != 403 || out["code"] != "SHARE_PREVIEW_DISABLED" {
		t.Fatalf("token on a limited share: %d %v", c, out)
	}
	anonGet(t, lbase+"/download?path=/a.txt&inline=1", nil)
	if c, _, _ := anonGet(t, lbase+"/download?path=/a.txt", nil); c == 200 {
		t.Fatal("inline read on a limited share was not counted")
	}
}
