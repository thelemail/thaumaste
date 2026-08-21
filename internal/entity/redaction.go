package entity

import (
	"errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

var (
	ErrCannotRedact       = errors.New("entity: not permitted to redact that event")
	ErrRedactionProtected = errors.New("entity: that event may not be redacted")
)

type NewRedaction struct {
	RoomID   string
	EventID  string
	Sender   string
	DeviceID string
	TxnID    string
	Reason   string
}

func (n NewRedaction) Validate() error {
	if n.TxnID == "" {
		return ErrTransactionMissing
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&n.EventID, validation.Required, validation.Length(1, MaxEventIDBytes)),
		validation.Field(&n.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.TxnID, validation.Length(1, MaxTransactionIDBytes)),
	)
}

func (n NewRedaction) Event() NewEvent {
	content := map[string]any{redactsKey: n.EventID}
	if n.Reason != "" {
		content["reason"] = n.Reason
	}
	return NewEvent{
		RoomID:  n.RoomID,
		Type:    EventTypeRedaction,
		Sender:  n.Sender,
		Content: content,
		Txn: &TransactionRef{
			DeviceID: n.DeviceID,
			Endpoint: EndpointRedact,
			TxnID:    n.TxnID,
		},
	}
}

func Redactable(e Event) bool {
	return e.Type() != EventTypeEncryption
}

func RedactionTarget(e Event) (string, bool) {
	if e.Type() != EventTypeRedaction {
		return "", false
	}
	target, ok := e.Content()[redactsKey].(string)
	return target, ok
}

type redactionRules struct {
	topLevel   []string
	content    map[string][]string
	allContent []string
}

var redactionAlgorithms = map[RedactionAlgorithm]redactionRules{
	RedactionAlgorithmV11: {
		topLevel: []string{
			"event_id",
			"type",
			"room_id",
			"sender",
			"state_key",
			"content",
			"hashes",
			"signatures",
			"depth",
			"prev_events",
			"auth_events",
			"origin_server_ts",
		},
		content: map[string][]string{
			EventTypeMember:            {"membership", "join_authorised_via_users_server"},
			EventTypeJoinRules:         {"join_rule", "allow"},
			EventTypeHistoryVisibility: {"history_visibility"},
			EventTypeRedaction:         {redactsKey},
			EventTypePowerLevels: {
				"ban", "events", "events_default", "invite",
				"kick", "redact", "state_default", "users", "users_default",
			},
		},
		allContent: []string{EventTypeCreate},
	},
}

func rulesFor(version RoomVersion) redactionRules {
	if rules, ok := redactionAlgorithms[version.RedactionAlgorithm]; ok {
		return rules
	}
	return redactionAlgorithms[RedactionAlgorithmV11]
}

func Redact(event map[string]any, version RoomVersion) map[string]any {
	rules := rulesFor(version)

	out := make(map[string]any, len(rules.topLevel))
	for _, key := range rules.topLevel {
		if value, ok := event[key]; ok {
			out[key] = value
		}
	}

	eventType, _ := event["type"].(string)
	content, _ := event["content"].(map[string]any)
	out["content"] = rules.redactContent(eventType, content)
	return out
}

func RedactedJSON(e Event, version RoomVersion) ([]byte, error) {
	return CanonicalJSON(Redact(e.Fields(), version))
}

func (r redactionRules) redactContent(eventType string, content map[string]any) map[string]any {
	if content == nil {
		return map[string]any{}
	}
	for _, keep := range r.allContent {
		if eventType == keep {
			return content
		}
	}

	kept := map[string]any{}
	for _, key := range r.content[eventType] {
		if value, ok := content[key]; ok {
			kept[key] = value
		}
	}
	if eventType == EventTypeMember {
		if invite, ok := content["third_party_invite"].(map[string]any); ok {
			if signed, ok := invite["signed"]; ok {
				kept["third_party_invite"] = map[string]any{"signed": signed}
			}
		}
	}
	return kept
}
