// Package transport exposes storage use cases over HTTP. Paths are only ever
// carried in query strings or JSON bodies, never in the URL path.
package transport

import (
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/storage/app"
	"github.com/go-chi/chi/v5"
)

type handlers struct {
	files *app.FileService
	audit *audit.Recorder
}

func (h handlers) record(r *http.Request, action, path, detail string) {
	sub := httpx.Subject(r)
	h.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: action, Path: path, Detail: detail, IP: httpx.ClientIP(r)})
}

func Mount(r chi.Router, files *app.FileService, rec *audit.Recorder, log *slog.Logger) {
	h := handlers{files: files, audit: rec}
	r.Get("/files/list", httpx.Handler(h.list).With(log))
	r.Get("/files/stat", httpx.Handler(h.stat).With(log))
	r.Get("/files/download", httpx.Handler(h.download).With(log))
	r.Post("/files/mkdir", httpx.Handler(h.mkdir).With(log))
	r.Post("/files/rename", httpx.Handler(h.rename).With(log))
	r.Post("/files/move", httpx.Handler(h.move).With(log))
	r.Post("/files/copy", httpx.Handler(h.copy).With(log))
	r.Post("/files/trash", httpx.Handler(h.trash).With(log))
	r.Get("/files/zip", httpx.Handler(h.zip).With(log))

	r.Get("/trash", httpx.Handler(h.trashList).With(log))
	r.Post("/trash/{id}/restore", httpx.Handler(h.restore).With(log))
	r.Delete("/trash/{id}", httpx.Handler(h.purge).With(log))
	r.Delete("/trash", httpx.Handler(h.empty).With(log))
}

func (h handlers) list(w http.ResponseWriter, r *http.Request) error {
	l, err := h.files.List(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, l)
	return nil
}

func (h handlers) stat(w http.ResponseWriter, r *http.Request) error {
	e, err := h.files.Stat(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, e)
	return nil
}

// download supports Range (video seeking) via http.ServeContent. Inline responses are
// sandboxed so an uploaded HTML/SVG file cannot run script on our origin.
func (h handlers) download(w http.ResponseWriter, r *http.Request) error {
	f, e, err := h.files.Open(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	defer f.Close()
	if r.Header.Get("Range") == "" || strings.HasPrefix(r.Header.Get("Range"), "bytes=0-") {
		h.record(r, "file.download", e.Path, "") // once per download, not per seek
	}
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": e.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	if e.Mime != "" {
		w.Header().Set("Content-Type", e.Mime)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, e.Name, e.ModTime, f)
	return nil
}

func (h handlers) mkdir(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Path string `json:"path"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	e, err := h.files.Mkdir(r.Context(), httpx.Subject(r), in.Path)
	if err != nil {
		return err
	}
	h.record(r, "file.mkdir", e.Path, "")
	httpx.WriteJSON(w, http.StatusCreated, e)
	return nil
}

func (h handlers) rename(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	e, err := h.files.Rename(r.Context(), httpx.Subject(r), in.Path, in.Name)
	if err != nil {
		return err
	}
	h.record(r, "file.rename", in.Path, "→ "+e.Path)
	httpx.WriteJSON(w, http.StatusOK, e)
	return nil
}

type transferReq struct {
	Sources    []string           `json:"sources"`
	Dest       string             `json:"dest"`
	OnConflict app.ConflictPolicy `json:"onConflict"`
}

func (in transferReq) validate() error {
	if len(in.Sources) == 0 || len(in.Sources) > 1000 {
		return errs.ErrInvalidInput.WithDetail("sources must contain 1..1000 items")
	}
	switch in.OnConflict {
	case "", app.ConflictFail, app.ConflictRename, app.ConflictOverwrite:
		return nil
	}
	return errs.ErrInvalidInput.WithDetail("onConflict must be fail, rename or overwrite")
}

func (h handlers) move(w http.ResponseWriter, r *http.Request) error { return h.transfer(w, r, false) }
func (h handlers) copy(w http.ResponseWriter, r *http.Request) error { return h.transfer(w, r, true) }

func (h handlers) transfer(w http.ResponseWriter, r *http.Request, copy bool) error {
	var in transferReq
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	fn := h.files.Move
	if copy {
		fn = h.files.Copy
	}
	done, err := fn(r.Context(), httpx.Subject(r), in.Sources, in.Dest, in.OnConflict)
	action := "file.move"
	if copy {
		action = "file.copy"
	}
	for i, p := range done {
		h.record(r, action, in.Sources[i], "→ "+p)
	}
	if err != nil {
		if ae, ok := errs.As(err); ok && len(done) > 0 {
			return ae.WithDetail(map[string]any{"completed": done, "reason": ae.Detail})
		}
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"paths": done})
	return nil
}

func (h handlers) trash(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Paths []string `json:"paths"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if len(in.Paths) == 0 || len(in.Paths) > 1000 {
		return errs.ErrInvalidInput.WithDetail("paths must contain 1..1000 items")
	}
	items, err := h.files.MoveToTrash(r.Context(), httpx.Subject(r), in.Paths)
	for _, it := range items {
		h.record(r, "file.trash", it.OriginPath, "")
	}
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (h handlers) trashList(w http.ResponseWriter, r *http.Request) error {
	items, err := h.files.ListTrash(r.Context(), httpx.Subject(r))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (h handlers) restore(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		OnConflict app.ConflictPolicy `json:"onConflict"`
	}
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
	}
	p, err := h.files.Restore(r.Context(), httpx.Subject(r), chi.URLParam(r, "id"), in.OnConflict)
	if err != nil {
		return err
	}
	h.record(r, "trash.restore", p, "")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"path": p})
	return nil
}

func (h handlers) purge(w http.ResponseWriter, r *http.Request) error {
	if err := h.files.Purge(r.Context(), httpx.Subject(r), chi.URLParam(r, "id")); err != nil {
		return err
	}
	h.record(r, "trash.purge", "", chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) empty(w http.ResponseWriter, r *http.Request) error {
	n, err := h.files.EmptyTrash(r.Context(), httpx.Subject(r))
	if err != nil {
		return err
	}
	h.record(r, "trash.empty", "", strconv.Itoa(n))
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"purged": n})
	return nil
}

// zip streams the selected paths (?path=a&path=b) as one archive.
func (h handlers) zip(w http.ResponseWriter, r *http.Request) error {
	paths := r.URL.Query()["path"]
	if len(paths) == 0 || len(paths) > 1000 {
		return errs.ErrInvalidInput.WithDetail("1..1000 path parameters required")
	}
	// Authorize everything before the first byte is written.
	for _, p := range paths {
		if _, err := h.files.Stat(r.Context(), httpx.Subject(r), p); err != nil {
			return err
		}
	}
	name := "files.zip"
	if len(paths) == 1 {
		if e, err := h.files.Stat(r.Context(), httpx.Subject(r), paths[0]); err == nil && e.Name != "" {
			name = e.Name + ".zip"
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	for _, p := range paths {
		h.record(r, "file.download", p, "zip")
	}
	return h.files.WriteZip(r.Context(), httpx.Subject(r), paths, w)
}
