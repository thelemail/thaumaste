package entity

import "errors"

const (
	RelReplace    = "m.replace"
	RelThread     = "m.thread"
	RelAnnotation = "m.annotation"
	RelReference  = "m.reference"

	EventTypeReaction = "m.reaction"

	MaxRelationTypeBytes   = 255
	MaxAggregationKeyBytes = 255
)

const (
	relatesToKey  = "m.relates_to"
	newContentKey = "m.new_content"
	redactsKey    = "redacts"
)

var (
	ErrDuplicateAnnotation = errors.New("entity: an identical annotation already exists")
	ErrThreadTargetRelated = errors.New("entity: a thread cannot start from an event that is itself related")
	ErrRelationTypeUnknown = errors.New("entity: unknown relation type")
)

type Relation struct {
	ParentID string
	RelType  string
	Key      string
}

func ParseRelation(content map[string]any) (Relation, bool) {
	relates, ok := content[relatesToKey].(map[string]any)
	if !ok {
		return Relation{}, false
	}

	relType, _ := relates["rel_type"].(string)
	parentID, _ := relates["event_id"].(string)
	key, _ := relates["key"].(string)

	switch {
	case relType == "" || len(relType) > MaxRelationTypeBytes:
		return Relation{}, false
	case parentID == "" || len(parentID) > MaxEventIDBytes:
		return Relation{}, false
	case len(key) > MaxAggregationKeyBytes:
		return Relation{}, false
	}
	return Relation{ParentID: parentID, RelType: relType, Key: key}, true
}

func RelationOf(e Event) (Relation, bool) {
	if e.IsState() {
		return Relation{}, false
	}
	return ParseRelation(e.Content())
}

func ValidReplacement(parent, child Event) bool {
	switch {
	case parent.RoomID() != child.RoomID():
		return false
	case parent.Sender() != child.Sender():
		return false
	case parent.Type() != child.Type():
		return false
	case parent.IsState() || child.IsState():
		return false
	}
	if relation, ok := ParseRelation(parent.Content()); ok && relation.RelType == RelReplace {
		return false
	}
	_, ok := child.Content()[newContentKey].(map[string]any)
	return ok
}

func MoreRecent(a, b Event) bool {
	if a.OriginServerTS() != b.OriginServerTS() {
		return a.OriginServerTS() > b.OriginServerTS()
	}
	return a.ID() > b.ID()
}

type NewEventRelation struct {
	ChildNID int64
	RoomNID  int64
	ParentID string
	RelType  string
	Sender   string
	Key      string
}

func (n NewEventRelation) Validate() error {
	switch {
	case n.ChildNID == 0 || n.RoomNID == 0:
		return ErrEventMalformed
	case n.ParentID == "" || len(n.ParentID) > MaxEventIDBytes:
		return ErrEventMalformed
	case n.RelType == "" || len(n.RelType) > MaxRelationTypeBytes:
		return ErrRelationTypeUnknown
	case n.Sender == "" || len(n.Sender) > MaxUserIDBytes:
		return ErrEventMalformed
	case len(n.Key) > MaxAggregationKeyBytes:
		return ErrEventMalformed
	default:
		return nil
	}
}

type RelationRef struct {
	ChildNID       int64
	EventID        string
	ParentID       string
	RelType        string
	EventType      string
	Sender         string
	OriginServerTS int64
	Position       Position
	Disposition    Disposition
}

type RelationQuery struct {
	ParentIDs []string
	RelType   string
	EventType string
	From      *Position
	To        *Position
	Backwards bool
	Limit     int
}
