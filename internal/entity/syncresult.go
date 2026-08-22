package entity

type OptionalString struct {
	Present bool
	Value   string
}

func SetString(value string) OptionalString { return OptionalString{Present: true, Value: value} }

func ClearString() OptionalString { return OptionalString{Present: true} }

func (o OptionalString) Cleared() bool { return o.Present && o.Value == "" }

type OptionalInt struct {
	Present bool
	Value   int
}

func SetInt(value int) OptionalInt { return OptionalInt{Present: true, Value: value} }

type Hero struct {
	UserID      string
	DisplayName string
	AvatarURL   string
}

type ListResult struct {
	Count int
}

type RoomResult struct {
	Initial          bool
	Membership       string
	Lists            []string
	BumpStamp        int64
	Name             OptionalString
	Avatar           OptionalString
	Heroes           []Hero
	HasHeroes        bool
	JoinedCount      OptionalInt
	InvitedCount     OptionalInt
	RequiredState    []ClientEvent
	Timeline         []ClientEvent
	StrippedState    []Event
	PrevBatch        string
	Limited          bool
	NumLive          int
	ExpandedTimeline bool
}

type SyncResult struct {
	Pos        SyncPosition
	Lists      map[string]ListResult
	Rooms      map[string]RoomResult
	Extensions SyncExtensions
}
