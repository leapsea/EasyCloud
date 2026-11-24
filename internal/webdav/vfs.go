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

// VFS maps the virtual tree of a config onto real paths and delegates to FS,
// whose FileService only authorizes what the config offers:
//
//	/<dir>/rest             a shared directory (read or read/write)
//	/<file>                 a single shared file (read-only)
//	/<folder>/<entry>/rest  an entry of a virtual folder (always read-only)
//
// The root lists them in the configured order.
type VFS struct {
	inner   *FS
	root    *os.Root
	names   map[string]pathguard.RelPath // shared directory name at the root -> real directory
	files   map[string]pathguard.RelPath // single file name at the root -> real file
	order   []string                     // every name at the root, in configured order
	folders map[string]map[string]entry  // virtual folder -> entry name -> entry
	eorder  map[string][]string          // entry names per virtual folder, in order
}

type entry struct {
	path  pathguard.RelPath
	isDir bool
}

// node is what a virtual path refers to.
type node struct {
	real    string // real path (display form) for a shared directory or an entry, or below them
	root    bool   // the endpoint's root
	top     bool   // a shared directory itself, or a virtual folder's entry itself
	folder  string // the virtual folder the path is, or lies in
	vfolder bool   // the virtual folder itself
	file    bool   // a single file shared at the root
	name    string // the name shown for top-level paths and entries
}

func (n node) readOnly() bool { return n.root || n.folder != "" || n.file }

// NewVFS builds the filesystem for one config. sub is the identity used for
// trash ownership and audit (the config's creator).
func NewVFS(files *storageapp.FileService, root *os.Root, c Config, sub pathguard.Subject) *VFS {
	v := &VFS{root: root, names: map[string]pathguard.RelPath{}, files: map[string]pathguard.RelPath{}, folders: map[string]map[string]entry{}, eorder: map[string][]string{}}
	var grants staticGrants
	for _, d := range c.Dirs {
		p, err := pathguard.Parse(d.Path)
		if err != nil || d.Missing {
			continue
		}
		if d.Folder != "" {
			grants = append(grants, v.addEntry(d, p)...)
			continue
		}
		if !d.IsDir {
			if p.IsRoot() {
				continue
			}
			// Granted through its folder; only the file itself resolves (see addEntry).
			grants = append(grants, pathguard.Grant{Path: p.Parent(), Perm: pathguard.PermRead})
			v.files[d.Name] = p
			v.order = append(v.order, d.Name)
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
	// Never an administrator: the config sees only what it offers.
	sub.IsAdmin = false
	guard := pathguard.NewGrantAuthorizer(root, grants)
	v.inner = NewFS(files.WithGuard(guard), sub)
	return v
}

// addEntry puts a file or folder into its virtual folder and returns the
// read-only grant that lets it be served.
func (v *VFS) addEntry(d Dir, p pathguard.RelPath) []pathguard.Grant {
	if p.IsRoot() {
		return nil
	}
	if _, ok := v.folders[d.Folder]; !ok {
		v.folders[d.Folder] = map[string]entry{}
		v.order = append(v.order, d.Folder)
	}
	v.folders[d.Folder][d.Name] = entry{path: p, isDir: d.IsDir}
	v.eorder[d.Folder] = append(v.eorder[d.Folder], d.Name)
	// Grants work on directories (the authorizer opens a root on one). A file
	// entry is granted through its folder; only the entry itself is reachable,
	// because paths resolve through the virtual folder's names, never by
	// browsing that folder.
	g := p
	if !d.IsDir {
		g = p.Parent()
	}
	return []pathguard.Grant{{Path: g, Perm: pathguard.PermRead}}
}

func (v *VFS) resolve(name string) (node, error) {
	p, err := pathguard.Parse(name)
	if err != nil {
		return node{}, fs.ErrNotExist
	}
	segs := p.Segments()
	if len(segs) == 0 {
		return node{root: true}, nil
	}
	join := func(base pathguard.RelPath, rest []string) (string, error) {
		r := base
		for _, s := range rest {
			if r, err = r.Join(s); err != nil {
				return "", fs.ErrNotExist
			}
		}
		return r.Display(), nil
	}
	if f, ok := v.files[segs[0]]; ok {
		if len(segs) > 1 {
			return node{}, fs.ErrNotExist
		}
		return node{real: f.Display(), top: true, file: true, name: segs[0]}, nil
	}
	if base, ok := v.names[segs[0]]; ok {
		real, err := join(base, segs[1:])
		return node{real: real, top: len(segs) == 1, name: segs[0]}, err
	}
	entries, ok := v.folders[segs[0]]
	if !ok {
		return node{}, fs.ErrNotExist
	}
	if len(segs) == 1 {
		return node{folder: segs[0], vfolder: true, name: segs[0]}, nil
	}
	e, ok := entries[segs[1]]
	if !ok || (!e.isDir && len(segs) > 2) {
		return node{}, fs.ErrNotExist
	}
	real, err := join(e.path, segs[2:])
	return node{real: real, top: len(segs) == 2, folder: segs[0], name: segs[1]}, err
}

func (v *VFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	n, err := v.resolve(name)
	switch {
	case err != nil:
		return fs.ErrPermission // no new folders at the root or in a virtual folder
	case n.root || n.top || n.vfolder:
		return fs.ErrExist
	case n.readOnly():
		return fs.ErrPermission
	}
	return v.inner.Mkdir(ctx, n.real, perm)
}

func (v *VFS) RemoveAll(ctx context.Context, name string) error {
	n, err := v.resolve(name)
	if err != nil {
		return err
	}
	if n.root || n.top || n.readOnly() {
		return fs.ErrPermission // shared directories and virtual folders are managed by the administrator
	}
	return v.inner.RemoveAll(ctx, n.real)
}

func (v *VFS) Rename(ctx context.Context, oldName, newName string) error {
	from, err := v.resolve(oldName)
	if err != nil {
		return err
	}
	to, err := v.resolve(newName)
	if err != nil {
		return fs.ErrPermission
	}
	if from.root || to.root || from.top || to.top || from.readOnly() || to.readOnly() {
		return fs.ErrPermission
	}
	return v.inner.Rename(ctx, from.real, to.real)
}

func (v *VFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	n, err := v.resolve(name)
	if err != nil {
		return nil, err
	}
	switch {
	case n.root:
		return fakeInfo{name: "/", mod: time.Now()}, nil
	case n.vfolder:
		return fakeInfo{name: n.name, mod: v.folderMod(n.folder)}, nil
	}
	fi, err := v.inner.Stat(ctx, n.real)
	if err != nil {
		return nil, err
	}
	if n.top {
		fi = renamed{fi, n.name}
	}
	if n.folder != "" || n.file {
		fi = readOnlyInfo{fi}
	}
	return fi, nil
}

func (v *VFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	n, err := v.resolve(name)
	if err != nil {
		if write {
			return nil, fs.ErrPermission // no new files at the root or in a virtual folder
		}
		return nil, err
	}
	if write && n.readOnly() {
		return nil, fs.ErrPermission
	}
	switch {
	case n.root:
		return v.rootDir(), nil
	case n.vfolder:
		return v.folderDir(n.folder), nil
	}
	f, err := v.inner.OpenFile(ctx, n.real, flag, perm)
	if err != nil {
		return nil, err
	}
	if n.folder != "" || n.file {
		f = readOnlyFile{f}
	}
	if n.top {
		return renamedFile{f, n.name}, nil
	}
	return f, nil
}

// rootDir lists what the config shares, in its configured order.
func (v *VFS) rootDir() *navDir {
	d := &navDir{info: fakeInfo{name: "/", mod: time.Now()}}
	for _, n := range v.order {
		file, isFile := v.files[n]
		switch {
		case v.folders[n] != nil:
			d.children = append(d.children, fakeInfo{name: n, mod: v.folderMod(n)})
		case isFile:
			if fi, err := v.root.Stat(file.String()); err == nil && fi.Mode().IsRegular() {
				d.children = append(d.children, readOnlyInfo{renamed{fi, n}})
			}
		default:
			if fi, err := v.root.Stat(v.names[n].String()); err == nil && fi.IsDir() {
				d.children = append(d.children, fakeInfo{name: n, mod: fi.ModTime()})
			}
		}
	}
	return d
}

// folderDir lists a virtual folder's entries with their real metadata.
func (v *VFS) folderDir(name string) *navDir {
	d := &navDir{info: fakeInfo{name: name, mod: v.folderMod(name)}}
	for _, en := range v.eorder[name] {
		fi, err := v.root.Stat(v.folders[name][en].path.String())
		if err != nil {
			continue
		}
		d.children = append(d.children, readOnlyInfo{renamed{fi, en}})
	}
	return d
}

// folderMod is the newest modification time among a virtual folder's entries.
func (v *VFS) folderMod(name string) time.Time {
	var mod time.Time
	for _, e := range v.folders[name] {
		if fi, err := v.root.Stat(e.path.String()); err == nil && fi.ModTime().After(mod) {
			mod = fi.ModTime()
		}
	}
	return mod
}

// renamed presents a shared directory or an entry under its configured name.
type renamed struct {
	os.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }

// readOnlyInfo clears the write bits, so clients show virtual folder content as read-only.
type readOnlyInfo struct{ os.FileInfo }

func (r readOnlyInfo) Mode() fs.FileMode { return r.FileInfo.Mode() &^ 0o222 }

type renamedFile struct {
	webdav.File
	name string
}

func (r renamedFile) Stat() (fs.FileInfo, error) {
	fi, err := r.File.Stat()
	if err != nil {
		return nil, err
	}
	return renamed{fi, r.name}, nil // the mode of a read-only inner file stays read-only
}

// readOnlyFile refuses writes and lists children as read-only (anything
// inside a virtual folder).
type readOnlyFile struct{ webdav.File }

func (r readOnlyFile) Write([]byte) (int, error) { return 0, fs.ErrPermission }
func (r readOnlyFile) Readdir(count int) ([]fs.FileInfo, error) {
	infos, err := r.File.Readdir(count)
	for i, fi := range infos {
		infos[i] = readOnlyInfo{fi}
	}
	return infos, err
}
func (r readOnlyFile) Stat() (fs.FileInfo, error) {
	fi, err := r.File.Stat()
	if err != nil {
		return nil, err
	}
	return readOnlyInfo{fi}, nil
}
