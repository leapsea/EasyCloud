package server_test

import (
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFileDetails(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	dir := filepath.Join(e.data, "share", "d")
	os.MkdirAll(dir, 0o755)

	img, _ := os.Create(filepath.Join(dir, "pic.png"))
	png.Encode(img, image.NewRGBA(image.Rect(0, 0, 64, 48)))
	img.Close()

	details := func(c *client, p string) (int, map[string]any) {
		return c.do("GET", "/api/files/details?path="+q(p), nil)
	}
	code, d := details(admin, "/share/d/pic.png")
	if code != 200 || d["name"] != "pic.png" {
		t.Fatalf("pic: %d %v", code, d)
	}
	if im, _ := d["image"].(map[string]any); im == nil || im["width"] != 64.0 || im["height"] != 48.0 || im["format"] != "png" {
		t.Fatalf("image info: %v", d["image"])
	}
	// Directories have details too (no media), including their timestamps.
	if code, d := details(admin, "/share/d"); code != 200 || d["isDir"] != true || d["media"] != nil || d["modTime"] == nil {
		t.Fatalf("dir: %d %v", code, d)
	}

	if _, err := exec.LookPath("ffmpeg"); err == nil {
		out := filepath.Join(dir, "clip.mp4")
		if b, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=25", "-f", "lavfi", "-i", "sine",
			"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", out).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, b)
		}
		code, d := details(admin, "/share/d/clip.mp4")
		m, _ := d["media"].(map[string]any)
		if code != 200 || m == nil {
			t.Fatalf("clip: %d %v", code, d)
		}
		v, _ := m["video"].(map[string]any)
		a, _ := m["audio"].(map[string]any)
		if v == nil || v["width"] != 160.0 || v["height"] != 120.0 || v["fps"] != 25.0 || v["codec"] == nil {
			t.Fatalf("video info: %v", m)
		}
		if a == nil || a["sampleRate"] != 44100.0 {
			t.Fatalf("audio info: %v", m)
		}
		if dur, _ := m["duration"].(float64); dur < 1.9 || dur > 2.2 {
			t.Fatalf("duration %v", m["duration"])
		}
	}

	// Same authorization as everything else.
	admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	alice := e.client()
	alice.login("alice", "alicepass1")
	if code, _ := details(alice, "/share/d/pic.png"); code != 404 {
		t.Fatalf("alice: %d", code)
	}
}
