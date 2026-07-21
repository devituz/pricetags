// Package migrations embeds SQL migration files so the binary can
// apply them on startup without shipping the files separately.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
