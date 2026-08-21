package entity

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrTenantNotFound      = errors.New("entity: tenant not found")
	ErrTenantAlreadyExists = errors.New("entity: tenant already exists")
	ErrTenantSuspended     = errors.New("entity: tenant is suspended")
	ErrHostAlreadyClaimed  = errors.New("entity: host already belongs to another tenant")
	ErrInvalidServerName   = errors.New("entity: invalid server name")
)

const MaxServerNameLength = 230

type TenantState string

const (
	TenantStateActive    TenantState = "active"
	TenantStateSuspended TenantState = "suspended"
)

func (s TenantState) Valid() bool {
	switch s {
	case TenantStateActive, TenantStateSuspended:
		return true
	default:
		return false
	}
}

type RegistrationMode string

const (
	RegistrationClosed   RegistrationMode = "closed"
	RegistrationInvite   RegistrationMode = "invite"
	RegistrationOpen     RegistrationMode = "open"
	RegistrationExternal RegistrationMode = "external"
)

func (m RegistrationMode) Valid() bool {
	switch m {
	case RegistrationClosed, RegistrationInvite, RegistrationOpen, RegistrationExternal:
		return true
	default:
		return false
	}
}

type Tenant struct {
	ID                 uuid.UUID
	ServerName         string
	State              TenantState
	RegistrationMode   RegistrationMode
	EncryptionRequired bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (Tenant) Validate() error { return nil }

func (t Tenant) Active() bool { return t.State == TenantStateActive }

func (t Tenant) Scope() TenantScope {
	return TenantScope{id: t.ID, serverName: t.ServerName}
}

// TenantScope is what a discovery query is answered against. Every repository method that lists,
// searches or resolves by name takes one, so a new endpoint that forgets to scope its reads does
// not compile. It deliberately does not gate reads of a room, event or media item: those are
// answered by membership, which is what leaves a cross-tenant room possible later.
type TenantScope struct {
	id         uuid.UUID
	serverName string
}

func NewTenantScope(id uuid.UUID, serverName string) TenantScope {
	return TenantScope{id: id, serverName: serverName}
}

func (s TenantScope) ID() uuid.UUID      { return s.id }
func (s TenantScope) ServerName() string { return s.serverName }
func (s TenantScope) Zero() bool         { return s.id == uuid.Nil }

type NewTenant struct {
	ServerName       string
	Hosts            []string
	RegistrationMode RegistrationMode
}

func (n NewTenant) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.ServerName, validation.Required, validation.By(validServerName)),
		validation.Field(&n.Hosts, validation.Each(validation.Required, validation.Length(1, 255))),
	); err != nil {
		return err
	}
	if !n.RegistrationMode.Valid() {
		return validation.Errors{"registrationMode": errors.New("must be closed, invite, open or external")}
	}
	return nil
}

var (
	dnsNamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	ipv4Pattern    = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	ipv6Pattern    = regexp.MustCompile(`^\[[0-9A-Fa-f:.]{2,45}\]$`)
)

// ValidateServerName holds the name to the grammar in the spec's appendices rather than to a
// general hostname rule: an underscore is a legal DNS label but not a legal Matrix server name,
// and accepting one produces user IDs no other server will parse.
func ValidateServerName(name string) error {
	if name == "" || len(name) > MaxServerNameLength {
		return ErrInvalidServerName
	}

	host := name
	if idx := strings.LastIndex(name, ":"); idx != -1 && !strings.HasSuffix(name, "]") {
		port := name[idx+1:]
		n, err := strconv.Atoi(port)
		if err != nil || len(port) > 5 || n < 1 || n > 65535 {
			return ErrInvalidServerName
		}
		host = name[:idx]
	}

	switch {
	case ipv6Pattern.MatchString(host), ipv4Pattern.MatchString(host):
		return nil
	case dnsNamePattern.MatchString(host):
		return nil
	default:
		return ErrInvalidServerName
	}
}

func validServerName(value any) error {
	name, _ := value.(string)
	return ValidateServerName(name)
}

func NormaliseHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}
