// Package migrations embeds SQL files in the application so deployments do not
// depend on a writable filesystem or a separately installed MySQL client.
package migrations

import "embed"

// Files embeds the initial schema and later SQL migrations.
// Top-level forward SQL files are executable. Files in retired/ are retained
// only to validate the checksums of databases that previously applied them.
//
//go:embed *
var Files embed.FS
