package server_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

// totpAt is an independent RFC 6238 implementation for the test.
func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha1.New, key)
	binary.Write(m, binary.BigEndian, at.Unix()/30)
	s := m.Sum(nil)
	o := s[len(s)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(s[o:o+4])&0x7fffffff)%1000000)
}

// Once an authenticator is bound, either the password or a current code signs
// in; codes are single use; binding/unbinding needs the password.
func TestTwoFactorLogin(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	_, ua := admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alicepass1"})
	alice := e.client()
	alice.login("alice", "alicepass1")
	loginCode := func(code string) int {
		c := e.client()
		st, _ := c.do("POST", "/api/auth/login", map[string]string{"username": "alice", "code": code})
		return st
	}

	// Without a bound authenticator a code never signs in.
	if st := loginCode("123456"); st != 401 {
		t.Fatalf("code login before binding: %d", st)
	}
	if st, _ := alice.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "wrong"}); st != 401 {
		t.Fatalf("setup with a wrong password: %d", st)
	}
	st, setup := alice.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "alicepass1"})
	secret, _ := setup["secret"].(string)
	if st != 200 || secret == "" || setup["uri"] == nil {
		t.Fatalf("setup: %d %v", st, setup)
	}
	// Pending only: not usable for login yet.
	if st := loginCode(totpAt(t, secret, time.Now())); st != 401 {
		t.Fatalf("code login with an unconfirmed secret: %d", st)
	}
	if st, _ := alice.do("POST", "/api/auth/2fa/enable", map[string]string{"code": "000000"}); st != 400 {
		t.Fatalf("enable with a wrong code: %d", st)
	}
	if st, _ := alice.do("POST", "/api/auth/2fa/enable", map[string]string{"code": totpAt(t, secret, time.Now())}); st != 204 {
		t.Fatalf("enable: %d", st)
	}
	if _, m := alice.do("GET", "/api/auth/me", nil); m["twoFactor"] != true {
		t.Fatalf("me: %v", m)
	}
	_, list := admin.do("GET", "/api/admin/users", nil)
	for _, it := range list["items"].([]any) {
		if u := it.(map[string]any); u["id"] == ua["id"] && u["twoFactor"] != true {
			t.Fatalf("admin list: %v", u)
		}
	}

	// Either factor signs in. The code of the step used for enabling is spent,
	// so use the next step's code (accepted within the drift window).
	next := totpAt(t, secret, time.Now().Add(30*time.Second))
	if st := loginCode(next); st != 200 {
		t.Fatalf("code login: %d", st)
	}
	if st := loginCode(next); st != 401 {
		t.Fatalf("reused code: %d", st)
	}
	pw := e.client()
	if st, _ := pw.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alicepass1"}); st != 200 {
		t.Fatalf("password login after binding: %d", st)
	}
	if st := loginCode("999999"); st != 401 {
		t.Fatalf("wrong code: %d", st)
	}

	// Unbinding needs the password (or a code); afterwards codes stop working.
	if st, _ := alice.do("POST", "/api/auth/2fa/disable", map[string]string{"password": "wrong"}); st != 401 {
		t.Fatalf("disable with a wrong password: %d", st)
	}
	if st, _ := alice.do("POST", "/api/auth/2fa/disable", map[string]string{"password": "alicepass1"}); st != 204 {
		t.Fatalf("disable: %d", st)
	}
	if st := loginCode(totpAt(t, secret, time.Now().Add(-30*time.Second))); st != 401 {
		t.Fatalf("code login after unbinding: %d", st)
	}

	// Administrators can remove a lost authenticator.
	_, setup = alice.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "alicepass1"})
	secret = setup["secret"].(string)
	alice.do("POST", "/api/auth/2fa/enable", map[string]string{"code": totpAt(t, secret, time.Now())})
	if st, _ := admin.do("POST", "/api/admin/users/"+ua["id"].(string)+"/2fa/reset", nil); st != 204 {
		t.Fatalf("admin reset: %d", st)
	}
	if _, m := alice.do("GET", "/api/auth/me", nil); m["twoFactor"] != false {
		t.Fatalf("after reset: %v", m)
	}
	if st, _ := alice.do("POST", "/api/admin/users/"+ua["id"].(string)+"/2fa/reset", nil); st == 204 {
		t.Fatal("non-admin reset another user's 2FA")
	}
}

// Wrong codes count towards the login rate limit like wrong passwords.
func TestTwoFactorRateLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	admin.login("admin", "adminpass1")
	admin.do("POST", "/api/admin/users", map[string]any{"username": "carol", "password": "carolpass1"})
	c := e.client()
	last := 0
	for i := 0; i < 7; i++ {
		last, _ = c.do("POST", "/api/auth/login", map[string]string{"username": "carol", "code": fmt.Sprintf("%06d", i)})
	}
	if last != 429 {
		t.Fatalf("after 7 wrong codes: %d, want 429", last)
	}
	if st, _ := c.do("POST", "/api/auth/login", map[string]string{"username": "carol", "password": "carolpass1"}); st != 429 {
		t.Fatalf("password login while limited: %d", st)
	}
}
