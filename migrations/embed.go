// Package migrations embeds goose migrations for both dialects. The two
// directories must always contain the same version numbers.
package migrations

import "embed"

//go:embed sqlite/*.sql
var SQLite embed.FS

//go:embed mysql/*.sql
var MySQL embed.FS
