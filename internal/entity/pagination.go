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

const (
	ThreadsAll          = "all"
	ThreadsParticipated = "participated"

	RecursionDepth = 3
)

var ErrBadInclude = errors.New("entity: include must be all or participated")

type RelationsRequest struct {
	RoomID    string
	EventID   string
	RelType   string
	EventType string
	Direction string
	From      string
	To        string
	Limit     *int
	Recurse   bool
}

type Relations struct {
	Chunk     []ClientEvent
	NextBatch string
	PrevBatch string
	Depth     *int
}

type ThreadsRequest struct {
	RoomID  string
	Include string
	From    string
	Limit   *int
}

type Threads struct {
	Chunk     []ClientEvent
	NextBatch string
}

func (r RelationsRequest) Validate() error {
	if r.Direction != DirectionBackward && r.Direction != DirectionForward {
		return fmt.Errorf("%w: %q", ErrBadDirection, r.Direction)
	}
	if r.RoomID == "" || len(r.RoomID) > MaxRoomIDBytes {
		return ErrRoomNotFound
	}
	if r.EventID == "" || len(r.EventID) > MaxEventIDBytes {
		return ErrEventNotFound
	}
	if len(r.RelType) > MaxRelationTypeBytes || len(r.EventType) > MaxEventTypeSize {
		return ErrRelationTypeUnknown
	}
	return nil
}

func (r RelationsRequest) Backwards() bool { return r.Direction == DirectionBackward }

func (r ThreadsRequest) Validate() error {
	if r.RoomID == "" || len(r.RoomID) > MaxRoomIDBytes {
		return ErrRoomNotFound
	}
	if r.Include != ThreadsAll && r.Include != ThreadsParticipated {
		return fmt.Errorf("%w: %q", ErrBadInclude, r.Include)
	}
	return nil
}
