package entity

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
