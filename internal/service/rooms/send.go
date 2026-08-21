package rooms

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) SendMessage(ctx context.Context, scope entity.TenantScope, in entity.NewMessage) (string, error) {
	if err := in.Validate(); err != nil {
		return "", err
	}
	if _, err := s.room(ctx, scope, in.RoomID); err != nil {
		return "", err
	}
	stored, err := s.events.Send(ctx, scope, in.Event())
	if err != nil {
		return "", err
	}
	return stored.Event.ID(), nil
}

func (s *srv) Event(ctx context.Context, scope entity.TenantScope, caller, deviceID, roomID, eventID string) (entity.ClientEvent, error) {
	if _, err := s.readableState(ctx, scope, caller, roomID); err != nil {
		if errors.Is(err, entity.ErrRoomNotFound) || errors.Is(err, entity.ErrNotInRoom) {
			return entity.ClientEvent{}, entity.ErrEventNotFound
		}
		return entity.ClientEvent{}, err
	}

	stored, err := s.events.Event(ctx, eventID)
	if err != nil {
		return entity.ClientEvent{}, err
	}
	if stored.Event.RoomID() != roomID {
		return entity.ClientEvent{}, entity.ErrEventNotFound
	}

	client := entity.ClientEvent{
		Event: stored.Event,
		Age:   s.clock().UTC().UnixMilli() - stored.Event.OriginServerTS(),
	}
	if deviceID != "" && stored.Event.Sender() == caller {
		client.TransactionID, err = s.events.TransactionFor(ctx, entity.TransactionSender{
			TenantID: scope.ID(),
			UserID:   caller,
			DeviceID: deviceID,
		}, eventID)
		if err != nil {
			return entity.ClientEvent{}, err
		}
	}
	return client, nil
}
