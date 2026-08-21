package rooms

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) SendMessage(ctx context.Context, scope entity.TenantScope, in entity.NewMessage) (string, error) {
	if err := in.Validate(); err != nil {
		return "", err
	}
	if _, err := s.room(ctx, scope, in.RoomID); err != nil {
		return "", err
	}
	if err := s.allow(ctx, scope, in.Sender, in.RoomID); err != nil {
		return "", err
	}
	stored, err := s.events.Send(ctx, scope, in.Event())
	if err != nil {
		return "", err
	}
	return stored.Event.ID(), nil
}

func (s *srv) Event(ctx context.Context, scope entity.TenantScope, caller, deviceID, roomID, eventID string) (entity.ClientEvent, error) {
	history, err := s.readableRoom(ctx, scope, caller, roomID)
	if err != nil {
		return entity.ClientEvent{}, errNotFound(err)
	}

	stored, err := s.events.Event(ctx, eventID)
	if err != nil {
		return entity.ClientEvent{}, err
	}
	if stored.Event.RoomID() != roomID {
		return entity.ClientEvent{}, entity.ErrEventNotFound
	}
	if !history.Visible(stored) {
		return entity.ClientEvent{}, entity.ErrEventNotFound
	}
	return s.clientEvent(ctx, scope, caller, deviceID, history, stored), nil
}
