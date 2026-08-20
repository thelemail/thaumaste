package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/handler/http/matrix"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	matrix.New().Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func TestVersionsReportsTheSupportedSpecVersions(t *testing.T) {
	rec := get(t, "/_matrix/client/versions")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q, want application/json", ct)
	}

	body := decode[struct {
		Versions         []string        `json:"versions"`
		UnstableFeatures map[string]bool `json:"unstable_features"`
	}](t, rec)

	if len(body.Versions) == 0 {
		t.Fatal("versions must not be empty")
	}
	if body.Versions[0] != "v1.16" {
		t.Fatalf("versions = %v, want v1.16", body.Versions)
	}
	if body.UnstableFeatures == nil {
		t.Fatal("unstable_features must be present, even when empty")
	}
}

func TestCapabilitiesReportsRoomVersionTwelveAsDefault(t *testing.T) {
	rec := get(t, "/_matrix/client/v3/capabilities")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decode[struct {
		Capabilities struct {
			RoomVersions struct {
				Default   string            `json:"default"`
				Available map[string]string `json:"available"`
			} `json:"m.room_versions"`
		} `json:"capabilities"`
	}](t, rec)

	if body.Capabilities.RoomVersions.Default != "12" {
		t.Fatalf("default room version = %q, want 12", body.Capabilities.RoomVersions.Default)
	}
	if got := body.Capabilities.RoomVersions.Available["12"]; got != "stable" {
		t.Fatalf("room version 12 = %q, want stable", got)
	}
}

func TestUnknownEndpointIsNotRecognised(t *testing.T) {
	rec := get(t, "/_matrix/client/v3/nothing_here")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	body := decode[struct {
		ErrCode string `json:"errcode"`
	}](t, rec)

	if body.ErrCode != "M_UNRECOGNIZED" {
		t.Fatalf("errcode = %q, want M_UNRECOGNIZED", body.ErrCode)
	}
}
