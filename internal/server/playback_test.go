package server_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The player endpoints: a file that browsers would struggle with is streamed
// as a fixed virtual file, the original stays untouched, and access follows
// the same grants as downloads.
func TestVideoPlaybackFix(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")

	dir := filepath.Join(e.data, "share", "v")
	os.MkdirAll(dir, 0o755)
	encode := func(name string, extra ...string) string {
		out := filepath.Join(dir, name)
		args := append([]string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=30", "-t", "2",
			"-c:v", "libx264", "-bf", "2", "-pix_fmt", "yuv420p"}, extra...)
		if b, err := exec.Command("ffmpeg", append(args, out)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, b)
		}
		return out
	}
	tail := encode("tail.mp4")                                                         // moov after the media data
	encode("ready.mp4", "-movflags", "+faststart")                                     // nothing to fix
	os.WriteFile(filepath.Join(dir, "fake.mp4"), bytes.Repeat([]byte{1}, 1000), 0o644) // not a real MP4
	before, _ := os.ReadFile(tail)

	info := func(c *client, name string) (int, map[string]any) {
		return c.do("GET", "/api/files/playback?path="+q("/share/v/"+name), nil)
	}
	if code, body := info(admin, "tail.mp4"); code != 200 || body["fixed"] != true || !strings.Contains(strings.Join(toStrings(body["reasons"]), ","), "faststart") {
		t.Fatalf("tail.mp4: %d %v", code, body)
	}
	for _, name := range []string{"ready.mp4", "fake.mp4"} {
		if code, body := info(admin, name); code != 200 || body["fixed"] != false {
			t.Fatalf("%s: %d %v", name, code, body)
		}
	}

	get := func(c *client, name, rng string) *http.Response {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/files/stream?path="+q("/share/v/"+name), nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		res, err := c.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Range works on the virtual file, and the header now comes first.
	res := get(admin, "tail.mp4", "bytes=0-4095")
	head, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || res.Header.Get("X-EasyCloud-Fixed") == "" || string(head[4:8]) != "ftyp" {
		t.Fatalf("range: %d %q %q", res.StatusCode, res.Header.Get("X-EasyCloud-Fixed"), head[:8])
	}
	if m, d := bytes.Index(head, []byte("moov")), bytes.Index(head, []byte("mdat")); m < 0 || (d >= 0 && d < m) {
		t.Fatal("moov is not ahead of mdat")
	}
	// The whole virtual file carries exactly the original audio/video data.
	res = get(admin, "tail.mp4", "")
	fixed := filepath.Join(t.TempDir(), "fixed.mp4")
	out, _ := os.Create(fixed)
	io.Copy(out, res.Body)
	out.Close()
	res.Body.Close()
	hash := func(f string) string {
		b, err := exec.Command("ffmpeg", "-v", "error", "-i", f, "-map", "0", "-c", "copy", "-f", "streamhash", "-hash", "md5", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if hash(fixed) != hash(tail) {
		t.Fatal("fixed stream changed the media data")
	}
	if after, _ := os.ReadFile(tail); !bytes.Equal(before, after) {
		t.Fatal("the source file was modified")
	}
	// Files that need nothing are streamed unchanged.
	res = get(admin, "ready.mp4", "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if orig, _ := os.ReadFile(filepath.Join(dir, "ready.mp4")); !bytes.Equal(body, orig) || res.Header.Get("X-EasyCloud-Fixed") != "" {
		t.Fatal("ready.mp4 was not streamed as is")
	}
	// The analysis is cached on disk.
	if m, _ := filepath.Glob(filepath.Join(e.app, "playback", "*.plan")); len(m) != 1 {
		t.Fatalf("cached plans: %v", m)
	}

	// Saving the fixed copy: a task writes "<name>-fix.mp4" next to the original,
	// byte for byte the stream the player gets, and never replaces anything.
	streamed, _ := os.ReadFile(fixed)
	runTask := func(c *client, name string) map[string]any {
		t.Helper()
		code, task := c.do("POST", "/api/tasks", map[string]any{"kind": "videofix", "sources": []string{"/share/v/" + name}})
		if code != 202 {
			t.Fatalf("videofix %s: %d %v", name, code, task)
		}
		var final map[string]any
		waitFor(t, "videofix task", func() bool {
			_, l := c.do("GET", "/api/tasks", nil)
			for _, it := range l["items"].([]any) {
				m := it.(map[string]any)
				if m["id"] == task["id"] && m["status"] != "queued" && m["status"] != "running" {
					final = m
					return true
				}
			}
			return false
		})
		return final
	}
	if task := runTask(admin, "tail.mp4"); task["status"] != "done" || toStrings(task["result"])[0] != "/share/v/tail-fix.mp4" {
		t.Fatalf("videofix: %v", task)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "tail-fix.mp4"))
	if err != nil || !bytes.Equal(saved, streamed) {
		t.Fatalf("saved copy differs from the fixed stream (%d vs %d bytes, %v)", len(saved), len(streamed), err)
	}
	if hash(filepath.Join(dir, "tail-fix.mp4")) != hash(tail) {
		t.Fatal("saved copy changed the media data")
	}
	if after, _ := os.ReadFile(tail); !bytes.Equal(before, after) {
		t.Fatal("the source file was modified by saving")
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, ".~easycloud-*")); len(tmp) != 0 {
		t.Fatalf("temporary files left: %v", tmp)
	}
	if task := runTask(admin, "tail.mp4"); task["status"] != "failed" || !strings.Contains(task["message"].(string), "CONFLICT") {
		t.Fatalf("second save should conflict: %v", task)
	}
	if task := runTask(admin, "ready.mp4"); task["status"] != "failed" {
		t.Fatalf("a video that needs no fix must not be saved: %v", task)
	}
	if _, err := os.Stat(filepath.Join(dir, "ready-fix.mp4")); err == nil {
		t.Fatal("ready-fix.mp4 written")
	}

	// Same authorization as downloads: no grant, no video.
	admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	alice := e.client()
	alice.login("alice", "alicepass1")
	if code, _ := info(alice, "tail.mp4"); code != 404 {
		t.Fatalf("alice playback info: %d", code)
	}
	if res := get(alice, "tail.mp4", ""); res.StatusCode != 404 {
		t.Fatalf("alice stream: %d", res.StatusCode)
	}
	if code, _ := alice.do("POST", "/api/tasks", map[string]any{"kind": "videofix", "sources": []string{"/share/v/tail.mp4"}}); code != 404 {
		t.Fatalf("alice videofix: %d", code)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
