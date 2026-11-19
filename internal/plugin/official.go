package plugin

import (
	"crypto/ed25519"
	_ "embed"
	"strings"
)

//go:embed official_keys.txt
var officialKeys string

// OfficialKeys returns the built-in trusted keys (official_keys.txt).
func OfficialKeys() []ed25519.PublicKey {
	var lines []string
	for _, l := range strings.Split(officialKeys, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	keys, err := ParseKeys(strings.Join(lines, ","))
	if err != nil {
		panic("official_keys.txt: " + err.Error()) // caught by TestOfficialKeys before release
	}
	return keys
}
