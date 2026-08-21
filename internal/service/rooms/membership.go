package rooms

import (
	"context"
	"errors"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

func (s *srv) Join(ctx context.Context, scope entity.TenantScope, caller, roomIDOrAlias string, in service.JoinInput) (string, error) {
	roomID, err := s.resolve(ctx, scope, roomIDOrAlias)
	if err != nil {
		return "", err
	}
	state, err := s.roomState(ctx, scope, roomID)
	if err != nil {
		return "", err
	}

	change := entity.MembershipChange{
		RoomID:     roomID,
		Sender:     caller,
		Target:     caller,
		Membership: entity.MembershipJoin,
		Reason:     in.Reason,
	}
	if err := s.applyProfile(ctx, scope, caller, &change, in.DisplayName, in.AvatarURL); err != nil {
		return "", err
	}
	if err := s.authoriseRestricted(ctx, scope, state, caller, &change); err != nil {
		return "", err
	}
	if err := s.transition(ctx, scope, change); err != nil {
		return "", err
	}
	return roomID, nil
}

func (s *srv) Knock(ctx context.Context, scope entity.TenantScope, caller, roomIDOrAlias, reason string) (string, error) {
	roomID, err := s.resolve(ctx, scope, roomIDOrAlias)
	if err != nil {
		return "", err
	}
	change := entity.MembershipChange{
		RoomID:     roomID,
		Sender:     caller,
		Target:     caller,
		Membership: entity.MembershipKnock,
		Reason:     reason,
	}
	if err := s.applyProfile(ctx, scope, caller, &change, nil, nil); err != nil {
		return "", err
	}
	if err := s.transition(ctx, scope, change); err != nil {
		return "", err
	}
	return roomID, nil
}

func (s *srv) Leave(ctx context.Context, scope entity.TenantScope, caller, roomID, reason string) error {
	return s.transition(ctx, scope, entity.MembershipChange{
		RoomID: roomID, Sender: caller, Target: caller,
		Membership: entity.MembershipLeave, Reason: reason,
	})
}

func (s *srv) Invite(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error {
	if err := s.requireLocal(ctx, scope, target); err != nil {
		return err
	}
	change := entity.MembershipChange{
		RoomID: roomID, Sender: caller, Target: target,
		Membership: entity.MembershipInvite, Reason: reason,
	}
	if err := s.applyProfile(ctx, scope, target, &change, nil, nil); err != nil {
		return err
	}
	return s.transition(ctx, scope, change)
}

func (s *srv) Kick(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error {
	state, err := s.roomState(ctx, scope, roomID)
	if err != nil {
		return err
	}
	switch state.Membership(target) {
	case entity.MembershipJoin, entity.MembershipInvite, entity.MembershipKnock:
	default:
		return entity.ErrAuthFailed
	}
	return s.transition(ctx, scope, entity.MembershipChange{
		RoomID: roomID, Sender: caller, Target: target,
		Membership: entity.MembershipLeave, Reason: reason,
	})
}

func (s *srv) Ban(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error {
	return s.transition(ctx, scope, entity.MembershipChange{
		RoomID: roomID, Sender: caller, Target: target,
		Membership: entity.MembershipBan, Reason: reason,
	})
}

func (s *srv) Unban(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error {
	state, err := s.roomState(ctx, scope, roomID)
	if err != nil {
		return err
	}
	if state.Membership(target) != entity.MembershipBan {
		return entity.ErrNotBanned
	}
	return s.transition(ctx, scope, entity.MembershipChange{
		RoomID: roomID, Sender: caller, Target: target,
		Membership: entity.MembershipLeave, Reason: reason,
	})
}

func (s *srv) Forget(ctx context.Context, scope entity.TenantScope, caller, roomID string) error {
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return err
	}
	membership, err := s.members.Get(ctx, room.NID, caller)
	if err != nil {
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return entity.ErrNotInRoom
		}
		return err
	}
	switch membership.Membership {
	case entity.MembershipLeave, entity.MembershipBan:
	default:
		return entity.ErrNotForgettable
	}
	return s.members.SetForgotten(ctx, room.NID, caller, true)
}

func (s *srv) Members(ctx context.Context, scope entity.TenantScope, caller, roomID string, filter entity.MembersFilter) ([]entity.Event, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	state, err := s.membersState(ctx, scope, caller, roomID, filter.At)
	if err != nil {
		return nil, err
	}

	var out []entity.Event
	for _, key := range state.Keys() {
		if key.Type != entity.EventTypeMember {
			continue
		}
		e := state[key]
		membership, _ := e.Content()["membership"].(string)
		if filter.Keeps(membership) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *srv) membersState(ctx context.Context, scope entity.TenantScope, caller, roomID, at string) (entity.StateMap, error) {
	if at == "" {
		return s.readableState(ctx, scope, caller, roomID)
	}
	if _, err := s.readableRoom(ctx, scope, caller, roomID); err != nil {
		return nil, err
	}
	position, err := s.at(ctx, roomID, at)
	if err != nil {
		return nil, err
	}
	if position == nil {
		return s.readableState(ctx, scope, caller, roomID)
	}
	page, err := s.events.Page(ctx, roomID, entity.PageRequest{
		From: position, Backwards: true, Inclusive: true, Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(page) == 0 {
		return entity.StateMap{}, nil
	}
	return s.events.StateAfter(ctx, page[0].NID)
}

func (s *srv) transition(ctx context.Context, scope entity.TenantScope, change entity.MembershipChange) error {
	if err := change.Validate(); err != nil {
		return err
	}
	if _, err := s.room(ctx, scope, change.RoomID); err != nil {
		return err
	}
	_, err := s.events.Send(ctx, scope, change.Event())
	return err
}

func (s *srv) resolve(ctx context.Context, scope entity.TenantScope, roomIDOrAlias string) (string, error) {
	if strings.HasPrefix(roomIDOrAlias, "#") {
		found, err := s.ResolveAlias(ctx, scope, roomIDOrAlias)
		if err != nil {
			return "", err
		}
		return found.RoomID, nil
	}
	return roomIDOrAlias, nil
}

func (s *srv) applyProfile(ctx context.Context, scope entity.TenantScope, userID string, change *entity.MembershipChange, displayName, avatarURL *string) error {
	profile, err := s.users.Get(ctx, scope, userID)
	if err != nil && !errors.Is(err, entity.ErrUserNotFound) {
		return err
	}
	change.DisplayName = profile.DisplayName
	change.AvatarURL = profile.AvatarURL
	if displayName != nil {
		change.DisplayName = *displayName
	}
	if avatarURL != nil {
		change.AvatarURL = *avatarURL
	}
	return nil
}

func (s *srv) requireLocal(ctx context.Context, scope entity.TenantScope, userID string) error {
	if _, err := s.users.Get(ctx, scope, userID); err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrForeignUser
		}
		return err
	}
	return nil
}

func (s *srv) authoriseRestricted(ctx context.Context, scope entity.TenantScope, state entity.StateMap, caller string, change *entity.MembershipChange) error {
	if !state.Restricted() {
		return nil
	}
	switch state.Membership(caller) {
	case entity.MembershipInvite, entity.MembershipJoin:
		return nil
	}

	admitted, err := s.joinedToAny(ctx, scope, caller, state.AllowedRooms())
	if err != nil {
		return err
	}
	if !admitted {
		return entity.ErrAuthFailed
	}

	version, err := s.version(state)
	if err != nil {
		return err
	}
	levels, err := state.PowerLevels(version)
	if err != nil {
		return err
	}
	for _, member := range state.MembersWith(entity.MembershipJoin) {
		if levels.CanInvite(member) {
			change.AuthorisedBy = member
			return nil
		}
	}
	return entity.ErrCannotGrantJoin
}

func (s *srv) joinedToAny(ctx context.Context, scope entity.TenantScope, userID string, roomIDs []string) (bool, error) {
	if len(roomIDs) == 0 {
		return false, nil
	}
	joined, err := s.members.ListForUser(ctx, scope, userID, entity.MembershipJoin)
	if err != nil {
		return false, err
	}
	for _, m := range joined {
		for _, roomID := range roomIDs {
			if m.RoomID == roomID {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *srv) roomState(ctx context.Context, scope entity.TenantScope, roomID string) (entity.StateMap, error) {
	if _, err := s.room(ctx, scope, roomID); err != nil {
		return nil, err
	}
	return s.events.CurrentState(ctx, roomID)
}
