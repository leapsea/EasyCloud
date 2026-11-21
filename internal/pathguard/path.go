// Package pathguard is the single entry point for turning client-supplied path
// strings into validated paths and filesystem handles.
package pathguard

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ReservedDir holds trash and upload chunks; no API may address it. It is
// reserved at every level, not only in the data root: a folder mounted from
// another filesystem keeps its own trash in <mount>/.easycloud (a rename
// cannot cross filesystems).
const ReservedDir = ".easycloud"

// IsReservedName reports whether a single path segment is the reserved name.
func IsReservedName(name string) bool { return name == ReservedDir }

var (
	ErrInvalidPath = errors.New("pathguard: invalid path")
	ErrNotFound    = errors.New("pathguard: not found")
)

// RelPath can only be built by Parse, so any RelPath in business code is validated.
type RelPath struct{ segs []string }

// Parse expects a string already decoded exactly once by net/http or encoding/json.
// It rejects rather than normalises suspicious input.
func Parse(raw string) (RelPath, error) {
	if !utf8.ValidString(raw) || strings.ContainsAny(raw, "\x00\\") {
		return RelPath{}, ErrInvalidPath
	}
	var segs []string
	for _, s := range strings.Split(raw, "/") {
		switch {
		case s == "":
			continue
		case s == "." || s == "..":
			return RelPath{}, ErrInvalidPath
		case len(s) > 255 || hasControlChar(s):
			return RelPath{}, ErrInvalidPath
		case s == ReservedDir:
			return RelPath{}, ErrInvalidPath
		}
		segs = append(segs, s)
	}
	return RelPath{segs: segs}, nil
}

// MustParse is for tests and constants.
func MustParse(raw string) RelPath {
	p, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return p
}

func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func (p RelPath) IsRoot() bool       { return len(p.segs) == 0 }
func (p RelPath) Segments() []string { return append([]string(nil), p.segs...) }

// String returns the path without a leading slash; the root is ".".
func (p RelPath) String() string {
	if len(p.segs) == 0 {
		return "."
	}
	return strings.Join(p.segs, "/")
}

// Display returns the client-facing form with a leading slash.
func (p RelPath) Display() string { return "/" + strings.Join(p.segs, "/") }

func (p RelPath) Name() string {
	if len(p.segs) == 0 {
		return ""
	}
	return p.segs[len(p.segs)-1]
}

func (p RelPath) Parent() RelPath {
	if len(p.segs) == 0 {
		return p
	}
	return RelPath{segs: p.segs[: len(p.segs)-1 : len(p.segs)-1]}
}

// Join appends one validated child name.
func (p RelPath) Join(name string) (RelPath, error) {
	if strings.Contains(name, "/") {
		return RelPath{}, ErrInvalidPath
	}
	c, err := Parse(name)
	if err != nil || len(c.segs) != 1 {
		return RelPath{}, ErrInvalidPath
	}
	segs := make([]string, 0, len(p.segs)+1)
	segs = append(segs, p.segs...)
	return RelPath{segs: append(segs, c.segs[0])}, nil
}

// HasPrefix compares by segment: "share/AA" is not a prefix of "share/AAB".
func (p RelPath) HasPrefix(q RelPath) bool {
	if len(q.segs) > len(p.segs) {
		return false
	}
	for i, s := range q.segs {
		if p.segs[i] != s {
			return false
		}
	}
	return true
}

func (p RelPath) Equal(q RelPath) bool {
	return len(p.segs) == len(q.segs) && p.HasPrefix(q)
}
