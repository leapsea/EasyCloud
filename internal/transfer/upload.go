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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/app"
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
	composer := tus.NewStoreComposer()
	svc.store.UseIn(composer)
	filelocker.New(dir).UseIn(composer)

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
	info := hook.Upload
	m := info.MetaData
	sub := httpx.SubjectFrom(hook.Context)
	e, err := s.files.PlaceUpload(hook.Context, sub, info.ID, m[metaDir], m[metaFilename], app.ConflictPolicy(m[metaOnConflict]))
	if err != nil {
		s.log.Warn("upload placement failed", "id", info.ID, "err", err)
		return tus.HTTPResponse{}, tusErr(err)
	}
	_ = os.Remove(filepath.Join(s.dir, info.ID+".info"))
	s.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: "file.upload", Path: e.Path,
		Detail: strconv.FormatInt(e.Size, 10), IP: hostOnly(hook.HTTPRequest.RemoteAddr)})
	return tus.HTTPResponse{Header: tus.HTTPHeader{"X-Easycloud-Path": e.Path}}, nil
}

// Handler wraps tusd with an owner check so one user cannot PATCH another's upload.
func (s *Service) Handler() http.Handler {
	inner := http.StripPrefix(strings.TrimSuffix(BasePath, "/"), s.handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, BasePath), "/")
		if id != "" && r.Method != http.MethodPost {
			if !s.ownedBy(r.Context(), id, httpx.Subject(r).UserID) {
				http.NotFound(w, r)
				return
			}
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

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// quietTus keeps tusd's per-request INFO lines out of the log; warnings still pass.
// tusd uses golang.org/x/exp/slog, a distinct type from log/slog.
func quietTus(_ *slog.Logger) *expslog.Logger {
	return expslog.New(expslog.NewJSONHandler(os.Stdout, &expslog.HandlerOptions{Level: expslog.LevelWarn}))
}
