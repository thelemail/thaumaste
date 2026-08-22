package typing

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	typing   repository.Typing
	members  repository.RoomMember
	events   service.Events
	notifier *notify.Notifier
	clock    func() time.Time
}

func New(typing repository.Typing, members repository.RoomMember, events service.Events,
	notifier *notify.Notifier, clock func() time.Time,
) service.Typing {
	if clock == nil {
		clock = time.Now
	}
	return &srv{typing: typing, members: members, events: events, notifier: notifier, clock: clock}
}

func (s *srv) Set(ctx context.Context, scope entity.TenantScope, caller, target, roomID string,
	typing bool, timeoutMS int,
) error {
	if caller != target {
		return entity.ErrProfileNotAllowed
	}
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return err
	}

	in := entity.NewTyping{
		TenantID: scope.ID(),
		RoomNID:  room.NID,
		UserID:   caller,
		Typing:   typing,
		Timeout:  time.Duration(timeoutMS) * time.Millisecond,
	}
	if typing && timeoutMS <= 0 {
		in.Timeout = entity.DefaultTypingTimeout
	}
	if err := in.Validate(); err != nil {
		return err
	}
	if err := s.typing.Set(ctx, in, s.clock().UTC()); err != nil {
		return err
	}
	s.notifier.Notify(ctx, entity.RoomWakeKey(roomID))
	return nil
}

func (s *srv) ForRoom(ctx context.Context, scope entity.TenantScope, caller,
	roomID string,
) ([]string, error) {
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return nil, err
	}
	return s.typing.ForRoom(ctx, scope, room.NID, s.clock().UTC())
}

func (s *srv) joined(ctx context.Context, scope entity.TenantScope, caller,
	roomID string,
) (entity.Room, error) {
	room, err := s.events.Room(ctx, roomID)
	if err != nil {
		return entity.Room{}, err
	}
	if room.TenantID != scope.ID() {
		return entity.Room{}, entity.ErrRoomNotFound
	}
	membership, err := s.members.Get(ctx, room.NID, caller)
	if err != nil {
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return entity.Room{}, entity.ErrNotInRoom
		}
		return entity.Room{}, err
	}
	if membership.Membership != entity.MembershipJoin || membership.Forgotten {
		return entity.Room{}, entity.ErrNotInRoom
	}
	return room, nil
}
