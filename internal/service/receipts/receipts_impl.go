package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	receipts repository.Receipt
	members  repository.RoomMember
	events   service.Events
	data     service.AccountData
	tx       repository.Transactor
	stream   *postgres.Stream
	notifier *notify.Notifier
	clock    func() time.Time
}

func New(receipts repository.Receipt, members repository.RoomMember, events service.Events,
	data service.AccountData, tx repository.Transactor, stream *postgres.Stream,
	notifier *notify.Notifier, clock func() time.Time,
) service.Receipts {
	if clock == nil {
		clock = time.Now
	}
	return &srv{receipts: receipts, members: members, events: events, data: data,
		tx: tx, stream: stream, notifier: notifier, clock: clock}
}

func (s *srv) Send(ctx context.Context, scope entity.TenantScope, caller, roomID, receiptType,
	eventID, threadID string,
) error {
	if receiptType == entity.AccountDataFullyRead {
		return s.Mark(ctx, scope, caller, roomID, entity.ReadMarker{FullyRead: eventID})
	}
	if !entity.ReceiptType(receiptType) {
		return entity.ErrReceiptTypeUnknown
	}

	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return err
	}
	target, err := s.inRoom(ctx, room, eventID)
	if err != nil {
		return err
	}
	if err := s.thread(ctx, room, threadID); err != nil {
		return err
	}

	in := entity.NewReceipt{
		TenantID:  scope.ID(),
		RoomNID:   room.NID,
		UserID:    caller,
		Type:      receiptType,
		ThreadID:  threadID,
		EventNID:  target.NID,
		Timestamp: s.clock().UTC().UnixMilli(),
	}
	if err := in.Validate(); err != nil {
		return err
	}

	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return err
	}
	defer positions.Release()

	if err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		return s.receipts.Set(ctx, in, positions.IDs[0])
	}); err != nil {
		return err
	}
	s.notifier.Notify(ctx, entity.RoomWakeKey(roomID))
	return nil
}

func (s *srv) Mark(ctx context.Context, scope entity.TenantScope, caller, roomID string,
	in entity.ReadMarker,
) error {
	if err := in.Validate(); err != nil {
		return err
	}
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return err
	}

	if in.FullyRead != "" {
		if _, err := s.inRoom(ctx, room, in.FullyRead); err != nil {
			return err
		}
		content, err := json.Marshal(map[string]string{"event_id": in.FullyRead})
		if err != nil {
			return err
		}
		if err := s.data.SetReserved(ctx, scope, caller, roomID, entity.AccountDataFullyRead, content); err != nil {
			return err
		}
	}
	for receiptType, eventID := range map[string]string{
		entity.ReceiptRead:        in.Read,
		entity.ReceiptReadPrivate: in.ReadPrivate,
	} {
		if eventID == "" {
			continue
		}
		if err := s.Send(ctx, scope, caller, roomID, receiptType, eventID, entity.ThreadUnthreaded); err != nil {
			return err
		}
	}
	s.notifier.Notify(ctx, entity.RoomWakeKey(roomID))
	return nil
}

func (s *srv) ForRoom(ctx context.Context, scope entity.TenantScope, caller,
	roomID string,
) ([]entity.Receipt, error) {
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return nil, err
	}
	return s.receipts.ForRoom(ctx, scope, room.NID, caller)
}

func (s *srv) ReadUpTo(ctx context.Context, scope entity.TenantScope, caller, roomID,
	threadID string,
) (int64, error) {
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return 0, err
	}
	return s.readUpTo(ctx, scope, room.NID, caller, threadID)
}

func (s *srv) readUpTo(ctx context.Context, scope entity.TenantScope, roomNID int64, caller,
	threadID string,
) (int64, error) {
	held, err := s.receipts.ForUser(ctx, scope, roomNID, caller)
	if err != nil {
		return 0, err
	}
	matching := make([]entity.Receipt, 0, len(held))
	for _, receipt := range held {
		if receipt.ThreadID == threadID {
			matching = append(matching, receipt)
		}
	}
	return entity.ReadUpTo(matching), nil
}

func (s *srv) Unread(ctx context.Context, scope entity.TenantScope, caller, roomID,
	threadID string,
) (int, error) {
	room, err := s.joined(ctx, scope, caller, roomID)
	if err != nil {
		return 0, err
	}
	position, err := s.readUpTo(ctx, scope, room.NID, caller, threadID)
	if err != nil {
		return 0, err
	}
	return s.receipts.UnreadSince(ctx, room.NID, position, caller)
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

func (s *srv) inRoom(ctx context.Context, room entity.Room, eventID string) (entity.StoredEvent, error) {
	stored, err := s.events.Event(ctx, eventID)
	if err != nil {
		return entity.StoredEvent{}, err
	}
	if stored.RoomNID != room.NID {
		return entity.StoredEvent{}, entity.ErrEventNotFound
	}
	return stored, nil
}

func (s *srv) thread(ctx context.Context, room entity.Room, threadID string) error {
	if threadID == entity.ThreadUnthreaded || threadID == entity.ThreadMain {
		return nil
	}
	root, err := s.inRoom(ctx, room, threadID)
	if err != nil {
		return entity.ErrThreadUnknown
	}
	if _, related := entity.RelationOf(root.Event); related {
		return entity.ErrThreadUnknown
	}
	return nil
}
