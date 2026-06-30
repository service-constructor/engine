// Package migrations embeds the SQL migration files so they ship inside the
// binary and can be applied at startup via golang-migrate.
package migrations

import "embed"

// FS holds all *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
