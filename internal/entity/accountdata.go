package entity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	MaxAccountDataTypeBytes = 255
	MaxAccountDataBytes     = 65536
	MaxTagBytes             = 255

	AccountDataFullyRead = "m.fully_read"
	AccountDataPushRules = "m.push_rules"
	AccountDataTags      = "m.tag"

	tagsKey  = "tags"
	orderKey = "order"
)

var (
	ErrAccountDataNotFound = errors.New("entity: account data not found")
	ErrAccountDataReserved = errors.New("entity: this type of account data is controlled by the server")
	ErrAccountDataTooLarge = errors.New("entity: account data is too large")
	ErrTagInvalid          = errors.New("entity: invalid tag")
	ErrAccountDataShape    = errors.New("entity: account data must be a JSON object")
	ErrAccountDataForeign  = errors.New("entity: account data belongs to another user")
)

func ReservedAccountData(dataType string) bool {
	return dataType == AccountDataFullyRead || dataType == AccountDataPushRules
}

type AccountData struct {
	RoomID   string
	Type     string
	Content  json.RawMessage
	StreamID int64
}

func (AccountData) Validate() error { return nil }

type NewAccountData struct {
	TenantID uuid.UUID
	UserID   string
	RoomNID  int64
	Type     string
	Content  []byte
}

func (n NewAccountData) Validate() error {
	if len(n.Content) > MaxAccountDataBytes {
		return ErrAccountDataTooLarge
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.Type, validation.Required, validation.Length(1, MaxAccountDataTypeBytes)),
		validation.Field(&n.Content, validation.Required),
	)
}

type RoomTags struct {
	Tags map[string]json.RawMessage
}

func ParseRoomTags(raw []byte) (RoomTags, error) {
	tags := RoomTags{Tags: map[string]json.RawMessage{}}
	if len(raw) == 0 {
		return tags, nil
	}
	var body struct {
		Tags map[string]json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RoomTags{}, ErrTagInvalid
	}
	for name, order := range body.Tags {
		tags.Tags[name] = order
	}
	return tags, nil
}

func (t RoomTags) Set(name string, order json.RawMessage) RoomTags {
	next := RoomTags{Tags: make(map[string]json.RawMessage, len(t.Tags)+1)}
	for existing, value := range t.Tags {
		next.Tags[existing] = value
	}
	if len(order) == 0 {
		order = json.RawMessage("{}")
	}
	next.Tags[name] = order
	return next
}

func (t RoomTags) Delete(name string) RoomTags {
	next := RoomTags{Tags: make(map[string]json.RawMessage, len(t.Tags))}
	for existing, value := range t.Tags {
		if existing == name {
			continue
		}
		next.Tags[existing] = value
	}
	return next
}

func (t RoomTags) JSON() ([]byte, error) {
	tags := t.Tags
	if tags == nil {
		tags = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(map[string]any{tagsKey: tags})
	if err != nil {
		return nil, fmt.Errorf("entity: marshal tags: %w", err)
	}
	return raw, nil
}

func AccountDataObject(raw []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ErrAccountDataShape
	}
	return nil
}

func NormalJSON(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("entity: decode: %w", err)
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal: %w", err)
	}
	return normal, nil
}

func ValidateTag(name string) error {
	if name == "" || len(name) > MaxTagBytes {
		return ErrTagInvalid
	}
	return nil
}
