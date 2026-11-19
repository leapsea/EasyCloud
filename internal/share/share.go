// Package share implements public share links. A share acts with its owner's
// current permissions: if the owner loses access or is disabled, the link stops working.
package share

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrGone      = errs.New(410, "SHARE_GONE")     // expired, revoked, or download limit reached
	ErrLocked    = errs.New(401, "SHARE_PASSWORD") // password required
	ErrBadPass   = errs.New(401, "SHARE_BAD_PASSWORD")
	ErrRateLimit = errs.New(429, "TOO_MANY_ATTEMPTS")
)

type Share struct {
	ID           string    `json:"id"`
	OwnerID      string    `json:"ownerId"`
	Path         string    `json:"path"`
	IsDir        bool      `json:"isDir"`
	HasPassword  bool      `json:"hasPassword"`
	ExpireAt     time.Time `json:"expireAt,omitzero"`
	MaxDownloads int       `json:"maxDownloads"`
	Downloads    int       `json:"downloads"`
	CreatedAt    time.Time `json:"createdAt"`
	hash         string
}

func (s Share) Expired(now time.Time) bool { return !s.ExpireAt.IsZero() && now.After(s.ExpireAt) }
func (s Share) Exhausted() bool            { return s.MaxDownloads > 0 && s.Downloads >= s.MaxDownloads }

type Service struct {
	db    *database.DB
	files *storageapp.FileService
	ids   *identity.Service

	mu       sync.Mutex
	failures map[string][]time.Time
}

func NewService(db *database.DB, files *storageapp.FileService, ids *identity.Service) *Service {
	return &Service{db: db, files: files, ids: ids, failures: map[string][]time.Time{}}
}

// newID returns 128 random bits, URL-safe (22 characters).
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

const cols = "id, owner_id, path, is_dir, password_hash, expire_at, max_downloads, downloads, created_at"

func scan(r interface{ Scan(...any) error }) (Share, error) {
	var s Share
	var exp, created int64
	err := r.Scan(&s.ID, &s.OwnerID, &s.Path, &s.IsDir, &s.hash, &exp, &s.MaxDownloads, &s.Downloads, &created)
	if exp > 0 {
		s.ExpireAt = time.Unix(exp, 0).UTC()
	}
	s.CreatedAt, s.HasPassword = time.Unix(created, 0).UTC(), s.hash != ""
	return s, err
}

type CreateInput struct {
	Path         string `json:"path"`
	Password     string `json:"password"`
	ExpireDays   int    `json:"expireDays"` // 0 = never
	MaxDownloads int    `json:"maxDownloads"`
}

func (s *Service) Create(ctx context.Context, sub pathguard.Subject, in CreateInput) (Share, error) {
	e, err := s.files.Stat(ctx, sub, in.Path)
	if err != nil {
		return Share{}, err
	}
	if e.Path == "/" {
		return Share{}, errs.ErrInvalidInput.WithDetail("the root directory cannot be shared")
	}
	if in.ExpireDays < 0 || in.ExpireDays > 3650 || in.MaxDownloads < 0 || len(in.Password) > 128 {
		return Share{}, errs.ErrInvalidInput
	}
	sh := Share{ID: newID(), OwnerID: sub.UserID, Path: e.Path, IsDir: e.IsDir, MaxDownloads: in.MaxDownloads, CreatedAt: time.Now().UTC()}
	if in.ExpireDays > 0 {
		sh.ExpireAt = time.Now().Add(time.Duration(in.ExpireDays) * 24 * time.Hour).UTC()
	}
	if in.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return Share{}, err
		}
		sh.hash, sh.HasPassword = string(h), true
	}
	var exp int64
	if !sh.ExpireAt.IsZero() {
		exp = sh.ExpireAt.Unix()
	}
	_, err = s.db.W.ExecContext(ctx, "INSERT INTO easycloud_shares ("+cols+") VALUES (?,?,?,?,?,?,?,?,?)",
		sh.ID, sh.OwnerID, sh.Path, sh.IsDir, sh.hash, exp, sh.MaxDownloads, 0, sh.CreatedAt.Unix())
	return sh, err
}

func (s *Service) list(ctx context.Context, where string, args ...any) ([]Share, error) {
	q := "SELECT " + cols + " FROM easycloud_shares"
	if where != "" {
		q += " WHERE " + where
	}
	rows, err := s.db.R.QueryContext(ctx, q+" ORDER BY created_at DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		sh, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Service) Mine(ctx context.Context, sub pathguard.Subject) ([]Share, error) {
	return s.list(ctx, "owner_id = ?", sub.UserID)
}

func (s *Service) All(ctx context.Context) ([]Share, error) { return s.list(ctx, "") }

// Delete revokes a share; only its owner or an administrator may.
func (s *Service) Delete(ctx context.Context, sub pathguard.Subject, id string) (Share, error) {
	sh, err := s.get(ctx, id)
	if err != nil || (sh.OwnerID != sub.UserID && !sub.IsAdmin) {
		return Share{}, errs.ErrNotFound
	}
	_, err = s.db.W.ExecContext(ctx, "DELETE FROM easycloud_shares WHERE id = ?", id)
	return sh, err
}

func (s *Service) get(ctx context.Context, id string) (Share, error) {
	sh, err := scan(s.db.R.QueryRowContext(ctx, "SELECT "+cols+" FROM easycloud_shares WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return sh, errs.ErrNotFound
	}
	return sh, err
}

// Access is a resolved, usable share.
type Access struct {
	Share Share
	Owner identity.User
}

func (a Access) Subject() pathguard.Subject { return a.Owner.Subject() }

// Resolve loads a share for public access and checks every revocation condition.
func (s *Service) Resolve(ctx context.Context, id string) (Access, error) {
	if len(id) != 22 {
		return Access{}, errs.ErrNotFound
	}
	sh, err := s.get(ctx, id)
	if err != nil {
		return Access{}, err
	}
	if sh.Expired(time.Now()) || sh.Exhausted() {
		return Access{}, ErrGone
	}
	owner, err := s.ids.GetUser(ctx, sh.OwnerID)
	if err != nil || owner.Disabled {
		return Access{}, ErrGone
	}
	if _, err := s.files.Stat(ctx, owner.Subject(), sh.Path); err != nil {
		return Access{}, ErrGone // owner lost access, or the file is gone
	}
	return Access{Share: sh, Owner: owner}, nil
}

// CheckPassword verifies a share password with per share+client rate limiting.
func (s *Service) CheckPassword(a Access, clientIP, password string) error {
	if !a.Share.HasPassword {
		return nil
	}
	key := a.Share.ID + "|" + clientIP
	s.mu.Lock()
	cut := time.Now().Add(-10 * time.Minute)
	kept := s.failures[key][:0]
	for _, t := range s.failures[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.failures[key] = kept
	limited := len(kept) >= 10
	s.mu.Unlock()
	if limited {
		return ErrRateLimit
	}
	if bcrypt.CompareHashAndPassword([]byte(a.Share.hash), []byte(password)) != nil {
		s.mu.Lock()
		s.failures[key] = append(s.failures[key], time.Now())
		s.mu.Unlock()
		return ErrBadPass
	}
	return nil
}

// Within maps a path relative to the share root to a display path, refusing escapes.
func (a Access) Within(rel string) (string, error) {
	root, err := pathguard.Parse(a.Share.Path)
	if err != nil {
		return "", errs.ErrNotFound
	}
	if rel == "" || rel == "/" {
		return root.Display(), nil
	}
	if !a.Share.IsDir {
		return "", errs.ErrNotFound
	}
	sub, err := pathguard.Parse(rel)
	if err != nil {
		return "", errs.ErrInvalidPath
	}
	p := root
	for _, seg := range sub.Segments() {
		if p, err = p.Join(seg); err != nil {
			return "", errs.ErrInvalidPath
		}
	}
	return p.Display(), nil
}

// CountDownload atomically consumes one download; false means the limit was reached.
func (s *Service) CountDownload(ctx context.Context, id string) (bool, error) {
	res, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_shares SET downloads = downloads + 1 WHERE id = ? AND (max_downloads = 0 OR downloads < max_downloads)", id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// PathMoved keeps shares pointing at moved or renamed files.
func (s *Service) PathMoved(ctx context.Context, from, to pathguard.RelPath) {
	shares, err := s.list(ctx, "")
	if err != nil {
		return
	}
	for _, sh := range shares {
		p, err := pathguard.Parse(sh.Path)
		if err != nil || !p.HasPrefix(from) {
			continue
		}
		np := to
		for _, seg := range p.Segments()[len(from.Segments()):] {
			np, _ = np.Join(seg)
		}
		_, _ = s.db.W.ExecContext(ctx, "UPDATE easycloud_shares SET path = ? WHERE id = ?", np.Display(), sh.ID)
	}
}

// PruneExpired deletes shares that expired more than 30 days ago.
func (s *Service) PruneExpired(ctx context.Context) {
	_, _ = s.db.W.ExecContext(ctx, "DELETE FROM easycloud_shares WHERE expire_at > 0 AND expire_at < ?", time.Now().Add(-30*24*time.Hour).Unix())
}

func displayName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
