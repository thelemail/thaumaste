package matrix_test

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

const goodPassword = "complement_meets_min_password_req"

type sessionBody struct {
	UserID       string `json:"user_id"`
	AccessToken  string `json:"access_token"`
	DeviceID     string `json:"device_id"`
	RefreshToken string `json:"refresh_token"`
	ExpiresInMS  int64  `json:"expires_in_ms"`
}

type challengeBody struct {
	Flows     []struct{ Stages []string } `json:"flows"`
	Session   string                      `json:"session"`
	Completed []string                    `json:"completed"`
	ErrCode   string                      `json:"errcode"`
}

func (s *server) do(t *testing.T, method, host, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func (s *server) open(t *testing.T, serverName string) entity.Tenant {
	t.Helper()
	created := s.tenant(t, serverName)
	updated, err := s.tenants.SetRegistration(t.Context(), created.ID, entity.RegistrationOpen)
	if err != nil {
		t.Fatalf("SetRegistration: %v", err)
	}
	return updated
}

// register walks the whole interactive flow the way a client must: a bare post that is challenged,
// then the same post carrying the session it was given.
func (s *server) register(t *testing.T, host, username, password string) sessionBody {
	t.Helper()
	challenged := s.do(t, http.MethodPost, host, "/_matrix/client/v3/register", "",
		map[string]any{"username": username, "password": password})
	if challenged.Code != http.StatusUnauthorized {
		t.Fatalf("first register = %d, want 401: %s", challenged.Code, challenged.Body)
	}
	challenge := decode[challengeBody](t, challenged)

	done := s.do(t, http.MethodPost, host, "/_matrix/client/v3/register", "", map[string]any{
		"username": username,
		"password": password,
		"auth":     map[string]any{"session": challenge.Session, "type": entity.LoginTypeDummy},
	})
	if done.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", done.Code, done.Body)
	}
	return decode[sessionBody](t, done)
}

func (s *server) loginAs(t *testing.T, host, identifier, password string) sessionBody {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/login", "", map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": identifier},
		"password":   password,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	return decode[sessionBody](t, rec)
}

func TestABareRegistrationIsChallengedAndTheChallengeCarriesASession(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/register", "",
		map[string]any{"username": "alice", "password": goodPassword})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := decode[challengeBody](t, rec)
	if body.Session == "" {
		t.Fatal("the challenge carries no session")
	}
	if len(body.Flows) == 0 || len(body.Flows[0].Stages) == 0 {
		t.Fatalf("the challenge offers no stages: %s", rec.Body)
	}
}

// The spec requires the availability verdict before any challenge, and a client that re-posts
// without auth must hear that the name is taken rather than be asked to authenticate again.
func TestAnUnusableUsernameIsRefusedBeforeTheChallenge(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	taken := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/register", "",
		map[string]any{"username": "alice", "password": goodPassword})
	if taken.Code != http.StatusBadRequest {
		t.Fatalf("taken name = %d, want 400", taken.Code)
	}
	if code := errcode(t, taken); code != "M_USER_IN_USE" {
		t.Fatalf("errcode = %q, want M_USER_IN_USE", code)
	}

	for _, bad := range []string{"bad!name", "Bad Name", "", strings.Repeat("a", 300)} {
		rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/register", "",
			map[string]any{"username": bad, "password": goodPassword})
		if bad == "" {
			continue
		}
		if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_INVALID_USERNAME" {
			t.Fatalf("username %q = %d %s", bad, rec.Code, rec.Body)
		}
	}
}

func TestUsernamesAreDowncased(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	session := s.register(t, "alpha.test", "user-UPPER", goodPassword)

	if session.UserID != "@user-upper:alpha.test" {
		t.Fatalf("user_id = %q", session.UserID)
	}
}

func TestRegistrationYieldsAUsableSessionBoundToADevice(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	session := s.register(t, "alpha.test", "alice", goodPassword)
	if session.AccessToken == "" || session.DeviceID == "" {
		t.Fatalf("register returned %+v", session)
	}

	rec := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", session.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami = %d: %s", rec.Code, rec.Body)
	}
	who := decode[struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}](t, rec)
	if who.UserID != session.UserID || who.DeviceID != session.DeviceID {
		t.Fatalf("whoami = %+v, want %+v", who, session)
	}
}

func TestLoginAcceptsALocalpartAFullIdentifierAndAnUppercasedOne(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	registered := s.register(t, "alpha.test", "alice", goodPassword)

	for _, identifier := range []string{"alice", "@alice:alpha.test", "@ALICE:alpha.test"} {
		session := s.loginAs(t, "alpha.test", identifier, goodPassword)
		if session.UserID != registered.UserID {
			t.Fatalf("%q logged in as %q", identifier, session.UserID)
		}
		if session.DeviceID == "" {
			t.Fatalf("%q got no device", identifier)
		}
	}
}

func TestAWrongPasswordAndAnUnknownUserAreBothRefused(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	for _, attempt := range []struct{ user, password string }{
		{"alice", "wrong-password-entirely"},
		{"nobody", goodPassword},
	} {
		rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", map[string]any{
			"type":       entity.LoginTypePassword,
			"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": attempt.user},
			"password":   attempt.password,
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s = %d, want 403", attempt.user, rec.Code)
		}
		if code := errcode(t, rec); code != "M_FORBIDDEN" {
			t.Fatalf("%s errcode = %q", attempt.user, code)
		}
	}
}

func TestATokenIsBoundToOneDeviceAndDiesWithIt(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	first := s.register(t, "alpha.test", "alice", goodPassword)
	second := s.loginAs(t, "alpha.test", "alice", goodPassword)

	if first.DeviceID == second.DeviceID {
		t.Fatal("two logins shared one device")
	}

	err := s.users.DeleteDevices(t.Context(), alpha.Scope(), first.UserID, []string{second.DeviceID})
	if err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}

	dead := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", second.AccessToken, nil)
	if dead.Code != http.StatusUnauthorized {
		t.Fatalf("the deleted device's token = %d, want 401", dead.Code)
	}
	alive := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", first.AccessToken, nil)
	if alive.Code != http.StatusOK {
		t.Fatalf("the other device's token = %d, want 200", alive.Code)
	}
}

func TestLogoutTakesTheDeviceWithIt(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	first := s.register(t, "alpha.test", "alice", goodPassword)
	second := s.loginAs(t, "alpha.test", "alice", goodPassword)

	if rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/logout", second.AccessToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d: %s", rec.Code, rec.Body)
	}

	if rec := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", second.AccessToken, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the logged-out token = %d, want 401", rec.Code)
	}
	devices := decode[struct {
		Devices []struct {
			DeviceID string `json:"device_id"`
		} `json:"devices"`
	}](t, s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/devices", first.AccessToken, nil))
	if len(devices.Devices) != 1 || devices.Devices[0].DeviceID != first.DeviceID {
		t.Fatalf("devices after logout = %+v", devices.Devices)
	}
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	first := s.register(t, "alpha.test", "alice", goodPassword)
	second := s.loginAs(t, "alpha.test", "alice", goodPassword)

	if rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/logout/all", second.AccessToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("logout/all = %d: %s", rec.Code, rec.Body)
	}
	for name, token := range map[string]string{"first": first.AccessToken, "second": second.AccessToken} {
		if rec := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", token, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s token after logout/all = %d, want 401", name, rec.Code)
		}
	}
}

// The old refresh token stays usable until the tokens it produced are, so a client that loses the
// response can present it again rather than being logged out by a dropped packet.
func TestRefreshRotatesAndTheOldTokenIsSpentOnce(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", map[string]any{
		"type":          entity.LoginTypePassword,
		"identifier":    map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":      goodPassword,
		"refresh_token": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	session := decode[sessionBody](t, rec)
	if session.RefreshToken == "" || session.ExpiresInMS == 0 {
		t.Fatalf("login with refresh returned %+v", session)
	}

	refreshed := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/refresh", "",
		map[string]any{"refresh_token": session.RefreshToken})
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", refreshed.Code, refreshed.Body)
	}
	next := decode[sessionBody](t, refreshed)
	if next.AccessToken == "" || next.RefreshToken == "" || next.RefreshToken == session.RefreshToken {
		t.Fatalf("refresh returned %+v", next)
	}

	if rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/refresh", "",
		map[string]any{"refresh_token": session.RefreshToken}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reusing a spent refresh token = %d, want 401", rec.Code)
	}
	if rec := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", next.AccessToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("the refreshed access token = %d, want 200", rec.Code)
	}
}

func TestAccessTokensDoNotExpireUnlessTheClientAsked(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	plain := s.loginBody(t, "alpha.test", false)
	if plain.ExpiresInMS != 0 || plain.RefreshToken != "" {
		t.Fatalf("a client that did not ask for refresh got %+v", plain)
	}
	asked := s.loginBody(t, "alpha.test", true)
	if asked.ExpiresInMS == 0 || asked.RefreshToken == "" {
		t.Fatalf("a client that asked for refresh got %+v", asked)
	}
}

func (s *server) loginBody(t *testing.T, host string, withRefresh bool) sessionBody {
	t.Helper()
	if rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/register", "", map[string]any{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("register challenge = %d", rec.Code)
	}
	name := "user-plain"
	if withRefresh {
		name = "user-refresh"
	}
	s.register(t, host, name, goodPassword)

	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/login", "", map[string]any{
		"type":          entity.LoginTypePassword,
		"identifier":    map[string]any{"type": entity.IdentifierTypeUser, "user": name},
		"password":      goodPassword,
		"refresh_token": withRefresh,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	return decode[sessionBody](t, rec)
}

func TestChangingAPasswordKeepsTheCallingSessionAndEndsTheRest(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	caller := s.register(t, "alpha.test", "alice", goodPassword)
	other := s.loginAs(t, "alpha.test", "alice", goodPassword)

	const replacement = "an-entirely-new-password"
	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/account/password", caller.AccessToken, map[string]any{
		"new_password": replacement,
		"auth": map[string]any{
			"type":       entity.LoginTypePassword,
			"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": caller.UserID},
			"password":   goodPassword,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("change password = %d: %s", rec.Code, rec.Body)
	}

	if got := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", caller.AccessToken, nil); got.Code != http.StatusOK {
		t.Fatalf("the calling session was ended: %d", got.Code)
	}
	if got := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", other.AccessToken, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("another session survived the password change: %d", got.Code)
	}
	s.loginAs(t, "alpha.test", "alice", replacement)
}

func TestChangingAPasswordWithTheWrongOneIsRefused(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	caller := s.register(t, "alpha.test", "alice", goodPassword)

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/account/password", caller.AccessToken, map[string]any{
		"new_password": "an-entirely-new-password",
		"auth": map[string]any{
			"type":       entity.LoginTypePassword,
			"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": caller.UserID},
			"password":   "not-the-current-password",
		},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := decode[challengeBody](t, rec)
	if body.ErrCode != "M_FORBIDDEN" || body.Session == "" {
		t.Fatalf("challenge = %s", rec.Body)
	}
	s.loginAs(t, "alpha.test", "alice", goodPassword)
}

func TestADeactivatedUserCannotLogIn(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	registered := s.register(t, "alpha.test", "alice", goodPassword)

	if err := s.users.Deactivate(t.Context(), alpha.Scope(), registered.UserID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":   goodPassword,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if rec := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/account/whoami", registered.AccessToken, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a deactivated user's token still works: %d", rec.Code)
	}
}

func TestRepeatedFailuresLockTheAccountOut(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	wrong := map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":   "wrong-password-entirely",
	}
	for range 3 {
		if rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", wrong); rec.Code != http.StatusForbidden {
			t.Fatalf("a failed attempt = %d, want 403", rec.Code)
		}
	}

	// Locked out now, and the correct password does not get through either: the lock is on the
	// account, not on the guess.
	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", wrong)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_LIMIT_EXCEEDED" {
		t.Fatalf("errcode = %q", code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After header")
	}
	body := decode[struct {
		RetryAfterMS int64 `json:"retry_after_ms"`
	}](t, rec)
	if body.RetryAfterMS <= 0 {
		t.Fatalf("retry_after_ms = %d", body.RetryAfterMS)
	}

	right := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":   goodPassword,
	})
	if right.Code != http.StatusTooManyRequests {
		t.Fatalf("the correct password got through a lockout: %d", right.Code)
	}
}

func TestASuccessfulLoginClearsTheFailureCount(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	wrong := map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":   "wrong-password-entirely",
	}
	for range 2 {
		s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", wrong)
	}
	s.loginAs(t, "alpha.test", "alice", goodPassword)

	for range 2 {
		if rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", wrong); rec.Code != http.StatusForbidden {
			t.Fatalf("the counter did not reset: %d", rec.Code)
		}
	}
}

func TestAnExternalAssertionLogsInAndProvisions(t *testing.T) {
	s := newAssertedServer(t)
	alpha := s.tenant(t, "alpha.test")
	if _, err := s.tenants.SetRegistration(t.Context(), alpha.ID, entity.RegistrationExternal); err != nil {
		t.Fatalf("SetRegistration: %v", err)
	}

	token := s.assert(t, "@newcomer:alpha.test", "alpha.test", time.Minute)
	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "",
		map[string]any{"type": entity.LoginTypeToken, "token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("assertion login = %d: %s", rec.Code, rec.Body)
	}
	session := decode[sessionBody](t, rec)
	if session.UserID != "@newcomer:alpha.test" || session.DeviceID == "" {
		t.Fatalf("assertion login returned %+v", session)
	}

	// The account now exists, so a second assertion reuses it rather than failing.
	again := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "",
		map[string]any{"type": entity.LoginTypeToken, "token": s.assert(t, "@newcomer:alpha.test", "alpha.test", time.Minute)})
	if again.Code != http.StatusOK {
		t.Fatalf("second assertion login = %d: %s", again.Code, again.Body)
	}
}

func TestAnAssertionThatDoesNotHoldUpIsRefused(t *testing.T) {
	_, wrongKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	// Each refusal is a failed login from the same address, so one shared server would lock itself
	// out partway through and answer 429 to whichever cases came last. Every token is minted
	// against the server that will judge it, so the expiry and domain cases are not quietly passing
	// on a key mismatch instead.
	for _, c := range []struct {
		name  string
		token func(*server) string
	}{
		{"a forged signature", func(*server) string {
			forged, err := signAssertion(wrongKey, "@intruder:alpha.test", "alpha.test", time.Now(), time.Minute)
			if err != nil {
				t.Fatalf("signAssertion: %v", err)
			}
			return forged
		}},
		{"an expired token", func(s *server) string {
			return s.assert(t, "@newcomer:alpha.test", "alpha.test", -time.Minute)
		}},
		{"another domain", func(s *server) string {
			return s.assert(t, "@newcomer:beta.test", "beta.test", time.Minute)
		}},
		{"not a token at all", func(*server) string { return "rubbish" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newAssertedServer(t)
			alpha := s.tenant(t, "alpha.test")
			if _, err := s.tenants.SetRegistration(t.Context(), alpha.ID, entity.RegistrationExternal); err != nil {
				t.Fatalf("SetRegistration: %v", err)
			}

			rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "",
				map[string]any{"type": entity.LoginTypeToken, "token": c.token(s)})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s = %d, want 403: %s", c.name, rec.Code, rec.Body)
			}
		})
	}
}

func TestRegistrationModeDecidesWhoMayCreateAnAccount(t *testing.T) {
	s := newAssertedServer(t)

	for _, mode := range []entity.RegistrationMode{entity.RegistrationClosed, entity.RegistrationExternal} {
		host := string(mode) + ".test"
		created := s.tenant(t, host)
		if _, err := s.tenants.SetRegistration(t.Context(), created.ID, mode); err != nil {
			t.Fatalf("SetRegistration: %v", err)
		}

		challenged := s.do(t, http.MethodPost, host, "/_matrix/client/v3/register", "",
			map[string]any{"username": "alice", "password": goodPassword})
		if challenged.Code != http.StatusUnauthorized {
			t.Fatalf("%s first register = %d", mode, challenged.Code)
		}
		challenge := decode[challengeBody](t, challenged)
		rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/register", "", map[string]any{
			"username": "alice",
			"password": goodPassword,
			"auth":     map[string]any{"session": challenge.Session, "type": entity.LoginTypeDummy},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s register = %d, want 403: %s", mode, rec.Code, rec.Body)
		}

		// An external assertion still provisions where the mode allows it, and nowhere else.
		token := s.assert(t, "@vouched:"+host, host, time.Minute)
		login := s.do(t, http.MethodPost, host, "/_matrix/client/v3/login", "",
			map[string]any{"type": entity.LoginTypeToken, "token": token})
		wanted := http.StatusForbidden
		if mode == entity.RegistrationExternal {
			wanted = http.StatusOK
		}
		if login.Code != wanted {
			t.Fatalf("%s assertion login = %d, want %d: %s", mode, login.Code, wanted, login.Body)
		}
	}
}

func TestGuestRegistrationIsRefusedDeliberately(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/register?kind=guest", "", map[string]any{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if code := errcode(t, rec); code != "M_FORBIDDEN" {
		t.Fatalf("errcode = %q", code)
	}
}

func TestTheSameNameOnTwoDomainsIsTwoPeople(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.open(t, "beta.test")

	alpha := s.register(t, "alpha.test", "alice", goodPassword)
	beta := s.register(t, "beta.test", "alice", goodPassword)

	if alpha.UserID == beta.UserID {
		t.Fatalf("both domains minted %s", alpha.UserID)
	}
	if rec := s.do(t, http.MethodGet, "beta.test", "/_matrix/client/v3/account/whoami", alpha.AccessToken, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("alpha's token worked on beta: %d", rec.Code)
	}

	// And the password of one is not the password of the other, even though the localpart matches.
	rec := s.do(t, http.MethodPost, "beta.test", "/_matrix/client/v3/login", "", map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "@alice:alpha.test"},
		"password":   goodPassword,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("alpha's identifier logged in against beta: %d", rec.Code)
	}
}

func TestAnAvailabilityCheckAnswersBeforeAnyoneRegisters(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	free := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/register/available?username=nobody-yet", "", nil)
	if free.Code != http.StatusOK {
		t.Fatalf("available = %d: %s", free.Code, free.Body)
	}
	if !decode[struct {
		Available bool `json:"available"`
	}](t, free).Available {
		t.Fatal("available was not true")
	}

	s.register(t, "alpha.test", "alice", goodPassword)
	taken := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/register/available?username=alice", "", nil)
	if taken.Code != http.StatusBadRequest || errcode(t, taken) != "M_USER_IN_USE" {
		t.Fatalf("taken = %d %s", taken.Code, taken.Body)
	}
	invalid := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/register/available?username=not,valid", "", nil)
	if invalid.Code != http.StatusBadRequest || errcode(t, invalid) != "M_INVALID_USERNAME" {
		t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body)
	}
}

func TestOnlyTheOwnerMayChangeAProfile(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	alice := s.register(t, "alpha.test", "alice", goodPassword)
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	set := s.do(t, http.MethodPut, "alpha.test", "/_matrix/client/v3/profile/"+alice.UserID+"/displayname",
		alice.AccessToken, map[string]any{"displayname": "Alice"})
	if set.Code != http.StatusOK {
		t.Fatalf("setting my own name = %d: %s", set.Code, set.Body)
	}

	stolen := s.do(t, http.MethodPut, "alpha.test", "/_matrix/client/v3/profile/"+alice.UserID+"/displayname",
		bob.AccessToken, map[string]any{"displayname": "Not Alice"})
	if stolen.Code != http.StatusForbidden {
		t.Fatalf("setting someone else's name = %d, want 403", stolen.Code)
	}

	read := s.do(t, http.MethodGet, "alpha.test", "/_matrix/client/v3/profile/"+alice.UserID, "", nil)
	if decode[struct {
		DisplayName string `json:"displayname"`
	}](t, read).DisplayName != "Alice" {
		t.Fatalf("profile = %s", read.Body)
	}
}

// A credential that is rejected must cost the same whether or not the account exists, or the
// timing alone tells an attacker which names are real.
func TestAnUnknownUserCostsTheSameAsAWrongPassword(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")
	s.register(t, "alpha.test", "alice", goodPassword)

	measure := func(user string) time.Duration {
		start := time.Now()
		s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", map[string]any{
			"type":       entity.LoginTypePassword,
			"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": user},
			"password":   "wrong-password-entirely",
		})
		return time.Since(start)
	}

	known, unknown := measure("alice"), measure("nobody-at-all")
	ratio := float64(known) / float64(unknown)
	if ratio < 0.2 || ratio > 5 {
		t.Fatalf("an unknown user answered in %s against %s for a known one", unknown, known)
	}
}

func TestAMalformedBodyIsNotJSON(t *testing.T) {
	s := newServer(t)
	s.open(t, "alpha.test")

	req := httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/register", strings.NewReader("{\"username\": \"\xff\xfe\"}"))
	req.Host = "alpha.test"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := errcode(t, rec); code != "M_NOT_JSON" {
		t.Fatalf("errcode = %q, want M_NOT_JSON", code)
	}
}

func (s *server) seedIdentity(t *testing.T, of entity.Tenant) {
	t.Helper()
	if _, err := s.tenants.SetRegistration(t.Context(), of.ID, entity.RegistrationOpen); err != nil {
		t.Fatalf("SetRegistration: %v", err)
	}
	s.register(t, of.ServerName, "resident", goodPassword)

	refreshed := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/login", "", map[string]any{
		"type":          entity.LoginTypePassword,
		"identifier":    map[string]any{"type": entity.IdentifierTypeUser, "user": "resident"},
		"password":      goodPassword,
		"refresh_token": true,
	})
	if refreshed.Code != http.StatusOK {
		t.Fatalf("login for a refresh token = %d: %s", refreshed.Code, refreshed.Body)
	}

	failed := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/login", "", map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "resident"},
		"password":   "not the password",
	})
	if failed.Code != http.StatusForbidden {
		t.Fatalf("login with a bad password = %d, want 403: %s", failed.Code, failed.Body)
	}

	// A completed flow deletes its session, so the only way to leave a row behind is a challenge
	// the client never answers.
	abandoned := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/register", "",
		map[string]any{"username": "abandoned", "password": goodPassword})
	if abandoned.Code != http.StatusUnauthorized {
		t.Fatalf("abandoned challenge = %d, want 401: %s", abandoned.Code, abandoned.Body)
	}
}
