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
	tx       repository.Transactor
	stream   *postgres.Stream
	notifier *notify.Notifier
}

func New(changes repository.DeviceList, members repository.RoomMember, tx repository.Transactor,
	stream *postgres.Stream, notifier *notify.Notifier,
) service.DeviceLists {
	return &srv{changes: changes, members: members, tx: tx, stream: stream, notifier: notifier}
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

	peers, err := s.members.SharedWith(ctx, scope, userID, nil)
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
	after int64,
) (entity.DeviceLists, int64, error) {
	upTo, err := s.stream.Published(ctx)
	if err != nil {
		return entity.DeviceLists{}, 0, err
	}
	changed, err := s.changes.ChangedSince(ctx, scope, after, upTo)
	if err != nil {
		return entity.DeviceLists{}, 0, err
	}
	if len(changed) == 0 {
		return entity.DeviceLists{}, upTo, nil
	}

	visible, err := s.members.SharedWith(ctx, scope, caller, changed)
	if err != nil {
		return entity.DeviceLists{}, 0, err
	}
	seen := make(map[string]bool, len(visible))
	for _, userID := range visible {
		seen[userID] = true
	}

	out := entity.DeviceLists{Changed: visible}
	for _, userID := range changed {
		if !seen[userID] && userID != caller {
			out.Left = append(out.Left, userID)
		}
	}
	sort.Strings(out.Changed)
	sort.Strings(out.Left)
	return out, upTo, nil
}
