package entity

import (
	"errors"
	"fmt"
)

var ErrBadDirection = errors.New("entity: direction must be b or f")

const (
	DirectionBackward = "b"
	DirectionForward  = "f"

	DefaultPageLimit = 10
	MaxPageLimit     = 1000
)

type PageRequest struct {
	From      *Position
	To        *Position
	Backwards bool
	Inclusive bool
	Limit     int
}

type MessagesRequest struct {
	RoomID    string
	Direction string
	From      string
	To        string
	Limit     *int
	Filter    string
}

type Messages struct {
	Chunk []ClientEvent
	Start string
	End   string
}

type ContextRequest struct {
	RoomID  string
	EventID string
	Limit   *int
	Filter  string
}

type Context struct {
	Event  ClientEvent
	Before []ClientEvent
	After  []ClientEvent
	State  []Event
	Start  string
	End    string
}

func (r MessagesRequest) Validate() error {
	if r.Direction != DirectionBackward && r.Direction != DirectionForward {
		return fmt.Errorf("%w: %q", ErrBadDirection, r.Direction)
	}
	if r.RoomID == "" || len(r.RoomID) > MaxRoomIDBytes {
		return ErrRoomNotFound
	}
	return nil
}

func (r ContextRequest) Validate() error {
	if r.RoomID == "" || len(r.RoomID) > MaxRoomIDBytes {
		return ErrRoomNotFound
	}
	if r.EventID == "" || len(r.EventID) > MaxEventIDBytes {
		return ErrEventNotFound
	}
	if r.Limit != nil && *r.Limit < 0 {
		return fmt.Errorf("%w: limit must not be negative", ErrBadFilter)
	}
	return nil
}

func PageLimit(requested *int, fromFilter int) int {
	limit := fromFilter
	if requested != nil {
		limit = *requested
	}
	if limit < 0 {
		limit = 0
	}
	if requested == nil && limit == 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	return limit
}

func (r MessagesRequest) Backwards() bool { return r.Direction == DirectionBackward }
