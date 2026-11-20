package index

import (
	"context"
	"fmt"
	"strings"
)

// Category groups files by kind for the "categories" views. Membership is
// decided by extension, with the MIME prefix as a fallback: the index derives
// MIME from the extension through the platform table, which in minimal
// containers misses common types (.mkv, .heic, ...).
type Category struct {
	Name       string
	MimePrefix string
	Exts       []string
}

var Categories = map[string]Category{
	"image": {"image", "image/", []string{"jpg", "jpeg", "png", "gif", "webp", "avif", "bmp", "tif", "tiff", "svg", "ico",
		"heic", "heif", "raw", "cr2", "cr3", "nef", "arw", "dng", "orf", "rw2", "raf"}},
	"video": {"video", "video/", []string{"mp4", "m4v", "mov", "mkv", "avi", "wmv", "flv", "webm", "ts", "m2ts", "mts",
		"3gp", "rm", "rmvb", "mpg", "mpeg", "vob"}},
	"audio": {"audio", "audio/", []string{"mp3", "flac", "wav", "aac", "m4a", "ogg", "oga", "opus", "wma", "ape", "aiff", "aif",
		"alac", "dsf", "dff", "mid", "midi"}},
	"doc": {"doc", "", []string{"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "txt", "md",
		"markdown", "csv", "epub", "mobi", "azw3", "pages", "numbers", "key", "wps", "et", "dps"}},
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

// CategorySort orders results.
var categorySort = map[string]string{"mtime": "mtime", "name": "LOWER(name)", "size": "size"}

// ByCategory lists files of a category below the given roots (relative paths;
// "." is the whole data root), newest first by default, with the total count.
func (s *Store) ByCategory(ctx context.Context, cat Category, roots []string, sort string, asc bool, offset, limit int) ([]Node, int, error) {
	if len(roots) == 0 {
		return nil, 0, nil
	}
	cond, args := cat.condition()
	var scope []string
	for _, r := range roots {
		w, a := subtree(r)
		scope = append(scope, w)
		args = append(args, a...)
	}
	where := "is_dir = 0 AND " + cond + " AND (" + strings.Join(scope, " OR ") + ")"
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
