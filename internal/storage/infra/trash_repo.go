package infra

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/storage/domain"
)

type TrashRepo struct{ db *database.DB }

func NewTrashRepo(db *database.DB) *TrashRepo { return &TrashRepo{db: db} }

const trashCols = "id, owner_id, origin_path, trash_path, is_dir, size, deleted_at, expire_at"

func (r *TrashRepo) Add(ctx context.Context, t domain.TrashItem) error {
	_, err := r.db.W.ExecContext(ctx, "INSERT INTO easycloud_trash ("+trashCols+") VALUES (?,?,?,?,?,?,?,?)",
		t.ID, t.OwnerID, t.OriginPath, t.TrashPath, t.IsDir, t.Size, t.DeletedAt.Unix(), t.ExpireAt.Unix())
	return err
}

func (r *TrashRepo) Get(ctx context.Context, owner domain.UserID, id string) (domain.TrashItem, error) {
	row := r.db.R.QueryRowContext(ctx, "SELECT "+trashCols+" FROM easycloud_trash WHERE owner_id = ? AND id = ?", owner, id)
	t, err := scanTrash(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	return t, err
}

func (r *TrashRepo) List(ctx context.Context, owner domain.UserID) ([]domain.TrashItem, error) {
	return r.query(ctx, "SELECT "+trashCols+" FROM easycloud_trash WHERE owner_id = ? ORDER BY deleted_at DESC", owner)
}

func (r *TrashRepo) Delete(ctx context.Context, owner domain.UserID, id string) error {
	res, err := r.db.W.ExecContext(ctx, "DELETE FROM easycloud_trash WHERE owner_id = ? AND id = ?", owner, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *TrashRepo) ListExpired(ctx context.Context, before time.Time, limit int) ([]domain.TrashItem, error) {
	return r.query(ctx, "SELECT "+trashCols+" FROM easycloud_trash WHERE expire_at < ? ORDER BY expire_at LIMIT ?", before.Unix(), limit)
}

func (r *TrashRepo) query(ctx context.Context, q string, args ...any) ([]domain.TrashItem, error) {
	rows, err := r.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TrashItem{}
	for rows.Next() {
		t, err := scanTrash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanTrash(s scanner) (domain.TrashItem, error) {
	var t domain.TrashItem
	var del, exp int64
	err := s.Scan(&t.ID, &t.OwnerID, &t.OriginPath, &t.TrashPath, &t.IsDir, &t.Size, &del, &exp)
	t.DeletedAt, t.ExpireAt = time.Unix(del, 0).UTC(), time.Unix(exp, 0).UTC()
	return t, err
}
