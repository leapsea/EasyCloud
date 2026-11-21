//go:build !unix

package app

import "io/fs"

func deviceOf(fs.FileInfo) (uint64, bool) { return 0, false }
