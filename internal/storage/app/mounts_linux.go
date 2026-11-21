package app

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// mountPoints lists the mount points of this process (/proc/self/mountinfo).
// Unlike comparing device numbers, it also sees bind mounts of the same disk
// (Docker volumes, a host folder mounted into /data), which a rename cannot
// cross either.
func mountPoints() ([]string, bool) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 {
			out = append(out, unescapeMount(fields[4]))
		}
	}
	return out, sc.Err() == nil
}

// unescapeMount decodes the octal escapes mountinfo uses (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
