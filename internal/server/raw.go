package server

import (
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/easycloud/easycloud/internal/filetoken"
	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"github.com/go-chi/chi/v5"
)

const rawTTL = 4 * time.Hour

// mountTokens adds POST /api/files/token (session) for issuing capability URLs.
func mountTokens(r chi.Router, signer *filetoken.Signer, files *storageapp.FileService, log *slog.Logger) {
	r.Post("/files/token", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		var in struct {
			Path string `json:"path"`
		}
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		sub := httpx.Subject(req)
		e, err := files.Stat(req.Context(), sub, in.Path)
		if err != nil {
			return err
		}
		if e.IsDir {
			return errs.ErrInvalidInput.WithDetail("not a file")
		}
		tok := signer.Issue(filetoken.Claims{UserID: sub.UserID, Path: e.Path, Purpose: "raw"}, rawTTL)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"url": "/api/raw/" + tok + "/" + urlName(e.Name), "expiresIn": int(rawTTL.Seconds())})
		return nil
	}).With(log))
}

func urlName(n string) string { return escapeSegment(n) }

// mountRaw serves GET /api/raw/{token}/{name} without a session. The token's
// user must still exist, be enabled and have read access to the path.
func mountRaw(r chi.Router, signer *filetoken.Signer, ids *identity.Service, files *storageapp.FileService, log *slog.Logger) {
	r.Get("/raw/{token}/*", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		c, err := signer.Verify(chi.URLParam(req, "token"), "raw")
		if err != nil {
			return errs.ErrNotFound
		}
		u, err := ids.GetUser(req.Context(), c.UserID)
		if err != nil || u.Disabled {
			return errs.ErrNotFound
		}
		f, e, err := files.Open(req.Context(), u.Subject(), c.Path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*") // the token is the credential; sandboxed viewers have an opaque origin
		h.Set("Cache-Control", "private, max-age=3600")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
		h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": e.Name}))
		if e.Mime != "" {
			h.Set("Content-Type", e.Mime)
		} else {
			h.Set("Content-Type", "application/octet-stream")
		}
		http.ServeContent(w, req, e.Name, e.ModTime, f)
		return nil
	}).With(log))
}
