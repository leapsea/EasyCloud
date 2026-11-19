package server_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/plugin"
)

func (c *client) raw(method, path string, body io.Reader, ctype string) (*http.Response, []byte) {
	c.e.t.Helper()
	req, _ := http.NewRequest(method, c.e.ts.URL+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func writePNG(t *testing.T, path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.RGBA{255, 0, 0, 255})
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, _ := os.Create(path)
	png.Encode(f, img)
	f.Close()
}

func TestThumbnailsAndTokens(t *testing.T) {
	e := newEnv(t)
	writePNG(t, filepath.Join(e.data, "share/pics/big.png"), 1200, 800)
	e.write("share/pics/notes.txt", "hello world, this is a file for range requests")
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, ua := admin.do("POST", "/api/admin/users", map[string]any{"username": "dave", "password": "davepass1"})

	res, body := admin.raw("GET", "/api/files/thumb?size=256&path="+q("/share/pics/big.png"), nil, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d %s", res.StatusCode, body)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 170 {
		t.Fatalf("thumb size %v %v", img.Bounds(), err)
	}
	if res, _ := admin.raw("GET", "/api/files/thumb?size=256&path="+q("/share/pics/notes.txt"), nil, ""); res.StatusCode != 415 {
		t.Fatalf("thumb of text: %d", res.StatusCode)
	}
	dave := e.client()
	dave.login("dave", "davepass1")
	if res, _ := dave.raw("GET", "/api/files/thumb?size=256&path="+q("/share/pics/big.png"), nil, ""); res.StatusCode != 404 {
		t.Fatalf("thumb without grant: %d", res.StatusCode)
	}
	// Listing carries dimensions (from the index) and the thumb flag.
	waitFor(t, "dimensions in listing", func() bool {
		_, l := admin.do("GET", "/api/files/list?path=/share/pics", nil)
		for _, x := range l["entries"].([]any) {
			m := x.(map[string]any)
			if m["name"] == "big.png" {
				return m["width"] == float64(1200) && m["thumb"] == true
			}
		}
		return false
	})

	// Capability URL works without cookies and supports Range.
	_, tok := admin.do("POST", "/api/files/token", map[string]string{"path": "/share/pics/notes.txt"})
	url := tok["url"].(string)
	anon := &http.Client{}
	req, _ := http.NewRequest("GET", e.ts.URL+url, nil)
	req.Header.Set("Range", "bytes=6-10")
	r2, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 206 || string(b) != "world" || r2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("raw range: %d %q", r2.StatusCode, b)
	}
	// Dave's token stops working once his account is disabled.
	admin.do("POST", "/api/admin/grants", map[string]any{"subjectType": "user", "subjectId": ua["id"], "path": "/share/pics", "perm": "r"})
	_, dtok := dave.do("POST", "/api/files/token", map[string]string{"path": "/share/pics/notes.txt"})
	durl := dtok["url"].(string)
	if r, _ := anon.Get(e.ts.URL + durl); r.StatusCode != 200 {
		t.Fatalf("dave raw: %d", r.StatusCode)
	}
	admin.do("PATCH", "/api/admin/users/"+ua["id"].(string), map[string]any{"disabled": true})
	if r, _ := anon.Get(e.ts.URL + durl); r.StatusCode != 404 {
		t.Fatalf("raw after disable: %d", r.StatusCode)
	}
	if r, _ := anon.Get(e.ts.URL + strings.Replace(url, "/api/raw/", "/api/raw/x", 1)); r.StatusCode != 404 {
		t.Fatalf("tampered token: %d", r.StatusCode)
	}
}

func signedPackage(t *testing.T, dir string, priv ed25519.PrivateKey) []byte {
	data, err := plugin.Build(dir, priv)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPluginsAndOffice(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e := newEnv(t, func(c *config.Config) { c.Plugins.TrustedKeys = base64.StdEncoding.EncodeToString(pub) })
	admin := e.client()
	admin.login("admin", "adminpass1")

	// Unsigned packages are refused; signed ones install.
	unsigned, _ := plugin.Build("../../plugins/markdown-viewer", nil)
	if res, _ := admin.raw("POST", "/api/admin/plugins", bytes.NewReader(unsigned), "application/zip"); res.StatusCode != 403 {
		t.Fatalf("unsigned install: %d", res.StatusCode)
	}
	if res, b := admin.raw("POST", "/api/admin/plugins", bytes.NewReader(signedPackage(t, "../../plugins/markdown-viewer", priv)), "application/zip"); res.StatusCode != 201 {
		t.Fatalf("install viewer: %d %s", res.StatusCode, b)
	}
	_, v := admin.do("GET", "/api/plugins/viewers", nil)
	entry := v["items"].([]any)[0].(map[string]any)["entry"].(string)
	res, js := admin.raw("GET", entry, nil, "")
	if res.StatusCode != 200 || !strings.Contains(string(js), "export default") || res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("viewer asset: %d", res.StatusCode)
	}
	if res, _ := admin.raw("GET", "/plugins/markdown-viewer/1.0.0/manifest.json", nil, ""); res.StatusCode != 404 {
		t.Fatal("manifest should not be served")
	}
	if res, _ := admin.raw("GET", "/plugins/markdown-viewer/1.0.0/../../../etc/passwd", nil, ""); res.StatusCode != 404 {
		t.Fatal("traversal in plugin assets")
	}

	// Office connector against a fake Document Server.
	const secret = "office-secret"
	saved := []byte("NEW DOCX CONTENT")
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(saved) }))
	defer ds.Close()
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("EVIL")) }))
	defer evil.Close()

	if res, b := admin.raw("POST", "/api/admin/plugins", bytes.NewReader(signedPackage(t, "../../plugins/onlyoffice", priv)), "application/zip"); res.StatusCode != 201 {
		t.Fatalf("install connector: %d %s", res.StatusCode, b)
	}
	if code, b := admin.do("PATCH", "/api/admin/plugins/onlyoffice", map[string]any{"config": map[string]any{"documentServerUrl": ds.URL, "jwtSecret": secret}}); code != 204 {
		t.Fatalf("configure: %d %v", code, b)
	}
	_, list := admin.do("GET", "/api/admin/plugins", nil)
	for _, it := range list["items"].([]any) {
		m := it.(map[string]any)
		if m["manifest"].(map[string]any)["id"] == "onlyoffice" && m["config"].(map[string]any)["jwtSecret"] != "********" {
			t.Fatal("secret not masked")
		}
	}
	e.write("share/docs/report.docx", "OLD")
	code, sess := admin.do("POST", "/api/office/session", map[string]string{"path": "/share/docs/report.docx"})
	if code != 200 {
		t.Fatalf("session: %d %v", code, sess)
	}
	cfg := sess["config"].(map[string]any)
	if cfg["editorConfig"].(map[string]any)["mode"] != "edit" || cfg["documentType"] != "word" || !verifyHS256(cfg["token"].(string), secret) {
		t.Fatalf("editor config %v", cfg)
	}
	// The document URL is fetchable by the Document Server without cookies.
	docURL := cfg["document"].(map[string]any)["url"].(string)
	if r, _ := http.Get(docURL); r.StatusCode != 200 {
		t.Fatalf("document url: %d", r.StatusCode)
	}
	cb := cfg["editorConfig"].(map[string]any)["callbackUrl"].(string)
	post := func(body map[string]any, jwtSecret string) (int, string) {
		body["token"] = hs256(body, jwtSecret)
		b, _ := json.Marshal(body)
		r, err := http.Post(cb, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(out)
	}
	if code, _ := post(map[string]any{"status": 2, "url": ds.URL + "/f"}, "wrong-secret"); code != 403 {
		t.Fatalf("bad jwt: %d", code)
	}
	if _, out := post(map[string]any{"status": 2, "url": evil.URL + "/f"}, secret); out != `{"error":1}` {
		t.Fatalf("SSRF guard: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "share/docs/report.docx")); string(b) != "OLD" {
		t.Fatal("file changed by foreign host")
	}
	if _, out := post(map[string]any{"status": 1}, secret); out != `{"error":0}` {
		t.Fatalf("status 1: %s", out)
	}
	if _, out := post(map[string]any{"status": 2, "url": ds.URL + "/f"}, secret); out != `{"error":0}` {
		t.Fatalf("save: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(e.data, "share/docs/report.docx")); string(b) != string(saved) {
		t.Fatalf("saved content %q", b)
	}
}

func hs256(claims map[string]any, secret string) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	c := map[string]any{}
	for k, v := range claims {
		if k != "token" {
			c[k] = v
		}
	}
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(h + "." + p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func verifyHS256(tok, secret string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(parts[0] + "." + parts[1]))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) == parts[2]
}
