package index_test

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/database/dbtest"
	"github.com/easycloud/easycloud/internal/index"
	"github.com/easycloud/easycloud/internal/pathguard"
)

func write(t *testing.T, base, rel string, n int) {
	p := filepath.Join(base, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestIndexer(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		write(t, dir, "user/alice/a.bin", 100)
		write(t, dir, "user/alice/sub/b.bin", 50)
		write(t, dir, "user/bob/Report Final.pdf", 10)
		write(t, dir, ".easycloud/uploads/chunk", 999)
		img := image.NewRGBA(image.Rect(0, 0, 32, 24))
		img.Set(1, 1, color.White)
		f, _ := os.Create(filepath.Join(dir, "user/alice/pic.png"))
		png.Encode(f, img)
		f.Close()

		root, _ := os.OpenRoot(dir)
		store := index.NewStore(db)
		var mu sync.Mutex
		var moves [][2]string
		var changed []string
		ix := index.New(root, dir, store, index.Hooks{
			Changed: func(d []string) { mu.Lock(); changed = append(changed, d...); mu.Unlock() },
			Moved: func(_ context.Context, from, to pathguard.RelPath) {
				mu.Lock()
				moves = append(moves, [2]string{from.String(), to.String()})
				mu.Unlock()
			},
		}, dbtest.Logger())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go ix.Run(ctx)
		<-ix.Ready()

		b, files, err := store.Usage(ctx, "user/alice")
		if err != nil {
			t.Fatal(err)
		}
		pngInfo, _ := os.Stat(filepath.Join(dir, "user/alice/pic.png"))
		if b != 150+pngInfo.Size() || files != 3 {
			t.Fatalf("usage %d bytes %d files", b, files)
		}
		if b, _, _ := store.Usage(ctx, "user/ali"); b != 0 {
			t.Fatal("prefix collision in usage")
		}
		if _, ok, _ := store.Get(ctx, ".easycloud/uploads/chunk"); ok {
			t.Fatal("reserved dir indexed")
		}
		n, ok, _ := store.Get(ctx, "user/alice/pic.png")
		if !ok || n.Width != 32 || n.Height != 24 || n.Mime != "image/png" {
			t.Fatalf("png node %+v", n)
		}
		res, _ := store.Search(ctx, "report", []string{"user/bob"}, 10)
		if len(res) != 1 || res[0].Name != "Report Final.pdf" {
			t.Fatalf("search %+v", res)
		}
		if res, _ := store.Search(ctx, "report", []string{"user/alice"}, 10); len(res) != 0 {
			t.Fatal("search leaked outside roots")
		}
		if res, _ := store.Search(ctx, "%", []string{"."}, 10); len(res) != 0 {
			t.Fatal("LIKE wildcard not escaped")
		}

		// Host-side changes are picked up by the watcher.
		write(t, dir, "user/alice/new.txt", 7)
		eventually(t, "new file indexed", func() bool { _, ok, _ := store.Get(ctx, "user/alice/new.txt"); return ok })
		mu.Lock()
		gotChange := len(changed) > 0
		mu.Unlock()
		if !gotChange {
			t.Error("no change notification")
		}

		// Host-side rename of a directory is reported as a move.
		if err := os.Rename(filepath.Join(dir, "user/alice/sub"), filepath.Join(dir, "user/bob/moved")); err != nil {
			t.Fatal(err)
		}
		eventually(t, "move detected", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(moves) == 1 && moves[0] == [2]string{"user/alice/sub", "user/bob/moved"}
		})
		eventually(t, "subtree reindexed", func() bool { _, ok, _ := store.Get(ctx, "user/bob/moved/b.bin"); return ok })
		if _, ok, _ := store.Get(ctx, "user/alice/sub/b.bin"); ok {
			t.Fatal("stale rows after move")
		}

		// Deletion.
		os.Remove(filepath.Join(dir, "user/alice/a.bin"))
		eventually(t, "deletion indexed", func() bool { _, ok, _ := store.Get(ctx, "user/alice/a.bin"); return !ok })
	})
}
