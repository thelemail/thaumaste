package entity

import (
	"errors"
	"maps"
	"slices"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

var (
	ErrInvalidPreset     = errors.New("entity: unknown room preset")
	ErrInvalidVisibility = errors.New("entity: unknown room visibility")
)

const (
	PresetPrivateChat        = "private_chat"
	PresetTrustedPrivateChat = "trusted_private_chat"
	PresetPublicChat         = "public_chat"
)

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

const (
	creatorPower         = 100
	trustedInviteePower  = 100
	protectedStatePower  = 100
	maxInitialStateItems = 128
	maxInviteesAtCreate  = 128
)

var presets = []string{PresetPrivateChat, PresetTrustedPrivateChat, PresetPublicChat}

type InitialState struct {
	Type     string
	StateKey string
	Content  map[string]any
}

type NewRoomRequest struct {
	Creator                   string
	CreatorDisplayName        string
	CreatorAvatarURL          string
	ServerName                string
	Version                   RoomVersionID
	Visibility                string
	Preset                    string
	AliasLocalpart            string
	Name                      string
	Topic                     string
	Invite                    []string
	CreationContent           map[string]any
	InitialState              []InitialState
	PowerLevelContentOverride map[string]any
}

func (n NewRoomRequest) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.Creator, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.InitialState, validation.Length(0, maxInitialStateItems)),
		validation.Field(&n.Invite, validation.Length(0, maxInviteesAtCreate)),
	); err != nil {
		return err
	}
	if !isUserID(n.Creator) {
		return ErrInvalidUsername
	}
	version, err := LookupRoomVersion(n.Version)
	if err != nil {
		return err
	}
	if n.Visibility != VisibilityPublic && n.Visibility != VisibilityPrivate {
		return ErrInvalidVisibility
	}
	if !slices.Contains(presets, n.Preset) {
		return ErrInvalidPreset
	}
	if n.AliasLocalpart != "" {
		if _, err := MintAlias(n.AliasLocalpart, n.ServerName); err != nil {
			return err
		}
	}
	for _, invitee := range n.Invite {
		if !isUserID(invitee) {
			return ErrInvalidUsername
		}
	}
	for _, in := range n.InitialState {
		if in.Type == "" || len(in.Type) > MaxEventTypeSize || len(in.StateKey) > MaxStateKeyBytes {
			return ErrEventMalformed
		}
		if err := ValidateStateContent(in.Type, in.Content); err != nil {
			return err
		}
	}
	if err := ValidateStateContent(EventTypeEncryption, n.Encryption()); err != nil {
		return err
	}
	if _, err := ParsePowerLevels(n.PowerLevelContent(version), version, []string{n.Creator}); err != nil {
		return err
	}
	return nil
}

func DefaultPreset(visibility string) string {
	if visibility == VisibilityPublic {
		return PresetPublicChat
	}
	return PresetPrivateChat
}

func (n NewRoomRequest) JoinRule() string {
	if n.Preset == PresetPublicChat {
		return JoinRulePublic
	}
	return JoinRuleInvite
}

func (n NewRoomRequest) HistoryVisibility() string { return HistoryVisibilityShared }

func TopicContent(topic string) map[string]any {
	return map[string]any{
		"topic": topic,
		"m.topic": map[string]any{
			"m.text": []any{map[string]any{"body": topic}},
		},
	}
}

func (n NewRoomRequest) Alias() (string, bool) {
	if n.AliasLocalpart == "" {
		return "", false
	}
	alias, err := MintAlias(n.AliasLocalpart, n.ServerName)
	return alias, err == nil
}

func (n NewRoomRequest) InitialStateFor(eventType, stateKey string) (map[string]any, bool) {
	for _, in := range n.InitialState {
		if in.Type == eventType && in.StateKey == stateKey {
			return in.Content, true
		}
	}
	return nil, false
}

func (n NewRoomRequest) Encryption() map[string]any {
	content := MandatoryEncryption()
	supplied, ok := n.InitialStateFor(EventTypeEncryption, "")
	if !ok {
		return content
	}
	maps.Copy(content, supplied)
	return content
}

func (n NewRoomRequest) CreateContent(version RoomVersion) map[string]any {
	content := make(map[string]any, len(n.CreationContent)+1)
	for key, value := range n.CreationContent {
		if key == "room_version" || key == "creator" {
			continue
		}
		content[key] = value
	}
	content["room_version"] = string(version.ID)
	return content
}

func (n NewRoomRequest) PowerLevelContent(version RoomVersion) map[string]any {
	users := map[string]any{}
	if !version.CreatorsOutrankPowerLevels {
		users[n.Creator] = int64(creatorPower)
	}
	if n.Preset == PresetTrustedPrivateChat {
		for _, invitee := range n.Invite {
			if invitee != n.Creator {
				users[invitee] = int64(trustedInviteePower)
			}
		}
	}

	content := map[string]any{
		"ban":            int64(defaultBan),
		"kick":           int64(defaultKick),
		"redact":         int64(defaultRedact),
		"invite":         int64(defaultInvite),
		"state_default":  int64(defaultStateDefault),
		"events_default": int64(defaultEventsDefault),
		"users_default":  int64(defaultUsersDefault),
		"users":          users,
		"events": map[string]any{
			EventTypeName:              int64(defaultStateDefault),
			EventTypeAvatar:            int64(defaultStateDefault),
			EventTypeCanonicalAlias:    int64(defaultStateDefault),
			EventTypePowerLevels:       int64(protectedStatePower),
			EventTypeHistoryVisibility: int64(protectedStatePower),
			EventTypeTombstone:         int64(protectedStatePower),
			EventTypeServerACL:         int64(protectedStatePower),
			EventTypeEncryption:        int64(protectedStatePower),
		},
	}
	maps.Copy(content, n.PowerLevelContentOverride)
	return content
}
