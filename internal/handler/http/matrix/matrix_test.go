package matrix_test

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/handler/http/matrix"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository/accesstoken"
	"github.com/thelemail/thaumaste/internal/repository/alias"
	"github.com/thelemail/thaumaste/internal/repository/authattempt"
	"github.com/thelemail/thaumaste/internal/repository/credential"
	"github.com/thelemail/thaumaste/internal/repository/device"
	"github.com/thelemail/thaumaste/internal/repository/event"
	"github.com/thelemail/thaumaste/internal/repository/refreshtoken"
	"github.com/thelemail/thaumaste/internal/repository/relation"
	"github.com/thelemail/thaumaste/internal/repository/room"
	"github.com/thelemail/thaumaste/internal/repository/roommember"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/state"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/repository/transaction"
	"github.com/thelemail/thaumaste/internal/repository/uiasession"
	"github.com/thelemail/thaumaste/internal/repository/user"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/events"
	"github.com/thelemail/thaumaste/internal/service/rooms"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/service/tokens"
	"github.com/thelemail/thaumaste/internal/service/users"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
	"github.com/thelemail/thaumaste/internal/testutil/valkeytest"
)

type server struct {
	router  chi.Router
	tenants service.Tenants
	tokens  service.Tokens
	events  service.Events
	users   service.Users
	rooms   service.Rooms

	assertionKey ed25519.PrivateKey
	db           *postgres.Client
}

func newServer(t *testing.T) *server {
	t.Helper()
	return buildServer(t, nil)
}

func newAssertedServer(t *testing.T) *server {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	s := buildServer(t, public)
	s.assertionKey = private
	return s
}

func (s *server) assert(t *testing.T, subject, serverName string, ttl time.Duration) string {
	t.Helper()
	token, err := signAssertion(s.assertionKey, subject, serverName, time.Now(), ttl)
	if err != nil {
		t.Fatalf("signAssertion: %v", err)
	}
	return token
}

func signAssertion(key ed25519.PrivateKey, subject, serverName string, issued time.Time, ttl time.Duration) (string, error) {
	return users.SignAssertion(key, subject, serverName, "", issued, ttl)
}

func buildServer(t *testing.T, assertion ed25519.PublicKey) *server {
	t.Helper()
	return wireServer(t, assertion, pgtest.Connect(t, "tenants"), nil, entity.SendLimits{})
}

func reopen(t *testing.T, s *server) *server {
	t.Helper()
	next := wireServer(t, nil, pgtest.Connect(t), nil, entity.SendLimits{})
	next.assertionKey = s.assertionKey
	return next
}

func newLimitedServer(t *testing.T, limits entity.SendLimits) *server {
	t.Helper()
	return wireServer(t, nil, pgtest.Connect(t, "tenants"),
		valkeytest.Connect(t, config.Limits{SendPerUser: limits.PerUser, SendWindow: limits.Window}), limits)
}

func wireServer(t *testing.T, assertion ed25519.PublicKey, pg *postgres.Client,
	limiter *valkey.Client, limits entity.SendLimits,
) *server {
	t.Helper()

	sealer, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}

	tenantSvc := tenants.New(tenant.New(pg), signingkey.New(pg), sealer, pg, nil, nil)
	tokenSvc := tokens.New(accesstoken.New(pg), nil, nil)

	eventRepo := event.New(pg)
	stream, err := postgres.NewStream(t.Context(), pg, postgres.StreamConfig{
		Name: "events", Instance: "test", Sequence: "events_stream_seq",
	})
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	roomRepo := room.New(pg, eventRepo)
	memberRepo := roommember.New(pg)
	relationRepo := relation.New(pg)
	eventSvc := events.New(roomRepo, eventRepo, state.New(pg), memberRepo, relationRepo, transaction.New(pg),
		tenantSvc, pg, stream, nil, serialiser.New(), "test", nil, nil)

	userSvc := users.New(
		user.New(pg), credential.New(pg), device.New(pg), refreshtoken.New(pg),
		uiasession.New(pg, nil), authattempt.New(pg),
		tokenSvc, tenantSvc, pg, config.Auth{
			AccessTokenTTL: time.Hour, RefreshTokenTTL: time.Hour, SessionTTL: 15 * time.Minute,
			Argon2Time: 1, Argon2MemoryK: 8 * 1024, Argon2Threads: 1,
			MaxFailures: 3, FailureWindow: time.Minute, LockFor: time.Minute,
			AssertionKey: entity.EncodeBase64(assertion), AssertionTTL: 5 * time.Minute,
		}, nil, nil)

	roomSvc := rooms.New(eventSvc, userSvc, roomRepo, alias.New(pg), memberRepo, pg,
		limiter, limits, nil)

	r := chi.NewRouter()
	matrix.New(
		tenantSvc,
		tokenSvc,
		userSvc,
		roomSvc,
		config.Server{PublicScheme: "https"},
		config.Signing{KeyValidity: 24 * time.Hour},
		nil,
	).Mount(r)

	return &server{router: r, tenants: tenantSvc, tokens: tokenSvc, events: eventSvc,
		users: userSvc, rooms: roomSvc, db: pg}
}

func (s *server) tenant(t *testing.T, serverName string, hosts ...string) entity.Tenant {
	t.Helper()
	created, err := s.tenants.Create(t.Context(), entity.NewTenant{
		ServerName:       serverName,
		Hosts:            hosts,
		RegistrationMode: entity.RegistrationClosed,
	})
	if err != nil {
		t.Fatalf("create tenant %s: %v", serverName, err)
	}
	return created
}

func (s *server) token(t *testing.T, of entity.Tenant, userID string) string {
	t.Helper()
	secret, _, err := s.tokens.Mint(t.Context(), of.Scope(), userID, time.Hour)
	if err != nil {
		t.Fatalf("mint token for %s: %v", of.ServerName, err)
	}
	return secret
}

func (s *server) get(t *testing.T, host, path string, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
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

func errcode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		ErrCode string `json:"errcode"`
	}](t, rec).ErrCode
}

type keysBody struct {
	ServerName    string                          `json:"server_name"`
	VerifyKeys    map[string]struct{ Key string } `json:"verify_keys"`
	OldVerifyKeys map[string]struct {
		Key       string `json:"key"`
		ExpiredTS int64  `json:"expired_ts"`
	} `json:"old_verify_keys"`
	ValidUntilTS int64                        `json:"valid_until_ts"`
	Signatures   map[string]map[string]string `json:"signatures"`
}

func TestVersionsReportsTheSupportedSpecVersions(t *testing.T) {
	s := newServer(t)

	rec := s.get(t, "", "/_matrix/client/versions", "")

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

	if len(body.Versions) == 0 || body.Versions[0] != "v1.16" {
		t.Fatalf("versions = %v, want v1.16", body.Versions)
	}
	if body.UnstableFeatures == nil {
		t.Fatal("unstable_features must be present, even when empty")
	}
}

func TestTwoDomainsCoexistWithDistinctKeys(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test")
	s.tenant(t, "beta.test")

	alpha := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))
	beta := decode[keysBody](t, s.get(t, "beta.test", "/_matrix/key/v2/server", ""))

	if alpha.ServerName != "alpha.test" || beta.ServerName != "beta.test" {
		t.Fatalf("server names = %q and %q", alpha.ServerName, beta.ServerName)
	}
	for id, key := range alpha.VerifyKeys {
		if other, ok := beta.VerifyKeys[id]; ok && other.Key == key.Key {
			t.Fatalf("both domains publish the same key %s", id)
		}
	}
}

func TestEachDomainSignsItsOwnKeysAndNotTheOthers(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test")
	s.tenant(t, "beta.test")

	raw := s.get(t, "alpha.test", "/_matrix/key/v2/server", "").Body.Bytes()
	alpha := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))
	beta := decode[keysBody](t, s.get(t, "beta.test", "/_matrix/key/v2/server", ""))

	for id, key := range alpha.VerifyKeys {
		public, err := entity.DecodeBase64(key.Key)
		if err != nil {
			t.Fatalf("decode key: %v", err)
		}
		if err := entity.VerifyJSON(raw, "alpha.test", entity.KeyID(id), ed25519.PublicKey(public)); err != nil {
			t.Fatalf("alpha does not verify under its own key: %v", err)
		}
	}
	for id, key := range beta.VerifyKeys {
		public, err := entity.DecodeBase64(key.Key)
		if err != nil {
			t.Fatalf("decode key: %v", err)
		}
		if err := entity.VerifyJSON(raw, "beta.test", entity.KeyID(id), ed25519.PublicKey(public)); err == nil {
			t.Fatal("alpha's keys verified under beta's key")
		}
	}
}

func TestValidUntilIsInTheFutureAndInsideTheWeekTheSpecAllows(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test")

	body := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))

	valid := time.UnixMilli(body.ValidUntilTS)
	if !valid.After(time.Now().Add(time.Hour)) {
		t.Fatalf("valid_until_ts = %s, must be more than an hour ahead", valid)
	}
	if valid.After(time.Now().Add(7 * 24 * time.Hour)) {
		t.Fatalf("valid_until_ts = %s, must be inside seven days", valid)
	}
}

func TestARotatedKeyIsRetainedAndStillPublished(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")

	before := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))
	var oldID string
	for id := range before.VerifyKeys {
		oldID = id
	}

	if _, err := s.tenants.RotateKey(t.Context(), alpha.Scope()); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	after := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))

	if _, ok := after.VerifyKeys[oldID]; ok {
		t.Fatalf("the rotated key %s is still advertised as current", oldID)
	}
	retired, ok := after.OldVerifyKeys[oldID]
	if !ok {
		t.Fatalf("the rotated key %s was dropped instead of retained", oldID)
	}
	if retired.ExpiredTS == 0 {
		t.Fatal("the retired key carries no expired_ts")
	}
	if retired.Key != before.VerifyKeys[oldID].Key {
		t.Fatal("the retired key material changed")
	}
	if len(after.VerifyKeys) != 1 {
		t.Fatalf("current keys after rotation = %d, want 1", len(after.VerifyKeys))
	}
}

func TestOneDomainCanAnswerOnSeveralHosts(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test", "matrix.alpha.test")

	direct := decode[keysBody](t, s.get(t, "alpha.test", "/_matrix/key/v2/server", ""))
	delegated := decode[keysBody](t, s.get(t, "matrix.alpha.test", "/_matrix/key/v2/server", ""))

	if delegated.ServerName != "alpha.test" {
		t.Fatalf("server name on the delegated host = %q, want alpha.test", delegated.ServerName)
	}
	if len(direct.VerifyKeys) != len(delegated.VerifyKeys) {
		t.Fatal("the two hosts publish different keys")
	}

	wellKnown := decode[struct {
		Homeserver struct {
			BaseURL string `json:"base_url"`
		} `json:"m.homeserver"`
	}](t, s.get(t, "matrix.alpha.test", "/.well-known/matrix/client", ""))

	if wellKnown.Homeserver.BaseURL != "https://matrix.alpha.test" {
		t.Fatalf("base_url = %q", wellKnown.Homeserver.BaseURL)
	}
}

func TestAnUnknownHostBelongsToNoDomain(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test")

	for _, path := range []string{
		"/_matrix/key/v2/server",
		"/.well-known/matrix/client",
		"/_matrix/client/v3/capabilities",
	} {
		rec := s.get(t, "nobody.test", path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s on an unknown host = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
		if code := errcode(t, rec); code != "M_NOT_FOUND" {
			t.Fatalf("%s on an unknown host errcode = %q", path, code)
		}
	}
}

func TestATokenIsRefusedOnAnotherDomainsHost(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")
	s.tenant(t, "beta.test")

	token := s.token(t, alpha, "@someone:alpha.test")

	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", token); rec.Code != http.StatusOK {
		t.Fatalf("on its own host = %d, want %d", rec.Code, http.StatusOK)
	}

	rec := s.get(t, "beta.test", "/_matrix/client/v3/capabilities", token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("on another domain's host = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if code := errcode(t, rec); code != "M_UNKNOWN_TOKEN" {
		t.Fatalf("errcode = %q, want M_UNKNOWN_TOKEN", code)
	}
}

func TestACrossDomainTokenIsIndistinguishableFromAnInventedOne(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")
	s.tenant(t, "beta.test")

	foreign := s.get(t, "beta.test", "/_matrix/client/v3/capabilities", s.token(t, alpha, "@someone:alpha.test"))
	invented := s.get(t, "beta.test", "/_matrix/client/v3/capabilities", "not-a-real-token")

	if foreign.Code != invented.Code {
		t.Fatalf("status %d for a foreign token, %d for an invented one", foreign.Code, invented.Code)
	}
	if foreign.Body.String() != invented.Body.String() {
		t.Fatalf("a foreign token is distinguishable: %s vs %s", foreign.Body, invented.Body)
	}
}

func TestAMissingTokenIsReportedSeparatelyFromABadOne(t *testing.T) {
	s := newServer(t)
	s.tenant(t, "alpha.test")

	missing := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", "")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}
	if code := errcode(t, missing); code != "M_MISSING_TOKEN" {
		t.Fatalf("errcode = %q, want M_MISSING_TOKEN", code)
	}
}

func TestARevokedTokenStopsWorking(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")

	secret, minted, err := s.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", secret); rec.Code != http.StatusOK {
		t.Fatalf("before revocation = %d", rec.Code)
	}

	if err := s.tokens.Revoke(t.Context(), minted.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rec := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", secret)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after revocation = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSuspendingADomainStopsTheClientApiButKeepsItsKeysPublished(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")
	token := s.token(t, alpha, "@someone:alpha.test")

	if _, err := s.tenants.Suspend(t.Context(), alpha.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	client := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", token)
	if client.Code != http.StatusForbidden {
		t.Fatalf("client api while suspended = %d, want %d", client.Code, http.StatusForbidden)
	}
	if code := errcode(t, client); code != "M_FORBIDDEN" {
		t.Fatalf("errcode = %q, want M_FORBIDDEN", code)
	}

	keys := s.get(t, "alpha.test", "/_matrix/key/v2/server", "")
	if keys.Code != http.StatusOK {
		t.Fatalf("key endpoint while suspended = %d, want %d", keys.Code, http.StatusOK)
	}

	if _, err := s.tenants.Resume(t.Context(), alpha.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", token); rec.Code != http.StatusOK {
		t.Fatalf("client api after resuming = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWhoamiNamesTheCallerTheTokenWasMintedFor(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")
	beta := s.tenant(t, "beta.test")

	rec := s.get(t, "alpha.test", "/_matrix/client/v3/account/whoami", s.token(t, alpha, "@someone:alpha.test"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := decode[struct {
		UserID string `json:"user_id"`
	}](t, rec)
	if body.UserID != "@someone:alpha.test" {
		t.Fatalf("user_id = %q", body.UserID)
	}

	foreign := s.get(t, "alpha.test", "/_matrix/client/v3/account/whoami", s.token(t, beta, "@someone:beta.test"))
	if foreign.Code != http.StatusUnauthorized {
		t.Fatalf("a token from the other domain = %d, want %d", foreign.Code, http.StatusUnauthorized)
	}
}

func TestUnknownEndpointIsNotRecognised(t *testing.T) {
	s := newServer(t)

	rec := s.get(t, "", "/_matrix/client/v3/nothing_here", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if code := errcode(t, rec); code != "M_UNRECOGNIZED" {
		t.Fatalf("errcode = %q, want M_UNRECOGNIZED", code)
	}
}

func TestWrongMethodOnAKnownEndpointIsNotAllowed(t *testing.T) {
	s := newServer(t)

	req := httptest.NewRequest(http.MethodPut, "/_matrix/client/versions", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if code := errcode(t, rec); code != "M_UNRECOGNIZED" {
		t.Fatalf("errcode = %q, want M_UNRECOGNIZED", code)
	}
}

func TestCapabilitiesReportsRoomVersionTwelveAsDefault(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")

	rec := s.get(t, "alpha.test", "/_matrix/client/v3/capabilities", s.token(t, alpha, "@someone:alpha.test"))
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

func (s *server) seedRoom(t *testing.T, of entity.Tenant, resident sessionBody) entity.Room {
	t.Helper()

	created, err := s.rooms.Create(t.Context(), of.Scope(), entity.NewRoomRequest{
		Creator:        resident.UserID,
		Version:        entity.DefaultRoomVersion,
		Visibility:     entity.VisibilityPublic,
		AliasLocalpart: "seeded",
		Name:           "seeded",
		Topic:          "a seeded room",
	})
	if err != nil {
		t.Fatalf("Create room: %v", err)
	}

	sent := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(created.RoomID)+"/send/m.room.message/seeded",
		resident.AccessToken, map[string]any{"msgtype": "m.text", "body": "hello"})
	if sent.Code != http.StatusOK {
		t.Fatalf("seed a message = %d: %s", sent.Code, sent.Body)
	}

	var body struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode seeded message: %v", err)
	}
	reacted := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(created.RoomID)+"/send/m.reaction/seeded-reaction",
		resident.AccessToken, map[string]any{"m.relates_to": map[string]any{
			"rel_type": entity.RelAnnotation, "event_id": body.EventID, "key": "\U0001F44D",
		}})
	if reacted.Code != http.StatusOK {
		t.Fatalf("seed a reaction = %d: %s", reacted.Code, reacted.Body)
	}
	return created
}

func (s *server) raw(t *testing.T, method, host, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}
