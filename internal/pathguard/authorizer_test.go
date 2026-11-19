package pathguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type staticGrants []Grant

func (s staticGrants) GrantsFor(context.Context, string) ([]Grant, error) { return s, nil }

func TestGrantAuthorizer(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"user/A", "user/B", "share/AA/x", "share/AAB", "share/BB"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dir, "share/AA/escape"))
	root, _ := os.OpenRoot(dir)
	a := NewGrantAuthorizer(root, staticGrants{
		{Path: MustParse("user/A"), Perm: PermRW},
		{Path: MustParse("share/AA"), Perm: PermRead},
		{Path: MustParse("share/AA/x"), Perm: PermRW},
	})
	sub := Subject{UserID: "a"}
	ctx := context.Background()

	cases := []struct {
		path string
		need Perm
		ok   bool
	}{
		{"user/A", PermRW, true},
		{"user/A/deep/file", PermWrite, true},
		{"user/B", PermRead, false},
		{"share/AA", PermRead, true},
		{"share/AA", PermWrite, false},
		{"share/AA/x/f", PermWrite, true},
		{"share/AAB", PermRead, false}, // segment boundary
		{"share", PermRead, false},     // ancestors are navigation-only
		{"", PermRead, false},
	}
	for _, c := range cases {
		h, err := a.Check(ctx, sub, MustParse(c.path), c.need)
		if (err == nil) != c.ok {
			t.Errorf("Check(%q, %v) err=%v, want ok=%v", c.path, c.need, err, c.ok)
		}
		h.Close()
	}

	// The handle root is the grant dir: escaping symlinks fail at the filesystem layer.
	h, err := a.Check(ctx, sub, MustParse("share/AA/escape"), PermRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Root.Stat(h.Rel); err == nil {
		t.Fatal("symlink escape must fail")
	}
	h.Close()

	v, err := a.Visible(ctx, sub, MustParse(""))
	if err != nil || v.Perm != 0 || len(v.Children) != 2 || !v.Children["user"] || !v.Children["share"] {
		t.Fatalf("root visibility %+v %v", v, err)
	}
	v, _ = a.Visible(ctx, sub, MustParse("share"))
	if len(v.Children) != 1 || !v.Children["AA"] {
		t.Fatalf("share visibility %+v", v)
	}
	if v, _ := a.Visible(ctx, sub, MustParse("share/AA")); v.Perm != PermRead {
		t.Fatalf("share/AA perm %v", v.Perm)
	}
	if _, err := a.Visible(ctx, sub, MustParse("share/BB")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("share/BB: %v", err)
	}
	if v, _ := a.Visible(ctx, Subject{UserID: "x", IsAdmin: true}, MustParse("share/BB")); v.Perm != PermRW {
		t.Fatal("admin sees everything")
	}
}
