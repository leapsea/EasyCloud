// Package index mirrors the data root into the database: path, inode/dev,
// size, mtime, MIME and image dimensions. It powers search, usage and quotas,
// thumbnails, and following host-side renames.
package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/database"
)

type Node struct {
	Path   string    `json:"path"` // relative, no leading slash
	Name   string    `json:"name"`
	IsDir  bool      `json:"isDir"`
	Size   int64     `json:"size"`
	MTime  time.Time `json:"modTime"`
	Dev    uint64    `json:"-"`
	Inode  uint64    `json:"-"`
	Mime   string    `json:"mime,omitempty"`
	Width  int       `json:"width,omitempty"`
	Height int       `json:"height,omitempty"`
}

func hashPath(p string) string {
	h := sha256.Sum256([]byte(p))
	return hex.EncodeToString(h[:])
}

func parentOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const nodeCols = "path, name, is_dir, size, mtime, dev, inode, mime, width, height"

func scanNode(s interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var mt int64
	err := s.Scan(&n.Path, &n.Name, &n.IsDir, &n.Size, &mt, &n.Dev, &n.Inode, &n.Mime, &n.Width, &n.Height)
	n.MTime = time.Unix(mt, 0).UTC()
	return n, err
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Node, error) {
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Children returns the indexed direct children of dir ("." for the root).
func (s *Store) Children(ctx context.Context, dir string) ([]Node, error) {
	return s.query(ctx, "SELECT "+nodeCols+" FROM easycloud_files WHERE parent_hash = ?", hashPath(dir))
}

func (s *Store) Get(ctx context.Context, p string) (Node, bool, error) {
	n, err := scanNode(s.db.R.QueryRowContext(ctx, "SELECT "+nodeCols+" FROM easycloud_files WHERE path_hash = ?", hashPath(p)))
	if err == sql.ErrNoRows {
		return n, false, nil
	}
	return n, err == nil, err
}

// Upsert writes nodes in one transaction.
func (s *Store) Upsert(ctx context.Context, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	q := s.db.Upsert("easycloud_files",
		[]string{"path_hash", "path", "parent_hash", "name", "is_dir", "size", "mtime", "dev", "inode", "mime", "width", "height"},
		[]string{"path_hash"})
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, n := range nodes {
		if _, err := stmt.ExecContext(ctx, hashPath(n.Path), n.Path, hashPath(parentOf(n.Path)), n.Name, n.IsDir, n.Size,
			n.MTime.Unix(), n.Dev, n.Inode, n.Mime, n.Width, n.Height); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// subtree returns the WHERE clause for p and everything below it. "/" sorts
// right before "0", so the half-open range covers exactly the descendants.
func subtree(p string) (string, []any) {
	if p == "." || p == "" {
		return "1 = 1", nil
	}
	return "(path_hash = ? OR (path >= ? AND path < ?))", []any{hashPath(p), p + "/", p + "0"}
}

// DeleteTree removes p and all descendants.
func (s *Store) DeleteTree(ctx context.Context, p string) error {
	w, args := subtree(p)
	_, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_files WHERE "+w, args...)
	return err
}

// Usage sums file sizes under p.
func (s *Store) Usage(ctx context.Context, p string) (bytes int64, files int64, err error) {
	w, args := subtree(p)
	var b, f sql.NullInt64
	err = s.db.R.QueryRowContext(ctx, "SELECT SUM(size), COUNT(*) FROM easycloud_files WHERE is_dir = 0 AND "+w, args...).Scan(&b, &f)
	return b.Int64, f.Int64, err
}

// Search finds names containing term under any of the given roots ("." = everything).
func (s *Store) Search(ctx context.Context, term string, roots []string, limit int) ([]Node, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	esc := likeEscape(strings.ToLower(term))
	var ors []string
	args := []any{"%" + esc + "%"}
	for _, r := range roots {
		w, a := subtree(r)
		ors = append(ors, w)
		args = append(args, a...)
	}
	args = append(args, limit)
	return s.query(ctx, "SELECT "+nodeCols+" FROM easycloud_files WHERE LOWER(name) LIKE ? ESCAPE '!' AND ("+
		strings.Join(ors, " OR ")+") ORDER BY is_dir DESC, name LIMIT ?", args...)
}

// TopLevel returns usage per top-level directory (for the dashboard).
func (s *Store) TopLevel(ctx context.Context) (map[string]int64, error) {
	dirs, err := s.Children(ctx, ".")
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, d := range dirs {
		b, _, err := s.Usage(ctx, d.Path)
		if err != nil {
			return nil, err
		}
		out[d.Name] = b
		if !d.IsDir {
			out[d.Name] = d.Size
		}
	}
	return out, nil
}

// likeEscape escapes LIKE wildcards with "!", which needs no quoting in either dialect.
func likeEscape(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}
