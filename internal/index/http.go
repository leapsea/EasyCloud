package index

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

type searchHit struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Mime    string `json:"mime,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

// Mount registers search and usage endpoints, scoped by the authorizer.
func Mount(r chi.Router, store *Store, guard pathguard.Authorizer, log *slog.Logger) {
	r.Get("/files/search", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		term := strings.TrimSpace(req.URL.Query().Get("q"))
		if len([]rune(term)) < 1 || len(term) > 200 {
			return errs.ErrInvalidInput.WithDetail("q must be 1-200 characters")
		}
		sub := httpx.Subject(req)
		roots, err := guard.Roots(req.Context(), sub)
		if err != nil {
			return err
		}
		// Optional scope: search only below `path`, intersected with the readable roots.
		var scoped []string
		if raw := req.URL.Query().Get("path"); raw != "" && raw != "/" {
			p, err := pathguard.Parse(raw)
			if err != nil {
				return errs.ErrInvalidPath
			}
			scoped = scopeRoots(p, roots)
		} else {
			for _, r := range roots {
				scoped = append(scoped, r.String())
			}
		}
		nodes, err := store.Search(req.Context(), term, scoped, 200)
		if err != nil {
			return err
		}
		hits := make([]searchHit, 0, len(nodes))
		for _, n := range nodes {
			hits = append(hits, searchHit{Name: n.Name, Path: "/" + n.Path, IsDir: n.IsDir, Size: n.Size,
				ModTime: n.MTime.Format("2006-01-02T15:04:05Z07:00"), Mime: n.Mime, Width: n.Width, Height: n.Height})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": hits, "truncated": len(hits) == 200})
		return nil
	}).With(log))

	r.Get("/files/usage", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		p, err := pathguard.Parse(req.URL.Query().Get("path"))
		if err != nil {
			return errs.ErrInvalidPath
		}
		h, err := guard.Check(req.Context(), httpx.Subject(req), p, pathguard.PermRead)
		if err != nil {
			return errs.ErrNotFound
		}
		h.Close()
		b, n, err := store.Usage(req.Context(), p.String())
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]int64{"bytes": b, "files": n})
		return nil
	}).With(log))
}

// scopeRoots intersects the subtree p with the readable roots.
func scopeRoots(p pathguard.RelPath, roots []pathguard.RelPath) []string {
	var out []string
	for _, r := range roots {
		if p.HasPrefix(r) {
			return []string{p.String()} // p lies inside a readable root
		}
		if r.HasPrefix(p) {
			out = append(out, r.String()) // readable roots below p
		}
	}
	return out
}
