package webdav

import (
	"log/slog"
	"net/http"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

// MountAdmin registers configuration management (caller applies RequireAdmin).
func MountAdmin(r chi.Router, svc *Service, rec *audit.Recorder, log *slog.Logger) {
	record := func(req *http.Request, action, detail string) {
		sub := httpx.Subject(req)
		rec.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: action, Detail: detail, IP: httpx.ClientIP(req)})
	}
	h := func(fn httpx.Handler) http.HandlerFunc { return httpx.Handler(fn).With(log) }

	r.Get("/webdav", h(func(w http.ResponseWriter, req *http.Request) error {
		items, err := svc.List(req.Context())
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}))
	r.Post("/webdav", h(func(w http.ResponseWriter, req *http.Request) error {
		var in Input
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		c, pw, err := svc.Create(req.Context(), httpx.Subject(req).UserID, in)
		if err != nil {
			return err
		}
		record(req, "webdav.create", c.Slug)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"item": c, "password": pw})
		return nil
	}))
	r.Put("/webdav/{id}", h(func(w http.ResponseWriter, req *http.Request) error {
		var in Input
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		c, err := svc.Update(req.Context(), chi.URLParam(req, "id"), in)
		if err != nil {
			return err
		}
		record(req, "webdav.update", c.Slug)
		httpx.WriteJSON(w, http.StatusOK, c)
		return nil
	}))
	r.Patch("/webdav/{id}", h(func(w http.ResponseWriter, req *http.Request) error {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		c, err := svc.SetEnabled(req.Context(), chi.URLParam(req, "id"), in.Enabled)
		if err != nil {
			return err
		}
		record(req, "webdav.update", c.Slug)
		httpx.WriteJSON(w, http.StatusOK, c)
		return nil
	}))
	r.Post("/webdav/{id}/password", h(func(w http.ResponseWriter, req *http.Request) error {
		pw, err := svc.ResetPassword(req.Context(), chi.URLParam(req, "id"))
		if err != nil {
			return err
		}
		record(req, "webdav.password_reset", chi.URLParam(req, "id"))
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"password": pw})
		return nil
	}))
	r.Delete("/webdav/{id}", h(func(w http.ResponseWriter, req *http.Request) error {
		if err := svc.Delete(req.Context(), chi.URLParam(req, "id")); err != nil {
			return err
		}
		record(req, "webdav.delete", chi.URLParam(req, "id"))
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}
