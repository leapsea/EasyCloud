//go:build unix

package app

import (
	"io/fs"
	"syscall"
)

// deviceOf returns the filesystem (device) a file lives on.
func deviceOf(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // int32 on darwin
}
