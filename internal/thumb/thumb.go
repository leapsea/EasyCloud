// Package thumb generates and caches thumbnails. Common image formats are
// decoded in pure Go; anything else is delegated to backend plugins.
package thumb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/pkg/pluginsdk"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

var Sizes = map[int]bool{128: true, 256: true, 512: true, 1024: true}

const maxPixels = 100_000_000 // refuse decompression bombs

var ErrUnsupported = errs.New(415, "THUMBNAIL_UNSUPPORTED")

// Providers finds a plugin able to thumbnail a MIME type.
type Providers interface {
	ThumbnailProvider(mime string) (pluginsdk.Extension, string, bool)
}

type Service struct {
	guard    pathguard.Authorizer
	absRoot  string
	cacheDir string
	plugins  Providers
	log      *slog.Logger

	sem      chan struct{}
	mu       sync.Mutex
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	err  error
}

func New(guard pathguard.Authorizer, absRoot, cacheDir string, plugins Providers, log *slog.Logger) *Service {
	return &Service{guard: guard, absRoot: absRoot, cacheDir: cacheDir, plugins: plugins, log: log,
		sem: make(chan struct{}, 2), inflight: map[string]*call{}}
}

var builtin = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/bmp": true, "image/tiff": true}

func mimeOf(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if m := mime.TypeByExtension(ext); m != "" {
		return strings.SplitN(m, ";", 2)[0]
	}
	switch ext {
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".cr2":
		return "image/x-canon-cr2"
	case ".nef":
		return "image/x-nikon-nef"
	case ".arw":
		return "image/x-sony-arw"
	case ".dng":
		return "image/x-adobe-dng"
	case ".mkv":
		return "video/x-matroska"
	}
	return ""
}

// Supported reports whether a thumbnail can be produced for name right now.
func (s *Service) Supported(name string) bool {
	m := mimeOf(name)
	if builtin[m] {
		return true
	}
	if s.plugins == nil || m == "" {
		return false
	}
	_, _, ok := s.plugins.ThumbnailProvider(m)
	return ok
}

// Get returns the path of a cached JPEG thumbnail, generating it if needed.
func (s *Service) Get(ctx context.Context, sub pathguard.Subject, raw string, size int) (string, time.Time, error) {
	if !Sizes[size] {
		return "", time.Time{}, errs.ErrInvalidInput.WithDetail("size must be 128, 256, 512 or 1024")
	}
	p, err := pathguard.Parse(raw)
	if err != nil {
		return "", time.Time{}, errs.ErrInvalidPath
	}
	h, err := s.guard.Check(ctx, sub, p, pathguard.PermRead)
	if err != nil {
		return "", time.Time{}, errs.ErrNotFound
	}
	defer h.Close()
	fi, err := h.Root.Stat(h.Rel) // follows symlinks only inside the grant
	if err != nil || !fi.Mode().IsRegular() {
		return "", time.Time{}, errs.ErrNotFound
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", p.String(), fi.ModTime().UnixNano(), fi.Size(), size)))
	key := hex.EncodeToString(sum[:])
	out := filepath.Join(s.cacheDir, key[:2], key+".jpg")
	if cfi, err := os.Stat(out); err == nil {
		if time.Since(cfi.ModTime()) > 24*time.Hour {
			now := time.Now()
			_ = os.Chtimes(out, now, now) // keep frequently viewed thumbnails out of Prune
		}
		return out, fi.ModTime(), nil
	}

	s.mu.Lock()
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		<-c.done
		return out, fi.ModTime(), c.err
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(c.done)
	}()

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		c.err = ctx.Err()
		return "", time.Time{}, c.err
	}
	img, err := s.generate(ctx, h, p, size)
	if err != nil {
		c.err = err
		return "", time.Time{}, err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		c.err = err
		return "", time.Time{}, err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, img, 0o644); err != nil {
		c.err = err
		return "", time.Time{}, err
	}
	c.err = os.Rename(tmp, out)
	return out, fi.ModTime(), c.err
}

func (s *Service) generate(ctx context.Context, h pathguard.Handle, p pathguard.RelPath, size int) ([]byte, error) {
	m := mimeOf(p.Name())
	if builtin[m] {
		f, err := h.Root.Open(h.Rel)
		if err != nil {
			return nil, errs.ErrNotFound
		}
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			return nil, ErrUnsupported.Wrap(err)
		}
		if cfg.Width*cfg.Height > maxPixels {
			return nil, ErrUnsupported.WithDetail("image too large to thumbnail")
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		img, err := imaging.Decode(f, imaging.AutoOrientation(true))
		if err != nil {
			return nil, ErrUnsupported.Wrap(err)
		}
		return encode(imaging.Fit(img, size, size, imaging.Lanczos))
	}
	if s.plugins == nil || m == "" {
		return nil, ErrUnsupported
	}
	ext, id, ok := s.plugins.ThumbnailProvider(m)
	if !ok {
		return nil, ErrUnsupported
	}
	// Authorization happened above and Stat went through the grant root; hand the
	// plugin the absolute path of exactly that file.
	abs := filepath.Join(s.absRoot, filepath.FromSlash(p.String()))
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	res, err := ext.Thumbnail(cctx, &pluginsdk.ThumbnailRequest{Path: abs, Mime: m, Size: size})
	if err != nil {
		s.log.Warn("plugin thumbnail failed", "plugin", id, "path", p.Display(), "err", err)
		return nil, ErrUnsupported.Wrap(err)
	}
	if res.Mime == "image/jpeg" && len(res.Image) > 0 {
		return res.Image, nil
	}
	img, _, err := image.Decode(bytes.NewReader(res.Image)) // normalise other formats to JPEG
	if err != nil {
		return nil, ErrUnsupported.Wrap(err)
	}
	return encode(imaging.Fit(img, size, size, imaging.Lanczos))
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Prune deletes cache files not modified for maxAge (thumbnails of deleted or changed files).
func (s *Service) Prune(maxAge time.Duration) {
	cut := time.Now().Add(-maxAge)
	_ = filepath.WalkDir(s.cacheDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().Before(cut) {
			_ = os.Remove(p)
		}
		return nil
	})
}
