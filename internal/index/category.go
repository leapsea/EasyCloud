package index

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Category groups files by kind for the "categories" views. Membership is
// decided by extension, with the MIME prefix as a fallback: the index derives
// MIME from the extension through the platform table, which in minimal
// containers misses common types (.mkv, .heic, ...).
type Category struct {
	Name       string
	MimePrefix string
	Exts       []string
	// Types are the filter tags of the category view, by extension. Files of
	// the category that match none of them form the "other" tag.
	Types []Subtype
}

type Subtype struct {
	Key  string
	Exts []string
}

// TypeOther selects the files of a category outside every listed type.
const TypeOther = "other"

var Categories = map[string]Category{
	"image": {"image", "image/", []string{"jpg", "jpeg", "png", "gif", "webp", "avif", "bmp", "tif", "tiff", "svg", "ico",
		"heic", "heif", "raw", "cr2", "cr3", "nef", "arw", "dng", "orf", "rw2", "raf"},
		[]Subtype{{"jpg", []string{"jpg", "jpeg"}}, {"png", []string{"png"}}, {"gif", []string{"gif"}}, {"webp", []string{"webp"}},
			{"heic", []string{"heic", "heif"}}, {"raw", []string{"raw", "cr2", "cr3", "nef", "arw", "dng", "orf", "rw2", "raf"}}}},
	"video": {"video", "video/", []string{"mp4", "m4v", "mov", "mkv", "avi", "wmv", "flv", "webm", "ts", "m2ts", "mts",
		"3gp", "rm", "rmvb", "mpg", "mpeg", "vob"},
		[]Subtype{{"mp4", []string{"mp4", "m4v"}}, {"mkv", []string{"mkv"}}, {"mov", []string{"mov"}}, {"avi", []string{"avi"}},
			{"ts", []string{"ts", "m2ts", "mts"}}}},
	"audio": {"audio", "audio/", []string{"mp3", "flac", "wav", "aac", "m4a", "ogg", "oga", "opus", "wma", "ape", "aiff", "aif",
		"alac", "dsf", "dff", "mid", "midi"},
		[]Subtype{{"mp3", []string{"mp3"}}, {"flac", []string{"flac"}}, {"wav", []string{"wav"}}, {"aac", []string{"aac", "m4a"}},
			{"ape", []string{"ape"}}}},
	"doc": {"doc", "", []string{"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "txt", "md",
		"markdown", "csv", "epub", "mobi", "azw3", "pages", "numbers", "key", "wps", "et", "dps"},
		[]Subtype{{"pdf", []string{"pdf"}}, {"txt", []string{"txt", "md", "markdown"}},
			{"word", []string{"doc", "docx", "odt", "rtf", "wps", "pages"}},
			{"excel", []string{"xls", "xlsx", "ods", "csv", "et", "numbers"}},
			{"ppt", []string{"ppt", "pptx", "odp", "key", "dps"}}}},
}

// categoryOrder fixes which category wins when the tables overlap (none do
// today); extCategory maps every listed extension to its category.
var categoryOrder = []string{"image", "video", "audio", "doc"}

var extCategory = func() map[string]string {
	m := map[string]string{}
	for _, k := range categoryOrder {
		for _, e := range Categories[k].Exts {
			if _, dup := m[e]; !dup {
				m[e] = k
			}
		}
	}
	return m
}()

// classify derives the stored category and extension of an index row, with
// the same rule as condition(): extension first, MIME prefix as fallback.
// ext is kept only when short and ASCII (the MariaDB column is ascii).
func classify(name, mime string, isDir bool) (cat, ext string) {
	if isDir {
		return "", ""
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 && i < len(name)-1 {
		ext = strings.ToLower(name[i+1:])
		if len(ext) > 16 || !isASCII(ext) {
			ext = ""
		}
	}
	if c, ok := extCategory[ext]; ok {
		return c, ext
	}
	for _, k := range categoryOrder {
		if p := Categories[k].MimePrefix; p != "" && strings.HasPrefix(mime, p) {
			return k, ext
		}
	}
	return "", ext
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// condition is a SQL predicate for the category. Every value is a constant
// from the table above, still passed as arguments.
func (c Category) condition() (string, []any) {
	var ors []string
	var args []any
	if c.MimePrefix != "" {
		ors = append(ors, "mime LIKE ?")
		args = append(args, c.MimePrefix+"%")
	}
	for _, e := range c.Exts {
		ors = append(ors, "LOWER(name) LIKE ?")
		args = append(args, "%."+e)
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

func extCondition(exts []string) (string, []any) {
	ors := make([]string, len(exts))
	args := make([]any, len(exts))
	for i, e := range exts {
		ors[i], args[i] = "LOWER(name) LIKE ?", "%."+e
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// HasType reports whether typ is a filter of the category ("" = all).
func (c Category) HasType(typ string) bool {
	if typ == "" || typ == TypeOther {
		return true
	}
	for _, t := range c.Types {
		if t.Key == typ {
			return true
		}
	}
	return false
}

// typeCondition narrows the category to one type: its extensions, or for
// "other" the category minus every type's extensions.
func (c Category) typeCondition(typ string) (string, []any) {
	cond, args := c.condition()
	if typ == "" {
		return cond, args
	}
	if typ == TypeOther {
		var all []string
		for _, t := range c.Types {
			all = append(all, t.Exts...)
		}
		if len(all) == 0 {
			return cond, args
		}
		ex, a := extCondition(all)
		return cond + " AND NOT " + ex, append(args, a...)
	}
	for _, t := range c.Types {
		if t.Key == typ {
			return extCondition(t.Exts)
		}
	}
	return "1 = 0", nil
}

// indexedCondition is typeCondition on the stored columns: an equality on
// the indexed category plus an IN list on ext.
func (c Category) indexedCondition(typ string) (string, []any) {
	cond, args := "category = ?", []any{c.Name}
	in := func(exts []string) (string, []any) {
		a := make([]any, len(exts))
		for i, e := range exts {
			a[i] = e
		}
		return "(" + strings.TrimSuffix(strings.Repeat("?,", len(exts)), ",") + ")", a
	}
	switch {
	case typ == "":
	case typ == TypeOther:
		var all []string
		for _, t := range c.Types {
			all = append(all, t.Exts...)
		}
		if len(all) > 0 {
			l, a := in(all)
			cond += " AND ext NOT IN " + l
			args = append(args, a...)
		}
	default:
		found := false
		for _, t := range c.Types {
			if t.Key == typ {
				l, a := in(t.Exts)
				cond += " AND ext IN " + l
				args = append(args, a...)
				found = true
			}
		}
		if !found {
			return "1 = 0", nil
		}
	}
	return cond, args
}

// Backfill fills the category columns of rows indexed before they existed,
// in batches, then switches the category views to the index. Until then they
// keep the slower pattern match, so results are complete either way.
func (s *Store) Backfill(ctx context.Context, log *slog.Logger) {
	const batch = 1000
	start, done := time.Now(), 0
	for ctx.Err() == nil {
		n, err := s.backfillBatch(ctx, batch)
		if err != nil {
			log.Warn("category backfill failed; category views keep the slow path", "err", err)
			return
		}
		done += n
		if n < batch {
			break
		}
		if done%(batch*50) == 0 {
			log.Info("category backfill in progress", "rows", done)
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.catReady.Store(true)
	if done > 0 {
		log.Info("category backfill done", "rows", done, "took", time.Since(start).Round(time.Millisecond).String())
	}
}

func (s *Store) backfillBatch(ctx context.Context, limit int) (int, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT path_hash, name, mime, is_dir FROM easycloud_files WHERE category IS NULL LIMIT ?", limit)
	if err != nil {
		return 0, err
	}
	type row struct {
		hash, cat, ext string
	}
	var todo []row
	for rows.Next() {
		var h, name, mime string
		var dir bool
		if err := rows.Scan(&h, &name, &mime, &dir); err != nil {
			rows.Close()
			return 0, err
		}
		c, e := classify(name, mime, dir)
		todo = append(todo, row{h, c, e})
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(todo) == 0 {
		return 0, err
	}
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE easycloud_files SET category = ?, ext = ? WHERE path_hash = ? AND category IS NULL")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, r := range todo {
		if _, err := stmt.ExecContext(ctx, r.cat, r.ext, r.hash); err != nil {
			return 0, err
		}
	}
	return len(todo), tx.Commit()
}

// CategorySort orders results.
var categorySort = map[string]string{"mtime": "mtime", "name": "LOWER(name)", "size": "size"}

// ByCategory lists files of a category (optionally one of its types) below the given roots (relative paths;
// "." is the whole data root), newest first by default, with the total count.
func (s *Store) ByCategory(ctx context.Context, cat Category, typ string, roots []string, sort string, asc bool, offset, limit int) ([]Node, int, error) {
	if len(roots) == 0 {
		return nil, 0, nil
	}
	var cond string
	var args []any
	if s.catReady.Load() {
		// Directories have category '' so no is_dir test: the count can be
		// answered from the index alone for unrestricted scopes.
		cond, args = cat.indexedCondition(typ)
	} else {
		c, a := cat.typeCondition(typ)
		cond, args = "is_dir = 0 AND "+c, a
	}
	var scope []string
	for _, r := range roots {
		w, a := subtree(r)
		scope = append(scope, w)
		args = append(args, a...)
	}
	where := cond + " AND (" + strings.Join(scope, " OR ") + ")"
	var total int
	if err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_files WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	col, ok := categorySort[sort]
	if !ok {
		col = "mtime"
	}
	dir := "DESC"
	if asc {
		dir = "ASC"
	}
	q := fmt.Sprintf("SELECT %s FROM easycloud_files WHERE %s ORDER BY %s %s, path LIMIT ? OFFSET ?", nodeCols, where, col, dir)
	nodes, err := s.query(ctx, q, append(args, limit, offset)...)
	return nodes, total, err
}
