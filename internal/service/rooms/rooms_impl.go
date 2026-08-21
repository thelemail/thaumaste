package rooms

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

const defaultPublicRoomsLimit = 100

type srv struct {
	events  service.Events
	users   service.Users
	rooms   repository.Room
	aliases repository.Alias
	members repository.RoomMember
	tx      repository.Transactor
}

func New(
	events service.Events,
	users service.Users,
	rooms repository.Room,
	aliases repository.Alias,
	members repository.RoomMember,
	tx repository.Transactor,
) service.Rooms {
	return &srv{events: events, users: users, rooms: rooms, aliases: aliases, members: members, tx: tx}
}

func (s *srv) Create(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, error) {
	in.ServerName = scope.ServerName()
	if in.Preset == "" {
		in.Preset = entity.DefaultPreset(in.Visibility)
	}
	if err := in.Validate(); err != nil {
		return entity.Room{}, err
	}

	creator, err := s.users.Get(ctx, scope, in.Creator)
	if err != nil {
		return entity.Room{}, err
	}
	in.CreatorDisplayName = creator.DisplayName
	in.CreatorAvatarURL = creator.AvatarURL

	var room entity.Room
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		var err error
		room, _, err = s.events.CreateRoom(ctx, scope, in)
		if err != nil {
			return err
		}
		if alias, ok := in.Alias(); ok {
			if err := s.insertAlias(ctx, scope, alias, room.NID, in.Creator); err != nil {
				return err
			}
		}
		if in.Visibility == entity.VisibilityPublic {
			if err := s.rooms.SetVisibility(ctx, room.NID, entity.VisibilityPublic); err != nil {
				return err
			}
			room.Visibility = entity.VisibilityPublic
		}
		return nil
	})
	if err != nil {
		return entity.Room{}, err
	}
	return room, nil
}

func (s *srv) State(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]entity.Event, error) {
	state, err := s.readableState(ctx, scope, caller, roomID)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Event, 0, len(state))
	for _, key := range state.Keys() {
		out = append(out, state[key])
	}
	return out, nil
}

func (s *srv) StateEvent(ctx context.Context, scope entity.TenantScope, caller, roomID, eventType, stateKey string) (entity.Event, error) {
	state, err := s.readableState(ctx, scope, caller, roomID)
	if err != nil {
		return entity.Event{}, err
	}
	e, ok := state.Get(eventType, stateKey)
	if !ok {
		return entity.Event{}, entity.ErrEventNotFound
	}
	return e, nil
}

func (s *srv) SetState(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (string, error) {
	if in.StateKey == nil {
		return "", entity.ErrEventMalformed
	}
	if _, err := s.room(ctx, scope, in.RoomID); err != nil {
		return "", err
	}
	if in.Type == entity.EventTypeCanonicalAlias {
		if err := s.checkCanonicalAlias(ctx, scope, in.RoomID, in.Content); err != nil {
			return "", err
		}
	}
	stored, err := s.events.Send(ctx, scope, in)
	if err != nil {
		return "", err
	}
	return stored.Event.ID(), nil
}

func (s *srv) JoinedMembers(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]entity.RoomMember, error) {
	state, err := s.readableState(ctx, scope, caller, roomID)
	if err != nil {
		return nil, err
	}
	joined := state.MembersWith(entity.MembershipJoin)
	out := make([]entity.RoomMember, 0, len(joined))
	for _, userID := range joined {
		e, _ := state.Get(entity.EventTypeMember, userID)
		content := e.Content()
		name, _ := content["displayname"].(string)
		avatar, _ := content["avatar_url"].(string)
		out = append(out, entity.RoomMember{UserID: userID, DisplayName: name, AvatarURL: avatar})
	}
	return out, nil
}

func (s *srv) JoinedRooms(ctx context.Context, scope entity.TenantScope, userID string) ([]string, error) {
	memberships, err := s.members.ListForUser(ctx, scope, userID, entity.MembershipJoin)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, m.RoomID)
	}
	return out, nil
}

func (s *srv) CreateAlias(ctx context.Context, scope entity.TenantScope, caller, alias, roomID string) error {
	if _, err := entity.ParseLocalAlias(alias, scope.ServerName()); err != nil {
		return err
	}
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return err
	}
	state, err := s.events.CurrentState(ctx, roomID)
	if err != nil {
		return err
	}
	if state.Membership(caller) != entity.MembershipJoin {
		return entity.ErrNotInRoom
	}
	return s.insertAlias(ctx, scope, alias, room.NID, caller)
}

func (s *srv) DeleteAlias(ctx context.Context, scope entity.TenantScope, caller, alias string) error {
	if _, err := entity.ParseLocalAlias(alias, scope.ServerName()); err != nil {
		return err
	}
	existing, err := s.ResolveAlias(ctx, scope, alias)
	if err != nil {
		return err
	}
	if existing.Creator != caller {
		state, err := s.events.CurrentState(ctx, existing.RoomID)
		if err != nil {
			return err
		}
		if err := s.checkAliasAuthority(state, caller); err != nil {
			return err
		}
	}
	if err := s.aliases.Delete(ctx, scope, alias); err != nil {
		if errors.Is(err, repository.ErrAliasNotFound) {
			return entity.ErrAliasNotFound
		}
		return err
	}
	return nil
}

func (s *srv) ResolveAlias(ctx context.Context, scope entity.TenantScope, alias string) (entity.RoomAlias, error) {
	if _, err := entity.ParseLocalAlias(alias, scope.ServerName()); err != nil {
		return entity.RoomAlias{}, err
	}
	found, err := s.aliases.Get(ctx, scope, alias)
	if err != nil {
		if errors.Is(err, repository.ErrAliasNotFound) {
			return entity.RoomAlias{}, entity.ErrAliasNotFound
		}
		return entity.RoomAlias{}, err
	}
	return found, nil
}

func (s *srv) Aliases(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]string, error) {
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return nil, err
	}
	state, err := s.events.CurrentState(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if state.Membership(caller) != entity.MembershipJoin && !state.WorldReadable() {
		return nil, entity.ErrNotInRoom
	}
	found, err := s.aliases.ListForRoom(ctx, scope, room.NID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(found))
	for _, a := range found {
		out = append(out, a.Alias)
	}
	return out, nil
}

func (s *srv) SetVisibility(ctx context.Context, scope entity.TenantScope, caller, roomID, visibility string) error {
	if visibility != entity.VisibilityPublic && visibility != entity.VisibilityPrivate {
		return entity.ErrInvalidVisibility
	}
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return err
	}
	state, err := s.events.CurrentState(ctx, roomID)
	if err != nil {
		return err
	}
	if err := s.checkAliasAuthority(state, caller); err != nil {
		return err
	}
	return s.rooms.SetVisibility(ctx, room.NID, visibility)
}

func (s *srv) Visibility(ctx context.Context, scope entity.TenantScope, roomID string) (string, error) {
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return "", err
	}
	return room.Visibility, nil
}

func (s *srv) PublicRooms(ctx context.Context, scope entity.TenantScope, filter service.PublicRoomsFilter) (service.PublicRooms, error) {
	rooms, err := s.rooms.ListPublic(ctx, scope)
	if err != nil {
		return service.PublicRooms{}, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > defaultPublicRoomsLimit {
		limit = defaultPublicRoomsLimit
	}

	out := make([]entity.PublicRoom, 0, len(rooms))
	for _, room := range rooms {
		state, err := s.events.CurrentState(ctx, room.RoomID)
		if err != nil {
			return service.PublicRooms{}, err
		}
		joined, err := s.members.CountForRoom(ctx, room.NID, entity.MembershipJoin)
		if err != nil {
			return service.PublicRooms{}, err
		}
		summary := entity.PublicRoom{
			RoomID:           room.RoomID,
			Name:             state.Name(),
			Topic:            state.Topic(),
			CanonicalAlias:   state.CanonicalAlias(),
			AvatarURL:        state.AvatarURL(),
			RoomType:         state.RoomType(),
			JoinRule:         state.JoinRule(),
			NumJoinedMembers: joined,
			WorldReadable:    state.WorldReadable(),
			GuestCanJoin:     state.GuestCanJoin(),
		}
		if summary.Matches(filter.SearchTerm) {
			out = append(out, summary)
		}
	}

	total := len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return service.PublicRooms{Chunk: out, TotalRooms: total}, nil
}

func (s *srv) insertAlias(ctx context.Context, scope entity.TenantScope, alias string, roomNID int64, creator string) error {
	in := entity.NewRoomAlias{
		TenantID: scope.ID(),
		Alias:    alias,
		RoomNID:  roomNID,
		Creator:  creator,
	}
	if err := in.Validate(); err != nil {
		return err
	}
	if _, err := s.aliases.Create(ctx, in); err != nil {
		if errors.Is(err, repository.ErrAliasInUse) {
			return entity.ErrAliasInUse
		}
		return err
	}
	return nil
}

func (s *srv) checkCanonicalAlias(ctx context.Context, scope entity.TenantScope, roomID string, content map[string]any) error {
	for _, alias := range entity.CanonicalAliases(content) {
		found, err := s.ResolveAlias(ctx, scope, alias)
		if errors.Is(err, entity.ErrAliasNotFound) {
			return entity.ErrAliasUnusable
		}
		if err != nil {
			return err
		}
		if found.RoomID != roomID {
			return entity.ErrAliasUnusable
		}
	}
	return nil
}

func (s *srv) checkAliasAuthority(state entity.StateMap, caller string) error {
	version, err := s.version(state)
	if err != nil {
		return err
	}
	levels, err := state.PowerLevels(version)
	if err != nil {
		return err
	}
	if !levels.CanSend(caller, entity.EventTypeCanonicalAlias, true) {
		return entity.ErrAuthFailed
	}
	return nil
}

func (s *srv) version(state entity.StateMap) (entity.RoomVersion, error) {
	create, ok := state.Create()
	if !ok {
		return entity.RoomVersion{}, entity.ErrStateMissing
	}
	id, _ := create.Content()["room_version"].(string)
	return entity.LookupRoomVersion(entity.RoomVersionID(id))
}

func (s *srv) room(ctx context.Context, scope entity.TenantScope, roomID string) (entity.Room, error) {
	room, err := s.events.Room(ctx, roomID)
	if err != nil {
		return entity.Room{}, err
	}
	if room.TenantID != scope.ID() {
		return entity.Room{}, entity.ErrRoomNotFound
	}
	return room, nil
}

func (s *srv) readableState(ctx context.Context, scope entity.TenantScope, caller, roomID string) (entity.StateMap, error) {
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return nil, err
	}
	state, err := s.events.CurrentState(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if state.Membership(caller) == entity.MembershipJoin || state.WorldReadable() {
		return state, nil
	}

	membership, err := s.members.Get(ctx, room.NID, caller)
	if err != nil {
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return nil, entity.ErrNotInRoom
		}
		return nil, err
	}
	switch membership.Membership {
	case entity.MembershipLeave, entity.MembershipBan:
		return s.events.StateAfter(ctx, membership.EventNID)
	default:
		return nil, entity.ErrNotInRoom
	}
}
