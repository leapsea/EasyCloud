package monitor

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

// MountAdmin registers metric history endpoints (caller applies RequireAdmin).
func MountAdmin(r chi.Router, store MetricStore, c *Collector, extra func(r *http.Request) map[string]any, log *slog.Logger) {
	r.Get("/metrics", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		q := req.URL.Query()
		to := time.Now()
		dur, err := time.ParseDuration(q.Get("range"))
		if err != nil || dur <= 0 {
			dur = time.Hour
		}
		if dur > 90*24*time.Hour {
			return errs.ErrInvalidInput.WithDetail("range must be at most 90 days")
		}
		if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && v > 0 {
			to = time.Unix(v, 0)
		}
		from := to.Add(-dur)
		points, _ := strconv.Atoi(q.Get("points"))
		if points <= 0 || points > 2000 {
			points = 360
		}
		out := map[string][]Point{}
		for _, name := range strings.Split(q.Get("metric"), ",") {
			id, ok := MetricNames[strings.TrimSpace(name)]
			if !ok {
				return errs.ErrInvalidInput.WithDetail("unknown metric " + name)
			}
			pts, err := Query(store, id, from, to, points)
			if err != nil {
				return err
			}
			if pts == nil {
				pts = []Point{}
			}
			out[name] = pts
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"from": from.Unix(), "to": to.Unix(), "series": out})
		return nil
	}).With(log))

	r.Get("/system", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		resp := map[string]any{"host": c.HostInfo(req.Context())}
		for k, v := range extra(req) {
			resp[k] = v
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
		return nil
	}).With(log))
}
