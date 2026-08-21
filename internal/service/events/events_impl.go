package events

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

const opaqueRoomIDBytes = 10

var opaqueEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type srv struct {
	rooms      repository.Room
	events     repository.Event
	state      repository.State
	tenants    service.Tenants
	tx         repository.Transactor
	stream     *postgres.Stream
	serialiser *serialiser.Serialiser
	instance   string
	clock      func() time.Time
	rnd        io.Reader
}

func New(
	rooms repository.Room,
	events repository.Event,
	state repository.State,
	tenants service.Tenants,
	tx repository.Transactor,
	stream *postgres.Stream,
	gate *serialiser.Serialiser,
	instance string,
	clock func() time.Time,
	rnd io.Reader,
) service.Events {
	if clock == nil {
		clock = time.Now
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	return &srv{
		rooms: rooms, events: events, state: state, tenants: tenants, tx: tx,
		stream: stream, serialiser: gate, instance: instance, clock: clock, rnd: rnd,
	}
}

func (s *srv) CreateRoom(ctx context.Context, scope entity.TenantScope, in entity.NewRoomEvent) (entity.Room, entity.StoredEvent, error) {
	if err := in.Validate(); err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}
	version, err := entity.LookupRoomVersion(in.Version)
	if err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}

	content := map[string]any{}
	for k, v := range in.Content {
		content[k] = v
	}
	content["room_version"] = string(version.ID)

	opaque, err := s.opaque()
	if err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}

	builder := entity.EventBuilder{
		Version:        version,
		Type:           entity.EventTypeCreate,
		StateKey:       new(string),
		Sender:         in.Creator,
		Content:        content,
		OriginServerTS: s.clock().UTC().UnixMilli(),
	}
	if version.CreateCarriesRoomID {
		builder.RoomID = "!" + opaque + ":" + scope.ServerName()
	}

	create, err := builder.Build(s.signer(ctx, scope))
	if err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}
	roomID, err := entity.RoomIDFor(create, version, scope.ServerName(), opaque)
	if err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}
	if err := entity.Authorise(create, entity.StateMap{}, version); err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}

	var (
		room   entity.Room
		stored entity.StoredEvent
	)
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		room, err = s.rooms.Create(ctx, entity.NewRoom{
			TenantID: scope.ID(),
			RoomID:   roomID,
			Version:  version.ID,
		})
		if err != nil {
			if errors.Is(err, repository.ErrRoomAlreadyExists) {
				return entity.ErrRoomAlreadyExists
			}
			return err
		}
		stored, err = s.persist(ctx, scope, room, create, entity.StateMap{})
		if err != nil {
			return err
		}
		if err := s.rooms.SetCreateEvent(ctx, room.NID, stored.NID); err != nil {
			return err
		}
		return s.rooms.ReplaceExtremities(ctx, room.NID, []int64{stored.NID})
	})
	if err != nil {
		return entity.Room{}, entity.StoredEvent{}, err
	}
	return room, stored, nil
}

func (s *srv) Send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error) {
	if err := in.Validate(); err != nil {
		return entity.StoredEvent{}, err
	}

	var stored entity.StoredEvent
	err := s.serialiser.Do(ctx, in.RoomID, func(ctx context.Context) error {
		return s.tx.WithTx(ctx, func(ctx context.Context) error {
			var err error
			stored, err = s.send(ctx, scope, in)
			return err
		})
	})
	if err != nil {
		return entity.StoredEvent{}, err
	}
	return stored, nil
}

func (s *srv) send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error) {
	room, version, err := s.roomAndVersion(ctx, in.RoomID)
	if err != nil {
		return entity.StoredEvent{}, err
	}

	parent, err := s.parent(ctx, room)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	before, err := s.state.Load(ctx, parent.StateSnapshotNID)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	before = before.Apply(parent.Event)

	builder := entity.EventBuilder{
		Version:        version,
		RoomID:         room.RoomID,
		Type:           in.Type,
		StateKey:       in.StateKey,
		Sender:         in.Sender,
		Content:        in.Content,
		PrevEvents:     []string{parent.Event.ID()},
		PrevDepth:      parent.Event.Depth(),
		OriginServerTS: s.clock().UTC().UnixMilli(),
	}
	builder.AuthEvents = entity.SelectAuthEvents(builder, before)

	built, err := builder.Build(s.signer(ctx, scope))
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if err := entity.Authorise(built, before, version); err != nil {
		return entity.StoredEvent{}, err
	}
	declared, err := s.events.GetManyByEventID(ctx, built.AuthEvents())
	if err != nil {
		return entity.StoredEvent{}, err
	}
	named := make([]entity.Event, 0, len(declared))
	for _, d := range declared {
		named = append(named, d.Event)
	}
	if err := entity.CheckAuthEvents(built, named, version); err != nil {
		return entity.StoredEvent{}, err
	}

	stored, err := s.persist(ctx, scope, room, built, before)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if err := s.rooms.ReplaceExtremities(ctx, room.NID, []int64{stored.NID}); err != nil {
		return entity.StoredEvent{}, err
	}
	return stored, nil
}

func (s *srv) persist(ctx context.Context, scope entity.TenantScope, room entity.Room, e entity.Event, before entity.StateMap) (entity.StoredEvent, error) {
	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	defer positions.Release()

	local, err := s.senderIsLocal(e.Sender(), scope)
	if err != nil {
		return entity.StoredEvent{}, err
	}

	in := entity.NewStoredEvent{
		RoomNID:             room.NID,
		Event:               e,
		SenderIsLocal:       local,
		StreamOrdering:      positions.IDs[0],
		TopologicalOrdering: e.Depth(),
		InstanceName:        s.instance,
		Disposition:         entity.DispositionAccepted,
	}
	if err := in.Validate(); err != nil {
		return entity.StoredEvent{}, err
	}

	stored, err := s.events.Insert(ctx, in)
	if err != nil {
		if errors.Is(err, repository.ErrEventExists) {
			return entity.StoredEvent{}, entity.ErrEventExists
		}
		return entity.StoredEvent{}, err
	}

	snapshotNID, err := s.state.Save(ctx, room.NID, before)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if err := s.events.SetStateSnapshot(ctx, stored.NID, snapshotNID); err != nil {
		return entity.StoredEvent{}, err
	}
	stored.StateSnapshotNID = snapshotNID
	return stored, nil
}

func (s *srv) Room(ctx context.Context, roomID string) (entity.Room, error) {
	room, err := s.rooms.GetByRoomID(ctx, roomID)
	if err != nil {
		if errors.Is(err, repository.ErrRoomNotFound) {
			return entity.Room{}, entity.ErrRoomNotFound
		}
		return entity.Room{}, err
	}
	return room, nil
}

func (s *srv) Timeline(ctx context.Context, roomID string) ([]entity.StoredEvent, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return nil, err
	}
	return s.events.ListForRoom(ctx, room.NID)
}

func (s *srv) CurrentState(ctx context.Context, roomID string) (entity.StateMap, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return nil, err
	}
	latest, err := s.parent(ctx, room)
	if err != nil {
		return nil, err
	}
	before, err := s.state.Load(ctx, latest.StateSnapshotNID)
	if err != nil {
		return nil, err
	}
	return before.Apply(latest.Event), nil
}

func (s *srv) StateBefore(ctx context.Context, eventID string) (entity.StateMap, error) {
	stored, err := s.events.GetByEventID(ctx, eventID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return nil, entity.ErrEventNotFound
		}
		return nil, err
	}
	return s.state.Load(ctx, stored.StateSnapshotNID)
}

func (s *srv) parent(ctx context.Context, room entity.Room) (entity.StoredEvent, error) {
	extremities, err := s.rooms.Extremities(ctx, room.NID)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if len(extremities) != 1 {
		return entity.StoredEvent{}, fmt.Errorf("%w: %s has %d forward extremities",
			entity.ErrForkedDAG, room.RoomID, len(extremities))
	}
	return extremities[0], nil
}

func (s *srv) roomAndVersion(ctx context.Context, roomID string) (entity.Room, entity.RoomVersion, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return entity.Room{}, entity.RoomVersion{}, err
	}
	version, err := entity.LookupRoomVersion(room.Version)
	if err != nil {
		return entity.Room{}, entity.RoomVersion{}, err
	}
	return room, version, nil
}

func (s *srv) senderIsLocal(sender string, scope entity.TenantScope) (bool, error) {
	domain, err := entity.SenderDomain(sender)
	if err != nil {
		return false, err
	}
	return domain == scope.ServerName(), nil
}

func (s *srv) signer(ctx context.Context, scope entity.TenantScope) entity.Signer {
	return func(document []byte) ([]byte, error) {
		return s.tenants.SignAs(ctx, scope, document)
	}
}

func (s *srv) opaque() (string, error) {
	raw := make([]byte, opaqueRoomIDBytes)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", fmt.Errorf("events: room id: %w", err)
	}
	return strings.ToLower(opaqueEncoding.EncodeToString(raw)), nil
}
