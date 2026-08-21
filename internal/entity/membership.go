package entity

import (
	"errors"
	"slices"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

var (
	ErrUnknownMembership = errors.New("entity: unknown membership")
	ErrCannotGrantJoin   = errors.New("entity: no local user can authorise this join")
	ErrNotForgettable    = errors.New("entity: the room has not been left")
	ErrForeignUser       = errors.New("entity: user belongs to another server")
	ErrNotBanned         = errors.New("entity: the user is not banned")
)

const MaxReasonBytes = 1024

type MembershipChange struct {
	RoomID       string
	Sender       string
	Target       string
	Membership   string
	Reason       string
	DisplayName  string
	AvatarURL    string
	AuthorisedBy string
}

func (m MembershipChange) Validate() error {
	if err := validation.ValidateStruct(&m,
		validation.Field(&m.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&m.Reason, validation.Length(0, MaxReasonBytes)),
		validation.Field(&m.DisplayName, validation.Length(0, MaxDisplayNameSize)),
		validation.Field(&m.AvatarURL, validation.Length(0, MaxAvatarURLSize)),
	); err != nil {
		return err
	}
	if !isUserID(m.Sender) || !isUserID(m.Target) {
		return ErrInvalidUsername
	}
	if !slices.Contains(memberships, m.Membership) {
		return ErrUnknownMembership
	}
	if m.AuthorisedBy != "" && !isUserID(m.AuthorisedBy) {
		return ErrInvalidUsername
	}
	return nil
}

func (m MembershipChange) Content() map[string]any {
	content := map[string]any{"membership": m.Membership}
	if m.Reason != "" {
		content["reason"] = m.Reason
	}
	if m.Membership == MembershipJoin || m.Membership == MembershipInvite || m.Membership == MembershipKnock {
		content["displayname"] = m.DisplayName
		content["avatar_url"] = m.AvatarURL
	}
	if m.AuthorisedBy != "" {
		content["join_authorised_via_users_server"] = m.AuthorisedBy
	}
	return content
}

func (m MembershipChange) Event() NewEvent {
	target := m.Target
	return NewEvent{
		RoomID:   m.RoomID,
		Type:     EventTypeMember,
		StateKey: &target,
		Sender:   m.Sender,
		Content:  m.Content(),
	}
}

type MembersFilter struct {
	Membership    string
	NotMembership string
	At            string
}

func (f MembersFilter) Validate() error {
	for _, value := range []string{f.Membership, f.NotMembership} {
		if value != "" && !slices.Contains(memberships, value) {
			return ErrUnknownMembership
		}
	}
	return nil
}

func (f MembersFilter) Keeps(membership string) bool {
	if f.Membership != "" && membership != f.Membership {
		return false
	}
	if f.NotMembership != "" && membership == f.NotMembership {
		return false
	}
	return true
}
