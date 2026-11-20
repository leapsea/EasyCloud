// Package app implements storage use cases on top of pathguard handles.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/domain"
)

type ConflictPolicy string

const (
	ConflictFail      ConflictPolicy = "fail"
	ConflictRename    ConflictPolicy = "rename"
	ConflictOverwrite ConflictPolicy = "overwrite"
)

// Event is published after a successful mutation (WebSocket hub, thumbnails, ...).
type Event struct {
	Kind string   `json:"kind"` // created | removed | renamed | changed
	Dirs []string `json:"dirs"` // affected directories, display form
}

type EventPublisher interface{ Publish(Event) }

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// PathObserver is told when a path is moved or renamed through the API
// (grants and shares keyed by path follow it).
type PathObserver interface {
	PathMoved(ctx context.Context, from, to pathguard.RelPath)
}

// Protector reports system directories that must not be deleted, moved or renamed.
type Protector interface {
	Protected(ctx context.Context, p pathguard.RelPath) bool
}

// ErrProtected is returned for attempts to delete, move or rename a system directory.
var ErrProtected = errs.New(403, "PROTECTED_PATH")

// Enricher adds index-derived metadata (image dimensions, thumbnail support) to listings.
type Enricher interface {
	Enrich(ctx context.Context, dir pathguard.RelPath, entries []domain.FileEntry)
}

// QuotaChecker rejects writes that would exceed a user's quota.
type QuotaChecker interface {
	Check(ctx context.Context, sub pathguard.Subject, target pathguard.RelPath, add int64) error
	// TreeSize estimates the bytes under p (from the index).
	TreeSize(ctx context.Context, p pathguard.RelPath) int64
}

type FileService struct {
	protector Protector
	enricher  Enricher
	quota     QuotaChecker
	observers []PathObserver
	guard     pathguard.Authorizer
	dataRoot  *os.Root // only used for the reserved dir (trash) and cross-handle renames
	trash     domain.TrashRepository
	events    EventPublisher
	clock     Clock
	settings  settings.Resolver
	log       *slog.Logger
}

func NewFileService(g pathguard.Authorizer, dataRoot *os.Root, t domain.TrashRepository,
	e EventPublisher, c Clock, s settings.Resolver, log *slog.Logger) *FileService {
	return &FileService{guard: g, dataRoot: dataRoot, trash: t, events: e, clock: c, settings: s, log: log}
}

func (s *FileService) AddObserver(o PathObserver) { s.observers = append(s.observers, o) }
func (s *FileService) SetQuota(q QuotaChecker)    { s.quota = q }
func (s *FileService) SetEnricher(e Enricher)     { s.enricher = e }
func (s *FileService) SetProtector(p Protector)   { s.protector = p }

// guardProtected refuses operations on system directories. It runs for every
// caller (web UI, API, WebDAV), so the frontend restriction cannot be bypassed.
func (s *FileService) guardProtected(ctx context.Context, p pathguard.RelPath) error {
	if s.protector != nil && s.protector.Protected(ctx, p) {
		return ErrProtected.WithDetail(map[string]string{"path": p.Display()})
	}
	return nil
}

func (s *FileService) checkQuota(ctx context.Context, sub pathguard.Subject, target pathguard.RelPath, add int64) error {
	if s.quota == nil {
		return nil
	}
	return s.quota.Check(ctx, sub, target, add)
}

func (s *FileService) check(ctx context.Context, sub pathguard.Subject, raw string, need pathguard.Perm) (pathguard.RelPath, pathguard.Handle, error) {
	p, err := pathguard.Parse(raw)
	if err != nil {
		return p, pathguard.Handle{}, errs.ErrInvalidPath
	}
	h, err := s.guard.Check(ctx, sub, p, need)
	if err != nil {
		return p, h, errs.ErrNotFound
	}
	return p, h, nil
}

func (s *FileService) publish(kind string, dirs ...pathguard.RelPath) {
	if s.events == nil {
		return
	}
	ds := make([]string, len(dirs))
	for i, d := range dirs {
		ds[i] = d.Display()
	}
	s.events.Publish(Event{Kind: kind, Dirs: ds})
}

// mapFSErr converts filesystem errors to AppErrors without leaking host paths.
func mapFSErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return errs.ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return errs.ErrConflict
	case errors.Is(err, fs.ErrPermission):
		return errs.New(403, "FS_PERMISSION_DENIED").Wrap(err)
	case strings.Contains(err.Error(), "path escapes"):
		return errs.ErrNotFound.Wrap(err)
	}
	return err
}

type Listing struct {
	Path string `json:"path"`
	// Access is "rw", "r", or "nav" (navigation only: an ancestor of granted directories).
	Access  string             `json:"access"`
	Entries []domain.FileEntry `json:"entries"`
}

func (s *FileService) List(ctx context.Context, sub pathguard.Subject, dir string) (Listing, error) {
	p, err := pathguard.Parse(dir)
	if err != nil {
		return Listing{}, errs.ErrInvalidPath
	}
	vis, err := s.guard.Visible(ctx, sub, p)
	if err != nil {
		return Listing{}, errs.ErrNotFound
	}
	if vis.Perm == 0 {
		return s.listNav(p, vis)
	}
	h, err := s.guard.Check(ctx, sub, p, pathguard.PermRead)
	if err != nil {
		return Listing{}, errs.ErrNotFound
	}
	defer h.Close()
	des, err := fs.ReadDir(h.Root.FS(), h.Rel)
	if err != nil {
		return Listing{}, mapFSErr(err)
	}
	out := make([]domain.FileEntry, 0, len(des))
	for _, de := range des {
		if p.IsRoot() && de.Name() == pathguard.ReservedDir {
			continue
		}
		child, err := p.Join(de.Name())
		if err != nil {
			continue // names we refuse to address (control chars etc.) are hidden
		}
		// Stat through the root: symlinks pointing outside are dropped here.
		fi, err := h.Root.Stat(path.Join(h.Rel, de.Name()))
		if err != nil {
			continue
		}
		out = append(out, toEntry(child, fi))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if s.enricher != nil {
		s.enricher.Enrich(ctx, p, out)
	}
	if s.protector != nil {
		for i := range out {
			if out[i].IsDir {
				if c, err := p.Join(out[i].Name); err == nil && s.protector.Protected(ctx, c) {
					out[i].Protected = true
				}
			}
		}
	}
	return Listing{Path: p.Display(), Access: vis.Perm.String(), Entries: out}, nil
}

// listNav lists only the children that lead to granted directories. Sizes are
// hidden; existence is checked so deleted grant targets do not show up.
func (s *FileService) listNav(p pathguard.RelPath, vis pathguard.Visibility) (Listing, error) {
	out := []domain.FileEntry{}
	for _, name := range vis.SortedChildren() {
		child, err := p.Join(name)
		if err != nil {
			continue
		}
		fi, err := s.dataRoot.Stat(child.String())
		if err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, domain.FileEntry{Name: name, Path: child.Display(), IsDir: true, ModTime: fi.ModTime().UTC()})
	}
	if len(out) == 0 && !p.IsRoot() {
		return Listing{}, errs.ErrNotFound
	}
	return Listing{Path: p.Display(), Access: "nav", Entries: out}, nil
}

func toEntry(p pathguard.RelPath, fi fs.FileInfo) domain.FileEntry {
	e := domain.FileEntry{Name: p.Name(), Path: p.Display(), IsDir: fi.IsDir(), ModTime: fi.ModTime().UTC()}
	if !fi.IsDir() {
		e.Size = fi.Size()
		e.Mime = mime.TypeByExtension(path.Ext(p.Name()))
	}
	return e
}

func (s *FileService) Stat(ctx context.Context, sub pathguard.Subject, raw string) (domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermRead)
	if err != nil {
		return domain.FileEntry{}, err
	}
	defer h.Close()
	fi, err := h.Root.Stat(h.Rel)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	return toEntry(p, fi), nil
}

// Open returns a regular file for download; directories are rejected.
func (s *FileService) Open(ctx context.Context, sub pathguard.Subject, raw string) (*os.File, domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermRead)
	if err != nil {
		return nil, domain.FileEntry{}, err
	}
	defer h.Close() // an opened *os.File stays valid after its Root is closed
	f, err := h.Root.Open(h.Rel)
	if err != nil {
		return nil, domain.FileEntry{}, mapFSErr(err)
	}
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		f.Close()
		return nil, domain.FileEntry{}, errs.ErrInvalidInput.WithDetail("not a file")
	}
	return f, toEntry(p, fi), nil
}

// OpenEntry opens a file or directory read-only for inspection (details such
// as the creation time); it needs the same read permission as Open.
func (s *FileService) OpenEntry(ctx context.Context, sub pathguard.Subject, raw string) (*os.File, domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermRead)
	if err != nil {
		return nil, domain.FileEntry{}, err
	}
	defer h.Close()
	f, err := h.Root.Open(h.Rel)
	if err != nil {
		return nil, domain.FileEntry{}, mapFSErr(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, domain.FileEntry{}, mapFSErr(err)
	}
	e := toEntry(p, fi)
	if s.protector != nil && e.IsDir {
		e.Protected = s.protector.Protected(ctx, p)
	}
	return f, e, nil
}

func (s *FileService) Mkdir(ctx context.Context, sub pathguard.Subject, raw string) (domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermWrite)
	if err != nil {
		return domain.FileEntry{}, err
	}
	defer h.Close()
	if p.IsRoot() || h.Rel == "." {
		return domain.FileEntry{}, errs.ErrInvalidPath
	}
	if err := h.Root.Mkdir(h.Rel, 0o755); err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	fi, err := h.Root.Stat(h.Rel)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	s.publish("created", p.Parent())
	return toEntry(p, fi), nil
}

// Rename changes the last segment in place.
func (s *FileService) Rename(ctx context.Context, sub pathguard.Subject, raw, newName string) (domain.FileEntry, error) {
	p, err := pathguard.Parse(raw)
	if err != nil || p.IsRoot() {
		return domain.FileEntry{}, errs.ErrInvalidPath
	}
	to, err := p.Parent().Join(newName)
	if err != nil {
		return domain.FileEntry{}, errs.ErrInvalidPath
	}
	if err := s.move(ctx, sub, p, to, ConflictFail); err != nil {
		return domain.FileEntry{}, err
	}
	return s.Stat(ctx, sub, to.Display())
}

// Move moves each source into destDir. Returns the final display paths.
func (s *FileService) Move(ctx context.Context, sub pathguard.Subject, sources []string, destDir string, policy ConflictPolicy) ([]string, error) {
	return s.transfer(ctx, sub, sources, destDir, policy, false)
}

func (s *FileService) Copy(ctx context.Context, sub pathguard.Subject, sources []string, destDir string, policy ConflictPolicy) ([]string, error) {
	return s.transfer(ctx, sub, sources, destDir, policy, true)
}

func (s *FileService) transfer(ctx context.Context, sub pathguard.Subject, sources []string, destDir string, policy ConflictPolicy, copy bool) ([]string, error) {
	dst, err := pathguard.Parse(destDir)
	if err != nil {
		return nil, errs.ErrInvalidPath
	}
	var done []string
	for _, raw := range sources {
		src, err := pathguard.Parse(raw)
		if err != nil || src.IsRoot() {
			return done, errs.ErrInvalidPath
		}
		if dst.HasPrefix(src) {
			return done, errs.ErrInvalidInput.WithDetail("cannot move or copy a directory into itself")
		}
		target, err := dst.Join(src.Name())
		if err != nil {
			return done, errs.ErrInvalidPath
		}
		if copy {
			target, err = s.copyOne(ctx, sub, src, target, policy)
		} else {
			target, err = s.moveResolve(ctx, sub, src, target, policy)
		}
		if err != nil {
			return done, err
		}
		done = append(done, target.Display())
	}
	return done, nil
}

// resolveTarget applies the conflict policy and returns the path to write to.
func (s *FileService) resolveTarget(h pathguard.Handle, target pathguard.RelPath, policy ConflictPolicy, srcIsDir bool) (pathguard.RelPath, pathguard.Handle, bool, error) {
	if h.Rel == "." {
		return target, h, false, errs.ErrInvalidPath // cannot replace a granted directory itself
	}
	// The parent must resolve inside the handle root: this also rejects parents
	// reached through symlinks that point outside the grant.
	if fi, err := h.Root.Stat(path.Dir(h.Rel)); err != nil || !fi.IsDir() {
		return target, h, false, errs.ErrNotFound
	}
	if _, err := h.Root.Lstat(h.Rel); errors.Is(err, fs.ErrNotExist) {
		return target, h, false, nil
	} else if err != nil {
		return target, h, false, mapFSErr(err)
	}
	switch policy {
	case ConflictOverwrite:
		fi, err := h.Root.Lstat(h.Rel)
		if err == nil && (fi.IsDir() || srcIsDir) {
			return target, h, false, errs.ErrConflict.WithDetail("directories cannot be overwritten")
		}
		return target, h, true, nil
	case ConflictRename:
		ext := path.Ext(target.Name())
		base := strings.TrimSuffix(target.Name(), ext)
		if srcIsDir {
			base, ext = target.Name(), ""
		}
		for i := 1; i < 1000; i++ {
			cand, err := target.Parent().Join(fmt.Sprintf("%s (%d)%s", base, i, ext))
			if err != nil {
				break
			}
			rel := path.Join(path.Dir(h.Rel), cand.Name())
			if _, err := h.Root.Lstat(rel); errors.Is(err, fs.ErrNotExist) {
				return cand, pathguard.Handle{Root: h.Root, Rel: rel, Perm: h.Perm}, false, nil
			}
		}
		return target, h, false, errs.ErrConflict
	default:
		return target, h, false, errs.ErrConflict.WithDetail(map[string]string{"path": target.Display()})
	}
}

func (s *FileService) moveResolve(ctx context.Context, sub pathguard.Subject, src, target pathguard.RelPath, policy ConflictPolicy) (pathguard.RelPath, error) {
	sh, err := s.guard.Check(ctx, sub, src, pathguard.PermWrite)
	if err != nil {
		return target, errs.ErrNotFound
	}
	defer sh.Close()
	if sh.Rel == "." {
		return target, errs.ErrInvalidInput.WithDetail("a granted directory cannot be moved")
	}
	if err := s.guardProtected(ctx, src); err != nil {
		return target, err
	}
	fi, err := sh.Root.Lstat(sh.Rel)
	if err != nil {
		return target, mapFSErr(err)
	}
	th, err := s.guard.Check(ctx, sub, target, pathguard.PermWrite)
	if err != nil {
		return target, errs.ErrNotFound
	}
	defer th.Close()
	target, th, overwrite, err := s.resolveTarget(th, target, policy, fi.IsDir())
	if err != nil {
		return target, err
	}
	if overwrite && src.Equal(target) {
		return target, nil
	}
	// Moving into a quota-limited home from elsewhere adds to its usage.
	if s.quota != nil && !sameHome(src, target) {
		size := fi.Size()
		if fi.IsDir() {
			size = s.quota.TreeSize(ctx, src)
		}
		if err := s.checkQuota(ctx, sub, target, size); err != nil {
			return target, err
		}
	}
	if err := s.renameAcross(ctx, sh, th); err != nil {
		return target, err
	}
	for _, o := range s.observers {
		o.PathMoved(ctx, src, target)
	}
	s.publish("renamed", src.Parent(), target.Parent())
	return target, nil
}

func (s *FileService) move(ctx context.Context, sub pathguard.Subject, src, target pathguard.RelPath, policy ConflictPolicy) error {
	_, err := s.moveResolve(ctx, sub, src, target, policy)
	return err
}

// full returns a handle's path relative to the data root.
func full(h pathguard.Handle) string {
	if h.Base.IsRoot() {
		return h.Rel
	}
	if h.Rel == "." {
		return h.Base.String()
	}
	return h.Base.String() + "/" + h.Rel
}

// relTo returns p relative to base, or false if p is not inside base.
func relTo(base pathguard.Handle, p string) (string, bool) {
	if base.Base.IsRoot() {
		return p, true
	}
	b := base.Base.String()
	if p == b {
		return ".", true
	}
	if strings.HasPrefix(p, b+"/") {
		return p[len(b)+1:], true
	}
	return "", false
}

// renameAcross renames through whichever grant root contains both paths, so
// symlinks stay confined. Across unrelated grants, or across devices, it
// falls back to copy + remove.
func (s *FileService) renameAcross(ctx context.Context, from, to pathguard.Handle) error {
	for _, r := range []pathguard.Handle{from, to} {
		fr, ok1 := relTo(r, full(from))
		tr, ok2 := relTo(r, full(to))
		if !ok1 || !ok2 {
			continue
		}
		err := r.Root.Rename(fr, tr)
		if err == nil || !errors.Is(err, syscall.EXDEV) {
			return mapFSErr(err)
		}
		break
	}
	if err := copyTree(ctx, from.Root, from.Rel, to.Root, to.Rel); err != nil {
		_ = to.Root.RemoveAll(to.Rel)
		return mapFSErr(err)
	}
	return mapFSErr(from.Root.RemoveAll(from.Rel))
}

func (s *FileService) copyOne(ctx context.Context, sub pathguard.Subject, src, target pathguard.RelPath, policy ConflictPolicy) (pathguard.RelPath, error) {
	sh, err := s.guard.Check(ctx, sub, src, pathguard.PermRead)
	if err != nil {
		return target, errs.ErrNotFound
	}
	defer sh.Close()
	fi, err := sh.Root.Stat(sh.Rel)
	if err != nil {
		return target, mapFSErr(err)
	}
	th, err := s.guard.Check(ctx, sub, target, pathguard.PermWrite)
	if err != nil {
		return target, errs.ErrNotFound
	}
	defer th.Close()
	target, th, _, err = s.resolveTarget(th, target, policy, fi.IsDir())
	if err != nil {
		return target, err
	}
	size := fi.Size()
	if fi.IsDir() && s.quota != nil {
		size = s.quota.TreeSize(ctx, src)
	}
	if err := s.checkQuota(ctx, sub, target, size); err != nil {
		return target, err
	}
	if err := copyTree(ctx, sh.Root, sh.Rel, th.Root, th.Rel); err != nil {
		return target, mapFSErr(err)
	}
	s.publish("created", target.Parent())
	return target, nil
}

// copyTree copies through os.Root on both sides, so symlinks cannot escape either root.
func copyTree(ctx context.Context, sr *os.Root, src string, dr *os.Root, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := sr.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil // symlinks are not followed or duplicated
	case fi.IsDir():
		if err := dr.Mkdir(dst, fi.Mode().Perm()|0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		des, err := fs.ReadDir(sr.FS(), src)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTree(ctx, sr, path.Join(src, de.Name()), dr, path.Join(dst, de.Name())); err != nil {
				return err
			}
		}
		return nil
	case fi.Mode().IsRegular():
		in, err := sr.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := dr.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(&progressWriter{w: out, ctx: ctx, report: progressFrom(ctx)}, in); err != nil {
			out.Close()
			_ = dr.Remove(dst) // no half-copied files
			return err
		}
		return out.Close()
	}
	return nil // devices, sockets, fifos are skipped
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UploadDir is where tus chunks live, relative to the data root (same filesystem as user files).
var UploadDir = path.Join(pathguard.ReservedDir, "uploads")

// CheckUploadTarget validates that sub may create dir/name (size bytes) before any bytes are accepted.
func (s *FileService) CheckUploadTarget(ctx context.Context, sub pathguard.Subject, dir, name string, size int64) error {
	d, err := pathguard.Parse(dir)
	if err != nil {
		return errs.ErrInvalidPath
	}
	target, err := d.Join(name)
	if err != nil {
		return errs.ErrInvalidPath
	}
	dh, err := s.guard.Check(ctx, sub, d, pathguard.PermWrite)
	if err != nil {
		return errs.ErrNotFound
	}
	defer dh.Close()
	if fi, err := dh.Root.Stat(dh.Rel); err != nil || !fi.IsDir() {
		return errs.ErrNotFound
	}
	return s.checkQuota(ctx, sub, target, size)
}

// PlaceUpload atomically renames a finished upload (uploadName inside UploadDir) to dir/name.
func (s *FileService) PlaceUpload(ctx context.Context, sub pathguard.Subject, uploadName, dir, name string, policy ConflictPolicy) (domain.FileEntry, error) {
	if err := s.CheckUploadTarget(ctx, sub, dir, name, 0); err != nil {
		return domain.FileEntry{}, err
	}
	d, _ := pathguard.Parse(dir)
	target, _ := d.Join(name)
	th, err := s.guard.Check(ctx, sub, target, pathguard.PermWrite)
	if err != nil {
		return domain.FileEntry{}, errs.ErrNotFound
	}
	defer th.Close()
	target, _, overwrite, err := s.resolveTarget(th, target, policy, false)
	if err != nil {
		return domain.FileEntry{}, err
	}
	if overwrite {
		// Never let an upload replace a directory or a symlink.
		if fi, err := th.Root.Lstat(th.Rel); err == nil && !fi.Mode().IsRegular() {
			return domain.FileEntry{}, errs.ErrConflict
		}
	}
	if err := s.dataRoot.Rename(path.Join(UploadDir, uploadName), target.String()); err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	s.publish("created", target.Parent())
	return s.Stat(ctx, sub, target.Display())
}

// sameHome reports whether a and b are inside the same user/<name> directory.
func sameHome(a, b pathguard.RelPath) bool {
	as, bs := a.Segments(), b.Segments()
	return len(as) >= 2 && len(bs) >= 2 && as[0] == "user" && bs[0] == "user" && as[1] == bs[1]
}

// Replace atomically overwrites an existing regular file with r (temp file in
// the same directory, then rename), e.g. when an online editor saves.
func (s *FileService) Replace(ctx context.Context, sub pathguard.Subject, raw string, r io.Reader) (domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermWrite)
	if err != nil {
		return domain.FileEntry{}, err
	}
	defer h.Close()
	fi, err := h.Root.Lstat(h.Rel)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	if !fi.Mode().IsRegular() {
		return domain.FileEntry{}, errs.ErrInvalidInput.WithDetail("not a regular file")
	}
	tmp := path.Join(path.Dir(h.Rel), ".~easycloud-"+newID()+".tmp")
	out, err := h.Root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	_, err = io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = h.Root.Remove(tmp)
		return domain.FileEntry{}, err
	}
	if err := h.Root.Rename(tmp, h.Rel); err != nil {
		_ = h.Root.Remove(tmp)
		return domain.FileEntry{}, mapFSErr(err)
	}
	s.publish("changed", p.Parent())
	nfi, err := h.Root.Stat(h.Rel)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	return toEntry(p, nfi), nil
}

// CreateFile writes r to a new file at raw, which must not exist yet: it never
// replaces anything. Write permission and the quota (size bytes) are checked
// first; data goes to a temporary file in the same directory that only takes
// the final name once complete, so a failed or cancelled write leaves nothing
// behind. Progress and cancellation follow WithProgress / ctx.
func (s *FileService) CreateFile(ctx context.Context, sub pathguard.Subject, raw string, r io.Reader, size int64) (domain.FileEntry, error) {
	p, h, err := s.check(ctx, sub, raw, pathguard.PermWrite)
	if err != nil {
		return domain.FileEntry{}, err
	}
	defer h.Close()
	if h.Rel == "." {
		return domain.FileEntry{}, errs.ErrInvalidPath
	}
	if fi, err := h.Root.Stat(path.Dir(h.Rel)); err != nil || !fi.IsDir() {
		return domain.FileEntry{}, errs.ErrNotFound
	}
	if _, err := h.Root.Lstat(h.Rel); err == nil {
		return domain.FileEntry{}, errs.ErrConflict.WithDetail(p.Name())
	}
	if err := s.checkQuota(ctx, sub, p, size); err != nil {
		return domain.FileEntry{}, err
	}
	tmp := path.Join(path.Dir(h.Rel), ".~easycloud-"+newID()+".tmp")
	out, err := h.Root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	defer h.Root.Remove(tmp) // gone after the link below, or on any failure
	_, err = io.Copy(&progressWriter{w: out, ctx: ctx, report: progressFrom(ctx)}, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return domain.FileEntry{}, err
	}
	// A hard link takes the name only if it is still free (a file created by
	// someone else meanwhile is not overwritten). Filesystems without hard
	// links fall back to check-then-rename.
	if err := h.Root.Link(tmp, h.Rel); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return domain.FileEntry{}, errs.ErrConflict.WithDetail(p.Name())
		}
		if _, lerr := h.Root.Lstat(h.Rel); lerr == nil {
			return domain.FileEntry{}, errs.ErrConflict.WithDetail(p.Name())
		}
		if err := h.Root.Rename(tmp, h.Rel); err != nil {
			return domain.FileEntry{}, mapFSErr(err)
		}
	}
	s.publish("created", p.Parent())
	fi, err := h.Root.Stat(h.Rel)
	if err != nil {
		return domain.FileEntry{}, mapFSErr(err)
	}
	return toEntry(p, fi), nil
}

// RenamePath moves src to the exact path dst (WebDAV MOVE semantics: the
// caller has already removed an existing destination when overwriting).
func (s *FileService) RenamePath(ctx context.Context, sub pathguard.Subject, src, dst string) error {
	from, err := pathguard.Parse(src)
	if err != nil || from.IsRoot() {
		return errs.ErrInvalidPath
	}
	to, err := pathguard.Parse(dst)
	if err != nil || to.IsRoot() {
		return errs.ErrInvalidPath
	}
	if to.HasPrefix(from) {
		return errs.ErrInvalidInput.WithDetail("cannot move a directory into itself")
	}
	return s.move(ctx, sub, from, to, ConflictFail)
}

// Guard exposes the authorizer to protocol gateways that need raw file handles.
func (s *FileService) Guard() pathguard.Authorizer { return s.guard }

// CheckQuota is exported for protocol gateways that write files directly.
func (s *FileService) CheckQuota(ctx context.Context, sub pathguard.Subject, target pathguard.RelPath, add int64) error {
	return s.checkQuota(ctx, sub, target, add)
}

// DataRoot exposes the data root for navigation-only stats.
func (s *FileService) DataRoot() *os.Root { return s.dataRoot }

type progressKey struct{}

// WithProgress attaches a byte counter to ctx; long copies report through it.
func WithProgress(ctx context.Context, fn func(n int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

func progressFrom(ctx context.Context) func(int64) {
	fn, _ := ctx.Value(progressKey{}).(func(int64))
	return fn
}

// progressWriter reports bytes and stops promptly when ctx is cancelled.
type progressWriter struct {
	w      io.Writer
	ctx    context.Context
	report func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.w.Write(b)
	if p.report != nil && n > 0 {
		p.report(int64(n))
	}
	return n, err
}

// WithGuard returns a copy of the service that authorizes with g (e.g. a WebDAV
// configuration's fixed directory set) while sharing everything else.
func (s *FileService) WithGuard(g pathguard.Authorizer) *FileService {
	c := *s
	c.guard = g
	return &c
}
