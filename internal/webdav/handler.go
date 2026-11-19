package webdav

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"golang.org/x/net/webdav"
)

const Prefix = "/dav"

// Methods are the WebDAV verbs; routers must know them to dispatch.
var Methods = []string{"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK"}

// Handler serves every config under /dav/<slug>/ with HTTP Basic authentication
// against that config's own account (use HTTPS in production).
func Handler(svc *Service, files *storageapp.FileService, root *os.Root, rec *audit.Recorder, log *slog.Logger) http.Handler {
	var locks sync.Map // config id -> webdav.LockSystem
	mutating := map[string]string{"PUT": "dav.put", "DELETE": "dav.delete", "MKCOL": "dav.mkcol", "MOVE": "dav.move", "COPY": "dav.copy"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, Prefix+"/")
		slug, _, _ := strings.Cut(rest, "/")
		if slug == "" || !slugRe.MatchString(slug) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == Prefix+"/"+slug { // clients expect the collection URL to end with a slash
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok {
			challenge(w, slug)
			return
		}
		c, err := svc.Authenticate(r.Context(), slug, httpx.ClientIP(r), user, pass)
		if errors.Is(err, errRateLimited) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		if err != nil {
			challenge(w, slug)
			return
		}
		ls, _ := locks.LoadOrStore(c.ID, webdav.NewMemLS())
		sub := pathguard.Subject{UserID: c.CreatedBy, Username: "webdav:" + c.Slug}
		h := &webdav.Handler{
			Prefix:     Prefix + "/" + slug,
			FileSystem: NewVFS(files, root, c, sub),
			LockSystem: ls.(webdav.LockSystem),
			Logger: func(r *http.Request, err error) {
				if action, ok := mutating[r.Method]; ok && err == nil {
					rec.Record(audit.Entry{UserID: c.CreatedBy, Username: "webdav:" + c.Slug, Action: action,
						Path: strings.TrimPrefix(r.URL.Path, Prefix+"/"+slug), Detail: r.Header.Get("Destination"), IP: httpx.ClientIP(r)})
				} else if err != nil && log != nil {
					log.Debug("webdav", "config", c.Slug, "method", r.Method, "path", r.URL.Path, "err", err)
				}
			},
		}
		h.ServeHTTP(w, r)
	})
}

func challenge(w http.ResponseWriter, slug string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="EasyCloud WebDAV `+slug+`", charset="UTF-8"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}
