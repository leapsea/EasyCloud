package thumb

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

func Mount(r chi.Router, s *Service, log *slog.Logger) {
	r.Get("/files/thumb", httpx.Handler(func(w http.ResponseWriter, req *http.Request) error {
		size, _ := strconv.Atoi(req.URL.Query().Get("size"))
		if size == 0 {
			size = 256
		}
		p, mod, err := s.Get(req.Context(), httpx.Subject(req), req.URL.Query().Get("path"), size)
		if err != nil {
			return err
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("ETag", fmt.Sprintf(`"%x-%d"`, mod.UnixNano(), size))
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, req, p)
		return nil
	}).With(log))
}
