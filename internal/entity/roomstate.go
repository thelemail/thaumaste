package entity

import (
	"errors"
	"slices"
)

var (
	ErrEncryptionRequired = errors.New("entity: rooms on this server are always encrypted")
	ErrInvalidRoomState   = errors.New("entity: invalid room state content")
)

const EncryptionAlgorithm = "m.megolm.v1.aes-sha2"

const (
	HistoryVisibilityWorldReadable = "world_readable"
	HistoryVisibilityShared        = "shared"
	HistoryVisibilityInvited       = "invited"
	HistoryVisibilityJoined        = "joined"
)

const (
	GuestAccessCanJoin   = "can_join"
	GuestAccessForbidden = "forbidden"
)

const MembershipTypeRoom = "m.room_membership"

var historyVisibilities = []string{
	HistoryVisibilityWorldReadable,
	HistoryVisibilityShared,
	HistoryVisibilityInvited,
	HistoryVisibilityJoined,
}

var joinRules = []string{
	JoinRulePublic,
	JoinRuleInvite,
	JoinRuleKnock,
	JoinRuleRestricted,
	JoinRuleKnockRestricted,
	JoinRulePrivate,
}

func MandatoryEncryption() map[string]any {
	return map[string]any{"algorithm": EncryptionAlgorithm}
}

func ValidateStateContent(eventType string, content map[string]any) error {
	switch eventType {
	case EventTypeEncryption:
		return validateEncryption(content)
	case EventTypeJoinRules:
		return validateJoinRules(content)
	case EventTypeHistoryVisibility:
		return validateHistoryVisibility(content)
	case EventTypeGuestAccess:
		return validateGuestAccess(content)
	case EventTypeCanonicalAlias:
		return validateCanonicalAlias(content)
	default:
		return nil
	}
}

func validateEncryption(content map[string]any) error {
	algorithm, _ := content["algorithm"].(string)
	if algorithm != EncryptionAlgorithm {
		return ErrEncryptionRequired
	}
	return nil
}

func validateJoinRules(content map[string]any) error {
	rule, _ := content["join_rule"].(string)
	if !slices.Contains(joinRules, rule) {
		return ErrInvalidRoomState
	}
	if rule != JoinRuleRestricted && rule != JoinRuleKnockRestricted {
		return nil
	}
	allow, ok := content["allow"]
	if !ok {
		return nil
	}
	entries, ok := allow.([]any)
	if !ok {
		return ErrInvalidRoomState
	}
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			return ErrInvalidRoomState
		}
		if kind, _ := entry["type"].(string); kind != MembershipTypeRoom {
			return ErrInvalidRoomState
		}
		roomID, _ := entry["room_id"].(string)
		if roomID == "" || len(roomID) > MaxRoomIDBytes || roomID[0] != '!' {
			return ErrInvalidRoomState
		}
	}
	return nil
}

func validateHistoryVisibility(content map[string]any) error {
	visibility, _ := content["history_visibility"].(string)
	if !slices.Contains(historyVisibilities, visibility) {
		return ErrInvalidRoomState
	}
	return nil
}

func validateGuestAccess(content map[string]any) error {
	access, _ := content["guest_access"].(string)
	if access != GuestAccessCanJoin && access != GuestAccessForbidden {
		return ErrInvalidRoomState
	}
	return nil
}

func validateCanonicalAlias(content map[string]any) error {
	for _, alias := range CanonicalAliases(content) {
		if _, _, err := ParseAlias(alias); err != nil {
			return err
		}
	}
	return nil
}

func CanonicalAliases(content map[string]any) []string {
	var out []string
	if alias, ok := content["alias"].(string); ok && alias != "" {
		out = append(out, alias)
	}
	alt, _ := content["alt_aliases"].([]any)
	for _, item := range alt {
		if alias, ok := item.(string); ok && alias != "" && !slices.Contains(out, alias) {
			out = append(out, alias)
		}
	}
	return out
}

func (s StateMap) stringField(eventType, stateKey, field string) string {
	e, ok := s.Get(eventType, stateKey)
	if !ok {
		return ""
	}
	value, _ := e.Content()[field].(string)
	return value
}

func (s StateMap) Name() string { return s.stringField(EventTypeName, "", "name") }

func (s StateMap) Topic() string { return s.stringField(EventTypeTopic, "", "topic") }

func (s StateMap) AvatarURL() string { return s.stringField(EventTypeAvatar, "", "url") }

func (s StateMap) CanonicalAlias() string {
	return s.stringField(EventTypeCanonicalAlias, "", "alias")
}

func (s StateMap) Encryption() string {
	return s.stringField(EventTypeEncryption, "", "algorithm")
}

func (s StateMap) RoomType() string {
	create, ok := s.Create()
	if !ok {
		return ""
	}
	value, _ := create.Content()["type"].(string)
	return value
}

func (s StateMap) HistoryVisibility() string {
	e, ok := s.Get(EventTypeHistoryVisibility, "")
	if !ok {
		return HistoryVisibilityShared
	}
	value, _ := e.Content()["history_visibility"].(string)
	return value
}

func (s StateMap) GuestAccess() string {
	e, ok := s.Get(EventTypeGuestAccess, "")
	if !ok {
		return GuestAccessForbidden
	}
	value, _ := e.Content()["guest_access"].(string)
	return value
}

func (s StateMap) WorldReadable() bool {
	return s.HistoryVisibility() == HistoryVisibilityWorldReadable
}

func (s StateMap) GuestCanJoin() bool {
	return s.GuestAccess() == GuestAccessCanJoin
}

func (s StateMap) MembersWith(membership string) []string {
	var out []string
	for key, e := range s {
		if key.Type != EventTypeMember {
			continue
		}
		if value, _ := e.Content()["membership"].(string); value == membership {
			out = append(out, key.StateKey)
		}
	}
	slices.Sort(out)
	return out
}
