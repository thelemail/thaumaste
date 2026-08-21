package entity

import (
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrUserNotFound      = errors.New("entity: user not found")
	ErrUserInUse         = errors.New("entity: user id is already taken")
	ErrInvalidUsername   = errors.New("entity: invalid username")
	ErrUserDeactivated   = errors.New("entity: user is deactivated")
	ErrRegistrationShut  = errors.New("entity: registration is not permitted")
	ErrProfileNotAllowed = errors.New("entity: profile belongs to another user")
)

const (
	MaxLocalpartBytes  = 255
	MaxDisplayNameSize = 256
	MaxAvatarURLSize   = 1024
)

type User struct {
	TenantID      uuid.UUID
	UserID        string
	Localpart     string
	DisplayName   string
	AvatarURL     string
	DeactivatedAt *time.Time
	CreatedAt     time.Time
}

func (User) Validate() error { return nil }

func (u User) Active() bool { return u.DeactivatedAt == nil }

type NewUser struct {
	TenantID    uuid.UUID
	Localpart   string
	ServerName  string
	DisplayName string
}

func (n NewUser) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.DisplayName, validation.Length(0, MaxDisplayNameSize)),
	); err != nil {
		return err
	}
	if _, err := MintUserID(n.Localpart, n.ServerName); err != nil {
		return err
	}
	return nil
}

func NormaliseLocalpart(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// MintUserID builds an identifier this server is prepared to own. It is deliberately stricter than
// ParseUserID: a name we create must satisfy the current grammar exactly, while a name we merely
// read may predate it.
func MintUserID(localpart, serverName string) (string, error) {
	if localpart == "" || len(localpart) > MaxLocalpartBytes {
		return "", ErrInvalidUsername
	}
	for i := range len(localpart) {
		if !isLocalpartByte(localpart[i]) {
			return "", ErrInvalidUsername
		}
	}
	if err := ValidateServerName(serverName); err != nil {
		return "", err
	}
	userID := "@" + localpart + ":" + serverName
	if len(userID) > MaxUserIDBytes {
		return "", ErrInvalidUsername
	}
	return userID, nil
}

func isLocalpartByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '.', c == '_', c == '=', c == '-', c == '/', c == '+':
		return true
	default:
		return false
	}
}

// ParseUserID accepts the historical character set, which is every non-surrogate codepoint except a
// colon and NUL. Rooms created years ago carry senders that the current grammar forbids, and a
// server that refuses to parse them cannot read those rooms at all.
func ParseUserID(userID string) (localpart, serverName string, err error) {
	if !strings.HasPrefix(userID, "@") || len(userID) > MaxUserIDBytes {
		return "", "", ErrInvalidUsername
	}
	body := userID[1:]
	idx := strings.Index(body, ":")
	if idx < 0 {
		return "", "", ErrInvalidUsername
	}
	localpart, serverName = body[:idx], body[idx+1:]
	if serverName == "" {
		return "", "", ErrInvalidUsername
	}
	for _, r := range localpart {
		if r == 0 || utf16.IsSurrogate(r) {
			return "", "", ErrInvalidUsername
		}
	}
	return localpart, serverName, nil
}

func ResolveUserID(identifier, serverName string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", ErrInvalidUsername
	}
	if !strings.HasPrefix(identifier, "@") {
		return MintUserID(NormaliseLocalpart(identifier), serverName)
	}
	localpart, domain, err := ParseUserID(identifier)
	if err != nil {
		return "", err
	}
	if domain != serverName {
		return "", ErrUserNotFound
	}
	return MintUserID(NormaliseLocalpart(localpart), serverName)
}

type UpdateProfile struct {
	DisplayName *string
	AvatarURL   *string
}

func (u UpdateProfile) Validate() error {
	return validation.ValidateStruct(&u,
		validation.Field(&u.DisplayName, validation.Length(0, MaxDisplayNameSize)),
		validation.Field(&u.AvatarURL, validation.Length(0, MaxAvatarURLSize)),
	)
}
