package entity

import (
	"errors"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrAliasNotFound = errors.New("entity: alias not found")
	ErrAliasInUse    = errors.New("entity: alias is already taken")
	ErrAliasInvalid  = errors.New("entity: invalid room alias")
	ErrAliasForeign  = errors.New("entity: alias belongs to another server")
	ErrAliasNotOwned = errors.New("entity: alias belongs to another room")
	ErrAliasUnusable = errors.New("entity: alias cannot be made canonical")
)

const MaxAliasBytes = 255

func MintAlias(localpart, serverName string) (string, error) {
	if localpart == "" || strings.ContainsAny(localpart, ":\x00") {
		return "", ErrAliasInvalid
	}
	if !utf8.ValidString(localpart) {
		return "", ErrAliasInvalid
	}
	for _, r := range localpart {
		if utf16.IsSurrogate(r) {
			return "", ErrAliasInvalid
		}
	}
	if err := ValidateServerName(serverName); err != nil {
		return "", err
	}
	alias := "#" + localpart + ":" + serverName
	if len(alias) > MaxAliasBytes {
		return "", ErrAliasInvalid
	}
	return alias, nil
}

func ParseAlias(alias string) (localpart, serverName string, err error) {
	if !strings.HasPrefix(alias, "#") || len(alias) > MaxAliasBytes {
		return "", "", ErrAliasInvalid
	}
	localpart, serverName, found := strings.Cut(alias[1:], ":")
	if !found {
		return "", "", ErrAliasInvalid
	}
	if _, err := MintAlias(localpart, serverName); err != nil {
		return "", "", err
	}
	return localpart, serverName, nil
}

func ParseLocalAlias(alias, serverName string) (string, error) {
	localpart, domain, err := ParseAlias(alias)
	if err != nil {
		return "", err
	}
	if domain != serverName {
		return "", ErrAliasForeign
	}
	return localpart, nil
}

type RoomAlias struct {
	TenantID  uuid.UUID
	Alias     string
	RoomNID   int64
	RoomID    string
	Creator   string
	CreatedAt time.Time
}

func (RoomAlias) Validate() error { return nil }

type NewRoomAlias struct {
	TenantID uuid.UUID
	Alias    string
	RoomNID  int64
	Creator  string
}

func (n NewRoomAlias) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.RoomNID, validation.Required),
		validation.Field(&n.Creator, validation.Required, validation.Length(1, MaxUserIDBytes)),
	); err != nil {
		return err
	}
	if _, _, err := ParseAlias(n.Alias); err != nil {
		return err
	}
	if !isUserID(n.Creator) {
		return ErrInvalidUsername
	}
	return nil
}
