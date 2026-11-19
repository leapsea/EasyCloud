package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/easycloud/easycloud/internal/database/dbtest"
	"github.com/easycloud/easycloud/internal/envfile"
	"github.com/easycloud/easycloud/internal/server"
)

func envFile(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0o755)
	p := filepath.Join(dir, ".env")
	os.WriteFile(p, []byte(fmt.Sprintf("EASYCLOUD_DATA_ROOT=%s\nEASYCLOUD_APP_DATA=%s\n%s", data, filepath.Join(dir, "app"), extra)), 0o600)
	return p
}

func TestInstallSQLite(t *testing.T) {
	var out bytes.Buffer
	if err := runInstall(envFile(t, ""), true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "SQLite") || !strings.Contains(out.String(), "✔ 数据库检查通过") {
		t.Fatal(out.String())
	}
}

func TestInstallFallbackToSQLite(t *testing.T) {
	p := envFile(t, "EASYCLOUD_DB_HOST=127.0.0.1\nEASYCLOUD_DB_PORT=1\nEASYCLOUD_DB_PASSWORD=x\n")
	var out bytes.Buffer
	// Retry once (still broken), then switch to SQLite and confirm.
	err := runInstall(p, true, strings.NewReader("1\n\n2\ny\n"), &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Count(out.String(), "✘ 连接失败") != 2 {
		t.Fatalf("expected two failed attempts:\n%s", out.String())
	}
	v, _ := envfile.Read(p)
	if v["EASYCLOUD_DB_DRIVER"] != "sqlite" {
		t.Fatalf("driver not written: %v", v)
	}
}

func TestInstallExitAndNonInteractive(t *testing.T) {
	p := envFile(t, "EASYCLOUD_DB_HOST=127.0.0.1\nEASYCLOUD_DB_PORT=1\n")
	var out bytes.Buffer
	err := runInstall(p, true, strings.NewReader("3\n"), &out)
	if !errors.Is(err, errUserExit) || exitCode(err) != exitConfig {
		t.Fatalf("exit choice: %v (code %d)", err, exitCode(err))
	}
	out.Reset()
	err = runInstall(p, false, strings.NewReader(""), &out)
	if err == nil || strings.Contains(out.String(), "请选择") {
		t.Fatalf("non-interactive must fail without prompting: %v\n%s", err, out.String())
	}
	if exitCode(err) != exitUnavailable {
		t.Fatalf("unreachable should be exit 69, got %d", exitCode(err))
	}
	_ = server.ErrConfig
}

func TestInstallMariaDB(t *testing.T) {
	mc, ok := dbtest.MariaDBConfig()
	if !ok {
		t.Skip("EASYCLOUD_TEST_MARIADB not set")
	}
	dbtest.Wipe(t, dbtest.OpenRaw(t, mc))
	p := envFile(t, fmt.Sprintf("EASYCLOUD_DB_HOST=%s\nEASYCLOUD_DB_PORT=%d\nEASYCLOUD_DB_USER=%s\nEASYCLOUD_DB_PASSWORD=%s\nEASYCLOUD_DB_NAME=%s\n",
		mc.Host, mc.Port, mc.User, mc.Password, mc.Name))
	var out bytes.Buffer
	if err := runInstall(p, false, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// Wrong password: authentication failure is a configuration error (78).
	bad := envFile(t, fmt.Sprintf("EASYCLOUD_DB_HOST=%s\nEASYCLOUD_DB_PORT=%d\nEASYCLOUD_DB_USER=%s\nEASYCLOUD_DB_PASSWORD=wrong\nEASYCLOUD_DB_NAME=%s\n", mc.Host, mc.Port, mc.User, mc.Name))
	out.Reset()
	err := runInstall(bad, false, strings.NewReader(""), &out)
	if exitCode(err) != exitConfig || !strings.Contains(out.String(), "authentication failed") {
		t.Fatalf("bad password: %v (code %d)\n%s", err, exitCode(err), out.String())
	}
}
