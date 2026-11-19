package server

import "net/url"

func escapeSegment(s string) string { return url.PathEscape(s) }
