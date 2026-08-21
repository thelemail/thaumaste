package accountdata

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	data   repository.AccountData
	rooms  repository.Room
	tx     repository.Transactor
	stream *postgres.Stream
}

func New(data repository.AccountData, rooms repository.Room, tx repository.Transactor,
	stream *postgres.Stream,
) service.AccountData {
	return &srv{data: data, rooms: rooms, tx: tx, stream: stream}
}

func (s *srv) Set(ctx context.Context, scope entity.TenantScope, caller, target, roomID, dataType string,
	content []byte,
) error {
	if caller != target {
		return entity.ErrAccountDataForeign
	}
	if entity.ReservedAccountData(dataType) {
		return entity.ErrAccountDataReserved
	}
	if err := entity.AccountDataObject(content); err != nil {
		return err
	}
	return s.commit(ctx, scope, target, roomID, dataType, content)
}

func (s *srv) commit(ctx context.Context, scope entity.TenantScope, target, roomID, dataType string,
	canonical []byte,
) error {
	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return err
	}
	defer positions.Release()

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		return s.store(ctx, scope, target, roomID, dataType, canonical, positions.IDs[0])
	})
}

func (s *srv) store(ctx context.Context, scope entity.TenantScope, target, roomID, dataType string,
	canonical []byte, stream int64,
) error {
	roomNID, err := s.roomNID(ctx, roomID)
	if err != nil {
		return err
	}
	in := entity.NewAccountData{
		TenantID: scope.ID(), UserID: target, RoomNID: roomNID, Type: dataType, Content: canonical,
	}
	if err := in.Validate(); err != nil {
		return err
	}
	return s.data.Set(ctx, in, stream)
}

func (s *srv) roomNID(ctx context.Context, roomID string) (int64, error) {
	if roomID == "" {
		return 0, nil
	}
	room, err := s.rooms.GetByRoomID(ctx, roomID)
	if err != nil {
		if errors.Is(err, repository.ErrRoomNotFound) {
			return 0, entity.ErrRoomNotFound
		}
		return 0, err
	}
	return room.NID, nil
}

func (s *srv) Get(ctx context.Context, scope entity.TenantScope, caller, target, roomID,
	dataType string,
) (entity.AccountData, error) {
	if caller != target {
		return entity.AccountData{}, entity.ErrAccountDataForeign
	}
	roomNID, err := s.roomNID(ctx, roomID)
	if err != nil {
		return entity.AccountData{}, err
	}
	found, err := s.data.Get(ctx, scope, target, roomNID, dataType)
	if err != nil {
		if errors.Is(err, repository.ErrAccountDataNotFound) {
			return entity.AccountData{}, entity.ErrAccountDataNotFound
		}
		return entity.AccountData{}, err
	}
	found.RoomID = roomID
	return found, nil
}

func (s *srv) Tags(ctx context.Context, scope entity.TenantScope, caller, target,
	roomID string,
) (entity.RoomTags, error) {
	found, err := s.Get(ctx, scope, caller, target, roomID, entity.AccountDataTags)
	if err != nil {
		if errors.Is(err, entity.ErrAccountDataNotFound) {
			return entity.RoomTags{Tags: map[string]json.RawMessage{}}, nil
		}
		return entity.RoomTags{}, err
	}
	return entity.ParseRoomTags(found.Content)
}

func (s *srv) SetTag(ctx context.Context, scope entity.TenantScope, caller, target, roomID, tag string,
	order []byte,
) error {
	if err := entity.ValidateTag(tag); err != nil {
		return err
	}
	return s.amend(ctx, scope, caller, target, roomID, func(tags entity.RoomTags) entity.RoomTags {
		return tags.Set(tag, order)
	})
}

func (s *srv) DeleteTag(ctx context.Context, scope entity.TenantScope, caller, target, roomID,
	tag string,
) error {
	if err := entity.ValidateTag(tag); err != nil {
		return err
	}
	return s.amend(ctx, scope, caller, target, roomID, func(tags entity.RoomTags) entity.RoomTags {
		return tags.Delete(tag)
	})
}

func (s *srv) amend(ctx context.Context, scope entity.TenantScope, caller, target, roomID string,
	change func(entity.RoomTags) entity.RoomTags,
) error {
	if caller != target {
		return entity.ErrAccountDataForeign
	}
	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return err
	}
	defer positions.Release()

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.data.Lock(ctx, scope, target); err != nil {
			return err
		}
		tags, err := s.Tags(ctx, scope, caller, target, roomID)
		if err != nil {
			return err
		}
		raw, err := change(tags).JSON()
		if err != nil {
			return err
		}
		return s.store(ctx, scope, target, roomID, entity.AccountDataTags, raw, positions.IDs[0])
	})
}
