// Package migrations embeds SQL files into the binary.
//
// Embedding means the container image does not need a `migrations/` directory
// at runtime, and there is no chance of running a different file than the one
// tested with the build. goose reads this virtual filesystem.
package migrations

import "embed"

// FS contains every .sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
