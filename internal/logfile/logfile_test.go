package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two lines per file
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	for _, n := range []string{"easycloud.log", "easycloud.log.1", "easycloud.log.2"} {
		st, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if st.Size() > 100 {
			t.Fatalf("%s is %d bytes, over the limit", n, st.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "easycloud.log.3")); !os.IsNotExist(err) {
		t.Fatal("kept more files than asked")
	}
	// Reopening appends instead of truncating.
	w, _ = Open(dir, 100, 2)
	before := w.size
	w.Write([]byte("y\n"))
	w.Close()
	if st, _ := os.Stat(filepath.Join(dir, "easycloud.log")); st.Size() != before+2 {
		t.Fatal("reopen did not append")
	}
}

func TestMissingDir(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nope"), 100, 1); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}
