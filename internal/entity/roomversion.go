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

type RoomVersion struct {
	ID                 RoomVersionID
	EventIDFormat      EventIDFormat
	RoomIDFormat       RoomIDFormat
	RedactionAlgorithm RedactionAlgorithm
	StateResolution    StateResolution

	CreateInAuthEvents bool

	CreateCarriesRoomID bool

	AdditionalCreators bool

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
