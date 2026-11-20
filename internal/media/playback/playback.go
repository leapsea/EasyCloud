// Package playback serves videos for the web player. Files that browsers
// would stutter on (see mp4fix) are streamed as a fixed virtual file built
// from the unchanged original; everything else is served as is.
package playback

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/media/mp4fix"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/storage/app"
	"github.com/easycloud/easycloud/internal/storage/domain"
	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
)

// Only ISO BMFF containers can be fixed.
var fixableExt = map[string]bool{".mp4": true, ".m4v": true, ".mov": true}

// memPlans is how many fixed headers stay in memory (a 3-hour video's is ~16 MB).
const memPlans = 4

type Service struct {
	files *app.FileService
	dir   string // persisted plans, under the app data directory
	audit *audit.Recorder
	log   *slog.Logger

	group singleflight.Group
	mu    sync.Mutex
	mem   []cached // most recent last
}

type cached struct {
	key  string
	plan *mp4fix.Plan // nil: plays fine as is
}

func New(files *app.FileService, cacheDir string, rec *audit.Recorder, log *slog.Logger) *Service {
	return &Service{files: files, dir: cacheDir, audit: rec, log: log}
}

// cacheKey changes whenever the file is replaced or modified.
func cacheKey(e domain.FileEntry) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", e.Path, e.Size, e.ModTime.UnixNano())))
	return hex.EncodeToString(h[:16])
}

// plan returns the fix plan for an opened file, or nil when it needs none
// (or cannot be fixed). Results are cached in memory and on disk; concurrent
// requests for the same file (a player issues several range requests) share
// one analysis.
func (s *Service) plan(f *os.File, e domain.FileEntry) *mp4fix.Plan {
	if !fixableExt[strings.ToLower(filepath.Ext(e.Name))] {
		return nil
	}
	key := cacheKey(e)
	if p, ok := s.fromMemory(key); ok {
		return p
	}
	v, _, _ := s.group.Do(key, func() (any, error) {
		if p, ok := s.fromDisk(key); ok {
			s.remember(key, p)
			return p, nil
		}
		start := time.Now()
		p, err := mp4fix.Analyze(f, e.Size)
		switch {
		case err == nil:
			s.log.Info("video played through a fixed header", "path", e.Path, "reasons", p.Reasons, "ms", time.Since(start).Milliseconds())
		case errors.Is(err, mp4fix.ErrNotNeeded), mp4fix.IsUnsupported(err):
			p = nil
		default: // I/O error: do not cache, try again next time
			s.log.Warn("video analysis failed", "path", e.Path, "err", err)
			return (*mp4fix.Plan)(nil), nil
		}
		s.remember(key, p)
		s.toDisk(key, p)
		return p, nil
	})
	return v.(*mp4fix.Plan)
}

func (s *Service) fromMemory(key string) (*mp4fix.Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.mem {
		if c.key == key {
			s.mem = append(append(s.mem[:i:i], s.mem[i+1:]...), c)
			return c.plan, true
		}
	}
	return nil, false
}

func (s *Service) remember(key string, p *mp4fix.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem = append(s.mem, cached{key, p})
	if len(s.mem) > memPlans {
		s.mem = s.mem[len(s.mem)-memPlans:]
	}
}

// On disk: <key>.plan holds the gob-encoded plan; <key>.ok marks "no fix needed".
func (s *Service) fromDisk(key string) (*mp4fix.Plan, bool) {
	if _, err := os.Stat(filepath.Join(s.dir, key+".ok")); err == nil {
		return nil, true
	}
	f, err := os.Open(filepath.Join(s.dir, key+".plan"))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var p mp4fix.Plan
	if err := gob.NewDecoder(f).Decode(&p); err != nil {
		return nil, false
	}
	return &p, true
}

func (s *Service) toDisk(key string, p *mp4fix.Plan) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return
	}
	if p == nil {
		_ = os.WriteFile(filepath.Join(s.dir, key+".ok"), nil, 0o640)
		return
	}
	tmp, err := os.CreateTemp(s.dir, key+".*.tmp")
	if err != nil {
		return
	}
	if err := gob.NewEncoder(tmp).Encode(p); err != nil || tmp.Close() != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	_ = os.Rename(tmp.Name(), filepath.Join(s.dir, key+".plan"))
}

// Prune removes cached plans not used for maxAge (their files may be gone).
func (s *Service) Prune(maxAge time.Duration) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, d := range entries {
		if fi, err := d.Info(); err == nil && time.Since(fi.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(s.dir, d.Name()))
		}
	}
}

// FixedName is the name a fixed copy is saved under: "-fix" before the extension.
func FixedName(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-fix" + ext
}

// SaveFixed writes the fixed version of the video at raw next to it as
// FixedName(name), leaving the original untouched. It needs read access to
// the video and write access to its folder, refuses to replace an existing
// file, and reports progress through app.WithProgress (it runs as a task).
func (s *Service) SaveFixed(ctx context.Context, sub pathguard.Subject, raw string) (string, error) {
	f, e, err := s.files.Open(ctx, sub, raw)
	if err != nil {
		return "", err
	}
	defer f.Close()
	p := s.plan(f, e)
	if p == nil {
		return "", errs.ErrInvalidInput.WithDetail("this video plays fine and needs no fixed copy")
	}
	src, err := pathguard.Parse(e.Path)
	if err != nil {
		return "", errs.ErrInvalidPath
	}
	dst, err := src.Parent().Join(FixedName(e.Name))
	if err != nil {
		return "", errs.ErrInvalidPath
	}
	out, err := s.files.CreateFile(ctx, sub, dst.Display(), p.Open(f), p.Size)
	if err != nil {
		return "", err
	}
	s.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: "file.fix_video", Path: out.Path,
		Detail: e.Path + " (" + strings.Join(p.Reasons, ",") + ")"})
	return out.Path, nil
}

// Mount registers the player endpoints (caller applies authentication).
//
//	GET /files/playback?path=  -> {"fixed": bool, "reasons": [...]}
//	GET /files/stream?path=    -> the video, fixed when needed (Range supported)
func Mount(r chi.Router, s *Service, log *slog.Logger) {
	r.Get("/files/playback", httpx.Handler(s.info).With(log))
	r.Get("/files/stream", httpx.Handler(s.stream).With(log))
}

func (s *Service) info(w http.ResponseWriter, r *http.Request) error {
	f, e, err := s.files.Open(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	defer f.Close()
	p := s.plan(f, e)
	out := map[string]any{"fixed": p != nil, "reasons": []string{}}
	if p != nil {
		out["reasons"] = p.Reasons
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// stream mirrors the inline download (same authorization, sandbox headers
// and audit entry) but serves the fixed virtual file when there is one.
func (s *Service) stream(w http.ResponseWriter, r *http.Request) error {
	f, e, err := s.files.Open(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	defer f.Close()
	if rg := r.Header.Get("Range"); rg == "" || strings.HasPrefix(rg, "bytes=0-") {
		sub := httpx.Subject(r)
		s.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: "file.download", Path: e.Path, Detail: "play", IP: httpx.ClientIP(r)})
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": e.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; media-src 'self'")
	ct := e.Mime
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	p := s.plan(f, e)
	if p == nil {
		http.ServeContent(w, r, e.Name, e.ModTime, f)
		return nil
	}
	w.Header().Set("X-EasyCloud-Fixed", strings.Join(p.Reasons, ","))
	http.ServeContent(w, r, e.Name, e.ModTime, p.Open(f))
	return nil
}
