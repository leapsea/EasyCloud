package plugin

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

//go:embed host/host.html
var hostHTML []byte

//go:embed host/host.js
var hostJS []byte

// Viewer pages are sandboxed; the CSP keeps plugin code from loading anything
// but its own files and the token URL of the file being viewed.
const viewerCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"media-src 'self' blob:; connect-src 'self'; font-src 'self' data:; frame-src 'none'; form-action 'none'; base-uri 'none'"

func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrUntrusted):
		return errs.New(http.StatusForbidden, "PLUGIN_UNTRUSTED").WithDetail(err.Error())
	case errors.Is(err, ErrInvalid):
		return errs.ErrInvalidInput.WithDetail(err.Error())
	}
	return err
}

// MountStatic serves plugin assets and the sandbox host page (no session needed:
// assets are not secret, and the sandboxed iframe cannot send cookies anyway).
func MountStatic(r chi.Router, m *Manager) {
	r.Get("/plugins/_host.html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", viewerCSP)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(hostHTML)
	})
	r.Get("/plugins/_host.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(hostJS)
	})
	r.Get("/plugins/{id}/{version}/*", func(w http.ResponseWriter, req *http.Request) {
		dir, ok := m.StaticDir(chi.URLParam(req, "id"), chi.URLParam(req, "version"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		rel := chi.URLParam(req, "*")
		if !validRel(rel) || rel == manifestName {
			http.NotFound(w, req)
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer root.Close()
		f, err := root.Open(rel)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			http.NotFound(w, req)
			return
		}
		if strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".mjs") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		w.Header().Set("Access-Control-Allow-Origin", "*") // module scripts from an opaque-origin iframe use CORS
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", viewerCSP)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // versioned URL
		http.ServeContent(w, req, rel, fi.ModTime(), f)
	})
}

// Mount registers routes for signed-in users.
func Mount(r chi.Router, m *Manager, log *slog.Logger) {
	r.Get("/plugins/viewers", func(w http.ResponseWriter, _ *http.Request) {
		_, office := m.Connector("onlyoffice")
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": m.Viewers(), "office": office})
	})
}

// HTTPFetcher downloads with a timeout and size limit.
func HTTPFetcher(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, fmt.Errorf("unsupported URL %q", url)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("response too large")
	}
	return b, err
}

// Audit is the subset of the audit recorder the admin handlers need.
type Audit func(r *http.Request, action, detail string)

// MountAdmin registers plugin management (caller applies RequireAdmin).
func MountAdmin(r chi.Router, m *Manager, fetch Fetcher, audit Audit, log *slog.Logger) {
	h := func(fn httpx.Handler) http.HandlerFunc {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error { return mapErr(fn(w, r)) }).With(log)
	}
	r.Get("/plugins", h(func(w http.ResponseWriter, _ *http.Request) error {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": m.List(), "repository": m.Repository()})
		return nil
	}))
	r.Post("/plugins", h(func(w http.ResponseWriter, req *http.Request) error {
		data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxPackageSize))
		if err != nil {
			return errs.ErrTooLarge.Wrap(err)
		}
		inst, err := m.Install(req.Context(), data)
		if err != nil {
			return err
		}
		audit(req, "plugin.install", inst.Manifest.ID+"@"+inst.Manifest.Version)
		httpx.WriteJSON(w, http.StatusCreated, inst)
		return nil
	}))
	r.Get("/plugins/repository", h(func(w http.ResponseWriter, req *http.Request) error {
		items, err := m.Available(req.Context(), fetch)
		if err != nil {
			return errs.New(http.StatusBadGateway, "PLUGIN_REPOSITORY_UNAVAILABLE").WithDetail(err.Error())
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}))
	r.Post("/plugins/repository/install", h(func(w http.ResponseWriter, req *http.Request) error {
		var in struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		inst, err := m.InstallFromRepo(req.Context(), fetch, in.ID, in.Version)
		if err != nil {
			return err
		}
		audit(req, "plugin.install", inst.Manifest.ID+"@"+inst.Manifest.Version)
		httpx.WriteJSON(w, http.StatusCreated, inst)
		return nil
	}))
	r.Patch("/plugins/{id}", h(func(w http.ResponseWriter, req *http.Request) error {
		var in struct {
			Enabled *bool          `json:"enabled"`
			Config  map[string]any `json:"config"`
		}
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		if err := m.Update(req.Context(), chi.URLParam(req, "id"), in.Enabled, in.Config); err != nil {
			return err
		}
		audit(req, "plugin.toggle", chi.URLParam(req, "id"))
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
	r.Delete("/plugins/{id}", h(func(w http.ResponseWriter, req *http.Request) error {
		if err := m.Remove(req.Context(), chi.URLParam(req, "id")); err != nil {
			return err
		}
		audit(req, "plugin.remove", chi.URLParam(req, "id"))
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}
