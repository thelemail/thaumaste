package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/db"
	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
)

const databasePrefix = "thaumaste_test"

var (
	once      sync.Once
	schemaErr error

	database = databasePrefix
)

// Go runs the test binaries of different packages at the same time, so a single shared database
// would have one package truncating tables another was midway through using. Each package gets its
// own, named after its directory, which means a package added later is isolated without anyone
// having to arrange it.
func databaseFor(pkg string) string {
	if pkg == "" {
		return databasePrefix
	}
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToLower(pkg))
	return databasePrefix + "_" + clean
}

func settings() config.Postgres {
	port, err := strconv.Atoi(env("THAUMASTE_POSTGRES_PORT", "5435"))
	if err != nil {
		port = 5435
	}
	return config.Postgres{
		Host:            env("THAUMASTE_POSTGRES_HOST", "127.0.0.1"),
		Port:            port,
		User:            env("THAUMASTE_POSTGRES_USER", "thaumaste"),
		Password:        env("THAUMASTE_POSTGRES_PASSWORD", "thaumaste"),
		Database:        database,
		SSLMode:         "disable",
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		LockTimeout:     5 * time.Second,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Connect returns a client against a dedicated test database with migrations applied,
// truncating the named tables first. It skips the test when Postgres is unreachable,
// unless THAUMASTE_TEST_REQUIRE_POSTGRES is set, which is how CI refuses to let these
// tests quietly disappear.
func Connect(t *testing.T, truncate ...string) *postgres.Client {
	t.Helper()

	ctx := t.Context()
	_, file, _, ok := runtime.Caller(1)
	once.Do(func() {
		if ok {
			database = databaseFor(filepath.Base(filepath.Dir(file)))
		}
		schemaErr = ensureSchema(ctx)
	})
	if schemaErr != nil {
		unavailable(t, schemaErr)
	}

	pg, err := postgres.New(ctx, settings())
	if err != nil {
		unavailable(t, err)
	}
	t.Cleanup(func() { _ = pg.Close() })

	for _, table := range truncate {
		if _, err := pg.ExecContext(ctx, "TRUNCATE "+table+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	return pg
}

func unavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("THAUMASTE_TEST_REQUIRE_POSTGRES") != "" {
		t.Fatalf("postgres is required but unavailable: %v", err)
	}
	t.Skipf("postgres unavailable: %v", err)
}

func ensureSchema(ctx context.Context) error {
	admin := settings()
	admin.Database = "postgres"

	root, err := sql.Open("pgx", admin.DSN())
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer func() { _ = root.Close() }()

	if err := root.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := root.ExecContext(ctx, "CREATE DATABASE "+database); err != nil && !exists(err) {
		return fmt.Errorf("create %s: %w", database, err)
	}

	pg, err := postgres.NewMigrator(ctx, settings())
	if err != nil {
		return fmt.Errorf("connect %s: %w", database, err)
	}
	defer func() { _ = pg.Close() }()

	provider, err := db.Provider(pg.DB)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate %s: %w", database, err)
	}
	return nil
}

func exists(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "42P04"))
}
