package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const dir = "migrations/postgres"

//go:embed migrations/postgres/*.sql
var Migrations embed.FS

func Provider(sqlDB *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(Migrations, dir)
	if err != nil {
		return nil, fmt.Errorf("db: migrations fs: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("db: migration locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("db: migration provider: %w", err)
	}
	return p, nil
}
