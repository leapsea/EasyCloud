package info

import (
	"os"
	"syscall"
	"time"
)

// birthTime returns when the file was created, if the platform records it.
func birthTime(_ *os.File, fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Birthtimespec.Sec == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
}
