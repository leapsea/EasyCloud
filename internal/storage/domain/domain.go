// Package domain holds storage entities and repository interfaces. No external deps.
package domain

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type UserID = string

type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Mime    string    `json:"mime,omitempty"`
	Width   int       `json:"width,omitempty"` // images, from the file index
	Height  int       `json:"height,omitempty"`
	Thumb   bool      `json:"thumb,omitempty"` // a thumbnail can be generated
	// Protected marks system directories (e.g. /user and personal spaces) that
	// cannot be deleted, moved or renamed.
	Protected bool `json:"protected,omitempty"`
}

type TrashItem struct {
	ID         string    `json:"id"`
	OwnerID    UserID    `json:"-"`
	OriginPath string    `json:"originPath"`
	TrashPath  string    `json:"-"` // relative to the reserved dir
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	DeletedAt  time.Time `json:"deletedAt"`
	ExpireAt   time.Time `json:"expireAt"`
}

// TrashRepository: every by-ID method requires owner to prevent IDOR by signature.
type TrashRepository interface {
	Add(ctx context.Context, item TrashItem) error
	Get(ctx context.Context, owner UserID, id string) (TrashItem, error)
	List(ctx context.Context, owner UserID) ([]TrashItem, error)
	Delete(ctx context.Context, owner UserID, id string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]TrashItem, error)
}
