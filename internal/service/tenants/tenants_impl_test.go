package tenants_test

import (
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
)

func newService(t *testing.T) (service.Tenants, *postgres.Client) {
	t.Helper()
	pg := pgtest.Connect(t, "tenants")
	sealer, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	return tenants.New(tenant.New(pg), signingkey.New(pg), sealer, pg, nil, nil), pg
}

func create(t *testing.T, svc service.Tenants, serverName string, hosts ...string) entity.Tenant {
	t.Helper()
	created, err := svc.Create(t.Context(), entity.NewTenant{ServerName: serverName, Hosts: hosts})
	if err != nil {
		t.Fatalf("Create(%s): %v", serverName, err)
	}
	return created
}

func TestADomainAlwaysClaimsItsOwnName(t *testing.T) {
	svc, _ := newService(t)

	created := create(t, svc, "alpha.test", "matrix.alpha.test")

	hosts, err := svc.Hosts(t.Context(), created.Scope())
	if err != nil {
		t.Fatalf("Hosts: %v", err)
	}
	var sawServerName bool
	for _, host := range hosts {
		if host == "alpha.test" {
			sawServerName = true
		}
	}
	if !sawServerName {
		t.Fatalf("hosts = %v, must include the server name itself", hosts)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v, want two", hosts)
	}
}

func TestAHostCannotBeClaimedTwice(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "alpha.test", "shared.test")

	_, err := svc.Create(t.Context(), entity.NewTenant{ServerName: "beta.test", Hosts: []string{"shared.test"}})
	if !errors.Is(err, entity.ErrHostAlreadyClaimed) {
		t.Fatalf("Create error = %v, want ErrHostAlreadyClaimed", err)
	}

	if _, err := svc.ByServerName(t.Context(), "beta.test"); !errors.Is(err, entity.ErrTenantNotFound) {
		t.Fatalf("the rejected tenant was left behind: %v", err)
	}
}

func TestTheSameDomainCannotBeAddedTwice(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "alpha.test")

	_, err := svc.Create(t.Context(), entity.NewTenant{ServerName: "ALPHA.test"})
	if !errors.Is(err, entity.ErrTenantAlreadyExists) {
		t.Fatalf("Create error = %v, want ErrTenantAlreadyExists", err)
	}
}

func TestADomainStartsClosedToRegistrationAndRequiringEncryption(t *testing.T) {
	svc, _ := newService(t)

	created := create(t, svc, "alpha.test")

	if created.RegistrationMode != entity.RegistrationClosed {
		t.Fatalf("registration mode = %q, want closed", created.RegistrationMode)
	}
	if !created.EncryptionRequired {
		t.Fatal("encryption must be required by default")
	}
	if !created.Active() {
		t.Fatalf("state = %q, want active", created.State)
	}
}

func TestADomainIsGivenASigningKeyWhenItIsCreated(t *testing.T) {
	svc, _ := newService(t)

	created := create(t, svc, "alpha.test")

	keys, err := svc.Keys(t.Context(), created.Scope())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || !keys[0].Active() {
		t.Fatalf("keys = %+v, want one active key", keys)
	}
}

func TestThePrivateKeyIsNotReadableFromTheDatabaseAlone(t *testing.T) {
	svc, pg := newService(t)
	created := create(t, svc, "alpha.test")

	var sealed []byte
	err := pg.QueryRowContext(t.Context(),
		`SELECT private_key FROM tenant_signing_keys WHERE tenant_id = $1`, created.ID.String()).Scan(&sealed)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}

	other, err := keyseal.NewWithKey(differentMasterKey())
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	if _, err := other.Open(sealed); !errors.Is(err, keyseal.ErrCiphertext) {
		t.Fatalf("the stored key opened under a different master key: %v", err)
	}
}

func differentMasterKey() []byte {
	key := make([]byte, keyseal.MasterKeySize)
	for i := range key {
		key[i] = 0xab
	}
	return key
}

func TestOnlyOneKeyIsEverActive(t *testing.T) {
	svc, _ := newService(t)
	created := create(t, svc, "alpha.test")

	for range 3 {
		if _, err := svc.RotateKey(t.Context(), created.Scope()); err != nil {
			t.Fatalf("RotateKey: %v", err)
		}
	}

	keys, err := svc.Keys(t.Context(), created.Scope())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 4 {
		t.Fatalf("keys = %d, want 4 retained", len(keys))
	}
	var active int
	for _, k := range keys {
		if k.Active() {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active keys = %d, want 1", active)
	}
}

func TestASuspendedDomainCanStillSign(t *testing.T) {
	svc, _ := newService(t)
	created := create(t, svc, "alpha.test")

	if _, err := svc.Suspend(t.Context(), created.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	if _, err := svc.SignAs(t.Context(), created.Scope(), []byte(`{"a":1}`)); err != nil {
		t.Fatalf("SignAs while suspended: %v", err)
	}
}

func TestServerNamesAreHeldToTheSpecGrammar(t *testing.T) {
	accepted := []string{
		"example.com",
		"example.com:8448",
		"localhost",
		"127.0.0.1",
		"127.0.0.1:8008",
		"[::1]",
		"[::1]:8448",
		"hs1",
	}
	for _, name := range accepted {
		if err := entity.ValidateServerName(name); err != nil {
			t.Fatalf("ValidateServerName(%q) = %v, want accepted", name, err)
		}
	}

	rejected := []string{
		"",
		"under_score.com",
		"has space.com",
		"example.com:",
		"example.com:0",
		"example.com:70000",
		"example.com:notaport",
		"exa!mple.com",
		"@example.com",
	}
	for _, name := range rejected {
		if err := entity.ValidateServerName(name); !errors.Is(err, entity.ErrInvalidServerName) {
			t.Fatalf("ValidateServerName(%q) = %v, want rejected", name, err)
		}
	}
}

func TestADomainNameThatIsNotAServerNameIsRefused(t *testing.T) {
	svc, _ := newService(t)

	if _, err := svc.Create(t.Context(), entity.NewTenant{ServerName: "under_score.test"}); err == nil {
		t.Fatal("Create accepted a name the spec does not allow")
	}
}

func TestResealingLetsTheMasterKeyChangeWithoutLosingAnything(t *testing.T) {
	pg := pgtest.Connect(t, "tenants")
	current, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	next, err := keyseal.NewWithKey(differentMasterKey())
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}

	before := tenants.New(tenant.New(pg), signingkey.New(pg), current, pg, nil, nil)
	alpha := create(t, before, "alpha.test")
	beta := create(t, before, "beta.test")
	if _, err := before.RotateKey(t.Context(), alpha.Scope()); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	signed, err := before.SignAs(t.Context(), alpha.Scope(), []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("SignAs: %v", err)
	}

	n, err := before.ResealKeys(t.Context(), next)
	if err != nil {
		t.Fatalf("ResealKeys: %v", err)
	}
	if n != 3 {
		t.Fatalf("resealed = %d, want 3", n)
	}

	after := tenants.New(tenant.New(pg), signingkey.New(pg), next, pg, nil, nil)
	resigned, err := after.SignAs(t.Context(), alpha.Scope(), []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("SignAs under the new master key: %v", err)
	}
	if string(resigned) != string(signed) {
		t.Fatal("the same document signs differently after resealing, so the key material changed")
	}
	if _, err := after.SignAs(t.Context(), beta.Scope(), []byte(`{"a":1}`)); err != nil {
		t.Fatalf("the other domain cannot sign after resealing: %v", err)
	}

	if _, err := before.SignAs(t.Context(), alpha.Scope(), []byte(`{"a":1}`)); err == nil {
		t.Fatal("the old master key still opens the stored keys")
	}
}

func TestResealingWithTheWrongCurrentKeyChangesNothing(t *testing.T) {
	pg := pgtest.Connect(t, "tenants")
	current, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	right := tenants.New(tenant.New(pg), signingkey.New(pg), current, pg, nil, nil)
	alpha := create(t, right, "alpha.test")

	wrong, err := keyseal.NewWithKey(differentMasterKey())
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	third := make([]byte, keyseal.MasterKeySize)
	for i := range third {
		third[i] = 0x5c
	}
	target, err := keyseal.NewWithKey(third)
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}

	mistaken := tenants.New(tenant.New(pg), signingkey.New(pg), wrong, pg, nil, nil)
	if _, err := mistaken.ResealKeys(t.Context(), target); err == nil {
		t.Fatal("ResealKeys accepted the wrong current master key")
	}

	if _, err := right.SignAs(t.Context(), alpha.Scope(), []byte(`{"a":1}`)); err != nil {
		t.Fatalf("a failed reseal damaged the stored keys: %v", err)
	}
}
