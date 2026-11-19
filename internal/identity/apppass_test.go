package identity

import "testing"

func TestNTHash(t *testing.T) {
	if got := NTHash("password"); got != "8846F7EAEE8FB117AD06BDD830B7586C" {
		t.Fatalf("NTHash(password) = %s", got)
	}
	if got := NTHash(""); got != "31D6CFE0D16AE931B73C59D7E0C089C0" {
		t.Fatalf("NTHash(empty) = %s", got)
	}
}

func TestAppPasswordFormat(t *testing.T) {
	p := randomAppPassword()
	if len(p) != 19 || p[4] != '-' || p[9] != '-' || p[14] != '-' {
		t.Fatalf("format %q", p)
	}
}
