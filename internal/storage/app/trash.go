package app

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/storage/domain"
)

// trashDir is relative to the data root. Same filesystem as user files, so rename is atomic.
func trashDir(owner string) string { return path.Join(pathguard.ReservedDir, "trash", owner) }

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
			return out, mapFSErr(err)
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
	if err := s.dataRoot.Rename(path.Join(pathguard.ReservedDir, item.TrashPath), target.String()); err != nil {
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
	err := s.dataRoot.RemoveAll(path.Join(pathguard.ReservedDir, it.TrashPath))
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
