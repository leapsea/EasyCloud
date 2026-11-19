package settings

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

type item struct {
	Key      string `json:"key"`
	Value    int64  `json:"value"`
	Default  int64  `json:"default"`
	Override bool   `json:"override"` // true when the value comes from the Dashboard
}

// Mount exposes runtime settings to administrators, plus a public client config.
func (r Resolver) Mount(rt chi.Router, log *slog.Logger) {
	rt.Get("/client-config", httpx.Handler(r.clientConfig).With(log))
	rt.Get("/settings", httpx.Handler(r.list).With(log))
	rt.Put("/settings/{key}", httpx.Handler(r.set).With(log))
	rt.Delete("/settings/{key}", httpx.Handler(r.reset).With(log))
}

func (r Resolver) clientConfig(w http.ResponseWriter, req *http.Request) error {
	chunk, _, err := r.Int(req.Context(), UploadChunkSize)
	if err != nil {
		return err
	}
	max, _, err := r.Int(req.Context(), UploadMaxFileSize)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"uploadChunkSize": chunk, "uploadMaxFileSize": max})
	return nil
}

func requireAdmin(req *http.Request) error {
	if !httpx.Subject(req).IsAdmin {
		return errs.ErrNotFound
	}
	return nil
}

func (r Resolver) list(w http.ResponseWriter, req *http.Request) error {
	if err := requireAdmin(req); err != nil {
		return err
	}
	out := []item{}
	for _, k := range All {
		v, over, err := r.Int(req.Context(), k)
		if err != nil {
			return err
		}
		out = append(out, item{Key: k.Name, Value: v, Default: k.Fallback(r.Cfg), Override: over})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

var limits = map[string][2]int64{
	UploadMaxFileSize.Name:  {0, 1 << 50},
	UploadChunkSize.Name:    {1 << 20, 1 << 30},
	TrashRetentionDays.Name: {0, 3650},
	MetricsRawDays.Name:     {1, 90},
	MetricsMinuteDays.Name:  {1, 3650},
}

func (r Resolver) set(w http.ResponseWriter, req *http.Request) error {
	if err := requireAdmin(req); err != nil {
		return err
	}
	key := chi.URLParam(req, "key")
	lim, ok := limits[key]
	if !ok {
		return errs.ErrNotFound
	}
	var in struct {
		Value int64 `json:"value"`
	}
	if err := httpx.DecodeJSON(req, &in); err != nil {
		return err
	}
	if in.Value < lim[0] || in.Value > lim[1] {
		return errs.ErrInvalidInput.WithDetail(map[string]int64{"min": lim[0], "max": lim[1]})
	}
	if err := r.Store.Set(req.Context(), key, strconv.FormatInt(in.Value, 10), httpx.Subject(req).UserID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (r Resolver) reset(w http.ResponseWriter, req *http.Request) error {
	if err := requireAdmin(req); err != nil {
		return err
	}
	if _, ok := limits[chi.URLParam(req, "key")]; !ok {
		return errs.ErrNotFound
	}
	if err := r.Store.Reset(req.Context(), chi.URLParam(req, "key")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
