package identity

import (
	"testing"
	"time"
)

// RFC 6238 appendix B (SHA-1 seed "12345678901234567890"), last 6 digits.
func TestTOTPVectors(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, c := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got := totpCode(key, c.unix/totpStep); got != c.want {
			t.Errorf("t=%d: %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestTOTPMatchWindow(t *testing.T) {
	secret := newTOTPSecret()
	key, _ := b32.DecodeString(secret)
	now := time.Unix(1_800_000_000, 0)
	step := now.Unix() / totpStep
	for d := int64(-1); d <= 1; d++ {
		if got, ok := totpMatch(secret, totpCode(key, step+d), now); !ok || got != step+d {
			t.Errorf("offset %d not accepted", d)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := totpMatch(secret, totpCode(key, step+d), now); ok {
			t.Errorf("offset %d accepted", d)
		}
	}
	if _, ok := totpMatch(secret, "12345", now); ok {
		t.Error("short code accepted")
	}
	if _, ok := totpMatch(secret, " "+totpCode(key, step)[:3]+" "+totpCode(key, step)[3:], now); !ok {
		t.Error("code with spaces rejected")
	}
}
