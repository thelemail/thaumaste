package rooms

import (
	"context"
	"log/slog"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) PropagateProfile(ctx context.Context, scope entity.TenantScope, userID string) error {
	profile, err := s.users.Get(ctx, scope, userID)
	if err != nil {
		return err
	}
	joined, err := s.members.ListForUser(ctx, scope, userID, entity.MembershipJoin)
	if err != nil {
		return err
	}

	for _, membership := range joined {
		change := entity.MembershipChange{
			RoomID:      membership.RoomID,
			Sender:      userID,
			Target:      userID,
			Membership:  entity.MembershipJoin,
			DisplayName: profile.DisplayName,
			AvatarURL:   profile.AvatarURL,
		}
		if err := s.transition(ctx, scope, change); err != nil {
			slog.WarnContext(ctx, "a profile change did not reach a room",
				"room_id", membership.RoomID, "user_id", userID, "error", err)
		}
	}
	return nil
}
