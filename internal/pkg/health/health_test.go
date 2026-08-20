package health_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thelemail/thaumaste/internal/pkg/health"
)

func probe(t *testing.T, state *health.State, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	health.Handler(state).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestLivenessStaysHealthyWhileDraining(t *testing.T) {
	state := health.NewState()
	state.StartDraining()

	if rec := probe(t, state, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz while draining = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadinessFailsWhileDraining(t *testing.T) {
	state := health.NewState()

	if rec := probe(t, state, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d, want %d", rec.Code, http.StatusOK)
	}

	state.StartDraining()

	rec := probe(t, state, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "draining" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "draining")
	}
}
