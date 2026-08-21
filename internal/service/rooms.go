package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type PublicRoomsFilter struct {
	SearchTerm string
	Limit      int
}

type PublicRooms struct {
	Chunk      []entity.PublicRoom
	TotalRooms int
}

type JoinInput struct {
	DisplayName *string
	AvatarURL   *string
	Reason      string
}

type Rooms interface {
	Create(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, error)

	Join(ctx context.Context, scope entity.TenantScope, caller, roomIDOrAlias string, in JoinInput) (string, error)
	Knock(ctx context.Context, scope entity.TenantScope, caller, roomIDOrAlias, reason string) (string, error)
	Leave(ctx context.Context, scope entity.TenantScope, caller, roomID, reason string) error
	Invite(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error
	Kick(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error
	Ban(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error
	Unban(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error
	Forget(ctx context.Context, scope entity.TenantScope, caller, roomID string) error

	Members(ctx context.Context, scope entity.TenantScope, caller, roomID string, filter entity.MembersFilter) ([]entity.Event, error)

	State(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]entity.Event, error)
	StateEvent(ctx context.Context, scope entity.TenantScope, caller, roomID, eventType, stateKey string) (entity.Event, error)
	SetState(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (string, error)

	SendMessage(ctx context.Context, scope entity.TenantScope, in entity.NewMessage) (string, error)
	Event(ctx context.Context, scope entity.TenantScope, caller, deviceID, roomID, eventID string) (entity.ClientEvent, error)

	JoinedMembers(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]entity.RoomMember, error)
	JoinedRooms(ctx context.Context, scope entity.TenantScope, userID string) ([]string, error)

	CreateAlias(ctx context.Context, scope entity.TenantScope, caller, alias, roomID string) error
	DeleteAlias(ctx context.Context, scope entity.TenantScope, caller, alias string) error
	ResolveAlias(ctx context.Context, scope entity.TenantScope, alias string) (entity.RoomAlias, error)
	Aliases(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]string, error)

	SetVisibility(ctx context.Context, scope entity.TenantScope, caller, roomID, visibility string) error
	Visibility(ctx context.Context, scope entity.TenantScope, roomID string) (string, error)
	PublicRooms(ctx context.Context, scope entity.TenantScope, filter PublicRoomsFilter) (PublicRooms, error)
}
