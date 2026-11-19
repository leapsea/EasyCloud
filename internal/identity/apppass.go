package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/md4" //nolint:staticcheck // NT hashes are MD4 by definition (Samba smbpasswd format)
)

// AppPassword is a random per-device password for WebDAV and SMB. The plaintext
// is shown once; we keep a bcrypt hash (WebDAV) and the NT hash (Samba's format).
type AppPassword struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitzero"`
}

// NTHash is MD4 over the UTF-16LE password, upper-case hex (smbpasswd format).
func NTHash(pw string) string {
	h := md4.New()
	for _, u := range utf16.Encode([]rune(pw)) {
		h.Write([]byte{byte(u), byte(u >> 8)})
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

func randomAppPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return sb.String()
}

func (s *Service) CreateAppPassword(ctx context.Context, userID, name string) (AppPassword, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return AppPassword{}, "", fmt.Errorf("%w: name", ErrInvalid)
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_app_passwords WHERE user_id = ?", userID).Scan(&n); err != nil {
		return AppPassword{}, "", err
	}
	if n >= 20 {
		return AppPassword{}, "", fmt.Errorf("%w: at most 20 app passwords", ErrInvalid)
	}
	pw := randomAppPassword()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return AppPassword{}, "", err
	}
	ap := AppPassword{ID: newID(), Name: name, CreatedAt: time.Now().UTC()}
	_, err = s.db.W.ExecContext(ctx, "INSERT INTO easycloud_app_passwords (id, user_id, name, hash, nt_hash, created_at) VALUES (?,?,?,?,?,?)",
		ap.ID, userID, ap.Name, string(h), NTHash(pw), ap.CreatedAt.Unix())
	if err == nil {
		s.notify()
	}
	return ap, pw, err
}

func (s *Service) ListAppPasswords(ctx context.Context, userID string) ([]AppPassword, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT id, name, created_at, last_used_at FROM easycloud_app_passwords WHERE user_id = ? ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppPassword{}
	for rows.Next() {
		var a AppPassword
		var c, u int64
		if err := rows.Scan(&a.ID, &a.Name, &c, &u); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(c, 0).UTC()
		if u > 0 {
			a.LastUsedAt = time.Unix(u, 0).UTC()
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) DeleteAppPassword(ctx context.Context, userID, id string) error {
	res, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_app_passwords WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.notify()
	return nil
}
