package database_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
)

func TestQueryLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", Log: "all"}, t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := db.W.ExecContext(ctx, "INSERT INTO easycloud_settings (name, value, updated_by, updated_at) VALUES (?, ?, ?, ?)", "k", "secret-value", "t", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := db.R.QueryContext(ctx, "SELECT name,\n   value FROM easycloud_settings")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	out := buf.String()
	for _, want := range []string{`sql="SELECT name, value FROM easycloud_settings"`, "rows=1", "pool=r", "pool=w", "caller=database_test.TestQueryLog"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-value") {
		t.Fatalf("arguments leaked into the log:\n%s", out)
	}

	// Default "slow": fast statements stay quiet.
	buf.Reset()
	db2, err := database.Open(ctx, config.Database{Driver: "sqlite"}, t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var one int
	db2.R.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	if buf.Len() != 0 || one != 1 {
		t.Fatalf("slow mode logged a fast query (%d): %s", one, buf.String())
	}
}
