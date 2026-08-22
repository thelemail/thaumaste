package presence

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	presence repository.Presence
	members  repository.RoomMember
	clock    func() time.Time
}

func New(presence repository.Presence, members repository.RoomMember, clock func() time.Time) service.Presence {
	if clock == nil {
		clock = time.Now
	}
	return &srv{presence: presence, members: members, clock: clock}
}

func (s *srv) Set(ctx context.Context, tenant entity.Tenant, caller, target, state,
	statusMsg string,
) error {
	if caller != target {
		return entity.ErrPresenceForeign
	}
	if !tenant.PresenceEnabled {
		if !entity.PresenceState(state) {
			return entity.ErrPresenceUnknown
		}
		return nil
	}

	in := entity.NewPresence{
		TenantID: tenant.ID, UserID: target, State: state, StatusMsg: statusMsg,
	}
	if err := in.Validate(); err != nil {
		return err
	}
	return s.presence.Set(ctx, in, s.clock().UTC())
}

func (s *srv) Get(ctx context.Context, tenant entity.Tenant, caller,
	target string,
) (entity.Presence, error) {
	if !tenant.PresenceEnabled {
		return entity.Presence{UserID: target, State: entity.PresenceOffline}, nil
	}
	if caller != target {
		shared, err := s.members.SharedWith(ctx, tenant.Scope(), caller, []string{target})
		if err != nil {
			return entity.Presence{}, err
		}
		if len(shared) == 0 {
			return entity.Presence{}, entity.ErrPresenceForeign
		}
	}

	found, err := s.presence.Get(ctx, tenant.Scope(), target)
	if err != nil {
		if errors.Is(err, repository.ErrPresenceNotFound) {
			return entity.Presence{UserID: target, State: entity.PresenceOffline}, nil
		}
		return entity.Presence{}, err
	}
	return found, nil
}
