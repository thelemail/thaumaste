package entity

var redactionKeepsTopLevel = []string{
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
}

var redactionKeepsContent = map[string][]string{
	EventTypeMember:            {"membership", "join_authorised_via_users_server"},
	EventTypeJoinRules:         {"join_rule", "allow"},
	EventTypeHistoryVisibility: {"history_visibility"},
	EventTypeRedaction:         {"redacts"},
	EventTypePowerLevels: {
		"ban", "events", "events_default", "invite",
		"kick", "redact", "state_default", "users", "users_default",
	},
}

func Redact(event map[string]any, version RoomVersion) map[string]any {
	_ = version

	out := make(map[string]any, len(redactionKeepsTopLevel))
	for _, key := range redactionKeepsTopLevel {
		if value, ok := event[key]; ok {
			out[key] = value
		}
	}

	eventType, _ := event["type"].(string)
	content, _ := event["content"].(map[string]any)
	out["content"] = redactContent(eventType, content)
	return out
}

func redactContent(eventType string, content map[string]any) map[string]any {
	if content == nil {
		return map[string]any{}
	}
	if eventType == EventTypeCreate {
		return content
	}

	kept := map[string]any{}
	for _, key := range redactionKeepsContent[eventType] {
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
