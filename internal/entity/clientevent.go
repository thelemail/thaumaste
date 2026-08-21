package entity

import (
	"encoding/json"
	"fmt"
)

type ClientEvent struct {
	Event         Event
	Age           int64
	TransactionID string
	Membership    string
}

func (c ClientEvent) JSON() (json.RawMessage, error) {
	fields := c.Event.Fields()
	fields["event_id"] = c.Event.ID()

	unsigned := map[string]any{"age": c.Age}
	if c.TransactionID != "" {
		unsigned["transaction_id"] = c.TransactionID
	}
	if c.Membership != "" {
		unsigned["membership"] = c.Membership
	}
	fields["unsigned"] = unsigned

	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal client event: %w", err)
	}
	return raw, nil
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
