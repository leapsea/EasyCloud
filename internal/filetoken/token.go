// Package filetoken issues short-lived capability URLs for single files, for
// clients that cannot send the session cookie (sandboxed viewer iframes,
// OnlyOffice Document Server, external players). Tokens are HMAC-signed and
// carry the user, so authorization is re-checked on every use.
package filetoken

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/database"
)

var ErrInvalid = errors.New("invalid or expired token")

type Claims struct {
	UserID  string `json:"u"`
	Path    string `json:"p"` // display path
	Purpose string `json:"k"` // "raw" | "office-cb" | ...
	Write   bool   `json:"w,omitempty"`
	Exp     int64  `json:"e"`
}

type Signer struct{ key []byte }

const secretName = "secret.filetoken"

// Load returns a signer whose key is stored in the database (created on first use),
// so tokens survive restarts and are shared by every process using the same database.
func Load(ctx context.Context, db *database.DB) (*Signer, error) {
	var v string
	err := db.R.QueryRowContext(ctx, "SELECT value FROM easycloud_settings WHERE name = ?", secretName).Scan(&v)
	if err == nil {
		k, err := hex.DecodeString(v)
		if err == nil && len(k) == 32 {
			return &Signer{key: k}, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	var q string
	if db.Dialect == database.MySQL {
		q = "INSERT IGNORE INTO easycloud_settings (name, value, updated_by, updated_at) VALUES (?,?,?,?)"
	} else {
		q = "INSERT INTO easycloud_settings (name, value, updated_by, updated_at) VALUES (?,?,?,?) ON CONFLICT(name) DO NOTHING"
	}
	if _, err := db.W.ExecContext(ctx, q, secretName, hex.EncodeToString(k), "system", time.Now().Unix()); err != nil {
		return nil, err
	}
	// Another process may have won the race: read back whatever is stored.
	if err := db.R.QueryRowContext(ctx, "SELECT value FROM easycloud_settings WHERE name = ?", secretName).Scan(&v); err != nil {
		return nil, err
	}
	k, err = hex.DecodeString(v)
	if err != nil {
		return nil, err
	}
	return &Signer{key: k}, nil
}

func NewSigner(key []byte) *Signer { return &Signer{key: key} }

func (s *Signer) mac(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Signer) Issue(c Claims, ttl time.Duration) string {
	c.Exp = time.Now().Add(ttl).Unix()
	b, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.mac(payload)
}

func (s *Signer) Verify(tok, purpose string) (Claims, error) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return Claims{}, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if json.Unmarshal(b, &c) != nil || c.Purpose != purpose || time.Now().Unix() > c.Exp {
		return Claims{}, ErrInvalid
	}
	return c, nil
}
