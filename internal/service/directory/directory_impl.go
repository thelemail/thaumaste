package directory

import (
	"context"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	users  repository.User
	rooms  repository.Room
	events repository.Event
	cfg    config.Directory
}

func New(users repository.User, rooms repository.Room, events repository.Event,
	cfg config.Directory,
) service.Directory {
	return &srv{users: users, rooms: rooms, events: events, cfg: cfg}
}

func (s *srv) Search(ctx context.Context, scope entity.TenantScope, caller string,
	in entity.DirectorySearch,
) ([]entity.DirectoryResult, bool, error) {
	if err := in.Validate(); err != nil {
		return nil, false, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = entity.DefaultDirectoryLimit
	}
	if s.cfg.MaxResults > 0 && limit > s.cfg.MaxResults {
		limit = s.cfg.MaxResults
	}

	discoverable, err := s.discoverable(ctx, scope)
	if err != nil {
		return nil, false, err
	}

	found, err := s.users.Search(ctx, scope, caller, in.Term, discoverable, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(found) > limit {
		return found[:limit], true, nil
	}
	return found, false, nil
}

func (s *srv) discoverable(ctx context.Context, scope entity.TenantScope) ([]int64, error) {
	rooms, err := s.rooms.ListForTenant(ctx, scope)
	if err != nil {
		return nil, err
	}
	if len(rooms) == 0 {
		return nil, nil
	}

	roomNIDs := make([]int64, 0, len(rooms))
	for _, room := range rooms {
		roomNIDs = append(roomNIDs, room.NID)
	}

	state, err := s.events.LatestState(ctx, roomNIDs, []entity.StateKey{
		{Type: entity.EventTypeJoinRules},
		{Type: entity.EventTypeHistoryVisibility},
	})
	if err != nil {
		return nil, err
	}

	out := make([]int64, 0, len(roomNIDs))
	for _, roomNID := range roomNIDs {
		if open(state[roomNID]) {
			out = append(out, roomNID)
		}
	}
	return out, nil
}

func open(state []entity.StoredEvent) bool {
	for _, stored := range state {
		switch stored.Event.Type() {
		case entity.EventTypeJoinRules:
			if rule, ok := stored.Event.Content()["join_rule"].(string); ok && rule == entity.JoinRulePublic {
				return true
			}
		case entity.EventTypeHistoryVisibility:
			if seen, ok := stored.Event.Content()["history_visibility"].(string); ok &&
				seen == entity.HistoryVisibilityWorldReadable {
				return true
			}
		}
	}
	return false
}
