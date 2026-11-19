// Package audit records who did what to which path. Writes are buffered and
// asynchronous so a slow disk never delays a file operation.
package audit

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/database"
)

type Entry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	UserID   string    `json:"userId"`
	Username string    `json:"username"`
	Action   string    `json:"action"`
	Path     string    `json:"path"`
	Detail   string    `json:"detail"`
	IP       string    `json:"ip"`
}

type Recorder struct {
	db  *database.DB
	log *slog.Logger
	ch  chan Entry
}

func NewRecorder(db *database.DB, log *slog.Logger) *Recorder {
	return &Recorder{db: db, log: log, ch: make(chan Entry, 1024)}
}

// Record never blocks; if the buffer is full the entry is logged and dropped.
func (r *Recorder) Record(e Entry) {
	if r == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if len(e.Detail) > 2000 {
		e.Detail = e.Detail[:2000]
	}
	select {
	case r.ch <- e:
	default:
		r.log.Warn("audit buffer full; entry dropped", "action", e.Action, "user", e.Username, "path", e.Path)
	}
}

// Run drains the buffer until ctx is done, then flushes what is left.
func (r *Recorder) Run(ctx context.Context) {
	for {
		select {
		case e := <-r.ch:
			r.write(context.Background(), e)
		case <-ctx.Done():
			for {
				select {
				case e := <-r.ch:
					r.write(context.Background(), e)
				default:
					return
				}
			}
		}
	}
}

func (r *Recorder) write(ctx context.Context, e Entry) {
	_, err := r.db.W.ExecContext(ctx, "INSERT INTO easycloud_audit (at, user_id, username, action, path, detail, ip) VALUES (?,?,?,?,?,?,?)",
		e.At.Unix(), e.UserID, e.Username, e.Action, e.Path, e.Detail, e.IP)
	if err != nil {
		r.log.Error("audit write", "err", err)
	}
}

type Query struct {
	UserID string
	Action string
	Path   string // prefix
	Before int64  // id cursor (exclusive); 0 = newest
	Limit  int
}

func (r *Recorder) List(ctx context.Context, q Query) ([]Entry, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	var where []string
	var args []any
	if q.UserID != "" {
		where, args = append(where, "user_id = ?"), append(args, q.UserID)
	}
	if q.Action != "" {
		where, args = append(where, "action = ?"), append(args, q.Action)
	}
	if q.Path != "" {
		esc := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(q.Path)
		where, args = append(where, `path LIKE ? ESCAPE '!'`), append(args, esc+"%")
	}
	if q.Before > 0 {
		where, args = append(where, "id < ?"), append(args, q.Before)
	}
	sqlq := "SELECT id, at, user_id, username, action, path, detail, ip FROM easycloud_audit"
	if len(where) > 0 {
		sqlq += " WHERE " + strings.Join(where, " AND ")
	}
	sqlq += " ORDER BY id DESC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := r.db.R.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.UserID, &e.Username, &e.Action, &e.Path, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune removes entries older than the retention window.
func (r *Recorder) Prune(ctx context.Context, keep time.Duration) {
	if _, err := r.db.W.ExecContext(ctx, "DELETE FROM easycloud_audit WHERE at < ?", time.Now().Add(-keep).Unix()); err != nil {
		r.log.Error("audit prune", "err", err)
	}
}
