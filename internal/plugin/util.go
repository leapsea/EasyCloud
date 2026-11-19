package plugin

import (
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/mod/semver"
)

func compareSemver(a, b string) int { return semver.Compare("v"+a, "v"+b) }

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
