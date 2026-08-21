package entity

import (
	"errors"
	"fmt"
	"slices"
)

var (
	ErrAuthFailed        = errors.New("entity: event is not authorised")
	ErrAuthEventsInvalid = errors.New("entity: auth_events do not match the selection rules")
)

func authFailure(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAuthFailed, fmt.Sprintf(format, args...))
}

// SelectAuthEvents names the state that gives the sender permission to send this event. From v12
// the create event is deliberately absent: the room ID is the create event's own ID, so listing it
// again is redundant and is rejected rather than ignored.
func SelectAuthEvents(e EventBuilder, state StateMap) []string {
	if e.Type == EventTypeCreate {
		return []string{}
	}

	var out []string
	add := func(eventType, stateKey string) {
		if found, ok := state.Get(eventType, stateKey); ok && !slices.Contains(out, found.ID()) {
			out = append(out, found.ID())
		}
	}

	if e.Version.CreateInAuthEvents {
		add(EventTypeCreate, "")
	}
	add(EventTypePowerLevels, "")
	add(EventTypeMember, e.Sender)

	if e.Type == EventTypeMember && e.StateKey != nil {
		add(EventTypeMember, *e.StateKey)

		membership, _ := e.Content["membership"].(string)
		switch membership {
		case MembershipJoin, MembershipInvite, MembershipKnock:
			add(EventTypeJoinRules, "")
		}
		if membership == MembershipInvite {
			if token := thirdPartyToken(e.Content); token != "" {
				add(EventTypeThirdPartyInvite, token)
			}
		}
		if membership == MembershipJoin {
			if via, ok := e.Content["join_authorised_via_users_server"].(string); ok && via != "" {
				add(EventTypeMember, via)
			}
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func thirdPartyToken(content map[string]any) string {
	invite, _ := content["third_party_invite"].(map[string]any)
	signed, _ := invite["signed"].(map[string]any)
	token, _ := signed["token"].(string)
	return token
}

func Authorise(e Event, state StateMap, version RoomVersion) error {
	if e.Type() == EventTypeCreate {
		return authoriseCreate(e, version)
	}

	create, ok := state.Create()
	if !ok {
		return authFailure("the room has no create event")
	}
	if err := checkRoomID(e, create, version); err != nil {
		return err
	}
	if err := checkFederate(e, create); err != nil {
		return err
	}

	levels, err := state.PowerLevels(version)
	if err != nil {
		return err
	}

	if e.Type() == EventTypeMember {
		return authoriseMember(e, state, levels, version)
	}

	if state.Membership(e.Sender()) != MembershipJoin {
		return authFailure("sender %s is not joined", e.Sender())
	}

	if e.Type() == EventTypeThirdPartyInvite {
		if !levels.UserLevel(e.Sender()).AtLeast(Power(levels.Invite)) {
			return authFailure("sender cannot issue third party invites")
		}
		return nil
	}

	stateKey, isState := e.StateKey()
	if !levels.UserLevel(e.Sender()).AtLeast(levels.EventLevel(e.Type(), isState)) {
		return authFailure("sender cannot send %s", e.Type())
	}
	if isState && len(stateKey) > 0 && stateKey[0] == '@' && stateKey != e.Sender() {
		return authFailure("state key %s does not belong to the sender", stateKey)
	}

	if e.Type() == EventTypePowerLevels {
		return authorisePowerLevels(e, state, levels, version)
	}
	return nil
}

func authoriseCreate(e Event, version RoomVersion) error {
	if len(e.PrevEvents()) != 0 {
		return authFailure("the create event cannot have prev_events")
	}
	_, hasRoomID := e.Fields()["room_id"]
	if hasRoomID != version.CreateCarriesRoomID {
		return authFailure("create event room_id present=%v under version %s", hasRoomID, version.ID)
	}
	declared, _ := e.Content()["room_version"].(string)
	if declared == "" {
		declared = string(RoomVersion11)
	}
	if RoomVersionID(declared) != version.ID {
		return fmt.Errorf("%w: create declares %q", ErrUnsupportedRoomVersion, declared)
	}
	if version.AdditionalCreators {
		extra, present := e.Content()["additional_creators"]
		if present {
			list, ok := extra.([]any)
			if !ok {
				return authFailure("additional_creators is not an array")
			}
			for _, item := range list {
				id, ok := item.(string)
				if !ok || !isUserID(id) {
					return authFailure("additional_creators holds a value that is not a user id")
				}
			}
		}
	}
	return nil
}

func checkRoomID(e Event, create Event, version RoomVersion) error {
	if version.RoomIDFormat != RoomIDFormatCreateEventHash {
		return nil
	}
	if e.RoomID() != "!"+create.ID()[1:] {
		return authFailure("room id %s does not name the create event", e.RoomID())
	}
	return nil
}

func checkFederate(e Event, create Event) error {
	federate, present := create.Content()["m.federate"].(bool)
	if !present || federate {
		return nil
	}
	senderDomain, err := SenderDomain(e.Sender())
	if err != nil {
		return err
	}
	createDomain, err := SenderDomain(create.Sender())
	if err != nil {
		return err
	}
	if senderDomain != createDomain {
		return authFailure("the room does not federate")
	}
	return nil
}

func authoriseMember(e Event, state StateMap, levels PowerLevels, version RoomVersion) error {
	target, ok := e.StateKey()
	if !ok {
		return authFailure("membership event has no state key")
	}
	membership, _ := e.Content()["membership"].(string)
	if membership == "" {
		return authFailure("membership event has no membership")
	}

	senderMembership := state.Membership(e.Sender())
	targetMembership := state.Membership(target)

	switch membership {
	case MembershipJoin:
		return authoriseJoin(e, state, target, targetMembership, version)
	case MembershipInvite:
		return authoriseInvite(e, levels, senderMembership, targetMembership, target)
	case MembershipLeave:
		return authoriseLeave(e, levels, target, senderMembership, targetMembership)
	case MembershipBan:
		if senderMembership != MembershipJoin {
			return authFailure("sender is not joined")
		}
		if !levels.UserLevel(e.Sender()).AtLeast(Power(levels.Ban)) {
			return authFailure("sender cannot ban")
		}
		if !levels.UserLevel(e.Sender()).GreaterThan(levels.UserLevel(target)) {
			return authFailure("sender does not outrank the target")
		}
		return nil
	case MembershipKnock:
		rule := state.JoinRule()
		if rule != JoinRuleKnock && rule != JoinRuleKnockRestricted {
			return authFailure("the room does not accept knocks")
		}
		if target != e.Sender() {
			return authFailure("a knock must be for the sender")
		}
		switch targetMembership {
		case MembershipBan, MembershipInvite, MembershipJoin:
			return authFailure("cannot knock while %s", targetMembership)
		}
		return nil
	default:
		return authFailure("unknown membership %q", membership)
	}
}

func authoriseJoin(e Event, state StateMap, target, targetMembership string, version RoomVersion) error {
	create, hasCreate := state.Create()
	onlyCreate := len(state) == 1 && hasCreate
	if onlyCreate && len(e.PrevEvents()) == 1 && target == create.Sender() {
		return nil
	}
	if target != e.Sender() {
		return authFailure("a join must be for the sender")
	}
	if targetMembership == MembershipBan {
		return authFailure("the user is banned")
	}

	switch rule := state.JoinRule(); rule {
	case JoinRuleInvite, JoinRuleKnock:
		if targetMembership == MembershipInvite || targetMembership == MembershipJoin {
			return nil
		}
		return authFailure("the room is invite only")
	case JoinRuleRestricted, JoinRuleKnockRestricted:
		if targetMembership == MembershipInvite || targetMembership == MembershipJoin {
			return nil
		}
		via, _ := e.Content()["join_authorised_via_users_server"].(string)
		if via == "" {
			return authFailure("a restricted join needs an authorising user")
		}
		if state.Membership(via) != MembershipJoin {
			return authFailure("the authorising user is not joined")
		}
		levels, err := state.PowerLevels(version)
		if err != nil {
			return err
		}
		if !levels.UserLevel(via).AtLeast(Power(levels.Invite)) {
			return authFailure("the authorising user cannot invite")
		}
		return nil
	case JoinRulePublic:
		return nil
	default:
		return authFailure("join rule %q does not permit joining", rule)
	}
}

func authoriseInvite(e Event, levels PowerLevels, senderMembership, targetMembership, target string) error {
	if thirdPartyToken(e.Content()) != "" {
		if senderMembership != MembershipJoin {
			return authFailure("sender is not joined")
		}
		if targetMembership == MembershipJoin || targetMembership == MembershipBan {
			return authFailure("the target is already %s", targetMembership)
		}
		return nil
	}
	if senderMembership != MembershipJoin {
		return authFailure("sender is not joined")
	}
	if targetMembership == MembershipJoin || targetMembership == MembershipBan {
		return authFailure("the target is already %s", targetMembership)
	}
	if !levels.UserLevel(e.Sender()).AtLeast(Power(levels.Invite)) {
		return authFailure("sender cannot invite")
	}
	_ = target
	return nil
}

func authoriseLeave(e Event, levels PowerLevels, target, senderMembership, targetMembership string) error {
	if target == e.Sender() {
		switch senderMembership {
		case MembershipInvite, MembershipJoin, MembershipKnock:
			return nil
		}
		return authFailure("cannot leave while %q", senderMembership)
	}
	if senderMembership != MembershipJoin {
		return authFailure("sender is not joined")
	}
	if targetMembership == MembershipBan && !levels.UserLevel(e.Sender()).AtLeast(Power(levels.Ban)) {
		return authFailure("sender cannot unban")
	}
	if !levels.UserLevel(e.Sender()).AtLeast(Power(levels.Kick)) {
		return authFailure("sender cannot kick")
	}
	if !levels.UserLevel(e.Sender()).GreaterThan(levels.UserLevel(target)) {
		return authFailure("sender does not outrank the target")
	}
	return nil
}

func authorisePowerLevels(e Event, state StateMap, current PowerLevels, version RoomVersion) error {
	proposed, err := ParsePowerLevels(e.Content(), version, state.Creators(version))
	if err != nil {
		return err
	}
	if _, exists := state.Get(EventTypePowerLevels, ""); !exists {
		return nil
	}

	sender := current.UserLevel(e.Sender())

	basics := []struct {
		name          string
		before, after int64
	}{
		{"users_default", current.UsersDefault, proposed.UsersDefault},
		{"events_default", current.EventsDefault, proposed.EventsDefault},
		{"state_default", current.StateDefault, proposed.StateDefault},
		{"ban", current.Ban, proposed.Ban},
		{"redact", current.Redact, proposed.Redact},
		{"kick", current.Kick, proposed.Kick},
		{"invite", current.Invite, proposed.Invite},
	}
	for _, b := range basics {
		if b.before == b.after {
			continue
		}
		if !sender.AtLeast(Power(b.before)) || !sender.AtLeast(Power(b.after)) {
			return authFailure("sender cannot change %s", b.name)
		}
	}

	if err := checkLevelTable(sender, current.Events, proposed.Events, "events"); err != nil {
		return err
	}
	if err := checkLevelTable(sender, current.Notifications, proposed.Notifications, "notifications"); err != nil {
		return err
	}
	return checkUserTable(sender, e.Sender(), current, proposed)
}

func checkLevelTable(sender PowerLevel, before, after map[string]int64, name string) error {
	for key, old := range before {
		next, still := after[key]
		if still && next == old {
			continue
		}
		if !sender.AtLeast(Power(old)) {
			return authFailure("sender cannot change %s.%s", name, key)
		}
	}
	for key, next := range after {
		if old, existed := before[key]; existed && old == next {
			continue
		}
		if !sender.AtLeast(Power(next)) {
			return authFailure("sender cannot set %s.%s", name, key)
		}
	}
	return nil
}

func checkUserTable(sender PowerLevel, senderID string, current, proposed PowerLevels) error {
	for user, old := range current.Users {
		next, still := proposed.Users[user]
		if still && next == old {
			continue
		}
		if user == senderID {
			continue
		}
		if !sender.GreaterThan(Power(old)) {
			return authFailure("sender cannot change the level of %s", user)
		}
	}
	for user, next := range proposed.Users {
		if old, existed := current.Users[user]; existed && old == next {
			continue
		}
		if !sender.AtLeast(Power(next)) {
			return authFailure("sender cannot grant %d to %s", next, user)
		}
	}
	return nil
}

func CheckAuthEvents(e Event, declared []Event, version RoomVersion) error {
	seen := map[StateKey]bool{}
	for _, a := range declared {
		stateKey, ok := a.StateKey()
		if !ok {
			return fmt.Errorf("%w: %s is not a state event", ErrAuthEventsInvalid, a.ID())
		}
		key := StateKey{Type: a.Type(), StateKey: stateKey}
		if seen[key] {
			return fmt.Errorf("%w: duplicate %s", ErrAuthEventsInvalid, key.Type)
		}
		seen[key] = true

		if a.Type() == EventTypeCreate && !version.CreateInAuthEvents {
			return fmt.Errorf("%w: %s must not be selected under version %s",
				ErrAuthEventsInvalid, EventTypeCreate, version.ID)
		}
		if a.RoomID() != "" && e.RoomID() != "" && a.RoomID() != e.RoomID() {
			return fmt.Errorf("%w: %s belongs to another room", ErrAuthEventsInvalid, a.ID())
		}
	}
	if version.CreateInAuthEvents && e.Type() != EventTypeCreate {
		if !seen[StateKey{Type: EventTypeCreate}] {
			return fmt.Errorf("%w: %s is required under version %s",
				ErrAuthEventsInvalid, EventTypeCreate, version.ID)
		}
	}
	return nil
}
