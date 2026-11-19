// Package dbtest opens migrated databases for tests. SQLite always runs;
// MariaDB runs when EASYCLOUD_TEST_MARIADB="host:port:user:password:dbname" is set.
package dbtest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
)

func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// MariaDBConfig returns the test MariaDB config, or ok=false when not configured.
func MariaDBConfig() (config.Database, bool) {
	v := os.Getenv("EASYCLOUD_TEST_MARIADB")
	parts := strings.SplitN(v, ":", 5)
	if len(parts) != 5 {
		return config.Database{}, false
	}
	port, _ := strconv.Atoi(parts[1])
	return config.Database{Driver: "mariadb", Host: parts[0], Port: port, User: parts[2], Password: parts[3], Name: parts[4], ConnectTimeout: 5}, true
}

// Each runs fn once per available dialect with a freshly migrated, empty database.
func Each(t *testing.T, fn func(t *testing.T, db *database.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, Open(t, config.Database{Driver: "sqlite"})) })
	if cfg, ok := MariaDBConfig(); ok {
		t.Run("mariadb", func(t *testing.T) { fn(t, Open(t, cfg)) })
	}
}

// Open returns a migrated database. MariaDB databases are wiped first.
func Open(t *testing.T, cfg config.Database) *database.DB {
	t.Helper()
	ctx := context.Background()
	db := OpenRaw(t, cfg)
	if cfg.Driver == "mariadb" {
		Wipe(t, db)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

// OpenRaw opens without migrating.
func OpenRaw(t *testing.T, cfg config.Database) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), cfg, t.TempDir(), Logger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Wipe drops every table in a MariaDB test database.
func Wipe(t *testing.T, db *database.DB) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.R.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	db.W.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, n := range names {
		if _, err := db.W.ExecContext(ctx, "DROP TABLE `"+n+"`"); err != nil {
			t.Fatal(err)
		}
	}
	db.W.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
}
