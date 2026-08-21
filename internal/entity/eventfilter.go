package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrBadFilter = errors.New("entity: malformed filter")

const MaxFilterEntries = 256

type RoomEventFilter struct {
	Types       []string
	NotTypes    []string
	Senders     []string
	NotSenders  []string
	ContainsURL *bool
	Limit       int
}

func ParseRoomEventFilter(raw string) (RoomEventFilter, error) {
	if raw == "" {
		return RoomEventFilter{}, nil
	}
	var body struct {
		Types       []string `json:"types"`
		NotTypes    []string `json:"not_types"`
		Senders     []string `json:"senders"`
		NotSenders  []string `json:"not_senders"`
		ContainsURL *bool    `json:"contains_url"`
		Limit       *int     `json:"limit"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return RoomEventFilter{}, fmt.Errorf("%w: %s", ErrBadFilter, err)
	}
	filter := RoomEventFilter{
		Types:       body.Types,
		NotTypes:    body.NotTypes,
		Senders:     body.Senders,
		NotSenders:  body.NotSenders,
		ContainsURL: body.ContainsURL,
	}
	if body.Limit != nil {
		filter.Limit = *body.Limit
	}
	return filter, filter.Validate()
}

func (f RoomEventFilter) Validate() error {
	for _, list := range [][]string{f.Types, f.NotTypes, f.Senders, f.NotSenders} {
		if len(list) > MaxFilterEntries {
			return fmt.Errorf("%w: too many entries", ErrBadFilter)
		}
	}
	for _, sender := range slices.Concat(f.Senders, f.NotSenders) {
		if !isUserID(sender) {
			return fmt.Errorf("%w: %q is not a user id", ErrBadFilter, sender)
		}
	}
	if f.Limit < 0 {
		return fmt.Errorf("%w: limit must not be negative", ErrBadFilter)
	}
	return nil
}

func (f RoomEventFilter) Keeps(e Event) bool {
	if len(f.Types) > 0 && !matchesAny(f.Types, e.Type()) {
		return false
	}
	if matchesAny(f.NotTypes, e.Type()) {
		return false
	}
	if len(f.Senders) > 0 && !slices.Contains(f.Senders, e.Sender()) {
		return false
	}
	if slices.Contains(f.NotSenders, e.Sender()) {
		return false
	}
	if f.ContainsURL != nil {
		_, has := e.Content()["url"].(string)
		if has != *f.ContainsURL {
			return false
		}
	}
	return true
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if globMatches(pattern, value) {
			return true
		}
	}
	return false
}

func globMatches(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]

	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		index := strings.Index(value, part)
		if index < 0 {
			return false
		}
		value = value[index+len(part):]
	}
	return strings.HasSuffix(value, last) && len(value) >= len(last)
}

func (f RoomEventFilter) JSON() ([]byte, error) {
	fields := map[string]any{}
	if f.Types != nil {
		fields["types"] = f.Types
	}
	if f.NotTypes != nil {
		fields["not_types"] = f.NotTypes
	}
	if f.Senders != nil {
		fields["senders"] = f.Senders
	}
	if f.NotSenders != nil {
		fields["not_senders"] = f.NotSenders
	}
	if f.ContainsURL != nil {
		fields["contains_url"] = *f.ContainsURL
	}
	if f.Limit > 0 {
		fields["limit"] = f.Limit
	}
	return CanonicalJSON(fields)
}
