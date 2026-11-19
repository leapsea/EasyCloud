package info

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// birthTime asks statx for the creation time; ext4, xfs, btrfs and zfs record
// it on recent kernels, others (and some network filesystems) do not.
func birthTime(f *os.File, _ os.FileInfo) (time.Time, bool) {
	var st unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_BTIME, &st); err != nil || st.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec)), true
}
