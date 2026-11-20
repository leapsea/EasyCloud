package server_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A video with a same-name picture next to it gets that picture as its
// thumbnail, even without a frame-rendering plugin; the category response
// says when no plugin can render frames.
func TestVideoCovers(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	img.Set(1, 1, color.White)
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	e.write("share/v/movie.mp4", "not really a video")
	e.write("share/v/movie.jpg", buf.String())
	e.write("share/v/clip.MKV", "x")
	e.write("share/v/clip-poster.jpg", buf.String())
	e.write("share/v/plain.mp4", "x")

	var items []any
	var body map[string]any
	waitFor(t, "index", func() bool {
		_, body = admin.do("GET", "/api/files/category?kind=video", nil)
		items, _ = body["items"].([]any)
		return len(items) == 3
	})
	if body["videoFrames"] != false {
		t.Fatalf("videoFrames = %v, want false (no plugin)", body["videoFrames"])
	}
	thumb := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]any)
		thumb[m["name"].(string)], _ = m["thumb"].(bool)
	}
	if !thumb["movie.mp4"] || !thumb["clip.MKV"] || thumb["plain.mp4"] {
		t.Fatalf("thumb flags = %v", thumb)
	}
	res, b := admin.raw("GET", "/api/files/thumb?size=128&path=/share/v/movie.mp4", nil, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover thumb: %d %s", res.StatusCode, b)
	}
	if res, _ := admin.raw("GET", "/api/files/thumb?size=128&path=/share/v/plain.mp4", nil, ""); res.StatusCode != 415 {
		t.Fatalf("video without cover or plugin: %d, want 415", res.StatusCode)
	}

	// Replacing the cover replaces the thumbnail (new ETag).
	r1, _ := admin.raw("GET", "/api/files/thumb?size=128&path=/share/v/movie.mp4", nil, "")
	big := image.NewRGBA(image.Rect(0, 0, 80, 20))
	buf.Reset()
	jpeg.Encode(&buf, big, nil)
	later := time.Now().Add(2 * time.Second)
	p := filepath.Join(e.data, "share", "v", "movie.jpg")
	os.WriteFile(p, buf.Bytes(), 0o644)
	os.Chtimes(p, later, later)
	r2, b2 := admin.raw("GET", "/api/files/thumb?size=128&path=/share/v/movie.mp4", nil, "")
	if r2.StatusCode != 200 || r1.Header.Get("ETag") == r2.Header.Get("ETag") {
		t.Fatalf("changed cover kept the old thumbnail: %d %q %q", r2.StatusCode, r1.Header.Get("ETag"), r2.Header.Get("ETag"))
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b2)); err != nil || cfg.Width != 80 { // the new 80x20 cover (Fit never upscales)
		t.Fatalf("new thumbnail: %v %+v", err, cfg)
	}

	// The file listing marks the same files.
	_, list := admin.do("GET", "/api/files/list?path=/share/v", nil)
	if _, ok := list["entries"].([]any); !ok {
		t.Fatalf("listing: %v", list)
	}
	seen := false
	for _, it := range list["entries"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "movie.mp4" {
			seen = true
		}
		if m["name"] == "movie.mp4" && m["thumb"] != true {
			t.Fatalf("listing: movie.mp4 thumb = %v", m["thumb"])
		}
	}
	if !seen {
		t.Fatal("listing lacks movie.mp4")
	}
}
