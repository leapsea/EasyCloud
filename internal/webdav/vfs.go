package webdav

import (
	"context"
	"io/fs"
	"os"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"golang.org/x/net/webdav"
)

// staticGrants turns a config's directories into grants for GrantAuthorizer.
type staticGrants []pathguard.Grant

func (g staticGrants) GrantsFor(context.Context, string) ([]pathguard.Grant, error) { return g, nil }

// VFS maps the virtual tree of a config — "/<name>/rest" — onto real paths and
// delegates to FS, whose FileService only authorizes the config's directories.
type VFS struct {
	inner *FS
	root  *os.Root
	names map[string]pathguard.RelPath // folder name at the root -> real directory
	order []string
}

// NewVFS builds the filesystem for one config. sub is the identity used for
// trash ownership and audit (the config's creator).
func NewVFS(files *storageapp.FileService, root *os.Root, c Config, sub pathguard.Subject) *VFS {
	v := &VFS{root: root, names: map[string]pathguard.RelPath{}}
	var grants staticGrants
	for _, d := range c.Dirs {
		p, err := pathguard.Parse(d.Path)
		if err != nil || d.Missing {
			continue
		}
		perm := pathguard.PermRead
		if d.Perm == "rw" {
			perm = pathguard.PermRW
		}
		grants = append(grants, pathguard.Grant{Path: p, Perm: perm})
		v.names[d.Name] = p
		v.order = append(v.order, d.Name)
	}
	// Never an administrator: the config sees only its own directories.
	sub.IsAdmin = false
	guard := pathguard.NewGrantAuthorizer(root, grants)
	v.inner = NewFS(files.WithGuard(guard), sub)
	return v
}

// resolve returns the real path for a virtual name. top reports a shared directory itself.
func (v *VFS) resolve(name string) (real string, isRoot, top bool, err error) {
	p, err := pathguard.Parse(name)
	if err != nil {
		return "", false, false, fs.ErrNotExist
	}
	segs := p.Segments()
	if len(segs) == 0 {
		return "", true, false, nil
	}
	base, ok := v.names[segs[0]]
	if !ok {
		return "", false, false, fs.ErrNotExist
	}
	r := base
	for _, s := range segs[1:] {
		if r, err = r.Join(s); err != nil {
			return "", false, false, fs.ErrNotExist
		}
	}
	return r.Display(), false, len(segs) == 1, nil
}

func (v *VFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	real, isRoot, top, err := v.resolve(name)
	switch {
	case err != nil && !isRoot:
		return fs.ErrPermission // new top-level folders cannot be created
	case isRoot || top:
		return fs.ErrExist
	}
	return v.inner.Mkdir(ctx, real, perm)
}

func (v *VFS) RemoveAll(ctx context.Context, name string) error {
	real, isRoot, top, err := v.resolve(name)
	if err != nil {
		return err
	}
	if isRoot || top {
		return fs.ErrPermission // shared directories themselves are managed by the administrator
	}
	return v.inner.RemoveAll(ctx, real)
}

func (v *VFS) Rename(ctx context.Context, oldName, newName string) error {
	from, r1, t1, err := v.resolve(oldName)
	if err != nil {
		return err
	}
	to, r2, t2, err := v.resolve(newName)
	if err != nil {
		return fs.ErrPermission
	}
	if r1 || r2 || t1 || t2 {
		return fs.ErrPermission
	}
	return v.inner.Rename(ctx, from, to)
}

func (v *VFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	real, isRoot, top, err := v.resolve(name)
	if err != nil {
		return nil, err
	}
	if isRoot {
		return fakeInfo{name: "/", mod: time.Now()}, nil
	}
	fi, err := v.inner.Stat(ctx, real)
	if err != nil {
		return nil, err
	}
	if top {
		return renamed{fi, v.base(name)}, nil
	}
	return fi, nil
}

func (v *VFS) base(name string) string {
	p, _ := pathguard.Parse(name)
	return p.Segments()[0]
}

func (v *VFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	real, isRoot, top, err := v.resolve(name)
	if err != nil {
		if write {
			return nil, fs.ErrPermission // no new files at the root
		}
		return nil, err
	}
	if isRoot {
		if write {
			return nil, fs.ErrPermission
		}
		return v.rootDir(), nil
	}
	f, err := v.inner.OpenFile(ctx, real, flag, perm)
	if err != nil {
		return nil, err
	}
	if top {
		return renamedFile{f, v.base(name)}, nil
	}
	return f, nil
}

// rootDir lists the shared directories under their configured names.
func (v *VFS) rootDir() *navDir {
	d := &navDir{info: fakeInfo{name: "/", mod: time.Now()}}
	for _, n := range v.order {
		fi, err := v.root.Stat(v.names[n].String())
		if err != nil || !fi.IsDir() {
			continue
		}
		d.children = append(d.children, fakeInfo{name: n, mod: fi.ModTime()})
	}
	return d
}

// renamed presents a shared directory under its configured name.
type renamed struct {
	os.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }

type renamedFile struct {
	webdav.File
	name string
}

func (r renamedFile) Stat() (fs.FileInfo, error) {
	fi, err := r.File.Stat()
	if err != nil {
		return nil, err
	}
	return renamed{fi, r.name}, nil
}
