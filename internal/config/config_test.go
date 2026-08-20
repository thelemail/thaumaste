package config_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
)

func testPostgres() config.Postgres {
	return config.Postgres{
		Host:                     "db.internal",
		Port:                     5435,
		User:                     "thaumaste",
		Password:                 "s3cret",
		Database:                 "thaumaste",
		SSLMode:                  "require",
		StatementTimeout:         15 * time.Second,
		LockTimeout:              5 * time.Second,
		IdleInTransactionTimeout: 30 * time.Second,
	}
}

func query(t *testing.T, dsn string) url.Values {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %q: %v", dsn, err)
	}
	return u.Query()
}

func TestDSNCarriesCredentialsHostAndSSLMode(t *testing.T) {
	dsn := testPostgres().DSN()

	if !strings.HasPrefix(dsn, "postgres://thaumaste:s3cret@db.internal:5435/thaumaste?") {
		t.Fatalf("DSN = %q, wrong prefix", dsn)
	}
	if got := query(t, dsn).Get("sslmode"); got != "require" {
		t.Fatalf("sslmode = %q, want require", got)
	}
}

func TestDSNEscapesPasswordPunctuation(t *testing.T) {
	p := testPostgres()
	p.Password = "p@ss/word"

	u, err := url.Parse(p.DSN())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pw, _ := u.User.Password()
	if pw != "p@ss/word" {
		t.Fatalf("password round-trip = %q, want %q", pw, "p@ss/word")
	}
}

func TestDSNSetsTimeoutsInMilliseconds(t *testing.T) {
	q := query(t, testPostgres().DSN())

	for name, want := range map[string]string{
		"statement_timeout":                   "15000",
		"lock_timeout":                        "5000",
		"idle_in_transaction_session_timeout": "30000",
	} {
		if got := q.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestDSNOmitsTimeoutsThatAreNotSet(t *testing.T) {
	p := testPostgres()
	p.StatementTimeout = 0

	if q := query(t, p.DSN()); q.Has("statement_timeout") {
		t.Fatalf("statement_timeout present when unset: %q", q.Get("statement_timeout"))
	}
}

func TestMigratorDSNLeavesStatementsUntimed(t *testing.T) {
	q := query(t, testPostgres().MigratorDSN())

	if q.Has("statement_timeout") {
		t.Fatalf("migrator must not cap statement time, got %q", q.Get("statement_timeout"))
	}
	if q.Has("idle_in_transaction_session_timeout") {
		t.Fatalf("migrator must not cap idle transaction time, got %q", q.Get("idle_in_transaction_session_timeout"))
	}
	if got := q.Get("lock_timeout"); got != "5000" {
		t.Fatalf("lock_timeout = %q, want 5000", got)
	}
}
