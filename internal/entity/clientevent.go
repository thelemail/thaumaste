package entity

import (
	"encoding/json"
	"fmt"
	"slices"
)

type ClientEvent struct {
	Event           Event
	Age             int64
	TransactionID   string
	Membership      string
	Relations       *Aggregation
	RedactedBecause *ClientEvent
}

func (c ClientEvent) JSON() (json.RawMessage, error) {
	fields := c.Event.Fields()
	fields["event_id"] = c.Event.ID()
	if c.Event.Type() == EventTypeRedaction {
		if redacts, ok := c.Event.Content()[redactsKey].(string); ok {
			fields[redactsKey] = redacts
		}
	}

	unsigned := map[string]any{"age": c.Age}
	if c.TransactionID != "" {
		unsigned["transaction_id"] = c.TransactionID
	}
	if c.Membership != "" {
		unsigned["membership"] = c.Membership
	}
	relations, err := c.Relations.json()
	if err != nil {
		return nil, err
	}
	if relations != nil {
		unsigned["m.relations"] = relations
	}
	if c.RedactedBecause != nil {
		because, err := c.RedactedBecause.JSON()
		if err != nil {
			return nil, err
		}
		unsigned["redacted_because"] = because
	}
	fields["unsigned"] = unsigned

	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal client event: %w", err)
	}
	return raw, nil
}

func StrippedEvents(events []Event) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(events))
	for _, e := range events {
		stateKey, ok := e.StateKey()
		if !ok {
			continue
		}
		raw, err := json.Marshal(map[string]any{
			"type":      e.Type(),
			"state_key": stateKey,
			"sender":    e.Sender(),
			"content":   e.Content(),
		})
		if err != nil {
			return nil, fmt.Errorf("entity: marshal stripped state: %w", err)
		}
		out = append(out, raw)
	}
	return out, nil
}

func ClientEvents(events []Event, now int64) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(events))
	for _, e := range events {
		raw, err := ClientEvent{Event: e, Age: now - e.OriginServerTS()}.JSON()
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

var strippedStateKeys = []StateKey{
	{Type: EventTypeCreate, StateKey: ""},
	{Type: EventTypeName, StateKey: ""},
	{Type: EventTypeAvatar, StateKey: ""},
	{Type: EventTypeTopic, StateKey: ""},
	{Type: EventTypeJoinRules, StateKey: ""},
	{Type: EventTypeCanonicalAlias, StateKey: ""},
	{Type: EventTypeEncryption, StateKey: ""},
}

func StrippedState(state []Event, caller string) []Event {
	out := make([]Event, 0, len(state))
	for _, candidate := range state {
		key, ok := candidate.StateKey()
		if !ok {
			continue
		}
		if candidate.Type() == EventTypeMember {
			if key == caller || key == candidate.Sender() {
				out = append(out, candidate)
			}
			continue
		}
		if slices.Contains(strippedStateKeys, StateKey{Type: candidate.Type(), StateKey: key}) {
			out = append(out, candidate)
		}
	}
	return out
}
