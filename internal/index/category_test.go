package index_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/database/dbtest"
	"github.com/easycloud/easycloud/internal/index"
)

// The indexed category columns must answer exactly like the pattern match
// they replace, for rows written before the migration (backfilled) and after.
func TestCategoryIndexMatchesPatterns(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		names := []string{"a.JPG", "b.jpeg", "c.png", "d.heic", "e.CR2", "f.bmp", "g", "h.mkv", "i.MP4", "j.webm",
			"k.mp3", "l.flac", "m.wma", "n.pdf", "o.Docx", "p.xlsx", "q.pptx", "r.md", "s.epub", "t.csv",
			"u.bin", "v.tar.gz", "w.文档", "x.unknownimage", "y.verylongextensionname"}
		var nodes []index.Node
		for i, n := range names {
			mime := ""
			if n == "x.unknownimage" {
				mime = "image/x-odd" // only the MIME says it is an image
			}
			nodes = append(nodes, index.Node{Path: "share/" + n, Name: n, Size: 1, MTime: time.Unix(int64(1000+i), 0), Mime: mime})
		}
		nodes = append(nodes, index.Node{Path: "share/dir.jpg", Name: "dir.jpg", IsDir: true, MTime: time.Unix(1, 0)})
		store := index.NewStore(db)
		if err := store.Upsert(ctx, nodes); err != nil {
			t.Fatal(err)
		}

		list := func() map[string]string {
			out := map[string]string{}
			for k, c := range index.Categories {
				for _, typ := range append([]string{"", index.TypeOther}, keys(c)...) {
					got, total, err := store.ByCategory(ctx, c, typ, []string{"share"}, "name", true, 0, 100)
					if err != nil {
						t.Fatal(k, typ, err)
					}
					var ps []string
					for _, n := range got {
						ps = append(ps, n.Name)
					}
					sort.Strings(ps)
					if total != len(ps) {
						t.Errorf("%s/%s total %d, listed %d", k, typ, total, len(ps))
					}
					out[k+"/"+typ] = strings.Join(ps, ",")
				}
			}
			return out
		}
		slow := list() // a fresh store uses the pattern match

		// Simulate rows from before the migration, then backfill.
		if _, err := db.W.ExecContext(ctx, "UPDATE easycloud_files SET category = NULL, ext = ''"); err != nil {
			t.Fatal(err)
		}
		store.Backfill(ctx, dbtest.Logger())
		fast := list()
		for k, v := range slow {
			if fast[k] != v {
				t.Errorf("%s: indexed %q, patterns %q", k, fast[k], v)
			}
		}
		if slow["doc/"] != "n.pdf,o.Docx,p.xlsx,q.pptx,r.md,s.epub,t.csv" || slow["image/other"] != "f.bmp,x.unknownimage" || slow["doc/other"] != "s.epub" ||
			slow["other/"] != "g,u.bin,v.tar.gz,w.文档,y.verylongextensionname" || slow["other/archive"] != "v.tar.gz" ||
			slow["other/other"] != "g,u.bin,w.文档,y.verylongextensionname" {
			t.Errorf("unexpected results: %v", slow)
		}
		var nulls int
		db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_files WHERE category IS NULL").Scan(&nulls)
		if nulls != 0 {
			t.Fatalf("%d rows left without category", nulls)
		}
	})
}

func keys(c index.Category) []string {
	var out []string
	for _, t := range c.Types {
		out = append(out, t.Key)
	}
	return out
}
