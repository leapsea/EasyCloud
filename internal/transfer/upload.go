// Package transfer adapts tusd for resumable chunked uploads.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	expslog "golang.org/x/exp/slog"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/app"
	"github.com/easycloud/easycloud/internal/storage/domain"
	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	tus "github.com/tus/tusd/v2/pkg/handler"
)

const BasePath = "/api/upload/"

// Metadata keys sent by the client (tus Upload-Metadata).
const (
	metaFilename   = "filename"
	metaDir        = "dir"
	metaOnConflict = "onConflict"
	metaOwner      = "owner" // set by the server, never trusted from the client
)

type Service struct {
	files    *app.FileService
	settings settings.Resolver
	store    filestore.FileStore
	locker   filelocker.FileLocker
	dir      string
	handler  *tus.Handler
	audit    *audit.Recorder
	log      *slog.Logger
}

func NewService(dataRoot string, files *app.FileService, s settings.Resolver, rec *audit.Recorder, log *slog.Logger) (*Service, error) {
	dir := filepath.Join(dataRoot, filepath.FromSlash(app.UploadDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	svc := &Service{files: files, settings: s, store: filestore.New(dir), dir: dir, audit: rec, log: log}
	svc.probe()
	// Only the extensions the web client uses. Concatenation is left out on
	// purpose: a "final" upload names other uploads by ID, and tusd neither
	// checks their owner nor goes through Handler's owner check (creation is
	// a POST), so it would let one user assemble another user's upload data.
	// Downloads are disabled, so no content server either.
	composer := tus.NewStoreComposer()
	composer.UseCore(svc.store)
	composer.UseTerminater(svc.store)
	composer.UseLengthDeferrer(svc.store)
	svc.locker = filelocker.New(dir)
	svc.locker.UseIn(composer)

	h, err := tus.NewHandler(tus.Config{
		BasePath:                  BasePath,
		StoreComposer:             composer,
		RespectForwardedHeaders:   true,
		DisableDownload:           true,
		PreUploadCreateCallback:   svc.preCreate,
		PreFinishResponseCallback: svc.preFinish,
		Cors:                      &tus.CorsConfig{Disable: true},
		Logger:                    quietTus(log),
	})
	if err != nil {
		return nil, err
	}
	svc.handler = h
	return svc, nil
}

// probe writes and removes a file in the upload area at startup. Uploads stage
// every chunk there, so if it is not writable (typically: created by another
// uid, e.g. an earlier run as root, or a data directory owned by someone
// else) every upload fails; say so once, plainly, instead of only per request.
func (s *Service) probe() {
	f, err := os.CreateTemp(s.dir, ".probe-*")
	if err == nil {
		f.Close()
		err = os.Remove(f.Name())
	}
	if err != nil {
		s.log.Error("upload area is not writable: uploads will fail",
			"dir", s.dir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err,
			"fix", fmt.Sprintf("on the host: sudo chown -R %d:%d <data dir>/%s", os.Getuid(), os.Getgid(), app.UploadDir))
	}
}

func tusErr(err error) error {
	ae, ok := errs.As(err)
	if !ok {
		return err
	}
	body, _ := json.Marshal(ae)
	return tus.Error{ErrorCode: ae.Code, Message: ae.Code, HTTPResponse: tus.HTTPResponse{
		StatusCode: ae.Status, Body: string(body), Header: tus.HTTPHeader{"Content-Type": "application/json"},
	}}
}

func (s *Service) preCreate(hook tus.HookEvent) (tus.HTTPResponse, tus.FileInfoChanges, error) {
	ctx := hook.Context
	sub := httpx.SubjectFrom(ctx)
	info := hook.Upload
	if info.IsFinal || info.IsPartial { // see NewService: concatenation is not offered
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, tusErr(errs.ErrInvalidInput.WithDetail("Upload-Concat is not supported"))
	}
	if info.SizeIsDeferred {
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, tusErr(errs.ErrInvalidInput.WithDetail("Upload-Length is required"))
	}
	max, _, err := s.settings.Int(ctx, settings.UploadMaxFileSize)
	if err != nil {
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, err
	}
	if max > 0 && info.Size > max {
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, tusErr(errs.ErrTooLarge.
			WithDetail(map[string]int64{"limit": max, "actual": info.Size}).
			WithAction(errs.Action{Key: "uploadLimit", Label: "Adjust the upload limit", Route: "/admin/settings", Focus: settings.UploadMaxFileSize.Name, Role: "admin"}))
	}
	name, dir := info.MetaData[metaFilename], info.MetaData[metaDir]
	if err := s.files.CheckUploadTarget(ctx, sub, dir, name, info.Size); err != nil {
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, tusErr(err)
	}
	if err := s.files.ProbeUploadDir(ctx, sub, dir); err != nil {
		s.log.Warn("upload refused: target folder not writable by the server process", "dir", dir, "uid", os.Getuid(), "gid", os.Getgid(), "err", err)
		return tus.HTTPResponse{}, tus.FileInfoChanges{}, tusErr(err)
	}
	policy := info.MetaData[metaOnConflict]
	if policy != string(app.ConflictOverwrite) && policy != string(app.ConflictFail) {
		policy = string(app.ConflictRename)
	}
	return tus.HTTPResponse{}, tus.FileInfoChanges{MetaData: tus.MetaData{
		metaFilename: name, metaDir: dir, metaOnConflict: policy, metaOwner: sub.UserID,
	}}, nil
}

// preFinish moves the finished file into place and reports the final path in a header.
func (s *Service) preFinish(hook tus.HookEvent) (tus.HTTPResponse, error) {
	e, err := s.place(hook.Context, httpx.SubjectFrom(hook.Context), hook.Upload, hook.HTTPRequest.RemoteAddr)
	if err != nil {
		return tus.HTTPResponse{}, tusErr(err)
	}
	return tus.HTTPResponse{Header: tus.HTTPHeader{"X-Easycloud-Path": headerPath(e.Path)}}, nil
}

func (s *Service) place(ctx context.Context, sub pathguard.Subject, info tus.FileInfo, remote string) (domain.FileEntry, error) {
	m := info.MetaData
	e, err := s.files.PlaceUpload(ctx, sub, info.ID, m[metaDir], m[metaFilename], app.ConflictPolicy(m[metaOnConflict]))
	if err != nil {
		s.log.Warn("upload placement failed", "id", info.ID, "dir", m[metaDir], "name", m[metaFilename], "err", err)
		return e, err
	}
	_ = os.Remove(filepath.Join(s.dir, info.ID+".info"))
	s.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: "file.upload", Path: e.Path,
		Detail: strconv.FormatInt(e.Size, 10), IP: hostOnly(remote)})
	return e, nil
}

// finishStale handles a HEAD on an upload whose data is complete but which is
// still in the upload area: its placement failed earlier (e.g. the folder was
// not writable). tus clients treat "offset == length" as success and never
// send another request, so without this the file would never be placed while
// the UI reports it uploaded. Placement is retried now; if it fails again the
// stale upload is removed and 404 returned, so the client starts a fresh
// upload whose creation reports the actual error. Returns false when the
// request is not such a case and tusd should answer it.
func (s *Service) finishStale(w http.ResponseWriter, r *http.Request, id string) bool {
	ctx := r.Context()
	up, err := s.store.GetUpload(ctx, id)
	if err != nil {
		return false
	}
	info, err := up.GetInfo(ctx)
	if err != nil || info.SizeIsDeferred || info.Offset != info.Size {
		return false
	}
	lock, err := s.locker.NewLock(id)
	if err != nil {
		return false
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := lock.Lock(lctx, func() {}); err != nil {
		return false // busy (its final PATCH may be finishing): let tusd answer
	}
	defer lock.Unlock()
	if info, err = up.GetInfo(ctx); err != nil { // re-read under the lock
		return false
	}
	if _, err := os.Stat(filepath.Join(s.dir, id)); err != nil {
		return false
	}
	e, err := s.place(ctx, httpx.Subject(r), info, r.RemoteAddr)
	if err != nil {
		_ = s.store.AsTerminatableUpload(up).Terminate(ctx)
		s.log.Info("stale upload removed after failed placement; the client will start over", "id", id)
		http.NotFound(w, r)
		return true
	}
	h := w.Header()
	h.Set("Tus-Resumable", "1.0.0")
	h.Set("Upload-Offset", strconv.FormatInt(info.Size, 10))
	h.Set("Upload-Length", strconv.FormatInt(info.Size, 10))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Easycloud-Path", headerPath(e.Path))
	w.WriteHeader(http.StatusOK)
	return true
}

// Handler wraps tusd with an owner check so one user cannot read, append to
// or terminate another user's upload.
//
// The check applies to every request that names an upload, whatever its
// method: tusd turns "POST + X-HTTP-Method-Override: PATCH|HEAD|DELETE" into
// that method after this wrapper has run, so trusting r.Method here would
// let such a request skip the check. The override header is refused outright
// as well, as is Upload-Concat; the web client sends neither.
func (s *Service) Handler() http.Handler {
	inner := http.StripPrefix(strings.TrimSuffix(BasePath, "/"), s.handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-HTTP-Method-Override") != "" {
			httpx.WriteError(w, r, s.log, errs.ErrInvalidInput.WithDetail("X-HTTP-Method-Override is not supported"))
			return
		}
		if r.Header.Get("Upload-Concat") != "" { // see NewService: concatenation is not offered
			httpx.WriteError(w, r, s.log, errs.ErrInvalidInput.WithDetail("Upload-Concat is not supported"))
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, BasePath), "/")
		if id != "" && !s.ownedBy(r.Context(), id, httpx.Subject(r).UserID) {
			http.NotFound(w, r)
			return
		}
		if id != "" && r.Method == http.MethodHead && s.finishStale(w, r, id) {
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func (s *Service) ownedBy(ctx context.Context, id, user string) bool {
	if strings.ContainsAny(id, "/\\.") {
		return false
	}
	up, err := s.store.GetUpload(ctx, id)
	if err != nil {
		return false
	}
	info, err := up.GetInfo(ctx)
	return err == nil && info.MetaData[metaOwner] == user
}

// StartJanitor removes abandoned partial uploads older than maxAge.
func (s *Service) StartJanitor(ctx context.Context) {
	const maxAge = 7 * 24 * time.Hour
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		if err := s.sweep(maxAge); err != nil {
			s.log.Error("upload janitor", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) sweep(maxAge time.Duration) error {
	des, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	cut := time.Now().Add(-maxAge)
	var errsOut []error
	for _, de := range des {
		fi, err := de.Info()
		if err != nil || fi.ModTime().After(cut) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, de.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errsOut = append(errsOut, fmt.Errorf("%s: %w", de.Name(), err))
		}
	}
	return errors.Join(errsOut...)
}

// headerPath percent-encodes a path for a response header: header values are
// bytes that browsers read as Latin-1, so raw UTF-8 (Chinese folder names)
// arrives garbled. Slashes stay; the client decodes with decodeURIComponent.
func headerPath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// quietTus routes tusd's warnings and errors (e.g. "InternalServerError:
// permission denied" while writing a chunk) into the application log, so they
// reach the log file like everything else; its per-request INFO lines are
// dropped. tusd uses golang.org/x/exp/slog, a distinct type from log/slog.
func quietTus(log *slog.Logger) *expslog.Logger {
	return expslog.New(tusBridge{log: log.With("component", "tus")})
}

type tusBridge struct{ log *slog.Logger }

func (b tusBridge) Enabled(_ context.Context, l expslog.Level) bool { return l >= expslog.LevelWarn }

func (b tusBridge) Handle(ctx context.Context, r expslog.Record) error {
	attrs := make([]any, 0, r.NumAttrs()*2)
	r.Attrs(func(a expslog.Attr) bool {
		attrs = append(attrs, a.Key, a.Value.Any())
		return true
	})
	b.log.Log(ctx, slog.Level(r.Level), "upload: "+r.Message, attrs...)
	return nil
}

func (b tusBridge) WithAttrs(as []expslog.Attr) expslog.Handler {
	kv := make([]any, 0, len(as)*2)
	for _, a := range as {
		kv = append(kv, a.Key, a.Value.Any())
	}
	return tusBridge{log: b.log.With(kv...)}
}

func (b tusBridge) WithGroup(name string) expslog.Handler {
	return tusBridge{log: b.log.WithGroup(name)}
}
