package pathguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	// Attack payload regression table (as received after exactly one decode).
	bad := []string{"..", "a/../b", "./a", "a/./b", "a\\b", "a\x00b", ".easycloud", "/.easycloud/trash", "a/.easycloud", "nas/.easycloud/trash/x", // reserved at any depth (mount trash)
		"a/\x07", "\xc0\xae", "\xc0\xae\xc0\xae/etc", strings.Repeat("x", 256), "../", "/../../etc/passwd",
		"..\\..\\windows", "a/..", "a\nb", "\xff"}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
	good := map[string]string{"": ".", "/": ".", "//a///b/": "a/b", "%2e%2e": "%2e%2e", "a/.easycloud2": "a/.easycloud2"}
	for in, want := range good {
		p, err := Parse(in)
		if err != nil || p.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, p.String(), err, want)
		}
	}
}

func TestHasPrefixBySegment(t *testing.T) {
	if MustParse("share/AAB").HasPrefix(MustParse("share/AA")) {
		t.Fatal("share/AA must not prefix share/AAB")
	}
	if !MustParse("share/AA/x").HasPrefix(MustParse("share/AA")) {
		t.Fatal("expected prefix")
	}
}

func TestJoin(t *testing.T) {
	p := MustParse("a")
	if _, err := p.Join("b/c"); err == nil {
		t.Fatal("join with slash must fail")
	}
	if _, err := p.Join(".."); err == nil {
		t.Fatal("join with .. must fail")
	}
	c, _ := p.Join("b")
	if c.String() != "a/b" || p.String() != "a" {
		t.Fatalf("got %q, parent %q", c, p)
	}
}

// Successful parse implies the joined path stays under the root.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "a/..%2f", "\x00", "a//b", ".easycloud"} {
		f.Add(s)
	}
	root := f.TempDir()
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Parse(s)
		if err != nil {
			return
		}
		joined := filepath.Join(root, filepath.FromSlash(p.String()))
		rel, err := filepath.Rel(root, joined)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Fatalf("escape: %q -> %q", s, joined)
		}
	})
}
