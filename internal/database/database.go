// Package database opens SQLite or MariaDB, refuses unsafe states, and runs migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/migrations"
	"github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"
)

var (
	// ErrConfig marks failures restarting cannot fix (exit 78).
	ErrConfig = errors.New("database configuration error")
	// ErrUnavailable marks transient failures (exit 69).
	ErrUnavailable = errors.New("database unavailable")
)

type Dialect int

const (
	SQLite Dialect = iota
	MySQL
)

// DB holds a writer and a reader pool. For SQLite the writer has exactly one
// connection (SQLite serialises writes) and readers run concurrently under WAL.
// For MariaDB both point at the same pool.
type DB struct {
	W, R    *sql.DB
	Dialect Dialect
	appData string
	cfg     config.Database
	log     *slog.Logger
}

const tablePrefix = "easycloud_"
const versionTable = "easycloud_goose_version"

func Open(ctx context.Context, cfg config.Database, appData string, log *slog.Logger) (*DB, error) {
	if cfg.Driver == "mariadb" {
		return openMySQL(ctx, cfg, appData, newQueryLog(cfg.Log, cfg.SlowMS, log), log)
	}
	return openSQLite(ctx, appData, newQueryLog(cfg.Log, cfg.SlowMS, log), log)
}

func SQLitePath(appData string) string { return filepath.Join(appData, "easycloud.db") }

func openSQLite(ctx context.Context, appData string, ql *queryLog, log *slog.Logger) (*DB, error) {
	if err := os.MkdirAll(appData, 0o750); err != nil {
		return nil, fmt.Errorf("%w: create %s: %v", ErrConfig, appData, err)
	}
	path := SQLitePath(appData)
	base := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	wc, err := connectorOf(&sqlite.Driver{}, base+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w := ql.open(wc, "w")
	w.SetMaxOpenConns(1)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("%w: open %s: %v", ErrConfig, path, err)
	}
	var check string
	if err := w.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		w.Close()
		return nil, fmt.Errorf("%w: %s is not a healthy SQLite file (refusing to touch it)", ErrConfig, path)
	}
	rc, err := connectorOf(&sqlite.Driver{}, base+"&_pragma=query_only(1)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r := ql.open(rc, "r")
	r.SetMaxOpenConns(8)
	return &DB{W: w, R: r, Dialect: SQLite, appData: appData, cfg: config.Database{Driver: "sqlite"}, log: log}, nil
}

func openMySQL(ctx context.Context, cfg config.Database, appData string, ql *queryLog, log *slog.Logger) (*DB, error) {
	mc := mysql.NewConfig()
	mc.User, mc.Passwd, mc.Net = cfg.User, cfg.Password, "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	mc.DBName = cfg.Name
	mc.Timeout = 5 * time.Second
	mc.Collation = "utf8mb4_bin"
	mc.Params = map[string]string{"sql_mode": "'STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"}
	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	db := ql.open(connector, "rw")
	db.SetMaxOpenConns(16)
	db.SetConnMaxLifetime(30 * time.Minute)

	deadline := time.Now().Add(time.Duration(cfg.ConnectTimeout) * time.Second)
	delay := time.Second
	for attempt := 1; ; attempt++ {
		err = db.PingContext(ctx)
		if err == nil {
			break
		}
		if kind := ClassifyMySQLError(err); kind == ErrConfig {
			db.Close()
			return nil, fmt.Errorf("%w: %s: %s", ErrConfig, cfg.Redacted(), Explain(err))
		}
		if time.Now().Add(delay).After(deadline) || ctx.Err() != nil {
			db.Close()
			return nil, fmt.Errorf("%w: %s unreachable after %d attempts: %s; check that MariaDB is running and EASYCLOUD_DB_HOST/PORT are correct",
				ErrUnavailable, cfg.Redacted(), attempt, Explain(err))
		}
		log.Warn("database not reachable yet, retrying", "target", cfg.Redacted(), "attempt", attempt, "retry_in", delay.String(), "err", Explain(err))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		delay = min(delay*2, 10*time.Second)
	}
	d := &DB{W: db, R: db, Dialect: MySQL, appData: appData, cfg: cfg, log: log}
	if err := d.checkMySQLVersion(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// ClassifyMySQLError returns ErrConfig for errors a retry cannot fix.
func ClassifyMySQLError(err error) error {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1044, 1045, 1049, 1698: // access denied to db, bad credentials, unknown db, plugin auth
			return ErrConfig
		}
	}
	return ErrUnavailable
}

// Explain turns driver errors into actionable text (never includes the password).
func Explain(err error) string {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1045, 1698:
			return "authentication failed: check EASYCLOUD_DB_USER / EASYCLOUD_DB_PASSWORD"
		case 1049:
			return "database does not exist: CREATE DATABASE it or fix EASYCLOUD_DB_NAME"
		case 1044:
			return "user has no access to this database: GRANT ALL ON <db>.* TO <user>"
		}
		return me.Error()
	}
	var ne net.Error
	if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
		return "network error: " + err.Error()
	}
	return err.Error()
}

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

func (d *DB) checkMySQLVersion(ctx context.Context) error {
	var v string
	if err := d.R.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("%w: cannot parse server version %q", ErrConfig, v)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if strings.Contains(strings.ToLower(v), "mariadb") {
		if major < 10 || (major == 10 && minor < 11) {
			return fmt.Errorf("%w: MariaDB %s is too old; 10.11 or newer is required", ErrConfig, v)
		}
		return nil
	}
	if major < 8 {
		return fmt.Errorf("%w: MySQL %s is too old; use MariaDB 10.11+", ErrConfig, v)
	}
	d.log.Warn("connected to MySQL rather than MariaDB; this is not an officially supported target", "version", v)
	return nil
}

func (d *DB) Close() error {
	err := d.W.Close()
	if d.R != d.W {
		err = errors.Join(err, d.R.Close())
	}
	return err
}

// Upsert returns an INSERT that updates the non-key columns on conflict.
func (d *DB) Upsert(table string, cols, keys []string) string {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ", table, strings.Join(cols, ", "), ph)
	isKey := map[string]bool{}
	for _, k := range keys {
		isKey[k] = true
	}
	var sets []string
	for _, c := range cols {
		if isKey[c] {
			continue
		}
		if d.Dialect == MySQL {
			sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", c, c))
		} else {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", c, c))
		}
	}
	if d.Dialect == MySQL {
		if len(sets) == 0 {
			return strings.Replace(q, "INSERT", "INSERT IGNORE", 1)
		}
		return q + "ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
	if len(sets) == 0 {
		return q + fmt.Sprintf("ON CONFLICT(%s) DO NOTHING", strings.Join(keys, ", "))
	}
	return q + fmt.Sprintf("ON CONFLICT(%s) DO UPDATE SET %s", strings.Join(keys, ", "), strings.Join(sets, ", "))
}

func (d *DB) tables(ctx context.Context) ([]string, error) {
	q := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"
	if d.Dialect == MySQL {
		q = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"
	}
	rows, err := d.R.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *DB) migrationSource() (fs.FS, string) {
	if d.Dialect == MySQL {
		return migrations.MySQL, "mysql"
	}
	return migrations.SQLite, "sqlite"
}

// Migrate is the existing-data compatibility check plus forward migration:
// recognise what we can, refuse what we cannot, never overwrite.
func (d *DB) Migrate(ctx context.Context) error {
	tables, err := d.tables(ctx)
	if err != nil {
		return fmt.Errorf("%w: list tables: %v", ErrUnavailable, err)
	}
	hasVersion, hasOurs := false, false
	var foreign []string
	for _, t := range tables {
		switch {
		case t == versionTable:
			hasVersion = true
		case strings.HasPrefix(t, tablePrefix):
			hasOurs = true
		default:
			foreign = append(foreign, t)
		}
	}
	if len(foreign) > 0 {
		d.log.Warn("database contains tables from other applications; they are left untouched, but a dedicated database is recommended",
			"count", len(foreign))
	}
	if hasOurs && !hasVersion {
		return fmt.Errorf("%w: easycloud_ tables exist but the version table %s is missing; inspect the database manually", ErrConfig, versionTable)
	}

	fsys, dir := d.migrationSource()
	goose.SetBaseFS(fsys)
	goose.SetTableName(versionTable)
	goose.SetLogger(goose.NopLogger())
	dialect := "sqlite3"
	if d.Dialect == MySQL {
		dialect = "mysql"
	}
	if err := goose.SetDialect(dialect); err != nil {
		return err
	}
	cur := int64(0)
	if hasVersion {
		cur, err = goose.GetDBVersionContext(ctx, d.W)
		if err != nil {
			return fmt.Errorf("%w: version table unreadable: %v", ErrConfig, err)
		}
	}
	migs, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		return err
	}
	latest := migs[len(migs)-1].Version
	switch {
	case cur > latest:
		return fmt.Errorf("%w: database schema version %d is newer than this program supports (%d); upgrade easycloud instead of downgrading",
			ErrConfig, cur, latest)
	case cur == latest:
		return nil
	case cur > 0:
		if err := d.backup(ctx, cur); err != nil {
			return fmt.Errorf("backup before migrate: %w", err)
		}
	}
	d.log.Info("migrating database", "from", cur, "to", latest, "target", d.cfg.Redacted())
	if err := goose.UpContext(ctx, d.W, dir); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (d *DB) backup(ctx context.Context, version int64) error {
	if d.Dialect == MySQL {
		d.log.Warn("upgrading MariaDB schema; take a backup first if you have not (mysqldump)", "from_version", version)
		return nil
	}
	// VACUUM INTO produces a consistent copy even with WAL.
	dst := fmt.Sprintf("%s.v%d.%s.bak", SQLitePath(d.appData), version, time.Now().Format("20060102-150405"))
	if _, err := d.W.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return copyFile(SQLitePath(d.appData), dst)
	}
	d.log.Info("database backed up before migration", "path", dst)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
