package db

import "embed"

//go:embed migrations/postgres/*.sql
var Migrations embed.FS
