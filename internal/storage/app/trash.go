package app

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/domain"
)

// trashDir is relative to the data root. Same filesystem as user files, so rename is atomic.
func trashDir(owner string) string { return path.Join(pathguard.ReservedDir, "trash", owner) }

// Folders mounted from another filesystem cannot be renamed into the central
// trash. Their items go to the trash of their own mount instead,
// <mount>/.easycloud/trash/<owner>, so deleting and restoring stay a rename.
// TrashPath then holds that location relative to the data root with a
// leading "/"; central items keep the original form (relative to the
// reserved dir), so existing records stay valid.

// trashLocation is where an item's data is, relative to the data root.
func trashLocation(it domain.TrashItem) string {
	if strings.HasPrefix(it.TrashPath, "/") {
		return it.TrashPath[1:]
	}
	return path.Join(pathguard.ReservedDir, it.TrashPath)
}

// mountBelow returns the deepest mount point strictly below the data root
// that contains dir (dir itself included), from the mount table.
// found=false: no mount table here, or dir is on the data root's own mount.
func (s *FileService) mountBelow(dir pathguard.RelPath) (pathguard.RelPath, bool) {
	mps, ok := mountPoints()
	if !ok {
		return dir, false
	}
	root, err := filepath.EvalSymlinks(s.dataRoot.Name())
	if err != nil {
		return dir, false
	}
	abs := filepath.Join(root, filepath.FromSlash(dir.String()))
	best := ""
	for _, mp := range mps {
		if !strings.HasPrefix(mp, root+"/") {
			continue // the data root's own mount or one elsewhere
		}
		if (abs == mp || strings.HasPrefix(abs, mp+"/")) && len(mp) > len(best) {
			best = mp
		}
	}
	if best == "" {
		return dir, false
	}
	rel, err := pathguard.Parse(filepath.ToSlash(strings.TrimPrefix(best, root)))
	if err != nil {
		return dir, false
	}
	return rel, true
}

// mountRoot returns the folder where the filesystem holding dir is mounted
// (below the data root): from the mount table where there is one, else the
// topmost directory with the same device number.
func (s *FileService) mountRoot(dir pathguard.RelPath) (pathguard.RelPath, error) {
	if mp, ok := s.mountBelow(dir); ok {
		return mp, nil
	}
	fi, err := s.dataRoot.Stat(dir.String())
	if err != nil {
		return dir, err
	}
	dev, ok := deviceOf(fi)
	if !ok {
		return dir, errs.ErrInvalidInput.WithDetail("cannot tell filesystems apart on this platform")
	}
	top := dir
	for cur := dir.Parent(); !cur.IsRoot(); cur = cur.Parent() {
		pfi, err := s.dataRoot.Stat(cur.String())
		if err != nil {
			break
		}
		if d, _ := deviceOf(pfi); d != dev {
			break
		}
		top = cur
	}
	return top, nil
}

// ErrMountPoint: a folder where another filesystem is mounted cannot be
// renamed anywhere (the kernel answers EBUSY, EXDEV or EACCES).
var ErrMountPoint = errs.New(400, "MOUNT_POINT")

// isMountPoint reports whether p is where another filesystem is mounted.
func (s *FileService) isMountPoint(p pathguard.RelPath) bool {
	if p.IsRoot() {
		return false
	}
	if _, ok := mountPoints(); ok {
		mp, found := s.mountBelow(p)
		return found && mp.Equal(p)
	}
	pfi, err1 := s.dataRoot.Lstat(p.String())
	dfi, err2 := s.dataRoot.Stat(p.Parent().String())
	if err1 != nil || err2 != nil || !pfi.IsDir() {
		return false
	}
	a, ok1 := deviceOf(pfi)
	b, ok2 := deviceOf(dfi)
	return ok1 && ok2 && a != b
}

// moveToMountTrash is the cross-filesystem path of MoveToTrash.
func (s *FileService) moveToMountTrash(p pathguard.RelPath, owner string, item *domain.TrashItem) error {
	root, err := s.mountRoot(p.Parent())
	if err != nil {
		return err
	}
	dir := path.Join(root.String(), pathguard.ReservedDir, "trash", owner)
	if err := s.dataRoot.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	loc := path.Join(dir, item.ID)
	if err := s.dataRoot.Rename(p.String(), loc); err != nil {
		return err
	}
	item.TrashPath = "/" + loc
	return nil
}

func ownerSegment(sub pathguard.Subject) (string, error) {
	p, err := pathguard.Parse(sub.UserID)
	if err != nil || len(p.Segments()) != 1 {
		return "", errs.ErrUnauthorized
	}
	return p.String(), nil
}

// MoveToTrash renames first and writes the record second: if the insert fails
// the item is still recoverable from .easycloud/trash, which is less harmful
// than a record pointing at a file that never moved.
func (s *FileService) MoveToTrash(ctx context.Context, sub pathguard.Subject, paths []string) ([]domain.TrashItem, error) {
	owner, err := ownerSegment(sub)
	if err != nil {
		return nil, err
	}
	days, _, err := s.settings.Int(ctx, settings.TrashRetentionDays)
	if err != nil {
		s.log.Warn("trash retention setting unreadable, using fallback", "err", err)
	}
	if err := s.dataRoot.MkdirAll(trashDir(owner), 0o700); err != nil {
		return nil, mapFSErr(err)
	}
	var out []domain.TrashItem
	for _, raw := range paths {
		p, h, err := s.check(ctx, sub, raw, pathguard.PermWrite)
		if err != nil {
			return out, err
		}
		// Lstat through the grant root proves the path resolves inside the grant
		// before the data-root rename below.
		fi, err := h.Root.Lstat(h.Rel)
		root := p.IsRoot() || h.Rel == "."
		h.Close()
		if root {
			return out, errs.ErrInvalidInput.WithDetail("a granted directory cannot be deleted")
		}
		if err := s.guardProtected(ctx, p); err != nil {
			return out, err
		}
		if s.isMountPoint(p) {
			return out, ErrMountPoint
		}
		if err != nil {
			return out, mapFSErr(err)
		}
		now := s.clock.Now().UTC()
		item := domain.TrashItem{
			ID: newID(), OwnerID: sub.UserID, OriginPath: p.Display(), IsDir: fi.IsDir(),
			DeletedAt: now, ExpireAt: now.Add(time.Duration(days) * 24 * time.Hour),
		}
		if !fi.IsDir() {
			item.Size = fi.Size()
		}
		item.TrashPath = path.Join("trash", owner, item.ID)
		// Authorization is done above; the rename uses the data root because the
		// reserved dir is outside any grant root.
		if err := s.dataRoot.Rename(p.String(), path.Join(pathguard.ReservedDir, item.TrashPath)); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return out, mapFSErr(err)
			}
			if err := s.moveToMountTrash(p, owner, &item); err != nil {
				return out, mapFSErr(err)
			}
		}
		if err := s.trash.Add(ctx, item); err != nil {
			s.log.Error("trash record insert failed; file kept in reserved dir", "id", item.ID, "err", err)
			return out, err
		}
		out = append(out, item)
		s.publish("removed", p.Parent())
	}
	return out, nil
}

func (s *FileService) ListTrash(ctx context.Context, sub pathguard.Subject) ([]domain.TrashItem, error) {
	return s.trash.List(ctx, sub.UserID)
}

// Restore moves an item back to its origin. Missing parent directories are recreated.
func (s *FileService) Restore(ctx context.Context, sub pathguard.Subject, id string, policy ConflictPolicy) (string, error) {
	item, err := s.trash.Get(ctx, sub.UserID, id)
	if errors.Is(err, domain.ErrNotFound) {
		return "", errs.ErrNotFound
	} else if err != nil {
		return "", err
	}
	origin, h, err := s.check(ctx, sub, item.OriginPath, pathguard.PermWrite)
	if err != nil {
		return "", err
	}
	defer h.Close()
	if err := h.Root.MkdirAll(path.Dir(h.Rel), 0o755); err != nil {
		return "", mapFSErr(err)
	}
	if policy == ConflictOverwrite {
		policy = ConflictFail // never silently destroy a live file on restore
	}
	target, _, _, err := s.resolveTarget(h, origin, policy, item.IsDir)
	if err != nil {
		return "", err
	}
	if err := s.dataRoot.Rename(trashLocation(item), target.String()); err != nil {
		return "", mapFSErr(err)
	}
	if err := s.trash.Delete(ctx, sub.UserID, id); err != nil {
		return "", err
	}
	s.publish("created", target.Parent())
	return target.Display(), nil
}

// Purge permanently deletes one trash item.
func (s *FileService) Purge(ctx context.Context, sub pathguard.Subject, id string) error {
	item, err := s.trash.Get(ctx, sub.UserID, id)
	if errors.Is(err, domain.ErrNotFound) {
		return errs.ErrNotFound
	} else if err != nil {
		return err
	}
	return s.purge(ctx, item)
}

func (s *FileService) EmptyTrash(ctx context.Context, sub pathguard.Subject) (int, error) {
	items, err := s.trash.List(ctx, sub.UserID)
	if err != nil {
		return 0, err
	}
	for i, it := range items {
		if err := s.purge(ctx, it); err != nil {
			return i, err
		}
	}
	return len(items), nil
}

func (s *FileService) purge(ctx context.Context, it domain.TrashItem) error {
	err := s.dataRoot.RemoveAll(trashLocation(it))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return mapFSErr(err)
	}
	return s.trash.Delete(ctx, it.OwnerID, it.ID)
}

// StartTrashReaper purges expired items every interval until ctx is done.
func (s *FileService) StartTrashReaper(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		items, err := s.trash.ListExpired(ctx, s.clock.Now(), 200)
		if err != nil && ctx.Err() == nil {
			s.log.Error("trash reaper", "err", err)
		}
		for _, it := range items {
			if err := s.purge(ctx, it); err != nil {
				s.log.Error("trash reaper purge", "id", it.ID, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
