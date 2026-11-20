package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"time"
)

// Query log: a thin database/sql driver wrapper that times every statement
// and logs the slow ones (or all of them), with the calling function. A
// query's time runs until its rows are closed, so it includes reading the
// results — for SQLite that is where most of the work happens.
//
// Only the SQL text is logged, never the arguments: they can hold password
// hashes, tokens and file names.
//
//	EASYCLOUD_DB_LOG=slow|all|off   (default slow)
//	EASYCLOUD_DB_SLOW_MS=300         threshold for "slow"

type queryLog struct {
	log  *slog.Logger
	all  bool
	slow time.Duration
}

func newQueryLog(mode string, slowMS int, log *slog.Logger) *queryLog {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "none", "false", "0":
		return nil
	case "all":
		return &queryLog{log: log, all: true, slow: ms(slowMS)}
	}
	return &queryLog{log: log, slow: ms(slowMS)}
}

func ms(v int) time.Duration {
	if v <= 0 {
		v = 300
	}
	return time.Duration(v) * time.Millisecond
}

// open returns a *sql.DB that logs through q (or the plain one when q is nil).
func (q *queryLog) open(c driver.Connector, pool string) *sql.DB {
	if q == nil {
		return sql.OpenDB(c)
	}
	return sql.OpenDB(&logConnector{inner: c, q: q, pool: pool})
}

// connectorOf turns a DSN-based driver into a connector.
func connectorOf(d driver.Driver, dsn string) (driver.Connector, error) {
	if dc, ok := d.(driver.DriverContext); ok {
		return dc.OpenConnector(dsn)
	}
	return dsnConnector{d, dsn}, nil
}

type dsnConnector struct {
	d   driver.Driver
	dsn string
}

func (c dsnConnector) Connect(context.Context) (driver.Conn, error) { return c.d.Open(c.dsn) }
func (c dsnConnector) Driver() driver.Driver                        { return c.d }

func (q *queryLog) done(pool, query string, start time.Time, rows int, err error) {
	d := time.Since(start)
	slow := d >= q.slow
	if !slow && !q.all {
		return
	}
	attrs := []any{"pool", pool, "ms", float64(d.Microseconds()) / 1000, "sql", compactSQL(query), "caller", caller()}
	if rows >= 0 {
		attrs = append(attrs, "rows", rows)
	}
	if err != nil && err != io.EOF && err != driver.ErrSkip {
		attrs = append(attrs, "err", err.Error())
	}
	if slow {
		q.log.Warn("slow query", attrs...)
	} else {
		q.log.Info("query", attrs...)
	}
}

// compactSQL folds whitespace and caps the length (category filters expand
// to dozens of LIKE terms).
func compactSQL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 2000 {
		s = s[:2000] + "…"
	}
	return s
}

// caller is the first frame outside database/sql and this file's package.
func caller() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		fn := f.Function
		if !strings.HasPrefix(fn, "database/sql") && !strings.Contains(fn, "/internal/database.") && !strings.HasPrefix(fn, "runtime.") {
			if i := strings.LastIndex(fn, "/"); i >= 0 {
				fn = fn[i+1:]
			}
			return fn
		}
		if !more {
			return ""
		}
	}
}

type logConnector struct {
	inner driver.Connector
	q     *queryLog
	pool  string
}

func (c *logConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &logConn{inner: conn, q: c.q, pool: c.pool}, nil
}
func (c *logConnector) Driver() driver.Driver { return c.inner.Driver() }

type logConn struct {
	inner driver.Conn
	q     *queryLog
	pool  string
}

func (c *logConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *logConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var st driver.Stmt
	var err error
	if p, ok := c.inner.(driver.ConnPrepareContext); ok {
		st, err = p.PrepareContext(ctx, query)
	} else {
		st, err = c.inner.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &logStmt{inner: st, c: c, query: query}, nil
}

func (c *logConn) Close() error { return c.inner.Close() }

func (c *logConn) Begin() (driver.Tx, error) { //nolint:staticcheck // required by driver.Conn
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *logConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.inner.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.inner.Begin() //nolint:staticcheck
}

func (c *logConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	r, err := e.ExecContext(ctx, query, args)
	if err != driver.ErrSkip { // ErrSkip: database/sql retries through Prepare, timed there
		c.q.done(c.pool, query, start, -1, err)
	}
	return r, err
}

func (c *logConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := qc.QueryContext(ctx, query, args)
	if err != nil {
		if err != driver.ErrSkip {
			c.q.done(c.pool, query, start, -1, err)
		}
		return nil, err
	}
	return &logRows{inner: rows, q: c.q, pool: c.pool, query: query, start: start}, nil
}

func (c *logConn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *logConn) ResetSession(ctx context.Context) error {
	if r, ok := c.inner.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *logConn) IsValid() bool {
	if v, ok := c.inner.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *logConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := c.inner.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type logStmt struct {
	inner driver.Stmt
	c     *logConn
	query string
}

func (s *logStmt) Close() error  { return s.inner.Close() }
func (s *logStmt) NumInput() int { return s.inner.NumInput() }

func (s *logStmt) Exec(args []driver.Value) (driver.Result, error) { //nolint:staticcheck
	start := time.Now()
	r, err := s.inner.Exec(args) //nolint:staticcheck
	s.c.q.done(s.c.pool, s.query, start, -1, err)
	return r, err
}

func (s *logStmt) Query(args []driver.Value) (driver.Rows, error) { //nolint:staticcheck
	start := time.Now()
	rows, err := s.inner.Query(args) //nolint:staticcheck
	return s.rows(rows, start, err)
}

func (s *logStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	e, ok := s.inner.(driver.StmtExecContext)
	if !ok {
		return s.Exec(namedToValues(args))
	}
	start := time.Now()
	r, err := e.ExecContext(ctx, args)
	s.c.q.done(s.c.pool, s.query, start, -1, err)
	return r, err
}

func (s *logStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.inner.(driver.StmtQueryContext)
	if !ok {
		return s.Query(namedToValues(args))
	}
	start := time.Now()
	rows, err := qc.QueryContext(ctx, args)
	return s.rows(rows, start, err)
}

func (s *logStmt) rows(rows driver.Rows, start time.Time, err error) (driver.Rows, error) {
	if err != nil {
		s.c.q.done(s.c.pool, s.query, start, -1, err)
		return nil, err
	}
	return &logRows{inner: rows, q: s.c.q, pool: s.c.pool, query: s.query, start: start}, nil
}

func (s *logStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := s.inner.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return s.c.CheckNamedValue(nv)
}

func namedToValues(nv []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(nv))
	for i, v := range nv {
		out[i] = v.Value
	}
	return out
}

// logRows counts rows and logs when the result set is closed.
type logRows struct {
	inner  driver.Rows
	q      *queryLog
	pool   string
	query  string
	start  time.Time
	n      int
	err    error
	logged bool
}

func (r *logRows) Columns() []string { return r.inner.Columns() }

func (r *logRows) Next(dest []driver.Value) error {
	err := r.inner.Next(dest)
	if err == nil {
		r.n++
	} else if err != io.EOF {
		r.err = err
	}
	return err
}

func (r *logRows) Close() error {
	err := r.inner.Close()
	if !r.logged {
		r.logged = true
		r.q.done(r.pool, r.query, r.start, r.n, r.err)
	}
	return err
}

func (r *logRows) HasNextResultSet() bool {
	if m, ok := r.inner.(driver.RowsNextResultSet); ok {
		return m.HasNextResultSet()
	}
	return false
}

func (r *logRows) NextResultSet() error {
	if m, ok := r.inner.(driver.RowsNextResultSet); ok {
		return m.NextResultSet()
	}
	return io.EOF
}

func (r *logRows) ColumnTypeScanType(i int) reflect.Type {
	if m, ok := r.inner.(driver.RowsColumnTypeScanType); ok {
		return m.ColumnTypeScanType(i)
	}
	return reflect.TypeFor[any]()
}

func (r *logRows) ColumnTypeDatabaseTypeName(i int) string {
	if m, ok := r.inner.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return m.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *logRows) ColumnTypeLength(i int) (int64, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypeLength); ok {
		return m.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *logRows) ColumnTypeNullable(i int) (bool, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypeNullable); ok {
		return m.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *logRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypePrecisionScale); ok {
		return m.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}
