package database_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/database/dbtest"
)

func TestMigrateIdempotentAndUpsert(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx); err != nil {
			t.Fatal("second migrate:", err)
		}
		q := db.Upsert("easycloud_settings", []string{"name", "value", "updated_by", "updated_at"}, []string{"name"})
		for _, v := range []string{"1", "2"} {
			if _, err := db.W.ExecContext(ctx, q, "k", v, "t", 1); err != nil {
				t.Fatal(err)
			}
		}
		var got string
		db.R.QueryRowContext(ctx, "SELECT value FROM easycloud_settings WHERE name = 'k'").Scan(&got)
		if got != "2" {
			t.Fatalf("upsert value %q", got)
		}
		// Case-sensitive path storage (utf8mb4_bin on MariaDB).
		for _, p := range []string{"/A.txt", "/a.txt"} {
			if _, err := db.W.ExecContext(ctx, q, p, "x", "t", 1); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_settings WHERE name IN ('/A.txt', '/a.txt')").Scan(&n)
		if n != 2 {
			t.Fatalf("case-insensitive collision: %d rows", n)
		}
	})
}

func TestRefusesNewerSchema(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		if _, err := db.W.ExecContext(ctx, "INSERT INTO easycloud_goose_version (version_id, is_applied) VALUES (9999, 1)"); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); !errors.Is(err, database.ErrConfig) {
			t.Fatalf("want ErrConfig, got %v", err)
		}
	})
}

func TestRefusesMissingVersionTable(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		if _, err := db.W.ExecContext(ctx, "DROP TABLE easycloud_goose_version"); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); !errors.Is(err, database.ErrConfig) {
			t.Fatalf("want ErrConfig, got %v", err)
		}
	})
}

func TestForeignTablesUntouched(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		if _, err := db.W.ExecContext(ctx, "CREATE TABLE other_app (id INT)"); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.R.ExecContext(ctx, "SELECT * FROM other_app"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestInstanceBinding(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		root1, root2 := t.TempDir(), t.TempDir()
		if err := db.BindInstance(ctx, root1, false); err != nil {
			t.Fatal(err)
		}
		if err := db.BindInstance(ctx, root1, false); err != nil {
			t.Fatal("rebind same root:", err)
		}
		// A data root that belongs to another installation.
		os.MkdirAll(filepath.Join(root2, ".easycloud"), 0o700)
		os.WriteFile(filepath.Join(root2, ".easycloud", "instance.json"), []byte(`{"id":"someone-else"}`), 0o600)
		if err := db.BindInstance(ctx, root2, false); !errors.Is(err, database.ErrConfig) {
			t.Fatalf("want ErrConfig, got %v", err)
		}
		if err := db.BindInstance(ctx, root2, true); err != nil {
			t.Fatal("forced rebind:", err)
		}
	})
}

func TestSQLiteCorruptFileRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(database.SQLitePath(dir), []byte("this is not sqlite at all, definitely not"), 0o600)
	_, err := database.Open(context.Background(), config.Database{Driver: "sqlite"}, dir, dbtest.Logger())
	if !errors.Is(err, database.ErrConfig) {
		t.Fatalf("want ErrConfig, got %v", err)
	}
	b, _ := os.ReadFile(database.SQLitePath(dir))
	if string(b) != "this is not sqlite at all, definitely not" {
		t.Fatal("corrupt file was modified")
	}
}

func TestMariaDBErrorClassification(t *testing.T) {
	cfg, ok := dbtest.MariaDBConfig()
	if !ok {
		t.Skip("EASYCLOUD_TEST_MARIADB not set")
	}
	ctx := context.Background()
	bad := cfg
	bad.Password = "wrong"
	if _, err := database.Open(ctx, bad, t.TempDir(), dbtest.Logger()); !errors.Is(err, database.ErrConfig) {
		t.Fatalf("bad password: want ErrConfig, got %v", err)
	}
	bad = cfg
	bad.Name = "does_not_exist_db"
	if _, err := database.Open(ctx, bad, t.TempDir(), dbtest.Logger()); !errors.Is(err, database.ErrConfig) {
		t.Fatalf("unknown db: want ErrConfig, got %v", err)
	}
	bad = cfg
	bad.Port = 1
	bad.ConnectTimeout = 1
	if _, err := database.Open(ctx, bad, t.TempDir(), dbtest.Logger()); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("unreachable: want ErrUnavailable, got %v", err)
	}
}
