// Package webdav exposes the data root over WebDAV. Every operation runs
// through PathGuard with the authenticated user's grants.
package webdav

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"golang.org/x/net/webdav"
)

type FS struct {
	files *storageapp.FileService
	sub   pathguard.Subject
}

func NewFS(files *storageapp.FileService, sub pathguard.Subject) *FS {
	return &FS{files: files, sub: sub}
}

// toOSErr maps application errors to the os errors the webdav package understands.
func toOSErr(err error) error {
	if err == nil {
		return nil
	}
	if ae, ok := errs.As(err); ok {
		switch ae.Status {
		case 404:
			return fs.ErrNotExist
		case 409:
			return fs.ErrExist
		case 400, 403, 507:
			return fs.ErrPermission
		}
	}
	if errors.Is(err, pathguard.ErrNotFound) || strings.Contains(err.Error(), "path escapes") {
		return fs.ErrNotExist
	}
	return err
}

func (f *FS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	_, err := f.files.Mkdir(ctx, f.sub, name)
	return toOSErr(err)
}

func (f *FS) RemoveAll(ctx context.Context, name string) error {
	_, err := f.files.MoveToTrash(ctx, f.sub, []string{name}) // recoverable from the recycle bin
	return toOSErr(err)
}

func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	return toOSErr(f.files.RenamePath(ctx, f.sub, oldName, newName))
}

func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	p, err := pathguard.Parse(name)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	vis, err := f.files.Guard().Visible(ctx, f.sub, p)
	if err != nil {
		// Files are not "visible" directories; fall through to a direct check.
		vis = pathguard.Visibility{}
	}
	if vis.Perm == 0 && len(vis.Children) > 0 {
		return navInfo(f.files.DataRoot(), p), nil
	}
	h, err := f.files.Guard().Check(ctx, f.sub, p, pathguard.PermRead)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	defer h.Close()
	fi, err := h.Root.Stat(h.Rel)
	if err != nil {
		return nil, toOSErr(err)
	}
	return fi, nil
}

func (f *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	p, err := pathguard.Parse(name)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if !write {
		if vis, err := f.files.Guard().Visible(ctx, f.sub, p); err == nil && vis.Perm == 0 {
			return newNavDir(f.files.DataRoot(), p, vis), nil
		}
	}
	need := pathguard.PermRead
	if write {
		need = pathguard.PermWrite
	}
	h, err := f.files.Guard().Check(ctx, f.sub, p, need)
	if err != nil {
		if write {
			// Readable but not writable: say "forbidden" (it reveals nothing the user cannot already see).
			if rh, rerr := f.files.Guard().Check(ctx, f.sub, p, pathguard.PermRead); rerr == nil {
				rh.Close()
				return nil, fs.ErrPermission
			}
		}
		return nil, fs.ErrNotExist
	}
	if write && h.Rel == "." {
		h.Close()
		return nil, fs.ErrPermission // a granted directory itself is not a file
	}
	if perm == 0 || perm&0o200 == 0 {
		perm = 0o644
	}
	file, err := h.Root.OpenFile(h.Rel, flag, perm)
	if err != nil {
		h.Close()
		return nil, toOSErr(err)
	}
	out := &realFile{File: file, root: f.files.DataRoot(), dir: p, handle: h}
	if write {
		// Bytes written may not exceed the owner's remaining quota.
		if err := f.files.CheckQuota(ctx, f.sub, p, 0); err != nil {
			out.Close()
			return nil, fs.ErrPermission
		}
		out.quota = func(n int64) error { return f.files.CheckQuota(context.WithoutCancel(ctx), f.sub, p, n) }
	}
	return out, nil
}

// realFile wraps an *os.File: listings hide the reserved directory and
// symlinks that leave the data root; writes are checked against quota.
type realFile struct {
	*os.File
	root    *os.Root
	dir     pathguard.RelPath
	handle  pathguard.Handle
	quota   func(int64) error
	written int64
	checked int64
	failed  bool
}

// Close releases the grant root; a write aborted by the quota leaves no partial file.
func (r *realFile) Close() error {
	err := r.File.Close()
	if r.failed {
		_ = r.handle.Root.Remove(r.handle.Rel)
	}
	r.handle.Close()
	return err
}

// ReadFrom keeps io.Copy from bypassing Write (and with it the quota check).
func (r *realFile) ReadFrom(src io.Reader) (int64, error) { return io.Copy(writerOnly{r}, src) }

type writerOnly struct{ io.Writer }

func (r *realFile) Readdir(count int) ([]fs.FileInfo, error) {
	infos, err := r.File.Readdir(count)
	out := infos[:0]
	for _, fi := range infos {
		if r.dir.IsRoot() && fi.Name() == pathguard.ReservedDir {
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			child, jerr := r.dir.Join(fi.Name())
			if jerr != nil {
				continue
			}
			st, serr := r.root.Stat(child.String())
			if serr != nil {
				continue // dangling or escaping symlink
			}
			fi = st
		}
		out = append(out, fi)
	}
	return out, err
}

func (r *realFile) Write(b []byte) (int, error) {
	if r.quota != nil {
		// Re-check in 8 MB steps to keep this cheap during large uploads.
		r.written += int64(len(b))
		if r.written-r.checked >= 8<<20 || r.checked == 0 {
			if err := r.quota(r.written); err != nil {
				r.failed = true
				return 0, fs.ErrPermission
			}
			r.checked = r.written
		}
	}
	return r.File.Write(b)
}

// navDir is a synthetic, read-only directory listing only the branches that lead to grants.
type navDir struct {
	info     fs.FileInfo
	children []fs.FileInfo
	pos      int
}

type fakeInfo struct {
	name string
	mod  time.Time
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (f fakeInfo) ModTime() time.Time { return f.mod }
func (f fakeInfo) IsDir() bool        { return true }
func (f fakeInfo) Sys() any           { return nil }

func navInfo(root *os.Root, p pathguard.RelPath) fs.FileInfo {
	name := p.Name()
	if name == "" {
		name = "/"
	}
	mod := time.Time{}
	if fi, err := root.Stat(p.String()); err == nil {
		mod = fi.ModTime()
	}
	return fakeInfo{name: name, mod: mod}
}

func newNavDir(root *os.Root, p pathguard.RelPath, vis pathguard.Visibility) *navDir {
	d := &navDir{info: navInfo(root, p)}
	for _, name := range vis.SortedChildren() {
		child, err := p.Join(name)
		if err != nil {
			continue
		}
		if fi, err := root.Stat(child.String()); err == nil && fi.IsDir() {
			d.children = append(d.children, fakeInfo{name: name, mod: fi.ModTime()})
		}
	}
	return d
}

func (d *navDir) Close() error                   { return nil }
func (d *navDir) Read([]byte) (int, error)       { return 0, io.EOF }
func (d *navDir) Write([]byte) (int, error)      { return 0, fs.ErrPermission }
func (d *navDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *navDir) Stat() (fs.FileInfo, error)     { return d.info, nil }
func (d *navDir) Readdir(count int) ([]fs.FileInfo, error) {
	if d.pos >= len(d.children) {
		if count > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	end := len(d.children)
	if count > 0 && d.pos+count < end {
		end = d.pos + count
	}
	out := d.children[d.pos:end]
	d.pos = end
	return out, nil
}
