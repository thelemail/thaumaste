package matrix_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"golang.org/x/crypto/curve25519"

	"github.com/thelemail/thaumaste/internal/entity"
)

type deviceIdentity struct {
	userID   string
	deviceID string
	signing  ed25519.PrivateKey
	ed       string
	curve    string
	device   map[string]any
	oneTime  map[string]any
}

func newIdentity(t *testing.T, seed uint64, userID, deviceID string, oneTimeKeys int) deviceIdentity {
	t.Helper()

	source := rand.NewChaCha8([32]byte{byte(seed), byte(seed >> 8)})
	public, private, err := ed25519.GenerateKey(source)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	identityScalar := make([]byte, curve25519.ScalarSize)
	if _, err := source.Read(identityScalar); err != nil {
		t.Fatalf("read randomness: %v", err)
	}
	identityPoint, err := curve25519.X25519(identityScalar, curve25519.Basepoint)
	if err != nil {
		t.Fatalf("derive curve key: %v", err)
	}

	id := deviceIdentity{
		userID:   userID,
		deviceID: deviceID,
		signing:  private,
		ed:       unpadded(public),
		curve:    unpadded(identityPoint),
	}
	id.device = map[string]any{
		"user_id":    userID,
		"device_id":  deviceID,
		"algorithms": []any{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"},
		"keys": map[string]any{
			"ed25519:" + deviceID:    id.ed,
			"curve25519:" + deviceID: id.curve,
		},
	}
	id.device["signatures"] = id.sign(t, id.device)

	id.oneTime = map[string]any{}
	for i := range oneTimeKeys {
		scalar := make([]byte, curve25519.ScalarSize)
		if _, err := source.Read(scalar); err != nil {
			t.Fatalf("read randomness: %v", err)
		}
		point, err := curve25519.X25519(scalar, curve25519.Basepoint)
		if err != nil {
			t.Fatalf("derive one-time key: %v", err)
		}
		key := map[string]any{"key": unpadded(point)}
		key["signatures"] = id.sign(t, key)
		id.oneTime[fmt.Sprintf("%s:%d-%d", entity.AlgorithmSignedCurve25519, seed, i)] = key
	}
	return id
}

func (d deviceIdentity) sign(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	unsigned := map[string]any{}
	for name, value := range object {
		if name == "signatures" || name == "unsigned" {
			continue
		}
		unsigned[name] = value
	}
	canonical, err := entity.CanonicalJSON(unsigned)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	return map[string]any{
		d.userID: map[string]any{"ed25519:" + d.deviceID: unpadded(ed25519.Sign(d.signing, canonical))},
	}
}

func (d deviceIdentity) oneTimeIDs() []string {
	out := make([]string, 0, len(d.oneTime))
	for id := range d.oneTime {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func unpadded(raw []byte) string { return base64.RawStdEncoding.EncodeToString(raw) }

func verify(t *testing.T, object map[string]json.RawMessage, userID, keyID, signingKey string) {
	t.Helper()

	var signatures map[string]map[string]string
	if err := json.Unmarshal(object["signatures"], &signatures); err != nil {
		t.Fatalf("decode signatures: %v", err)
	}
	signature, ok := signatures[userID][keyID]
	if !ok {
		t.Fatalf("no signature by %s/%s in %v", userID, keyID, signatures)
	}
	raw, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	unsigned := map[string]json.RawMessage{}
	for name, value := range object {
		if name == "signatures" || name == "unsigned" {
			continue
		}
		unsigned[name] = value
	}
	canonical, err := entity.CanonicalJSON(unsigned)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	public, err := base64.RawStdEncoding.DecodeString(signingKey)
	if err != nil {
		t.Fatalf("decode signing key: %v", err)
	}
	if !ed25519.Verify(public, canonical, raw) {
		t.Fatalf("the signature over %s does not verify", canonical)
	}
}

type uploadBody struct {
	OneTimeKeyCounts map[string]int `json:"one_time_key_counts"`
}

type queryBody struct {
	DeviceKeys map[string]map[string]json.RawMessage `json:"device_keys"`
}

type claimBody struct {
	OneTimeKeys map[string]map[string]json.RawMessage `json:"one_time_keys"`
}

func (s *server) upload(t *testing.T, host, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPost, host, "/_matrix/client/v3/keys/upload", token, body)
}

func (s *server) mustUpload(t *testing.T, host, token string, body map[string]any) uploadBody {
	t.Helper()
	rec := s.upload(t, host, token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload keys = %d: %s", rec.Code, rec.Body)
	}
	return decode[uploadBody](t, rec)
}

func (s *server) queryKeys(t *testing.T, host, token string, wanted map[string][]string) queryBody {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/keys/query", token,
		map[string]any{"device_keys": wanted})
	if rec.Code != http.StatusOK {
		t.Fatalf("query keys = %d: %s", rec.Code, rec.Body)
	}
	return decode[queryBody](t, rec)
}

func (s *server) claimKey(t *testing.T, host, token, userID, deviceID string) claimBody {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/keys/claim", token, map[string]any{
		"one_time_keys": map[string]any{userID: map[string]string{deviceID: entity.AlgorithmSignedCurve25519}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim keys = %d: %s", rec.Code, rec.Body)
	}
	return decode[claimBody](t, rec)
}

func TestUploadingKeysAnswersTheExactOneTimeKeyCounts(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 1, alice.UserID, alice.DeviceID, 3)

	body := s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})
	if got := body.OneTimeKeyCounts[entity.AlgorithmSignedCurve25519]; got != 3 {
		t.Fatalf("one_time_key_counts = %v, want 3 signed_curve25519", body.OneTimeKeyCounts)
	}
}

func TestUploadingOnlyDeviceKeysStillAnswersACountsObject(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 2, alice.UserID, alice.DeviceID, 0)

	rec := s.upload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": id.device})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload keys = %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	counts, ok := raw["one_time_key_counts"]
	if !ok {
		t.Fatalf("one_time_key_counts is missing from %s", rec.Body)
	}
	if string(counts) != "{}" {
		t.Fatalf("one_time_key_counts = %s, want an empty object", counts)
	}
}

func TestUploadingTheSameOneTimeKeysAgainDoesNotDuplicateThem(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 3, alice.UserID, alice.DeviceID, 4)

	for attempt := range 3 {
		body := s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
			"device_keys":   id.device,
			"one_time_keys": id.oneTime,
		})
		if got := body.OneTimeKeyCounts[entity.AlgorithmSignedCurve25519]; got != 4 {
			t.Fatalf("upload %d: one_time_key_counts = %v, want 4", attempt, body.OneTimeKeyCounts)
		}
	}
}

func TestOverlappingOneTimeKeyIdentifiersUploadOnce(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 4, alice.UserID, alice.DeviceID, 4)

	ids := id.oneTimeIDs()
	first := map[string]any{ids[0]: id.oneTime[ids[0]], ids[1]: id.oneTime[ids[1]], ids[2]: id.oneTime[ids[2]]}
	second := map[string]any{ids[1]: id.oneTime[ids[1]], ids[2]: id.oneTime[ids[2]], ids[3]: id.oneTime[ids[3]]}

	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": id.device})
	if got := s.mustUpload(t, of.ServerName, alice.AccessToken,
		map[string]any{"one_time_keys": first}).OneTimeKeyCounts[entity.AlgorithmSignedCurve25519]; got != 3 {
		t.Fatalf("after the first three keys the count is %d, want 3", got)
	}
	if got := s.mustUpload(t, of.ServerName, alice.AccessToken,
		map[string]any{"one_time_keys": second}).OneTimeKeyCounts[entity.AlgorithmSignedCurve25519]; got != 4 {
		t.Fatalf("after the overlapping upload the count is %d, want 4", got)
	}
}

func TestReusingAOneTimeKeyIdentifierForDifferentMaterialIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 5, alice.UserID, alice.DeviceID, 1)
	other := newIdentity(t, 6, alice.UserID, alice.DeviceID, 1)

	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	name := id.oneTimeIDs()[0]
	rec := s.upload(t, of.ServerName, alice.AccessToken, map[string]any{
		"one_time_keys": map[string]any{name: other.oneTime[other.oneTimeIDs()[0]]},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("re-using a key id for different material = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_INVALID_PARAM" {
		t.Fatalf("errcode = %s, want M_INVALID_PARAM", code)
	}
}

func TestTheSameOneTimeKeyWithADifferentSignatureIsANoOp(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 7, alice.UserID, alice.DeviceID, 1)

	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	name := id.oneTimeIDs()[0]
	resigned := map[string]any{}
	for field, value := range id.oneTime[name].(map[string]any) {
		resigned[field] = value
	}
	resigned["signatures"] = map[string]any{
		alice.UserID: map[string]any{"ed25519:other": unpadded(make([]byte, ed25519.SignatureSize))},
	}

	body := s.mustUpload(t, of.ServerName, alice.AccessToken,
		map[string]any{"one_time_keys": map[string]any{name: resigned}})
	if got := body.OneTimeKeyCounts[entity.AlgorithmSignedCurve25519]; got != 1 {
		t.Fatalf("one_time_key_counts = %v, want 1", body.OneTimeKeyCounts)
	}
}

func TestDeviceKeysWithoutTheRequiredFieldsAreRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")

	rec := s.upload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys": map[string]any{"user_id": alice.UserID, "device_id": alice.DeviceID},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("device keys without algorithms = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_BAD_JSON" {
		t.Fatalf("errcode = %s, want M_BAD_JSON", code)
	}
}

func TestUnverifiableKeyMaterialAndEmptySignaturesAreAccepted(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")

	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys": map[string]any{
			"user_id":    alice.UserID,
			"device_id":  alice.DeviceID,
			"algorithms": []any{"m.olm.v1.curve25519-aes-sha2"},
			"keys":       map[string]any{"ed25519:" + alice.DeviceID: "not a key at all"},
			"signatures": map[string]any{},
		},
	})

	found := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	if _, ok := found.DeviceKeys[alice.UserID][alice.DeviceID]; !ok {
		t.Fatalf("the device is missing from %v", found.DeviceKeys)
	}
}

func TestDeviceKeysNamingAnotherUserOrDeviceAreRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, of.ServerName, "bob", "correct horse battery staple")

	stolen := newIdentity(t, 8, alice.UserID, alice.DeviceID, 0)
	if rec := s.upload(t, of.ServerName, bob.AccessToken,
		map[string]any{"device_keys": stolen.device}); rec.Code != http.StatusBadRequest {
		t.Fatalf("uploading another user's identity = %d, want 400: %s", rec.Code, rec.Body)
	}

	misdirected := newIdentity(t, 9, bob.UserID, "SOMEOTHERDEVICE", 0)
	if rec := s.upload(t, of.ServerName, bob.AccessToken,
		map[string]any{"device_keys": misdirected.device}); rec.Code != http.StatusBadRequest {
		t.Fatalf("uploading for another device = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestADeviceMayReplaceItsOwnKeys(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")

	first := newIdentity(t, 10, alice.UserID, alice.DeviceID, 0)
	second := newIdentity(t, 11, alice.UserID, alice.DeviceID, 0)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": first.device})
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": second.device})

	found := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(found.DeviceKeys[alice.UserID][alice.DeviceID], &fields); err != nil {
		t.Fatalf("decode device keys: %v", err)
	}
	verify(t, fields, alice.UserID, "ed25519:"+alice.DeviceID, second.ed)
}

func TestMoreOneTimeKeysThanTheCapIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 12, alice.UserID, alice.DeviceID, 9)

	rec := s.upload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("uploading past the cap = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_TOO_LARGE" {
		t.Fatalf("errcode = %s, want M_TOO_LARGE", code)
	}
}

func TestAUserWithNoKeysComesBackAsAnEmptyObject(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, of.ServerName, "bob", "correct horse battery staple")

	rec := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/keys/query", alice.AccessToken,
		map[string]any{"device_keys": map[string][]string{bob.UserID: {}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("query keys = %d: %s", rec.Code, rec.Body)
	}

	var body struct {
		DeviceKeys map[string]json.RawMessage `json:"device_keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	raw, ok := body.DeviceKeys[bob.UserID]
	if !ok {
		t.Fatalf("%s is missing from device_keys in %s, but the spec wants an empty object", bob.UserID, rec.Body)
	}
	if string(raw) != "{}" {
		t.Fatalf("device_keys[%s] = %s, want an empty object", bob.UserID, raw)
	}
}

func TestADeviceIdentifierListSentAsAnObjectIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, of.ServerName, "bob", "correct horse battery staple")

	rec := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/keys/query", alice.AccessToken,
		map[string]any{"device_keys": map[string]any{
			bob.UserID: map[string]bool{"device_id1": true, "device_id2": true},
		}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a device list sent as an object = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_BAD_JSON" {
		t.Fatalf("errcode = %s, want M_BAD_JSON", code)
	}
}

func TestQueryingASpecificDeviceReturnsOnlyThatDevice(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	second := s.loginAs(t, of.ServerName, "alice", "correct horse battery staple")

	first := newIdentity(t, 13, alice.UserID, alice.DeviceID, 0)
	other := newIdentity(t, 14, second.UserID, second.DeviceID, 0)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": first.device})
	s.mustUpload(t, of.ServerName, second.AccessToken, map[string]any{"device_keys": other.device})

	all := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	if len(all.DeviceKeys[alice.UserID]) != 2 {
		t.Fatalf("querying every device returned %d, want 2", len(all.DeviceKeys[alice.UserID]))
	}

	one := s.queryKeys(t, of.ServerName, alice.AccessToken,
		map[string][]string{alice.UserID: {alice.DeviceID}})
	if len(one.DeviceKeys[alice.UserID]) != 1 {
		t.Fatalf("querying one device returned %d, want 1", len(one.DeviceKeys[alice.UserID]))
	}
	if _, ok := one.DeviceKeys[alice.UserID][alice.DeviceID]; !ok {
		t.Fatalf("the requested device is missing from %v", one.DeviceKeys)
	}
}

func TestTheQueryCarriesTheDeviceDisplayName(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 15, alice.UserID, alice.DeviceID, 0)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{"device_keys": id.device})

	renamed := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/devices/"+alice.DeviceID, alice.AccessToken,
		map[string]any{"display_name": "Alice's laptop"})
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename device = %d: %s", renamed.Code, renamed.Body)
	}

	found := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	var fields struct {
		Unsigned struct {
			DisplayName string `json:"device_display_name"`
		} `json:"unsigned"`
	}
	if err := json.Unmarshal(found.DeviceKeys[alice.UserID][alice.DeviceID], &fields); err != nil {
		t.Fatalf("decode device keys: %v", err)
	}
	if fields.Unsigned.DisplayName != "Alice's laptop" {
		t.Fatalf("unsigned.device_display_name = %q, want the name set on the device", fields.Unsigned.DisplayName)
	}
}

func TestACallerSharingNoRoomCannotTellTheTargetFromAUserWithNoKeys(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, of.ServerName, "bob", "correct horse battery staple")
	nobody := "@ghost:alpha.test"

	id := newIdentity(t, 16, bob.UserID, bob.DeviceID, 0)
	s.mustUpload(t, of.ServerName, bob.AccessToken, map[string]any{"device_keys": id.device})

	stranger := s.queryKeys(t, of.ServerName, alice.AccessToken,
		map[string][]string{bob.UserID: {}, nobody: {}})
	if len(stranger.DeviceKeys[bob.UserID]) != 0 {
		t.Fatalf("a caller sharing no room saw %v", stranger.DeviceKeys[bob.UserID])
	}
	if len(stranger.DeviceKeys[nobody]) != 0 {
		t.Fatalf("a user who does not exist answered %v", stranger.DeviceKeys[nobody])
	}

	room := s.seedRoom(t, of, alice)
	joined := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+room.RoomID+"/join", bob.AccessToken, map[string]any{})
	if joined.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", joined.Code, joined.Body)
	}

	shared := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{bob.UserID: {}})
	if _, ok := shared.DeviceKeys[bob.UserID][bob.DeviceID]; !ok {
		t.Fatalf("a caller sharing a room cannot see the device in %v", shared.DeviceKeys)
	}
}

func TestAClaimedKeyIsHandedOutOnceAndThenGone(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 17, alice.UserID, alice.DeviceID, 1)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	name := id.oneTimeIDs()[0]
	first := s.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, alice.DeviceID)
	var handed map[string]json.RawMessage
	if err := json.Unmarshal(first.OneTimeKeys[alice.UserID][alice.DeviceID], &handed); err != nil {
		t.Fatalf("decode the claimed key: %v", err)
	}
	if _, ok := handed[name]; !ok {
		t.Fatalf("the claim returned %v, want %s", handed, name)
	}

	rec := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/keys/claim", alice.AccessToken,
		map[string]any{"one_time_keys": map[string]any{
			alice.UserID: map[string]string{alice.DeviceID: entity.AlgorithmSignedCurve25519},
		}})
	if rec.Code != http.StatusOK {
		t.Fatalf("second claim = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		OneTimeKeys map[string]json.RawMessage `json:"one_time_keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	if _, ok := body.OneTimeKeys[alice.UserID]; ok {
		t.Fatalf("an exhausted device is still listed in %s", rec.Body)
	}
}

func TestOneTimeKeysAreIssuedInUploadOrderRatherThanByIdentifier(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 18, alice.UserID, alice.DeviceID, 2)

	ids := id.oneTimeIDs()
	later, earlier := ids[0], ids[1]
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": map[string]any{earlier: id.oneTime[earlier]},
	})
	s.mustUpload(t, of.ServerName, alice.AccessToken,
		map[string]any{"one_time_keys": map[string]any{later: id.oneTime[later]}})

	for _, want := range []string{earlier, later} {
		claimed := s.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, alice.DeviceID)
		var handed map[string]json.RawMessage
		if err := json.Unmarshal(claimed.OneTimeKeys[alice.UserID][alice.DeviceID], &handed); err != nil {
			t.Fatalf("decode the claimed key: %v", err)
		}
		if _, ok := handed[want]; !ok {
			t.Fatalf("the claim returned %v, want %s issued first", handed, want)
		}
	}
}

func TestExhaustingOneTimeKeysServesTheFallbackKeyAndKeepsServingIt(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 19, alice.UserID, alice.DeviceID, 1)

	ids := id.oneTimeIDs()
	fallbackName := entity.AlgorithmSignedCurve25519 + ":fallback"
	fallback := map[string]any{}
	for field, value := range id.oneTime[ids[0]].(map[string]any) {
		fallback[field] = value
	}
	fallback["fallback"] = true

	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"fallback_keys": map[string]any{fallbackName: fallback},
		"one_time_keys": map[string]any{ids[0]: id.oneTime[ids[0]]},
	})

	unused, err := s.keys.FallbackAlgorithms(t.Context(), of.Scope(), alice.UserID, alice.DeviceID)
	if err != nil {
		t.Fatalf("FallbackAlgorithms: %v", err)
	}
	if len(unused) != 1 || unused[0] != entity.AlgorithmSignedCurve25519 {
		t.Fatalf("unused fallback algorithms = %v, want one signed_curve25519", unused)
	}

	first := s.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, alice.DeviceID)
	if !claimedHolds(t, first, alice, ids[0]) {
		t.Fatalf("the first claim did not hand out the one-time key: %v", first.OneTimeKeys)
	}

	for attempt := range 2 {
		claimed := s.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, alice.DeviceID)
		if !claimedHolds(t, claimed, alice, fallbackName) {
			t.Fatalf("claim %d after exhaustion did not serve the fallback key: %v", attempt, claimed.OneTimeKeys)
		}
	}

	if unused, err = s.keys.FallbackAlgorithms(t.Context(), of.Scope(), alice.UserID, alice.DeviceID); err != nil {
		t.Fatalf("FallbackAlgorithms: %v", err)
	}
	if len(unused) != 0 {
		t.Fatalf("the fallback key is still reported unused: %v", unused)
	}

	s.mustUpload(t, of.ServerName, alice.AccessToken,
		map[string]any{"fallback_keys": map[string]any{fallbackName: fallback}})
	if unused, err = s.keys.FallbackAlgorithms(t.Context(), of.Scope(), alice.UserID, alice.DeviceID); err != nil {
		t.Fatalf("FallbackAlgorithms: %v", err)
	}
	if len(unused) != 0 {
		t.Fatalf("re-uploading the identical fallback key marked it unused again: %v", unused)
	}
}

func claimedHolds(t *testing.T, body claimBody, session sessionBody, keyID string) bool {
	t.Helper()
	raw, ok := body.OneTimeKeys[session.UserID][session.DeviceID]
	if !ok {
		return false
	}
	var handed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &handed); err != nil {
		t.Fatalf("decode the claimed key: %v", err)
	}
	_, ok = handed[keyID]
	return ok
}

func TestTwoFallbackKeysForOneAlgorithmAreRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 20, alice.UserID, alice.DeviceID, 2)

	ids := id.oneTimeIDs()
	rec := s.upload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys": id.device,
		"fallback_keys": map[string]any{
			entity.AlgorithmSignedCurve25519 + ":one": id.oneTime[ids[0]],
			entity.AlgorithmSignedCurve25519 + ":two": id.oneTime[ids[1]],
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("two fallback keys for one algorithm = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestTwoDevicesEstablishAnEncryptedSessionThroughThisServer(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, of.ServerName, "bob", "correct horse battery staple")

	room := s.seedRoom(t, of, bob)
	joined := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+room.RoomID+"/join", alice.AccessToken, map[string]any{})
	if joined.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", joined.Code, joined.Body)
	}

	id := newIdentity(t, 21, alice.UserID, alice.DeviceID, 1)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	found := s.queryKeys(t, of.ServerName, bob.AccessToken, map[string][]string{alice.UserID: {}})
	published, ok := found.DeviceKeys[alice.UserID][alice.DeviceID]
	if !ok {
		t.Fatalf("bob cannot see alice's device in %v", found.DeviceKeys)
	}
	var device map[string]json.RawMessage
	if err := json.Unmarshal(published, &device); err != nil {
		t.Fatalf("decode device keys: %v", err)
	}

	var advertised map[string]string
	if err := json.Unmarshal(device["keys"], &advertised); err != nil {
		t.Fatalf("decode advertised keys: %v", err)
	}
	signingKey := advertised["ed25519:"+alice.DeviceID]
	verify(t, device, alice.UserID, "ed25519:"+alice.DeviceID, signingKey)

	claimed := s.claimKey(t, of.ServerName, bob.AccessToken, alice.UserID, alice.DeviceID)
	var handed map[string]json.RawMessage
	if err := json.Unmarshal(claimed.OneTimeKeys[alice.UserID][alice.DeviceID], &handed); err != nil {
		t.Fatalf("decode the claimed key: %v", err)
	}
	raw, ok := handed[id.oneTimeIDs()[0]]
	if !ok {
		t.Fatalf("the claim returned %v", handed)
	}
	var oneTime map[string]json.RawMessage
	if err := json.Unmarshal(raw, &oneTime); err != nil {
		t.Fatalf("decode the one-time key: %v", err)
	}
	verify(t, oneTime, alice.UserID, "ed25519:"+alice.DeviceID, signingKey)

	var identity, ephemeral string
	if err := json.Unmarshal(oneTime["key"], &ephemeral); err != nil {
		t.Fatalf("decode the one-time key material: %v", err)
	}
	identity = advertised["curve25519:"+alice.DeviceID]
	if identity != id.curve || ephemeral != decodeKey(t, id.oneTime[id.oneTimeIDs()[0]]) {
		t.Fatalf("the key material bob received is not what alice published")
	}
}

func decodeKey(t *testing.T, key any) string {
	t.Helper()
	object, ok := key.(map[string]any)
	if !ok {
		t.Fatalf("%v is not a key object", key)
	}
	material, ok := object["key"].(string)
	if !ok {
		t.Fatalf("%v carries no key", key)
	}
	return material
}

func TestClaimingNeverHandsTheSameKeyToTwoCallers(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")

	const available = 8
	id := newIdentity(t, 22, alice.UserID, alice.DeviceID, available)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	const claimers = available + 4
	var wg sync.WaitGroup
	handed := make(chan string, claimers)
	failures := make(chan string, claimers)

	for range claimers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := s.do(t, http.MethodPost, of.ServerName, "/_matrix/client/v3/keys/claim",
				alice.AccessToken, map[string]any{"one_time_keys": map[string]any{
					alice.UserID: map[string]string{alice.DeviceID: entity.AlgorithmSignedCurve25519},
				}})
			if rec.Code != http.StatusOK {
				failures <- fmt.Sprintf("claim = %d: %s", rec.Code, rec.Body)
				return
			}
			var body claimBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				failures <- fmt.Sprintf("decode %s: %v", rec.Body, err)
				return
			}
			raw, ok := body.OneTimeKeys[alice.UserID][alice.DeviceID]
			if !ok {
				return
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil {
				failures <- fmt.Sprintf("decode the claimed key: %v", err)
				return
			}
			for name := range keys {
				handed <- name
			}
		}()
	}
	wg.Wait()
	close(handed)
	close(failures)

	for failure := range failures {
		t.Fatal(failure)
	}

	seen := map[string]int{}
	total := 0
	for name := range handed {
		seen[name]++
		total++
	}
	if total != available {
		t.Fatalf("%d claimers against %d keys received %d keys", claimers, available, total)
	}
	if len(seen) != available {
		t.Fatalf("%d distinct keys were handed out, want %d: %v", len(seen), available, seen)
	}
	for name, count := range seen {
		if count != 1 {
			t.Fatalf("%s was handed to %d callers", name, count)
		}
	}
}

func TestDeletingADeviceDestroysItsKeys(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	doomed := s.loginAs(t, of.ServerName, "alice", "correct horse battery staple")

	kept := newIdentity(t, 23, alice.UserID, alice.DeviceID, 1)
	going := newIdentity(t, 24, doomed.UserID, doomed.DeviceID, 2)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys": kept.device, "one_time_keys": kept.oneTime,
	})
	s.mustUpload(t, of.ServerName, doomed.AccessToken, map[string]any{
		"device_keys":   going.device,
		"one_time_keys": going.oneTime,
		"fallback_keys": map[string]any{
			entity.AlgorithmSignedCurve25519 + ":fallback": going.oneTime[going.oneTimeIDs()[0]],
		},
	})

	if err := s.users.DeleteDevices(t.Context(), of.Scope(), alice.UserID, []string{doomed.DeviceID}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}

	found := s.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	if _, ok := found.DeviceKeys[alice.UserID][doomed.DeviceID]; ok {
		t.Fatalf("the deleted device is still served in %v", found.DeviceKeys)
	}
	if _, ok := found.DeviceKeys[alice.UserID][alice.DeviceID]; !ok {
		t.Fatalf("the surviving device disappeared from %v", found.DeviceKeys)
	}

	claimed := s.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, doomed.DeviceID)
	if _, ok := claimed.OneTimeKeys[alice.UserID]; ok {
		t.Fatalf("the deleted device still hands out keys: %v", claimed.OneTimeKeys)
	}

	for _, table := range []string{"device_keys", "device_one_time_keys", "device_fallback_keys"} {
		var count int
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE device_id = $1", table)
		if err := s.db.QueryRowContext(t.Context(), query, doomed.DeviceID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s still holds %d rows for the deleted device", table, count)
		}
	}

	if err := s.users.LogoutAll(t.Context(), of.Scope(), alice.UserID); err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}
	for _, table := range []string{"device_keys", "device_one_time_keys", "device_fallback_keys"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(),
			fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s still holds %d rows after logging every device out", table, count)
		}
	}
}

func TestKeysSurviveAServerRestart(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", "correct horse battery staple")
	id := newIdentity(t, 25, alice.UserID, alice.DeviceID, 1)
	s.mustUpload(t, of.ServerName, alice.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	restarted := reopen(t, s)
	found := restarted.queryKeys(t, of.ServerName, alice.AccessToken, map[string][]string{alice.UserID: {}})
	if _, ok := found.DeviceKeys[alice.UserID][alice.DeviceID]; !ok {
		t.Fatalf("the device is missing after a restart: %v", found.DeviceKeys)
	}
	claimed := restarted.claimKey(t, of.ServerName, alice.AccessToken, alice.UserID, alice.DeviceID)
	if !claimedHolds(t, claimed, alice, id.oneTimeIDs()[0]) {
		t.Fatalf("the one-time key did not survive the restart: %v", claimed.OneTimeKeys)
	}
}

func TestKeysOfOneDomainAreNeverVisibleToAnother(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	beta := s.open(t, "beta.test")

	alice := s.register(t, alpha.ServerName, "alice", "correct horse battery staple")
	bob := s.register(t, beta.ServerName, "bob", "correct horse battery staple")

	id := newIdentity(t, 26, bob.UserID, bob.DeviceID, 1)
	s.mustUpload(t, beta.ServerName, bob.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	found := s.queryKeys(t, alpha.ServerName, alice.AccessToken, map[string][]string{bob.UserID: {}})
	if len(found.DeviceKeys[bob.UserID]) != 0 {
		t.Fatalf("a caller of alpha.test saw beta.test keys: %v", found.DeviceKeys)
	}

	claimed := s.claimKey(t, alpha.ServerName, alice.AccessToken, bob.UserID, bob.DeviceID)
	if len(claimed.OneTimeKeys) != 0 {
		t.Fatalf("a caller of alpha.test claimed a beta.test key: %v", claimed.OneTimeKeys)
	}

	own := s.claimKey(t, beta.ServerName, bob.AccessToken, bob.UserID, bob.DeviceID)
	if !claimedHolds(t, own, bob, id.oneTimeIDs()[0]) {
		t.Fatalf("the key was consumed by the cross-domain claim: %v", own.OneTimeKeys)
	}
}

func (s *server) seedForeignDevice(t *testing.T, of entity.Tenant, userID, deviceID, keyJSON string) {
	t.Helper()

	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO users (tenant_id, user_id, localpart) VALUES ($1, $2, $3)`,
		of.ID.String(), userID, "shared"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO devices (tenant_id, user_id, device_id) VALUES ($1, $2, $3)`,
		of.ID.String(), userID, deviceID); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO device_keys (tenant_id, user_id, device_id, key_json) VALUES ($1, $2, $3, $4)`,
		of.ID.String(), userID, deviceID, []byte(keyJSON)); err != nil {
		t.Fatalf("seed device keys: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO device_one_time_keys (tenant_id, user_id, device_id, algorithm, key_id, key_json)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		of.ID.String(), userID, deviceID, entity.AlgorithmSignedCurve25519, deviceID,
		[]byte(`{"key":"`+deviceID+`"}`)); err != nil {
		t.Fatalf("seed one-time key: %v", err)
	}
}

func TestTheKeyStoreScopesEveryReadToOneDomain(t *testing.T) {
	s := newServer(t)
	alpha := s.tenant(t, "alpha.test")
	beta := s.tenant(t, "beta.test")

	const shared = "@shared:example.test"
	s.seedForeignDevice(t, alpha, shared, "ALPHA", `{"device_id":"ALPHA"}`)
	s.seedForeignDevice(t, beta, shared, "BETA", `{"device_id":"BETA"}`)

	found, err := s.keys.Query(t.Context(), alpha.Scope(), shared,
		entity.KeyQuery{Devices: map[string][]string{shared: {}}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found[shared]) != 1 {
		t.Fatalf("querying under alpha.test returned %d devices, want only its own", len(found[shared]))
	}
	if _, ok := found[shared]["ALPHA"]; !ok {
		t.Fatalf("querying under alpha.test returned %v", found[shared])
	}

	claimed, err := s.keys.Claim(t.Context(), alpha.Scope(),
		entity.KeyClaim{Devices: map[string]map[string]string{
			shared: {"BETA": entity.AlgorithmSignedCurve25519},
		}})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("a caller of alpha.test claimed %v from beta.test", claimed)
	}

	var count int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM device_one_time_keys WHERE device_id = 'BETA'`).Scan(&count); err != nil {
		t.Fatalf("count beta keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("beta.test's one-time key was consumed by another domain")
	}
}
