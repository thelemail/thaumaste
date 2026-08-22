package devicelists

import (
	"context"
	"sort"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	changes  repository.DeviceList
	members  repository.RoomMember
	events   repository.Event
	tx       repository.Transactor
	stream   *postgres.Stream
	notifier *notify.Notifier
}

func New(changes repository.DeviceList, members repository.RoomMember, events repository.Event,
	tx repository.Transactor, stream *postgres.Stream, notifier *notify.Notifier,
) service.DeviceLists {
	return &srv{changes: changes, members: members, events: events, tx: tx,
		stream: stream, notifier: notifier}
}

func (s *srv) Record(ctx context.Context, scope entity.TenantScope, userID string) error {
	in := entity.NewDeviceListChange{TenantID: scope.ID(), UserID: userID}
	if err := in.Validate(); err != nil {
		return err
	}

	positions, err := s.stream.Next(ctx, 1)
	if err != nil {
		return err
	}
	defer positions.Release()

	if err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		return s.changes.Record(ctx, in, positions.IDs[0])
	}); err != nil {
		return err
	}

	peers, err := s.members.Peers(ctx, scope, userID)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(peers)+1)
	keys = append(keys, entity.UserWakeKey(userID))
	for _, peer := range peers {
		keys = append(keys, entity.UserWakeKey(peer))
	}
	s.notifier.Notify(ctx, keys...)
	return nil
}

func (s *srv) ChangedSince(ctx context.Context, scope entity.TenantScope, caller string,
	from, to entity.SyncCursors,
) (entity.DeviceLists, error) {
	rooms, err := s.members.ListForSync(ctx, scope, caller)
	if err != nil {
		return entity.DeviceLists{}, err
	}
	windows, boundary, err := s.scope(ctx, rooms, from, to)
	if err != nil {
		return entity.DeviceLists{}, err
	}

	seen := map[string]bool{}
	collect := func(userIDs []string) {
		for _, userID := range userIDs {
			if userID != caller {
				seen[userID] = true
			}
		}
	}

	changed, err := s.changes.ChangedSince(ctx, scope, from.DeviceLists, to.DeviceLists)
	if err != nil {
		return entity.DeviceLists{}, err
	}
	collect(changed)

	churned, err := s.events.MembersChangedSince(ctx, windows, to.Events)
	if err != nil {
		return entity.DeviceLists{}, err
	}
	collect(churned)

	present, err := s.members.PresentIn(ctx, boundary)
	if err != nil {
		return entity.DeviceLists{}, err
	}
	collect(present)

	if len(seen) == 0 {
		return entity.DeviceLists{}, nil
	}
	candidates := make([]string, 0, len(seen))
	for userID := range seen {
		candidates = append(candidates, userID)
	}
	sort.Strings(candidates)

	visible, err := s.members.SharedWith(ctx, scope, caller, candidates)
	if err != nil {
		return entity.DeviceLists{}, err
	}
	shared := make(map[string]bool, len(visible))
	for _, userID := range visible {
		shared[userID] = true
	}

	out := entity.DeviceLists{Changed: visible}
	for _, userID := range candidates {
		if !shared[userID] {
			out.Left = append(out.Left, userID)
		}
	}
	sort.Strings(out.Changed)
	return out, nil
}

func (s *srv) scope(ctx context.Context, rooms []entity.SyncRoom,
	from, to entity.SyncCursors,
) ([]entity.RoomWindow, []int64, error) {
	nids := make([]int64, 0, len(rooms))
	for _, room := range rooms {
		nids = append(nids, room.EventNID)
	}
	stored, err := s.events.GetManyByNID(ctx, nids)
	if err != nil {
		return nil, nil, err
	}
	at := make(map[int64]int64, len(stored))
	for _, event := range stored {
		at[event.NID] = event.StreamOrdering
	}

	var windows []entity.RoomWindow
	var boundary []int64
	for _, room := range rooms {
		position := at[room.EventNID]
		crossed := position > from.Events && position <= to.Events
		switch room.Membership {
		case entity.MembershipJoin, entity.MembershipInvite:
			windows = append(windows, entity.RoomWindow{
				RoomNID: room.RoomNID,
				After:   max(from.Events, position),
			})
			if crossed {
				boundary = append(boundary, room.RoomNID)
			}
		default:
			if crossed {
				boundary = append(boundary, room.RoomNID)
			}
		}
	}
	return windows, boundary, nil
}
