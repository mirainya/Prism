package migrations

import "embed"

const BaselineVersion = "20260718_150000"

// Files contains the immutable SQL migration history.
//
//go:embed *.sql
var Files embed.FS
