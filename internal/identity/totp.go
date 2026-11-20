package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/easycloud/easycloud/internal/platform/errs"
)

// Authenticator-app codes (TOTP, RFC 6238: HMAC-SHA1, 30-second steps, 6
// digits), the format every authenticator app understands.
//
// Login policy (chosen by the operator): once bound, a user signs in with
// either their password or a current code. Codes are single use, failures
// share the login rate limiter, and binding or unbinding needs the password
// (unbinding: or a code) so a hijacked session cannot add its own
// authenticator and keep access after a password change.
const (
	totpStep   = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step either side for clock drift
	totpIssuer = "EasyCloud"
)

var (
	ErrTOTPBadCode    = errs.New(400, "TOTP_BAD_CODE")
	ErrTOTPNotEnabled = errs.New(400, "TOTP_NOT_ENABLED")
	ErrTOTPEnabled    = errs.New(409, "TOTP_ALREADY_ENABLED")
	ErrTOTPNoSetup    = errs.New(400, "TOTP_NO_SETUP")
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20) // 160 bits, as RFC 4226 recommends
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

func totpCode(key []byte, step int64) string {
	m := hmac.New(sha1.New, key)
	_ = binary.Write(m, binary.BigEndian, step)
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1000000)
}

// totpMatch returns the time step a code belongs to, if it is valid now.
func totpMatch(secret, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(secret)
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / totpStep
	match := int64(0)
	ok := false
	for d := int64(-totpSkew); d <= totpSkew; d++ { // no early exit: same work for every code
		if subtle.ConstantTimeCompare([]byte(totpCode(key, cur+d)), []byte(code)) == 1 {
			match, ok = cur+d, true
		}
	}
	return match, ok
}

func otpauthURI(username, secret string) string {
	label := url.PathEscape(totpIssuer + ":" + username)
	q := url.Values{"secret": {secret}, "issuer": {totpIssuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

type totpRow struct {
	secret, pending string
	lastStep        int64
}

func (s *Service) totp(ctx context.Context, userID string) (totpRow, error) {
	var r totpRow
	err := s.db.R.QueryRowContext(ctx, "SELECT secret, pending, last_step FROM easycloud_totp WHERE user_id = ?", userID).Scan(&r.secret, &r.pending, &r.lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return totpRow{}, nil
	}
	return r, err
}

// TOTPEnabled reports whether the user has an authenticator bound.
func (s *Service) TOTPEnabled(ctx context.Context, userID string) bool {
	r, err := s.totp(ctx, userID)
	return err == nil && r.secret != ""
}

// TOTPUsers returns the IDs of users with an authenticator bound (admin list).
func (s *Service) TOTPUsers(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT user_id FROM easycloud_totp WHERE secret <> ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Service) checkPassword(ctx context.Context, userID, password string) error {
	u, err := s.r.userBy(ctx, "id", userID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(password)) != nil {
		return ErrBadCredentials
	}
	return nil
}

// SetupTOTP starts binding: after the password check it stores a fresh
// pending secret and returns it with the otpauth:// URI for the QR code.
// Nothing changes for login until EnableTOTP confirms a code.
func (s *Service) SetupTOTP(ctx context.Context, userID, password string) (secret, uri string, err error) {
	if err := s.checkPassword(ctx, userID, password); err != nil {
		return "", "", err
	}
	u, err := s.r.userBy(ctx, "id", userID)
	if err != nil {
		return "", "", err
	}
	if s.TOTPEnabled(ctx, userID) {
		return "", "", ErrTOTPEnabled
	}
	secret = newTOTPSecret()
	if err := s.upsertTOTP(ctx, userID, "pending = ?", secret); err != nil {
		return "", "", err
	}
	return secret, otpauthURI(u.Username, secret), nil
}

// EnableTOTP confirms the pending secret with a current code from the app.
func (s *Service) EnableTOTP(ctx context.Context, userID, code string) error {
	r, err := s.totp(ctx, userID)
	if err != nil {
		return err
	}
	if r.secret != "" {
		return ErrTOTPEnabled
	}
	if r.pending == "" {
		return ErrTOTPNoSetup
	}
	step, ok := totpMatch(r.pending, code, time.Now())
	if !ok {
		return ErrTOTPBadCode
	}
	_, err = s.db.W.ExecContext(ctx, "UPDATE easycloud_totp SET secret = pending, pending = '', last_step = ?, updated_at = ? WHERE user_id = ?",
		step, time.Now().Unix(), userID)
	return err
}

// DisableTOTP unbinds after checking the password or a current code.
func (s *Service) DisableTOTP(ctx context.Context, userID, password, code string) error {
	r, err := s.totp(ctx, userID)
	if err != nil {
		return err
	}
	if r.secret == "" {
		return ErrTOTPNotEnabled
	}
	if password != "" {
		if err := s.checkPassword(ctx, userID, password); err != nil {
			return err
		}
	} else if !s.consumeCode(ctx, userID, r, code) {
		return ErrTOTPBadCode
	}
	return s.ResetTOTP(ctx, userID)
}

// ResetTOTP removes a user's authenticator (administrators, for a lost phone).
func (s *Service) ResetTOTP(ctx context.Context, userID string) error {
	_, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_totp WHERE user_id = ?", userID)
	return err
}

func (s *Service) upsertTOTP(ctx context.Context, userID, set string, v any) error {
	now := time.Now().Unix()
	res, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_totp SET "+set+", updated_at = ? WHERE user_id = ?", v, now, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.W.ExecContext(ctx, "INSERT INTO easycloud_totp (user_id, secret, pending, last_step, updated_at) VALUES (?, '', ?, 0, ?)", userID, v, now)
	return err
}

// consumeCode accepts a code once: the stored last step only moves forward,
// and the conditional update makes two concurrent uses of one code fail.
func (s *Service) consumeCode(ctx context.Context, userID string, r totpRow, code string) bool {
	if r.secret == "" {
		return false
	}
	step, ok := totpMatch(r.secret, code, time.Now())
	if !ok || step <= r.lastStep {
		return false
	}
	res, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_totp SET last_step = ? WHERE user_id = ? AND last_step < ?", step, userID, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// LoginWithCode signs in with the username and a current authenticator code
// instead of the password (same rate limiting and session as Login).
func (s *Service) LoginWithCode(ctx context.Context, clientIP, username, code string) (User, string, error) {
	key := clientIP + "|" + strings.ToLower(username)
	if s.limiter.limited(clientIP) || s.limiter.limited(key) {
		return User{}, "", ErrRateLimited
	}
	u, err := s.r.userBy(ctx, "username", strings.TrimSpace(strings.ToLower(username)))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return User{}, "", err
	}
	ok := false
	if err == nil && !u.Disabled {
		if r, terr := s.totp(ctx, u.ID); terr == nil {
			ok = s.consumeCode(ctx, u.ID, r, code)
		}
	}
	if !ok { // unknown user, no authenticator, wrong or reused code: all the same answer
		s.limiter.record(clientIP, key)
		return User{}, "", ErrBadCredentials
	}
	tok, err := s.newSession(ctx, u.ID)
	return u, tok, err
}
