package index

import (
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"log/slog"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/fsnotify/fsnotify"
	_ "golang.org/x/image/webp"
)

// Hooks connect the indexer to the rest of the system without import cycles.
type Hooks struct {
	// Changed is called with display paths of directories whose contents changed on disk.
	Changed func(dirs []string)
	// Moved is called when an entry disappeared and reappeared with the same inode.
	Moved func(ctx context.Context, from, to pathguard.RelPath)
}

type Indexer struct {
	root    *os.Root
	absRoot string
	store   *Store
	hooks   Hooks
	log     *slog.Logger

	watcher      *fsnotify.Watcher
	watchBroken  bool
	mu           sync.Mutex
	dirty        map[string]bool // dirs awaiting a debounced re-sync
	pendingGone  map[inodeKey]pending
	pendingAdded map[inodeKey]pending
	ready        chan struct{}
}

type inodeKey struct{ dev, ino uint64 }

type pending struct {
	path  string
	isDir bool
	at    time.Time
}

const moveWindow = 2 * time.Second

func New(root *os.Root, absRoot string, store *Store, hooks Hooks, log *slog.Logger) *Indexer {
	return &Indexer{root: root, absRoot: absRoot, store: store, hooks: hooks, log: log,
		dirty: map[string]bool{}, pendingGone: map[inodeKey]pending{}, pendingAdded: map[inodeKey]pending{}, ready: make(chan struct{})}
}

// Ready is closed after the first full scan.
func (ix *Indexer) Ready() <-chan struct{} { return ix.ready }

func statKey(fi fs.FileInfo) (uint64, uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino)
	}
	return 0, 0
}

func isReserved(p string) bool {
	return p == pathguard.ReservedDir || strings.HasPrefix(p, pathguard.ReservedDir+"/")
}

// Run performs the initial scan, then follows fsnotify events and reconciles periodically.
func (ix *Indexer) Run(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		ix.log.Warn("file watching unavailable; falling back to periodic scans", "err", err)
		ix.watchBroken = true
	} else {
		ix.watcher = w
		defer w.Close()
	}
	start := time.Now()
	ix.FullScan(ctx)
	close(ix.ready)
	ix.log.Info("file index ready", "ms", time.Since(start).Milliseconds(), "watching", !ix.watchBroken)

	reconcile := 6 * time.Hour
	if ix.watchBroken {
		reconcile = 10 * time.Minute
	}
	recon := time.NewTicker(reconcile)
	defer recon.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()

	var events <-chan fsnotify.Event
	var errsCh <-chan error
	if ix.watcher != nil {
		events, errsCh = ix.watcher.Events, ix.watcher.Errors
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			ix.onEvent(ev)
		case err := <-errsCh:
			ix.log.Warn("file watcher error", "err", err)
		case <-tick.C:
			ix.flush(ctx)
		case <-recon.C:
			ix.FullScan(ctx)
		}
	}
}

func (ix *Indexer) rel(abs string) (string, bool) {
	r, err := filepath.Rel(ix.absRoot, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func (ix *Indexer) onEvent(ev fsnotify.Event) {
	p, ok := ix.rel(ev.Name)
	if !ok || p == "." || isReserved(p) {
		return
	}
	ix.mu.Lock()
	ix.dirty[parentOf(p)] = true
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		ix.dirty[p] = false // a removed directory needs no re-scan of its own
	}
	ix.mu.Unlock()
}

// MarkDirty schedules a re-sync of dir (used after API mutations when watching is off).
func (ix *Indexer) MarkDirty(dir string) {
	ix.mu.Lock()
	ix.dirty[dir] = true
	ix.mu.Unlock()
}

func (ix *Indexer) flush(ctx context.Context) {
	ix.mu.Lock()
	dirs := ix.dirty
	ix.dirty = map[string]bool{}
	ix.mu.Unlock()
	var changed []string
	for d, ok := range dirs {
		if !ok {
			continue
		}
		if c, err := ix.syncDir(ctx, d, false); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				ix.log.Warn("index sync", "dir", d, "err", err)
			}
		} else if c {
			changed = append(changed, display(d))
		}
	}
	changed = append(changed, ix.expirePending(ctx, false)...)
	if len(changed) > 0 && ix.hooks.Changed != nil {
		ix.hooks.Changed(changed)
	}
}

func display(p string) string {
	if p == "." {
		return "/"
	}
	return "/" + p
}

// FullScan re-syncs the whole tree.
func (ix *Indexer) FullScan(ctx context.Context) {
	if _, err := ix.syncDir(ctx, ".", true); err != nil {
		ix.log.Error("full index scan", "err", err)
	}
	ix.expirePending(ctx, true)
}

// syncDir reconciles the rows of dir's children with the filesystem. When
// recursive is false, newly appeared subdirectories are still scanned in full.
func (ix *Indexer) syncDir(ctx context.Context, dir string, recursive bool) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	des, err := fs.ReadDir(ix.root.FS(), dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if dir != "." {
				// Directory itself vanished: its parent's sync records the removal.
				ix.MarkDirty(parentOf(dir))
			}
			return false, err
		}
		return false, err
	}
	ix.watch(dir)
	existing, err := ix.store.Children(ctx, dir)
	if err != nil {
		return false, err
	}
	old := make(map[string]Node, len(existing))
	for _, n := range existing {
		old[n.Name] = n
	}
	var upserts []Node
	var subdirs []string
	changed := false
	seen := map[string]bool{}
	for _, de := range des {
		name := de.Name()
		p := name
		if dir != "." {
			p = dir + "/" + name
		}
		if isReserved(p) || de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if _, err := pathguard.Parse(p); err != nil {
			continue // names the API cannot address are not indexed
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		seen[name] = true
		dev, ino := statKey(fi)
		n := Node{Path: p, Name: name, IsDir: fi.IsDir(), MTime: fi.ModTime().UTC().Truncate(time.Second), Dev: dev, Inode: ino}
		if !n.IsDir {
			n.Size = fi.Size()
			n.Mime = mime.TypeByExtension(strings.ToLower(path.Ext(name)))
		}
		prev, had := old[name]
		if had && prev.IsDir == n.IsDir && prev.Size == n.Size && prev.MTime.Equal(n.MTime) && prev.Inode == n.Inode {
			if n.IsDir && recursive {
				subdirs = append(subdirs, p)
			}
			continue
		}
		if !had {
			ix.noteAdded(ctx, n)
		}
		if strings.HasPrefix(n.Mime, "image/") {
			n.Width, n.Height = ix.dimensions(p)
		}
		upserts = append(upserts, n)
		changed = true
		if n.IsDir && (recursive || !had) {
			subdirs = append(subdirs, p)
		}
	}
	if err := ix.store.Upsert(ctx, upserts); err != nil {
		return changed, err
	}
	for name, n := range old {
		if !seen[name] {
			changed = true
			ix.noteGone(ctx, n)
		}
	}
	for _, sd := range subdirs {
		if c, err := ix.syncDir(ctx, sd, true); err == nil && c {
			changed = true
		}
	}
	return changed, nil
}

func (ix *Indexer) dimensions(p string) (int, int) {
	f, err := ix.root.Open(p)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func (ix *Indexer) watch(dir string) {
	if ix.watcher == nil || ix.watchBroken {
		return
	}
	abs := filepath.Join(ix.absRoot, filepath.FromSlash(dir))
	if err := ix.watcher.Add(abs); err != nil {
		if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) {
			ix.watchBroken = true
			ix.log.Warn("file watch limit reached; host-side changes will be picked up by periodic scans. "+
				"Raise fs.inotify.max_user_watches to watch everything", "dir", dir)
		}
	}
}

// noteGone/noteAdded pair up removals and additions of the same inode into moves.
func (ix *Indexer) noteGone(ctx context.Context, n Node) {
	k := inodeKey{n.Dev, n.Inode}
	ix.mu.Lock()
	add, ok := ix.pendingAdded[k]
	if ok {
		delete(ix.pendingAdded, k)
	} else {
		ix.pendingGone[k] = pending{path: n.Path, isDir: n.IsDir, at: time.Now()}
	}
	ix.mu.Unlock()
	if err := ix.store.DeleteTree(ctx, n.Path); err != nil {
		ix.log.Warn("index delete", "path", n.Path, "err", err)
	}
	if ok {
		ix.moved(ctx, n.Path, add.path)
	}
}

func (ix *Indexer) noteAdded(ctx context.Context, n Node) {
	k := inodeKey{n.Dev, n.Inode}
	ix.mu.Lock()
	gone, ok := ix.pendingGone[k]
	if ok {
		delete(ix.pendingGone, k)
	} else {
		ix.pendingAdded[k] = pending{path: n.Path, isDir: n.IsDir, at: time.Now()}
	}
	ix.mu.Unlock()
	if ok {
		ix.moved(ctx, gone.path, n.Path)
	}
}

func (ix *Indexer) moved(ctx context.Context, from, to string) {
	if from == to || ix.hooks.Moved == nil {
		return
	}
	f, err1 := pathguard.Parse(from)
	t, err2 := pathguard.Parse(to)
	if err1 == nil && err2 == nil {
		ix.log.Info("host-side move detected", "from", from, "to", to)
		ix.hooks.Moved(ctx, f, t)
	}
}

// expirePending forgets unmatched removals/additions older than the move window.
func (ix *Indexer) expirePending(_ context.Context, all bool) []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	cut := time.Now().Add(-moveWindow)
	for k, p := range ix.pendingGone {
		if all || p.at.Before(cut) {
			delete(ix.pendingGone, k)
		}
	}
	for k, p := range ix.pendingAdded {
		if all || p.at.Before(cut) {
			delete(ix.pendingAdded, k)
		}
	}
	return nil
}
