//go:build !linux && !darwin

package info

import (
	"os"
	"time"
)

func birthTime(*os.File, os.FileInfo) (time.Time, bool) { return time.Time{}, false }
