package filetoken

import (
	"context"
	"testing"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/database/dbtest"
)

func TestTokens(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	tok := s.Issue(Claims{UserID: "u1", Path: "/a.txt", Purpose: "raw"}, time.Minute)
	c, err := s.Verify(tok, "raw")
	if err != nil || c.UserID != "u1" || c.Path != "/a.txt" {
		t.Fatal(c, err)
	}
	if _, err := s.Verify(tok, "office-cb"); err == nil {
		t.Fatal("purpose not enforced")
	}
	if _, err := s.Verify(tok[:len(tok)-2]+"xx", "raw"); err == nil {
		t.Fatal("tampered signature accepted")
	}
	if _, err := NewSigner([]byte("another key another key another k")).Verify(tok, "raw"); err == nil {
		t.Fatal("wrong key accepted")
	}
	expired := s.Issue(Claims{UserID: "u1", Path: "/a", Purpose: "raw"}, -time.Second)
	if _, err := s.Verify(expired, "raw"); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestKeyPersists(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		a, err := Load(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Load(ctx, db)
		tok := a.Issue(Claims{UserID: "x", Purpose: "raw"}, time.Minute)
		if _, err := b.Verify(tok, "raw"); err != nil {
			t.Fatal("key not persisted:", err)
		}
	})
}
