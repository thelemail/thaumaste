package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thelemail/thaumaste/internal/pkg/health"
)

func probe(t *testing.T, state *health.State, ready health.ReadyFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	health.Handler(state, ready).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestLivenessStaysHealthyWhileDraining(t *testing.T) {
	state := health.NewState()
	state.StartDraining()

	if rec := probe(t, state, nil, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz while draining = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadinessFailsWhileDraining(t *testing.T) {
	state := health.NewState()

	if rec := probe(t, state, nil, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d, want %d", rec.Code, http.StatusOK)
	}

	state.StartDraining()

	rec := probe(t, state, nil, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "draining" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "draining")
	}
}

func TestReadinessFailsWhenTheDependencyIsUnreachable(t *testing.T) {
	unreachable := func(context.Context) error { return errors.New("no connection") }

	rec := probe(t, health.NewState(), unreachable, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "not ready" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "not ready")
	}
}

func TestLivenessIgnoresTheDependency(t *testing.T) {
	unreachable := func(context.Context) error { return errors.New("no connection") }

	if rec := probe(t, health.NewState(), unreachable, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want %d", rec.Code, http.StatusOK)
	}
}
