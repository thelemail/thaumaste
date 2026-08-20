package config_test

import (
	"testing"

	"github.com/thelemail/thaumaste/internal/config"
)

func TestDSNCarriesCredentialsHostAndSSLMode(t *testing.T) {
	p := config.Postgres{
		Host:     "db.internal",
		Port:     5435,
		User:     "thaumaste",
		Password: "s3cret",
		Database: "thaumaste",
		SSLMode:  "require",
	}
	want := "postgres://thaumaste:s3cret@db.internal:5435/thaumaste?sslmode=require"
	if got := p.DSN(); got != want {
		t.Fatalf("DSN = %q, want %q", got, want)
	}
}

func TestDSNEscapesPasswordPunctuation(t *testing.T) {
	p := config.Postgres{Host: "127.0.0.1", Port: 5435, User: "u", Password: "p@ss/word", Database: "d", SSLMode: "disable"}
	want := "postgres://u:p%40ss%2Fword@127.0.0.1:5435/d?sslmode=disable"
	if got := p.DSN(); got != want {
		t.Fatalf("DSN = %q, want %q", got, want)
	}
}
