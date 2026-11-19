// Package httpx holds shared HTTP plumbing: JSON I/O, error mapping, middleware.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keySubject
)

const maxJSONBody = 1 << 20

func RequestID(r *http.Request) string { s, _ := r.Context().Value(keyRequestID).(string); return s }

func Subject(r *http.Request) pathguard.Subject { return SubjectFrom(r.Context()) }

func SubjectFrom(ctx context.Context) pathguard.Subject {
	s, _ := ctx.Value(keySubject).(pathguard.Subject)
	return s
}

func WithSubject(ctx context.Context, s pathguard.Subject) context.Context {
	return context.WithValue(ctx, keySubject, s)
}

// DecodeJSON decodes a bounded JSON body. Paths in the body are decoded exactly once here.
func DecodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errs.ErrInvalidInput.Wrap(err)
	}
	return nil
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	*errs.AppError
	RequestID string `json:"requestId"`
}

// WriteError maps err to a JSON response; unknown errors become an opaque 500.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	ae, ok := errs.As(err)
	if !ok {
		log.Error("internal error", "err", err, "request_id", RequestID(r), "path", r.URL.Path)
		ae = errs.New(http.StatusInternalServerError, "INTERNAL")
	} else if ae.Status >= 500 {
		log.Error("server error", "err", err, "request_id", RequestID(r))
	}
	WriteJSON(w, ae.Status, errorBody{AppError: ae, RequestID: RequestID(r)})
}

// Handler adapts an error-returning handler.
type Handler func(w http.ResponseWriter, r *http.Request) error

func (h Handler) With(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	n, err := s.ResponseWriter.Write(b)
	s.n += int64(n)
	return n, err
}
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logging adds a request id and logs one line per request.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id := hex.EncodeToString(b)
			w.Header().Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), keyRequestID, id)
			sw := &statusWriter{ResponseWriter: w}
			start := time.Now()
			next.ServeHTTP(sw, r.WithContext(ctx))
			if r.URL.Path == "/healthz" && sw.status < 400 {
				return // container healthchecks would otherwise flood the log every 30 s
			}
			log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"bytes", sw.n, "ms", time.Since(start).Milliseconds(), "request_id", id)
		})
	}
}

// SameOrigin rejects unsafe requests whose Origin header names another host (CSRF defence
// on top of SameSite=Lax cookies). Requests without Origin (curl, tus clients) pass.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !OriginAllowed(r) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func OriginAllowed(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.EqualFold(u.Host, host)
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func IsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

var ErrNoSession = errors.New("no session")

// RequireAdmin hides admin endpoints from everyone else (404, not 403).
func RequireAdmin(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Subject(r).IsAdmin {
				WriteError(w, r, log, errs.ErrNotFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
