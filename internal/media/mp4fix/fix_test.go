package mp4fix

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ffmpeg is the oracle: it writes a correct ctts for B-frame H.264, which the
// tests delete and expect Analyze to reconstruct exactly.
func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
}

func makeVideo(t *testing.T, dir string, faststart bool) string {
	t.Helper()
	out := filepath.Join(dir, "src.mp4")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30", "-f", "lavfi", "-i", "sine",
		"-t", "6", "-c:v", "libx264", "-bf", "3", "-g", "48", "-pix_fmt", "yuv420p", "-c:a", "aac"}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	if b, err := exec.Command("ffmpeg", append(args, out)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	return out
}

// stripCtts rewrites the moov (kept at the end) without the video ctts,
// reproducing the broken files seen in the wild.
func stripCtts(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	tops, err := topLevel(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	last := tops[len(tops)-1]
	if last.typ != "moov" {
		t.Fatal("expected moov at the end")
	}
	kids, err := parseBoxes(data[last.off:])
	if err != nil {
		t.Fatal(err)
	}
	moov := kids[0]
	for _, trak := range moov.children {
		if trak.typ == "trak" && handler(trak) == "vide" {
			stbl := trak.find("mdia", "minf", "stbl")
			var keep []*box
			for _, c := range stbl.children {
				if c.typ != "ctts" {
					keep = append(keep, c)
				}
			}
			if len(keep) == len(stbl.children) {
				t.Fatal("source has no ctts to strip")
			}
			stbl.children = keep
		}
	}
	out := moov.encode(append([]byte{}, data[:last.off]...))
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePlan(t *testing.T, p *Plan, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if n, err := io.Copy(out, p.Open(in)); err != nil || n != p.Size {
		t.Fatalf("copy virtual file: %d of %d bytes, %v", n, p.Size, err)
	}
}

// presentation returns the video frame timestamps in display order.
func presentation(t *testing.T, file string) []float64 {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	var pts []float64
	for _, l := range strings.Fields(string(b)) {
		v, err := strconv.ParseFloat(strings.TrimSuffix(l, ","), 64)
		if err != nil {
			t.Fatalf("pts %q", l)
		}
		pts = append(pts, v)
	}
	return pts
}

func streamHash(t *testing.T, file string) string {
	t.Helper()
	b, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-map", "0", "-c", "copy", "-f", "streamhash", "-hash", "md5", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func analyzeFile(t *testing.T, file string) (*Plan, error) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	return Analyze(f, fi.Size())
}

func TestRestoresMissingCtts(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := makeVideo(t, dir, false)
	broken := filepath.Join(dir, "broken.mp4")
	stripCtts(t, src, broken)

	p, err := analyzeFile(t, broken)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Reasons, ",") != "faststart,ctts" {
		t.Fatalf("reasons %v", p.Reasons)
	}
	fixed := filepath.Join(dir, "fixed.mp4")
	writePlan(t, p, broken, fixed)

	// Same presentation timestamps as ffmpeg's own ctts, for every packet.
	want, got := presentation(t, src), presentation(t, fixed)
	if len(want) != len(got) {
		t.Fatalf("%d packets, want %d", len(got), len(want))
	}
	for i := range want {
		if d := want[i] - got[i]; d > 0.0005 || d < -0.0005 {
			t.Fatalf("packet %d pts %.4f, want %.4f", i, got[i], want[i])
		}
	}
	// Broken input really was broken (pts == dts), so the check above means something.
	if b := presentation(t, broken); b[1] == want[1] && b[2] == want[2] && b[3] == want[3] {
		t.Fatal("stripping ctts did not change the timestamps")
	}
	// Sample data untouched, and the header now comes first.
	if streamHash(t, fixed) != streamHash(t, src) {
		t.Fatal("audio/video data changed")
	}
	if string(p.Header[4:8]) != "ftyp" || !bytes.Contains(p.Header[:64], []byte("moov")) {
		t.Fatal("moov is not right after ftyp")
	}
}

func TestFaststartOnly(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := makeVideo(t, dir, false) // has ctts, moov at the end
	p, err := analyzeFile(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Reasons, ",") != "faststart" {
		t.Fatalf("reasons %v", p.Reasons)
	}
	fixed := filepath.Join(dir, "fixed.mp4")
	writePlan(t, p, src, fixed)
	if streamHash(t, fixed) != streamHash(t, src) {
		t.Fatal("audio/video data changed")
	}
	want, got := presentation(t, src), presentation(t, fixed)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("packet %d pts changed", i)
		}
	}
}

func TestNotNeeded(t *testing.T) {
	needFFmpeg(t)
	src := makeVideo(t, t.TempDir(), true)
	if _, err := analyzeFile(t, src); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("err = %v, want ErrNotNeeded", err)
	}
}

func TestNotAnMP4(t *testing.T) {
	junk := bytes.Repeat([]byte{0xff}, 4096)
	if _, err := Analyze(bytes.NewReader(junk), int64(len(junk))); err == nil || !IsUnsupported(err) {
		t.Fatalf("err = %v, want unsupported", err)
	}
}

func TestVirtualFileReads(t *testing.T) {
	src := bytes.Repeat([]byte("0123456789"), 100)
	p := &Plan{Header: []byte("HEADER"), Parts: []Part{{Src: 10, Len: 20}, {Src: 500, Len: 7}}}
	p.Size = int64(len(p.Header)) + 27
	want := append(append([]byte("HEADER"), src[10:30]...), src[500:507]...)
	f := p.Open(bytes.NewReader(src))
	all, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(all, want) {
		t.Fatalf("read %q, %v", all, err)
	}
	// Reads spanning header/part and part/part boundaries.
	for off := 0; off < len(want); off++ {
		for n := 1; off+n <= len(want); n += 3 {
			b := make([]byte, n)
			if _, err := f.ReadAt(b, int64(off)); err != nil && err != io.EOF {
				t.Fatal(err)
			}
			if !bytes.Equal(b, want[off:off+n]) {
				t.Fatalf("ReadAt(%d, %d) = %q", off, n, b)
			}
		}
	}
}

func TestInspect(t *testing.T) {
	needFFmpeg(t)
	src := makeVideo(t, t.TempDir(), false) // 320x240, 30 fps, 6 s, mono AAC from "sine" (44.1 kHz)
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	m, err := Inspect(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	if m.Duration < 5.9 || m.Duration > 6.2 {
		t.Fatalf("duration %v", m.Duration)
	}
	var v, a *Track
	for i := range m.Tracks {
		switch m.Tracks[i].Kind {
		case "video":
			v = &m.Tracks[i]
		case "audio":
			a = &m.Tracks[i]
		}
	}
	if v == nil || v.Codec != "avc1" || v.Width != 320 || v.Height != 240 || v.FPS < 29.5 || v.FPS > 30.5 || v.Bitrate <= 0 {
		t.Fatalf("video %+v", v)
	}
	if a == nil || a.Codec != "mp4a" || a.SampleRate != 44100 || a.Channels < 1 {
		t.Fatalf("audio %+v", a)
	}
}
