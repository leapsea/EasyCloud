package app_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/app"
	"github.com/easycloud/easycloud/internal/storage/infra"
)

var admin = pathguard.Subject{UserID: "admin", IsAdmin: true}

func setup(t *testing.T) (*app.FileService, string) {
	t.Helper()
	ctx := context.Background()
	dataDir, appDir := t.TempDir(), t.TempDir()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite"}, appDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Trash.RetentionDays = 30
	svc := app.NewFileService(pathguard.NewAdminAuthorizer(root), root, infra.NewTrashRepo(db), nil,
		app.SystemClock{}, settings.Resolver{Store: settings.NewSQLStore(db), Cfg: cfg},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, dataDir
}

func write(t *testing.T, base, rel, body string) {
	t.Helper()
	p := filepath.Join(base, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	if ae, ok := errs.As(err); ok {
		return ae.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestListHidesReservedAndEscapingSymlinks(t *testing.T) {
	svc, dir := setup(t)
	ctx := context.Background()
	write(t, dir, "a.txt", "x")
	os.MkdirAll(filepath.Join(dir, ".easycloud"), 0o755)
	outside := t.TempDir()
	write(t, outside, "secret", "s")
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "evil"))

	l, err := svc.List(ctx, admin, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "a.txt" {
		t.Fatalf("entries = %+v", l.Entries)
	}
	if _, _, err := svc.Open(ctx, admin, "/evil"); code(err) != "NOT_FOUND" {
		t.Fatalf("symlink escape open: %v", err)
	}
	if _, err := svc.List(ctx, admin, "/.easycloud"); code(err) != "INVALID_PATH" {
		t.Fatalf("reserved dir: %v", err)
	}
	if _, err := svc.List(ctx, pathguard.Subject{UserID: "x"}, "/"); code(err) != "NOT_FOUND" {
		t.Fatalf("non-admin: %v", err)
	}
}

func TestMkdirRenameMoveCopy(t *testing.T) {
	svc, dir := setup(t)
	ctx := context.Background()
	write(t, dir, "doc.txt", "hello")
	if _, err := svc.Mkdir(ctx, admin, "/d"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rename(ctx, admin, "/doc.txt", "../x"); code(err) != "INVALID_PATH" {
		t.Fatalf("rename traversal: %v", err)
	}
	if _, err := svc.Rename(ctx, admin, "/doc.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Copy(ctx, admin, []string{"/b.txt"}, "/d", app.ConflictFail); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Copy(ctx, admin, []string{"/b.txt"}, "/d", app.ConflictRename)
	if err != nil || got[0] != "/d/b (1).txt" {
		t.Fatalf("copy rename: %v %v", got, err)
	}
	if _, err := svc.Move(ctx, admin, []string{"/b.txt"}, "/d", app.ConflictFail); code(err) != "CONFLICT" {
		t.Fatalf("move conflict: %v", err)
	}
	if _, err := svc.Move(ctx, admin, []string{"/d"}, "/d", app.ConflictFail); code(err) != "INVALID_INPUT" {
		t.Fatalf("move into self: %v", err)
	}
	if _, err := svc.Move(ctx, admin, []string{"/b.txt"}, "/d", app.ConflictOverwrite); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "d/b.txt"))
	if string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
}

func TestTrashRoundTrip(t *testing.T) {
	svc, dir := setup(t)
	ctx := context.Background()
	write(t, dir, "p/q.txt", "1")
	items, err := svc.MoveToTrash(ctx, admin, []string{"/p"})
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p")); !os.IsNotExist(err) {
		t.Fatal("still present")
	}
	write(t, dir, "p/other", "2")
	if _, err := svc.Restore(ctx, admin, items[0].ID, app.ConflictFail); code(err) != "CONFLICT" {
		t.Fatalf("restore conflict: %v", err)
	}
	got, err := svc.Restore(ctx, admin, items[0].ID, app.ConflictRename)
	if err != nil || got != "/p (1)" {
		t.Fatalf("restore: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p (1)/q.txt")); err != nil {
		t.Fatal(err)
	}
	// Owner-scoped lookup: another subject cannot purge it.
	items, _ = svc.MoveToTrash(ctx, admin, []string{"/p"})
	other := pathguard.Subject{UserID: "bob", IsAdmin: true}
	if err := svc.Purge(ctx, other, items[0].ID); code(err) != "NOT_FOUND" {
		t.Fatalf("cross-owner purge: %v", err)
	}
	if n, err := svc.EmptyTrash(ctx, admin); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
