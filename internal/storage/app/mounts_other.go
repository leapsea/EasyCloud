//go:build !linux

package app

// mountPoints is unavailable here; callers fall back to device numbers.
func mountPoints() ([]string, bool) { return nil, false }
