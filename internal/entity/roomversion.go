package entity

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupportedRoomVersion = errors.New("entity: unsupported room version")
	ErrRoomVersionMismatch    = errors.New("entity: event does not match the room version")
)

type RoomVersionID string

const (
	RoomVersion11 RoomVersionID = "11"
	RoomVersion12 RoomVersionID = "12"

	DefaultRoomVersion = RoomVersion12
)

type EventIDFormat int

const (
	// EventIDFormatHash derives the event ID from the reference hash in url-safe base64. Older
	// formats, where the origin server picked the ID, are not implemented: no room this server
	// creates can use one, and a stub would claim support that does not exist.
	EventIDFormatHash EventIDFormat = iota + 1
)

type RoomIDFormat int

const (
	RoomIDFormatOpaqueDomain RoomIDFormat = iota + 1
	RoomIDFormatCreateEventHash
)

type RedactionAlgorithm int

const (
	RedactionAlgorithmV11 RedactionAlgorithm = iota + 1
)

type StateResolution int

const (
	StateResolutionV2 StateResolution = iota + 1
	StateResolutionV21
)

// RoomVersion resolves the version-dependent rules on separate axes rather than through one
// version number. Synapse carries a single "event format version" and says in its own source that
// the term is not in the spec; the axes really are independent, and collapsing them is what makes
// adding a version a rewrite instead of a table entry.
type RoomVersion struct {
	ID                 RoomVersionID
	EventIDFormat      EventIDFormat
	RoomIDFormat       RoomIDFormat
	RedactionAlgorithm RedactionAlgorithm
	StateResolution    StateResolution

	// CreateInAuthEvents is false from v12, where the room ID is the create event's ID and so
	// already names it. Listing it becomes a rejection rather than an omission.
	CreateInAuthEvents bool

	// CreateCarriesRoomID is false from v12, where the create event must not have the field at
	// all. An empty string is not the same as absent: it changes the canonical bytes.
	CreateCarriesRoomID bool

	// AdditionalCreators is v12's array of extra users who count as room creators.
	AdditionalCreators bool

	// CreatorsOutrankPowerLevels gives creators an unbounded power level that m.room.power_levels
	// cannot name or lower.
	CreatorsOutrankPowerLevels bool
}

var roomVersions = map[RoomVersionID]RoomVersion{
	RoomVersion11: {
		ID:                         RoomVersion11,
		EventIDFormat:              EventIDFormatHash,
		RoomIDFormat:               RoomIDFormatOpaqueDomain,
		RedactionAlgorithm:         RedactionAlgorithmV11,
		StateResolution:            StateResolutionV2,
		CreateInAuthEvents:         true,
		CreateCarriesRoomID:        true,
		AdditionalCreators:         false,
		CreatorsOutrankPowerLevels: false,
	},
	RoomVersion12: {
		ID:                         RoomVersion12,
		EventIDFormat:              EventIDFormatHash,
		RoomIDFormat:               RoomIDFormatCreateEventHash,
		RedactionAlgorithm:         RedactionAlgorithmV11,
		StateResolution:            StateResolutionV21,
		CreateInAuthEvents:         false,
		CreateCarriesRoomID:        false,
		AdditionalCreators:         true,
		CreatorsOutrankPowerLevels: true,
	},
}

func LookupRoomVersion(id RoomVersionID) (RoomVersion, error) {
	v, ok := roomVersions[id]
	if !ok {
		return RoomVersion{}, fmt.Errorf("%w: %q", ErrUnsupportedRoomVersion, id)
	}
	return v, nil
}

func SupportedRoomVersions() []RoomVersionID {
	return []RoomVersionID{RoomVersion11, RoomVersion12}
}
