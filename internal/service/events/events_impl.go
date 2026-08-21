package events

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

const opaqueRoomIDBytes = 10

var opaqueEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type positionsKey struct{}

type heldPositions struct {
	mu  sync.Mutex
	all []*postgres.Positions
}

func (h *heldPositions) hold(p *postgres.Positions) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.all = append(h.all, p)
}

func (h *heldPositions) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.all {
		p.Release()
	}
	h.all = nil
}

type srv struct {
	rooms      repository.Room
	events     repository.Event
	state      repository.State
	members    repository.RoomMember
	relations  repository.Relation
	txns       repository.Transaction
	tenants    service.Tenants
	tx         repository.Transactor
	stream     *postgres.Stream
	locks      *valkey.Client
	notifier   *notify.Notifier
	serialiser *serialiser.Serialiser
	instance   string
	clock      func() time.Time
	rnd        io.Reader
}

func New(
	rooms repository.Room,
	events repository.Event,
	state repository.State,
	members repository.RoomMember,
	relations repository.Relation,
	txns repository.Transaction,
	tenants service.Tenants,
	tx repository.Transactor,
	stream *postgres.Stream,
	locks *valkey.Client,
	notifier *notify.Notifier,
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
		rooms: rooms, events: events, state: state, members: members, relations: relations,
		txns: txns, tenants: tenants, tx: tx,
		stream: stream, locks: locks, notifier: notifier, serialiser: gate, instance: instance, clock: clock, rnd: rnd,
	}
}

func (s *srv) CreateRoom(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, []entity.StoredEvent, error) {
	if err := in.Validate(); err != nil {
		return entity.Room{}, nil, err
	}
	version, err := entity.LookupRoomVersion(in.Version)
	if err != nil {
		return entity.Room{}, nil, err
	}

	opaque, err := s.opaque()
	if err != nil {
		return entity.Room{}, nil, err
	}

	builder := entity.EventBuilder{
		Version:        version,
		Type:           entity.EventTypeCreate,
		StateKey:       new(string),
		Sender:         in.Creator,
		Content:        in.CreateContent(version),
		OriginServerTS: s.clock().UTC().UnixMilli(),
	}
	if version.CreateCarriesRoomID {
		builder.RoomID = "!" + opaque + ":" + scope.ServerName()
	}

	create, err := builder.Build(s.signer(ctx, scope))
	if err != nil {
		return entity.Room{}, nil, err
	}
	roomID, err := entity.RoomIDFor(create, version, scope.ServerName(), opaque)
	if err != nil {
		return entity.Room{}, nil, err
	}
	if err := entity.Authorise(create, entity.StateMap{}, version); err != nil {
		return entity.Room{}, nil, err
	}

	var (
		room    entity.Room
		written []entity.StoredEvent
	)
	err = s.write(ctx, roomID, func(ctx context.Context) error {
		written = nil
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
		stored, err := s.persist(ctx, scope, room, create, entity.StateMap{})
		if err != nil {
			return err
		}
		if err := s.rooms.SetCreateEvent(ctx, room.NID, stored.NID); err != nil {
			return err
		}
		if err := s.rooms.ReplaceExtremities(ctx, room.NID, []int64{stored.NID}); err != nil {
			return err
		}
		written = append(written, stored)

		for _, next := range creationChain(in, roomID, version) {
			stored, err := s.send(ctx, scope, next)
			if err != nil {
				return err
			}
			written = append(written, stored)
		}
		return nil
	})
	if err != nil {
		return entity.Room{}, nil, err
	}
	s.announce(ctx, written...)
	return room, written, nil
}

func creationChain(in entity.NewRoomRequest, roomID string, version entity.RoomVersion) []entity.NewEvent {
	empty := ""
	state := func(eventType string, content map[string]any) entity.NewEvent {
		return entity.NewEvent{
			RoomID: roomID, Type: eventType, StateKey: &empty,
			Sender: in.Creator, Content: content,
		}
	}
	member := func(target, membership string, content map[string]any) entity.NewEvent {
		key := target
		out := map[string]any{"membership": membership}
		for k, v := range content {
			out[k] = v
		}
		return entity.NewEvent{
			RoomID: roomID, Type: entity.EventTypeMember, StateKey: &key,
			Sender: in.Creator, Content: out,
		}
	}

	chain := []entity.NewEvent{
		member(in.Creator, entity.MembershipJoin, map[string]any{
			"displayname": in.CreatorDisplayName,
			"avatar_url":  in.CreatorAvatarURL,
		}),
		state(entity.EventTypePowerLevels, in.PowerLevelContent(version)),
	}

	if alias, ok := in.Alias(); ok {
		chain = append(chain, state(entity.EventTypeCanonicalAlias, map[string]any{"alias": alias}))
	}

	chain = append(chain,
		state(entity.EventTypeJoinRules, map[string]any{"join_rule": in.JoinRule()}),
		state(entity.EventTypeHistoryVisibility, map[string]any{"history_visibility": in.HistoryVisibility()}),
		state(entity.EventTypeGuestAccess, map[string]any{"guest_access": entity.GuestAccessForbidden}),
		state(entity.EventTypeEncryption, in.Encryption()),
	)

	for _, item := range in.InitialState {
		if item.Type == entity.EventTypeEncryption && item.StateKey == "" {
			continue
		}
		key := item.StateKey
		chain = append(chain, entity.NewEvent{
			RoomID: roomID, Type: item.Type, StateKey: &key,
			Sender: in.Creator, Content: item.Content,
		})
	}

	if in.Name != "" {
		chain = append(chain, state(entity.EventTypeName, map[string]any{"name": in.Name}))
	}
	if in.Topic != "" {
		chain = append(chain, state(entity.EventTypeTopic, entity.TopicContent(in.Topic)))
	}
	for _, invitee := range in.Invite {
		if invitee == in.Creator {
			continue
		}
		chain = append(chain, member(invitee, entity.MembershipInvite, nil))
	}
	return chain
}

func (s *srv) Send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error) {
	if err := in.Validate(); err != nil {
		return entity.StoredEvent{}, err
	}

	var stored entity.StoredEvent
	err := s.write(ctx, in.RoomID, func(ctx context.Context) error {
		var err error
		stored, err = s.send(ctx, scope, in)
		return err
	})
	if err != nil {
		return entity.StoredEvent{}, err
	}
	s.announce(ctx, stored)
	return stored, nil
}

func (s *srv) send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error) {
	if in.StateKey != nil {
		if err := entity.ValidateStateContent(in.Type, in.Content); err != nil {
			return entity.StoredEvent{}, err
		}
	}
	if replayed, ok, err := s.replay(ctx, scope, in); err != nil || ok {
		return replayed, err
	}

	room, version, err := s.roomAndVersion(ctx, in.RoomID)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if err := s.checkRelation(ctx, room, in); err != nil {
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
	if err := s.recordTransaction(ctx, scope, in, stored.Event.ID()); err != nil {
		return entity.StoredEvent{}, err
	}
	return stored, nil
}

func (s *srv) transactionFor(scope entity.TenantScope, in entity.NewEvent, eventID string) entity.NewEventTransaction {
	return entity.NewEventTransaction{
		TenantID: scope.ID(),
		UserID:   in.Sender,
		DeviceID: in.Txn.DeviceID,
		Endpoint: in.Txn.Endpoint,
		RoomID:   in.RoomID,
		TxnID:    in.Txn.TxnID,
		EventID:  eventID,
	}
}

func (s *srv) replay(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, bool, error) {
	if in.Txn == nil {
		return entity.StoredEvent{}, false, nil
	}
	recorded, err := s.txns.Get(ctx, s.transactionFor(scope, in, "").Key())
	if err != nil {
		if errors.Is(err, repository.ErrTransactionNotFound) {
			return entity.StoredEvent{}, false, nil
		}
		return entity.StoredEvent{}, false, err
	}
	stored, err := s.events.GetByEventID(ctx, recorded.EventID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return entity.StoredEvent{}, false, entity.ErrEventNotFound
		}
		return entity.StoredEvent{}, false, err
	}
	return stored, true, nil
}

func (s *srv) recordTransaction(ctx context.Context, scope entity.TenantScope, in entity.NewEvent, eventID string) error {
	if in.Txn == nil {
		return nil
	}
	record := s.transactionFor(scope, in, eventID)
	if err := record.Validate(); err != nil {
		return err
	}
	return s.txns.Record(ctx, record)
}

func (s *srv) announce(ctx context.Context, written ...entity.StoredEvent) {
	if s.notifier == nil || len(written) == 0 {
		return
	}
	keys := []string{entity.RoomWakeKey(written[0].Event.RoomID())}
	for _, stored := range written {
		if stored.Event.Type() != entity.EventTypeMember {
			continue
		}
		if target, ok := stored.Event.StateKey(); ok {
			keys = append(keys, entity.UserWakeKey(target))
		}
	}
	s.notifier.Notify(ctx, keys...)
}

func (s *srv) write(ctx context.Context, roomID string, fn func(context.Context) error) error {
	held := &heldPositions{}
	defer held.release()

	return s.serialiser.Do(context.WithValue(ctx, positionsKey{}, held), roomID, func(ctx context.Context) error {
		ctx, released, err := s.lease(ctx, roomID)
		if err != nil {
			return err
		}
		defer released()

		return s.tx.WithTx(ctx, func(ctx context.Context) error {
			if err := s.rooms.Lock(ctx, roomID); err != nil {
				return err
			}
			return fn(ctx)
		})
	})
}

func (s *srv) lease(ctx context.Context, roomID string) (context.Context, func(), error) {
	if s.locks == nil {
		return ctx, func() {}, nil
	}
	held, release, err := s.locks.Lock(ctx, roomID)
	switch {
	case err == nil:
		return held, release, nil
	case errors.Is(err, valkey.ErrUnavailable):
		slog.WarnContext(ctx, "writing without the shared room lease", "room_id", roomID, "error", err)
		return ctx, func() {}, nil
	default:
		return nil, nil, err
	}
}

func (s *srv) nextPosition(ctx context.Context) (int64, error) {
	held, ok := ctx.Value(positionsKey{}).(*heldPositions)
	if !ok {
		return 0, entity.ErrUnorderedWrite
	}
	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return 0, err
	}
	held.hold(positions)
	return positions.IDs[0], nil
}

func (s *srv) persist(ctx context.Context, scope entity.TenantScope, room entity.Room, e entity.Event, before entity.StateMap) (entity.StoredEvent, error) {
	position, err := s.nextPosition(ctx)
	if err != nil {
		return entity.StoredEvent{}, err
	}

	local, err := s.senderIsLocal(e.Sender(), scope)
	if err != nil {
		return entity.StoredEvent{}, err
	}

	in := entity.NewStoredEvent{
		RoomNID:             room.NID,
		Event:               e,
		SenderIsLocal:       local,
		StreamOrdering:      position,
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

	if err := s.projectMembership(ctx, room, stored); err != nil {
		return entity.StoredEvent{}, err
	}
	if err := s.projectRelation(ctx, room, stored); err != nil {
		return entity.StoredEvent{}, err
	}
	if err := s.projectActivity(ctx, room, stored); err != nil {
		return entity.StoredEvent{}, err
	}
	return stored, nil
}

func (s *srv) projectActivity(ctx context.Context, room entity.Room, stored entity.StoredEvent) error {
	return s.rooms.SetActivity(ctx, room.NID, stored.StreamOrdering, entity.Bumping(stored.Event.Type()))
}

func (s *srv) Redact(ctx context.Context, scope entity.TenantScope, in entity.NewRedaction) (entity.StoredEvent, error) {
	if err := in.Validate(); err != nil {
		return entity.StoredEvent{}, err
	}

	var stored entity.StoredEvent
	err := s.write(ctx, in.RoomID, func(ctx context.Context) error {
		var err error
		stored, err = s.redact(ctx, scope, in)
		return err
	})
	if err != nil {
		return entity.StoredEvent{}, err
	}
	s.announce(ctx, stored)
	return stored, nil
}

func (s *srv) redact(ctx context.Context, scope entity.TenantScope, in entity.NewRedaction) (entity.StoredEvent, error) {
	room, version, err := s.roomAndVersion(ctx, in.RoomID)
	if err != nil {
		return entity.StoredEvent{}, err
	}

	target, err := s.events.GetByEventID(ctx, in.EventID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return entity.StoredEvent{}, entity.ErrEventNotFound
		}
		return entity.StoredEvent{}, err
	}
	if target.RoomNID != room.NID {
		return entity.StoredEvent{}, entity.ErrEventNotFound
	}
	if !entity.Redactable(target.Event) {
		return entity.StoredEvent{}, entity.ErrRedactionProtected
	}
	if err := s.mayRedact(ctx, room, version, in.Sender, target); err != nil {
		return entity.StoredEvent{}, err
	}

	stored, err := s.send(ctx, scope, in.Event())
	if err != nil {
		return entity.StoredEvent{}, err
	}
	return stored, s.apply(ctx, version, stored, target)
}

func (s *srv) mayRedact(ctx context.Context, room entity.Room, version entity.RoomVersion,
	sender string, target entity.StoredEvent,
) error {
	if sender == target.Event.Sender() {
		return nil
	}

	latest, err := s.parent(ctx, room)
	if err != nil {
		return err
	}
	before, err := s.state.Load(ctx, latest.StateSnapshotNID)
	if err != nil {
		return err
	}
	levels, err := before.Apply(latest.Event).PowerLevels(version)
	if err != nil {
		return err
	}
	if !levels.CanRedact(sender) {
		return entity.ErrCannotRedact
	}
	return nil
}

func (s *srv) apply(ctx context.Context, version entity.RoomVersion, redaction, target entity.StoredEvent) error {
	redacted, err := entity.RedactedJSON(target.Event, version)
	if err != nil {
		return err
	}
	if err := s.events.Redacted(ctx, target.NID, redaction.NID, redacted); err != nil {
		return err
	}
	return s.relations.Delete(ctx, target.NID)
}

func (s *srv) checkRelation(ctx context.Context, room entity.Room, in entity.NewEvent) error {
	if in.StateKey != nil {
		return nil
	}
	relation, ok := entity.ParseRelation(in.Content)
	if !ok || relation.RelType != entity.RelThread {
		return nil
	}

	target, err := s.events.GetByEventID(ctx, relation.ParentID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return nil
		}
		return err
	}
	if target.RoomNID != room.NID {
		return nil
	}
	if _, related := entity.RelationOf(target.Event); related {
		return entity.ErrThreadTargetRelated
	}
	return nil
}

func (s *srv) projectRelation(ctx context.Context, room entity.Room, stored entity.StoredEvent) error {
	relation, ok := entity.RelationOf(stored.Event)
	if !ok {
		return nil
	}
	err := s.relations.Insert(ctx, entity.NewEventRelation{
		ChildNID: stored.NID,
		RoomNID:  room.NID,
		ParentID: relation.ParentID,
		RelType:  relation.RelType,
		Sender:   stored.Event.Sender(),
		Key:      relation.Key,
	})
	if errors.Is(err, repository.ErrRelationExists) {
		return entity.ErrDuplicateAnnotation
	}
	return err
}

func (s *srv) projectMembership(ctx context.Context, room entity.Room, stored entity.StoredEvent) error {
	if stored.Event.Type() != entity.EventTypeMember {
		return nil
	}
	target, ok := stored.Event.StateKey()
	if !ok {
		return nil
	}
	membership, _ := stored.Event.Content()["membership"].(string)
	in := entity.NewRoomMembership{
		TenantID:   room.TenantID,
		RoomNID:    room.NID,
		UserID:     target,
		Membership: membership,
		EventNID:   stored.NID,
	}
	if err := in.Validate(); err != nil {
		return err
	}
	return s.members.Upsert(ctx, in)
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

func (s *srv) StateAfter(ctx context.Context, eventNID int64) (entity.StateMap, error) {
	stored, err := s.events.GetByNID(ctx, eventNID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return nil, entity.ErrEventNotFound
		}
		return nil, err
	}
	before, err := s.state.Load(ctx, stored.StateSnapshotNID)
	if err != nil {
		return nil, err
	}
	return before.Apply(stored.Event), nil
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

func (s *srv) Event(ctx context.Context, eventID string) (entity.StoredEvent, error) {
	stored, err := s.events.GetByEventID(ctx, eventID)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return entity.StoredEvent{}, entity.ErrEventNotFound
		}
		return entity.StoredEvent{}, err
	}
	return stored, nil
}

func (s *srv) TransactionsFor(ctx context.Context, sender entity.TransactionSender, eventIDs []string) (map[string]string, error) {
	return s.txns.ForEvents(ctx, sender, eventIDs)
}

func (s *srv) SweepTransactions(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.txns.DeleteBefore(ctx, cutoff)
}

func (s *srv) Relations(ctx context.Context, roomID string, q entity.RelationQuery) ([]entity.RelationRef, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return nil, err
	}
	return s.relations.Find(ctx, room.NID, q)
}

func (s *srv) Many(ctx context.Context, eventNIDs []int64) ([]entity.StoredEvent, error) {
	return s.events.GetManyByNID(ctx, eventNIDs)
}

func (s *srv) ManyByID(ctx context.Context, eventIDs []string) ([]entity.StoredEvent, error) {
	return s.events.GetManyByEventID(ctx, eventIDs)
}

func (s *srv) Page(ctx context.Context, roomID string, in entity.PageRequest) ([]entity.StoredEvent, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return nil, err
	}
	return s.events.Page(ctx, room.NID, in)
}

func (s *srv) VisibilityFor(ctx context.Context, roomID, caller string) (entity.HistoryFilter, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return entity.HistoryFilter{}, err
	}
	visibility, err := s.events.ListStateOfType(ctx, room.NID, entity.EventTypeHistoryVisibility, "")
	if err != nil {
		return entity.HistoryFilter{}, err
	}
	memberships, err := s.events.ListStateOfType(ctx, room.NID, entity.EventTypeMember, caller)
	if err != nil {
		return entity.HistoryFilter{}, err
	}
	return entity.NewHistoryFilter(caller, visibility, memberships), nil
}

func (s *srv) PositionAtStream(ctx context.Context, roomID string, stream int64) (entity.Position, error) {
	room, err := s.Room(ctx, roomID)
	if err != nil {
		return entity.Position{}, err
	}
	stored, err := s.events.AtStream(ctx, room.NID, stream)
	if err != nil {
		if errors.Is(err, repository.ErrEventNotFound) {
			return entity.Position{}, entity.ErrEventNotFound
		}
		return entity.Position{}, err
	}
	return entity.PositionOf(stored), nil
}
