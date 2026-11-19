package main

import (
	"encoding/json"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"sort"

	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

// uidBase is the first uid handed to SMB accounts inside the Samba container.
const uidBase = 20000

// uidMap assigns every SMB account a unique, stable unix uid inside the
// container. It is persisted next to smb.conf (uids.json) because the
// container's users outlive a single sync.
//
// Earlier versions derived the uid from crc32(name) % 40000; two names with
// the same value made the second adduser fail, and that user could not sign
// in. Now uids are allocated: a new account gets one above the highest ever
// handed out, and entries of removed accounts are kept so their uid is never
// reused (a running container still has that unix user).
type uidMap map[string]int

func loadUIDs(path string) (uidMap, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // first run: assign seeds from the legacy scheme
	}
	if err != nil {
		return nil, err
	}
	m := uidMap{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// legacyUID is the uid earlier versions gave name.
func legacyUID(name string) int { return uidBase + int(crc32.ChecksumIEEE([]byte(name))%40000) }

// assignUIDs gives every account without a uid a free one and reports whether
// the map changed. A nil map means "no uids.json yet": accounts first keep
// their legacy uid where it is unique, so existing containers stay consistent;
// colliding names (in name order) and later accounts get fresh uids. reserved
// holds uids that must never be used (the force user/group).
func assignUIDs(m uidMap, accounts []pluginsdk.Account, reserved ...int) (uidMap, bool) {
	seed := m == nil
	if seed {
		m = uidMap{}
	}
	used := map[int]bool{}
	for _, r := range reserved {
		used[r] = true
	}
	next := uidBase
	for _, u := range m {
		used[u] = true
		next = max(next, u+1)
	}
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if _, ok := m[a.Username]; !ok {
			names = append(names, a.Username)
		}
	}
	sort.Strings(names)
	changed := seed
	if seed {
		for _, n := range names {
			if u := legacyUID(n); !used[u] {
				m[n], used[u] = u, true
				next = max(next, u+1)
			}
		}
	}
	for _, n := range names {
		if _, ok := m[n]; ok {
			continue
		}
		for used[next] {
			next++
		}
		m[n], used[next] = next, true
		next++
		changed = true
	}
	return m, changed
}

func (m uidMap) encode() string {
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b) + "\n"
}
