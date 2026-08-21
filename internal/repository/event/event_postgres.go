package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/jackc/pgx/v5/pgconn"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const uniqueViolation = "23505"

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Event {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, in entity.NewStoredEvent) (entity.StoredEvent, error) {
	exec := r.db.Querier(ctx)

	typeNID, err := r.internType(ctx, in.Event.Type())
	if err != nil {
		return entity.StoredEvent{}, err
	}
	row := dbpg.Event{
		RoomNid:             in.RoomNID,
		EventID:             in.Event.ID(),
		EventTypeNid:        typeNID,
		Sender:              in.Event.Sender(),
		SenderIsLocal:       in.SenderIsLocal,
		Depth:               in.Event.Depth(),
		StreamOrdering:      in.StreamOrdering,
		TopologicalOrdering: in.TopologicalOrdering,
		InstanceName:        in.InstanceName,
		OriginServerTS:      in.Event.OriginServerTS(),
		Disposition:         string(in.Disposition),
		EventJSON:           in.Event.JSON(),
	}
	if stateKey, ok := in.Event.StateKey(); ok {
		stateKeyNID, err := r.internStateKey(ctx, stateKey)
		if err != nil {
			return entity.StoredEvent{}, err
		}
		row.EventStateKeyNid = null.Int64From(stateKeyNID)
	}
	if in.StateSnapshotNID != 0 {
		row.StateSnapshotNid = null.Int64From(in.StateSnapshotNID)
	}

	if err := row.Insert(ctx, exec, boil.Infer()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return entity.StoredEvent{}, repository.ErrEventExists
		}
		return entity.StoredEvent{}, fmt.Errorf("repository: insert event: %w", err)
	}

	if err := r.insertEdges(ctx, in.RoomNID, row.EventNid, in.Event); err != nil {
		return entity.StoredEvent{}, err
	}

	return entity.StoredEvent{
		NID:                 row.EventNid,
		RoomNID:             in.RoomNID,
		Event:               in.Event,
		SenderIsLocal:       in.SenderIsLocal,
		StreamOrdering:      in.StreamOrdering,
		TopologicalOrdering: in.TopologicalOrdering,
		InstanceName:        in.InstanceName,
		StateSnapshotNID:    in.StateSnapshotNID,
		Disposition:         in.Disposition,
	}, nil
}

func (r *repo) insertEdges(ctx context.Context, roomNID, eventNID int64, e entity.Event) error {
	exec := r.db.Querier(ctx)
	for _, parent := range e.PrevEvents() {
		row := dbpg.EventPrevEdge{RoomNid: roomNID, ChildNid: eventNID, ParentID: parent}
		if err := row.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("repository: insert prev edge: %w", err)
		}
	}
	for _, parent := range e.AuthEvents() {
		row := dbpg.EventAuthEdge{RoomNid: roomNID, ChildNid: eventNID, ParentID: parent}
		if err := row.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("repository: insert auth edge: %w", err)
		}
	}
	return nil
}

func (r *repo) GetByEventID(ctx context.Context, eventID string) (entity.StoredEvent, error) {
	row, err := dbpg.Events(
		dbpg.EventWhere.EventID.EQ(eventID),
		qm.Load(dbpg.EventRels.RoomNidRoom),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.StoredEvent{}, repository.ErrEventNotFound
		}
		return entity.StoredEvent{}, fmt.Errorf("repository: get event: %w", err)
	}
	return toStoredEvent(row)
}

func (r *repo) GetManyByEventID(ctx context.Context, eventIDs []string) ([]entity.StoredEvent, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	rows, err := dbpg.Events(
		dbpg.EventWhere.EventID.IN(eventIDs),
		qm.Load(dbpg.EventRels.RoomNidRoom),
		qm.OrderBy(dbpg.EventColumns.EventNid),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list events: %w", err)
	}
	return toStoredEvents(rows)
}

func (r *repo) ListForRoom(ctx context.Context, roomNID int64) ([]entity.StoredEvent, error) {
	rows, err := dbpg.Events(
		dbpg.EventWhere.RoomNid.EQ(roomNID),
		qm.Load(dbpg.EventRels.RoomNidRoom),
		qm.OrderBy(dbpg.EventColumns.TopologicalOrdering+", "+dbpg.EventColumns.StreamOrdering),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list events: %w", err)
	}
	return toStoredEvents(rows)
}

func (r *repo) SetDisposition(ctx context.Context, eventNID int64, disposition entity.Disposition) error {
	row := dbpg.Event{EventNid: eventNID, Disposition: string(disposition)}
	n, err := row.Update(ctx, r.db.Querier(ctx), boil.Whitelist(dbpg.EventColumns.Disposition))
	if err != nil {
		return fmt.Errorf("repository: set disposition: %w", err)
	}
	if n == 0 {
		return repository.ErrEventNotFound
	}
	return nil
}

func (r *repo) SetStateSnapshot(ctx context.Context, eventNID, snapshotNID int64) error {
	row := dbpg.Event{EventNid: eventNID, StateSnapshotNid: null.Int64From(snapshotNID)}
	n, err := row.Update(ctx, r.db.Querier(ctx), boil.Whitelist(dbpg.EventColumns.StateSnapshotNid))
	if err != nil {
		return fmt.Errorf("repository: set state snapshot: %w", err)
	}
	if n == 0 {
		return repository.ErrEventNotFound
	}
	return nil
}

func (r *repo) ParentsOf(ctx context.Context, eventNID int64) ([]string, error) {
	rows, err := dbpg.EventPrevEdges(
		dbpg.EventPrevEdgeWhere.ChildNid.EQ(eventNID),
		qm.OrderBy(dbpg.EventPrevEdgeColumns.ParentID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list prev edges: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ParentID)
	}
	return out, nil
}

func (r *repo) AuthParentsOf(ctx context.Context, eventNID int64) ([]string, error) {
	rows, err := dbpg.EventAuthEdges(
		dbpg.EventAuthEdgeWhere.ChildNid.EQ(eventNID),
		qm.OrderBy(dbpg.EventAuthEdgeColumns.ParentID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list auth edges: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ParentID)
	}
	return out, nil
}

// The interning upserts stay hand-written: sqlboiler's Upsert cannot return the existing row's key
// on conflict without updating it, and these tables are append-only by design.
const (
	internTypeSQL = `
		INSERT INTO event_types (event_type) VALUES ($1)
		ON CONFLICT (event_type) DO UPDATE SET event_type = EXCLUDED.event_type
		RETURNING event_type_nid`
	internStateKeySQL = `
		INSERT INTO event_state_keys (event_state_key) VALUES ($1)
		ON CONFLICT (event_state_key) DO UPDATE SET event_state_key = EXCLUDED.event_state_key
		RETURNING event_state_key_nid`
)

func (r *repo) internType(ctx context.Context, eventType string) (int64, error) {
	var nid int64
	if err := r.db.Querier(ctx).QueryRowContext(ctx, internTypeSQL, eventType).Scan(&nid); err != nil {
		return 0, fmt.Errorf("repository: intern event type: %w", err)
	}
	return nid, nil
}

func (r *repo) internStateKey(ctx context.Context, stateKey string) (int64, error) {
	var nid int64
	if err := r.db.Querier(ctx).QueryRowContext(ctx, internStateKeySQL, stateKey).Scan(&nid); err != nil {
		return 0, fmt.Errorf("repository: intern state key: %w", err)
	}
	return nid, nil
}

func toStoredEvents(rows dbpg.EventSlice) ([]entity.StoredEvent, error) {
	out := make([]entity.StoredEvent, 0, len(rows))
	for _, row := range rows {
		converted, err := toStoredEvent(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func toStoredEvent(row *dbpg.Event) (entity.StoredEvent, error) {
	if row.R == nil || row.R.RoomNidRoom == nil {
		return entity.StoredEvent{}, fmt.Errorf("repository: event %s has no room loaded", row.EventID)
	}
	version, err := entity.LookupRoomVersion(entity.RoomVersionID(row.R.RoomNidRoom.RoomVersion))
	if err != nil {
		return entity.StoredEvent{}, err
	}
	parsed, err := entity.NewEventFromJSON(row.EventJSON, version)
	if err != nil {
		return entity.StoredEvent{}, fmt.Errorf("repository: read stored event: %w", err)
	}
	return entity.StoredEvent{
		NID:                 row.EventNid,
		RoomNID:             row.RoomNid,
		Event:               parsed,
		SenderIsLocal:       row.SenderIsLocal,
		StreamOrdering:      row.StreamOrdering,
		TopologicalOrdering: row.TopologicalOrdering,
		InstanceName:        row.InstanceName,
		StateSnapshotNID:    row.StateSnapshotNid.Int64,
		Disposition:         entity.Disposition(row.Disposition),
	}, nil
}
