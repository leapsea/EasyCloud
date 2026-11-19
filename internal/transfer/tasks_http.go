package transfer

import (
	"log/slog"
	"net/http"

	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

// MountTasks registers task endpoints for signed-in users.
func (m *TaskManager) MountTasks(r chi.Router, log *slog.Logger) {
	r.Post("/tasks", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		var in struct {
			Kind string `json:"kind"`
			TaskParams
		}
		if err := httpx.DecodeJSON(req, &in); err != nil {
			return err
		}
		t, err := m.Create(req.Context(), httpx.Subject(req), in.Kind, in.TaskParams)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusAccepted, t)
		return nil
	}).With(log))
	r.Get("/tasks", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		items, err := m.List(req.Context(), httpx.Subject(req))
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}).With(log))
	r.Post("/tasks/{id}/cancel", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		if err := m.Cancel(req.Context(), httpx.Subject(req), chi.URLParam(req, "id")); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}).With(log))
	r.Delete("/tasks", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		if err := m.ClearFinished(req.Context(), httpx.Subject(req)); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}).With(log))
}
