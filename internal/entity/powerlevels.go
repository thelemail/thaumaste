package entity

import (
	"encoding/json"
	"errors"
	"slices"
)

var (
	ErrPowerLevelsMalformed   = errors.New("entity: power levels are malformed")
	ErrPowerLevelsNameCreator = errors.New("entity: power levels cannot name a room creator")
)

const (
	defaultBan           = 50
	defaultKick          = 50
	defaultRedact        = 50
	defaultInvite        = 0
	defaultStateDefault  = 50
	defaultEventsDefault = 0
	defaultUsersDefault  = 0
)

type PowerLevel struct {
	value   int64
	creator bool
}

func Power(value int64) PowerLevel { return PowerLevel{value: value} }

func CreatorPower() PowerLevel { return PowerLevel{creator: true} }

func (p PowerLevel) IsCreator() bool { return p.creator }

func (p PowerLevel) Value() int64 { return p.value }

func (p PowerLevel) AtLeast(other PowerLevel) bool {
	if p.creator {
		return true
	}
	if other.creator {
		return false
	}
	return p.value >= other.value
}

func (p PowerLevel) GreaterThan(other PowerLevel) bool {
	if other.creator {
		return false
	}
	if p.creator {
		return true
	}
	return p.value > other.value
}

type PowerLevels struct {
	Ban           int64
	Kick          int64
	Redact        int64
	Invite        int64
	StateDefault  int64
	EventsDefault int64
	UsersDefault  int64
	Events        map[string]int64
	Users         map[string]int64
	Notifications map[string]int64

	creators []string
	version  RoomVersion
}

func DefaultPowerLevels(version RoomVersion, creators []string) PowerLevels {
	return PowerLevels{
		Ban:           defaultBan,
		Kick:          defaultKick,
		Redact:        defaultRedact,
		Invite:        defaultInvite,
		StateDefault:  defaultStateDefault,
		EventsDefault: defaultEventsDefault,
		UsersDefault:  defaultUsersDefault,
		Events:        map[string]int64{},
		Users:         map[string]int64{},
		Notifications: map[string]int64{},
		creators:      creators,
		version:       version,
	}
}

func ParsePowerLevels(content map[string]any, version RoomVersion, creators []string) (PowerLevels, error) {
	levels := DefaultPowerLevels(version, creators)

	ints := map[string]*int64{
		"ban":            &levels.Ban,
		"kick":           &levels.Kick,
		"redact":         &levels.Redact,
		"invite":         &levels.Invite,
		"state_default":  &levels.StateDefault,
		"events_default": &levels.EventsDefault,
		"users_default":  &levels.UsersDefault,
	}
	for key, target := range ints {
		raw, present := content[key]
		if !present {
			continue
		}
		value, ok := asInt(raw)
		if !ok {
			return PowerLevels{}, ErrPowerLevelsMalformed
		}
		*target = value
	}

	tables := map[string]*map[string]int64{
		"events":        &levels.Events,
		"users":         &levels.Users,
		"notifications": &levels.Notifications,
	}
	for key, target := range tables {
		raw, present := content[key]
		if !present {
			continue
		}
		object, ok := raw.(map[string]any)
		if !ok {
			return PowerLevels{}, ErrPowerLevelsMalformed
		}
		parsed := make(map[string]int64, len(object))
		for name, item := range object {
			value, ok := asInt(item)
			if !ok {
				return PowerLevels{}, ErrPowerLevelsMalformed
			}
			parsed[name] = value
		}
		*target = parsed
	}

	for user := range levels.Users {
		if !isUserID(user) {
			return PowerLevels{}, ErrPowerLevelsMalformed
		}
		if version.CreatorsOutrankPowerLevels && slices.Contains(creators, user) {
			return PowerLevels{}, ErrPowerLevelsNameCreator
		}
	}
	return levels, nil
}

func (p PowerLevels) UserLevel(userID string) PowerLevel {
	if p.version.CreatorsOutrankPowerLevels && slices.Contains(p.creators, userID) {
		return CreatorPower()
	}
	if value, ok := p.Users[userID]; ok {
		return Power(value)
	}
	return Power(p.UsersDefault)
}

func (p PowerLevels) EventLevel(eventType string, isState bool) PowerLevel {
	if value, ok := p.Events[eventType]; ok {
		return Power(value)
	}
	if isState {
		return Power(p.StateDefault)
	}
	return Power(p.EventsDefault)
}

func (p PowerLevels) Creators() []string { return p.creators }

func (p PowerLevels) CanSend(userID, eventType string, isState bool) bool {
	return p.UserLevel(userID).AtLeast(p.EventLevel(eventType, isState))
}

func asInt(raw any) (int64, bool) {
	switch value := raw.(type) {
	case json.Number:
		n, err := value.Int64()
		return n, err == nil
	case float64:
		if value != float64(int64(value)) {
			return 0, false
		}
		return int64(value), true
	case int64:
		return value, true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}

func isUserID(id string) bool {
	_, _, err := ParseUserID(id)
	return err == nil
}
