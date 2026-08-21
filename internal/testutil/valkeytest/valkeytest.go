package valkeytest

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
)

var counter atomic.Int64

func Settings(t *testing.T) config.Valkey {
	t.Helper()
	return config.Valkey{
		Addrs:        []string{env("THAUMASTE_VALKEY_ADDR", "127.0.0.1:6382")},
		Password:     os.Getenv("THAUMASTE_VALKEY_PASSWORD"),
		KeyPrefix:    prefix(t),
		DialTimeout:  2 * time.Second,
		LockValidity: 10 * time.Second,
	}
}

func Require(t *testing.T, cfg config.Valkey) {
	t.Helper()

	client, err := valkey.New(t.Context(), cfg, config.Limits{SendPerUser: 1, SendWindow: time.Second})
	if err == nil {
		defer client.Close()
		err = client.Ping(t.Context())
	}
	if err == nil {
		return
	}
	if os.Getenv("THAUMASTE_TEST_REQUIRE_VALKEY") != "" {
		t.Fatalf("valkey is required but unavailable: %v", err)
	}
	t.Skipf("valkey unavailable: %v", err)
}

func Connect(t *testing.T, limits config.Limits) *valkey.Client {
	t.Helper()

	client, err := valkey.New(t.Context(), Settings(t), limits)
	if err == nil {
		t.Cleanup(client.Close)
		err = client.Ping(t.Context())
	}
	if err != nil {
		if os.Getenv("THAUMASTE_TEST_REQUIRE_VALKEY") != "" {
			t.Fatalf("valkey is required but unavailable: %v", err)
		}
		t.Skipf("valkey unavailable: %v", err)
	}
	return client
}

func prefix(t *testing.T) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, t.Name())
	return fmt.Sprintf("thaumaste_test:%s:%d", clean, counter.Add(1))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
