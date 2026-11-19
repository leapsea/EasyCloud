package share

import (
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/filetoken"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/storage/domain"
	"github.com/easycloud/easycloud/internal/thumb"
	"github.com/go-chi/chi/v5"
)

const unlockTTL = 12 * time.Hour

type HTTP struct {
	svc    *Service
	signer *filetoken.Signer
	thumbs *thumb.Service
	audit  *audit.Recorder
	log    *slog.Logger
}

func NewHTTP(svc *Service, signer *filetoken.Signer, thumbs *thumb.Service, rec *audit.Recorder, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, signer: signer, thumbs: thumbs, audit: rec, log: log}
}

func (h *HTTP) record(r *http.Request, action, path, detail string) {
	sub := httpx.Subject(r)
	h.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: action, Path: path, Detail: detail, IP: httpx.ClientIP(r)})
}

type shareResp struct {
	Share
	URL string `json:"url"`
}

// Mount registers owner endpoints (session required).
func (h *HTTP) Mount(r chi.Router) {
	r.Post("/shares", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		var in CreateInput
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		sh, err := h.svc.Create(req.Context(), httpx.Subject(req), in)
		if err != nil {
			return err
		}
		h.record(req, "share.create", sh.Path, sh.ID)
		httpx.WriteJSON(w, http.StatusCreated, shareResp{sh, "/s/" + sh.ID})
		return nil
	}).With(h.log))
	r.Get("/shares", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		items, err := h.svc.Mine(req.Context(), httpx.Subject(req))
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}).With(h.log))
	r.Delete("/shares/{id}", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		sh, err := h.svc.Delete(req.Context(), httpx.Subject(req), chi.URLParam(req, "id"))
		if err != nil {
			return err
		}
		h.record(req, "share.delete", sh.Path, sh.ID)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}).With(h.log))
}

// MountAdmin lists every share (caller applies RequireAdmin).
func (h *HTTP) MountAdmin(r chi.Router) {
	r.Get("/shares", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		items, err := h.svc.All(req.Context())
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}).With(h.log))
}

// MountPublic registers the visitor API under /api/s/{id} (no session).
func (h *HTTP) MountPublic(r chi.Router) {
	r.Route("/s/{id}", func(s chi.Router) {
		s.Use(noStore)
		s.Get("/", httpx.Handler(h.meta).With(h.log))
		s.Post("/unlock", httpx.Handler(h.unlock).With(h.log))
		s.Get("/list", httpx.Handler(h.list).With(h.log))
		s.Get("/download", httpx.Handler(h.download).With(h.log))
		s.Get("/zip", httpx.Handler(h.zip).With(h.log))
		s.Get("/thumb", httpx.Handler(h.thumb).With(h.log))
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex")
		next.ServeHTTP(w, r)
	})
}

func cookieName(id string) string { return "ec_share_" + id }

// open resolves the share and enforces the password.
func (h *HTTP) open(r *http.Request) (Access, error) {
	a, err := h.svc.Resolve(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return a, err
	}
	if !a.Share.HasPassword {
		return a, nil
	}
	tok := r.URL.Query().Get("token")
	if c, err := r.Cookie(cookieName(a.Share.ID)); tok == "" && err == nil {
		tok = c.Value
	}
	if c, err := h.signer.Verify(tok, "share"); err == nil && c.Path == a.Share.ID {
		return a, nil
	}
	return a, ErrLocked
}

type metaResp struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	IsDir         bool      `json:"isDir"`
	Size          int64     `json:"size,omitempty"`
	Mime          string    `json:"mime,omitempty"`
	Owner         string    `json:"owner"`
	ExpireAt      time.Time `json:"expireAt,omitzero"`
	HasPassword   bool      `json:"hasPassword"`
	Unlocked      bool      `json:"unlocked"`
	DownloadsLeft int       `json:"downloadsLeft"` // -1 = unlimited
}

func (h *HTTP) meta(w http.ResponseWriter, r *http.Request) error {
	a, err := h.open(r)
	if err != nil && err != ErrLocked {
		return err
	}
	m := metaResp{ID: a.Share.ID, Name: displayName(a.Share.Path), IsDir: a.Share.IsDir, Owner: a.Owner.DisplayName,
		ExpireAt: a.Share.ExpireAt, HasPassword: a.Share.HasPassword, Unlocked: err == nil, DownloadsLeft: -1}
	if a.Share.MaxDownloads > 0 {
		m.DownloadsLeft = a.Share.MaxDownloads - a.Share.Downloads
	}
	if err == nil && !a.Share.IsDir {
		if e, err := h.svc.files.Stat(r.Context(), a.Subject(), a.Share.Path); err == nil {
			m.Size, m.Mime = e.Size, e.Mime
		}
	}
	httpx.WriteJSON(w, http.StatusOK, m)
	return nil
}

func (h *HTTP) unlock(w http.ResponseWriter, r *http.Request) error {
	a, err := h.svc.Resolve(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.CheckPassword(a, httpx.ClientIP(r), in.Password); err != nil {
		return err
	}
	tok := h.signer.Issue(filetoken.Claims{Path: a.Share.ID, Purpose: "share"}, unlockTTL)
	http.SetCookie(w, &http.Cookie{Name: cookieName(a.Share.ID), Value: tok, Path: "/api/s/" + a.Share.ID, HttpOnly: true,
		Secure: httpx.IsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: int(unlockTTL.Seconds())})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"token": tok})
	return nil
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) error {
	a, err := h.open(r)
	if err != nil {
		return err
	}
	if !a.Share.IsDir {
		return errs.ErrNotFound
	}
	dir, err := a.Within(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	l, err := h.svc.files.List(r.Context(), a.Subject(), dir)
	if err != nil {
		return err
	}
	root := strings.TrimSuffix(a.Share.Path, "/")
	rel := func(p string) string {
		r := strings.TrimPrefix(p, root)
		if r == "" {
			return "/"
		}
		return r
	}
	entries := make([]domain.FileEntry, len(l.Entries))
	for i, e := range l.Entries {
		e.Path = rel(e.Path)
		entries[i] = e
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"path": rel(l.Path), "entries": entries})
	return nil
}

func firstByte(r *http.Request) bool {
	rg := r.Header.Get("Range")
	return rg == "" || strings.HasPrefix(rg, "bytes=0-")
}

func (h *HTTP) consume(r *http.Request, a Access, p string) error {
	if !firstByte(r) {
		return nil // resuming or seeking: not a new download
	}
	ok, err := h.svc.CountDownload(r.Context(), a.Share.ID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGone
	}
	h.audit.Record(audit.Entry{Username: "visitor", Action: "share.download", Path: p, Detail: a.Share.ID, IP: httpx.ClientIP(r)})
	return nil
}

func (h *HTTP) download(w http.ResponseWriter, r *http.Request) error {
	a, err := h.open(r)
	if err != nil {
		return err
	}
	p, err := a.Within(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	f, e, err := h.svc.files.Open(r.Context(), a.Subject(), p)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := h.consume(r, a, e.Path); err != nil {
		return err
	}
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disp = "inline"
	}
	hd := w.Header()
	hd.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": e.Name}))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	if e.Mime != "" {
		hd.Set("Content-Type", e.Mime)
	} else {
		hd.Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, e.Name, e.ModTime, f)
	return nil
}

func (h *HTTP) zip(w http.ResponseWriter, r *http.Request) error {
	a, err := h.open(r)
	if err != nil {
		return err
	}
	if !a.Share.IsDir {
		return errs.ErrNotFound
	}
	p, err := a.Within(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	if err := h.consume(r, a, p); err != nil {
		return err
	}
	name := displayName(p)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
	if err := h.svc.files.WriteZip(r.Context(), a.Subject(), []string{p}, w); err != nil {
		h.log.Warn("share zip aborted", "share", a.Share.ID, "err", err)
	}
	return nil
}

func (h *HTTP) thumb(w http.ResponseWriter, r *http.Request) error {
	a, err := h.open(r)
	if err != nil {
		return err
	}
	p, err := a.Within(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size == 0 {
		size = 256
	}
	file, _, err := h.thumbs.Get(r.Context(), a.Subject(), p, size)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, file)
	return nil
}
