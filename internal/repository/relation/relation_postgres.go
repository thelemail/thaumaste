package relation

import (
	"context"
	"errors"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/jackc/pgx/v5/pgconn"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const uniqueViolation = "23505"

const insertRelationSQL = `
	INSERT INTO event_relations
		(child_nid, room_nid, parent_id, rel_type, sender, event_type_nid, aggregation_key)
	SELECT $1, $2, $3, $4, $5, events.event_type_nid, $6
	  FROM events WHERE events.event_nid = $1`

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Relation {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, in entity.NewEventRelation) error {
	if err := in.Validate(); err != nil {
		return err
	}

	result, err := r.db.Querier(ctx).ExecContext(ctx, insertRelationSQL,
		in.ChildNID, in.RoomNID, in.ParentID, in.RelType, in.Sender, in.Key)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return repository.ErrRelationExists
		}
		return fmt.Errorf("repository: insert relation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: insert relation: %w", err)
	}
	if affected == 0 {
		return repository.ErrEventNotFound
	}
	return nil
}

func (r *repo) Delete(ctx context.Context, childNID int64) error {
	_, err := dbpg.EventRelations(
		dbpg.EventRelationWhere.ChildNid.EQ(childNID),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete relation: %w", err)
	}
	return nil
}

type refRow struct {
	ChildNid            int64  `boil:"child_nid"`
	EventID             string `boil:"event_id"`
	ParentID            string `boil:"parent_id"`
	RelType             string `boil:"rel_type"`
	EventType           string `boil:"event_type"`
	Sender              string `boil:"sender"`
	OriginServerTS      int64  `boil:"origin_server_ts"`
	TopologicalOrdering int64  `boil:"topological_ordering"`
	StreamOrdering      int64  `boil:"stream_ordering"`
	Disposition         string `boil:"disposition"`
}

func (r *repo) Find(ctx context.Context, roomNID int64, q entity.RelationQuery) ([]entity.RelationRef, error) {
	if q.ParentIDs != nil && len(q.ParentIDs) == 0 {
		return nil, nil
	}

	mods := []qm.QueryMod{
		qm.Select(
			"event_relations.child_nid",
			"event_relations.parent_id",
			"event_relations.rel_type",
			"event_relations.sender",
			"e.event_id",
			"e.origin_server_ts",
			"e.topological_ordering",
			"e.stream_ordering",
			"e.disposition",
			"t.event_type",
		),
		qm.From("event_relations"),
		qm.InnerJoin("events e on e.event_nid = event_relations.child_nid"),
		qm.InnerJoin("event_types t on t.event_type_nid = event_relations.event_type_nid"),
		qm.Where("event_relations.room_nid = ?", roomNID),
	}
	if len(q.ParentIDs) > 0 {
		mods = append(mods, qm.WhereIn("event_relations.parent_id in ?", asAny(q.ParentIDs)...))
	}
	if q.RelType != "" {
		mods = append(mods, qm.Where("event_relations.rel_type = ?", q.RelType))
	}
	if q.EventType != "" {
		mods = append(mods, qm.Where("t.event_type = ?", q.EventType))
	}
	if q.Backwards {
		mods = append(mods, qm.OrderBy("e.topological_ordering DESC, e.stream_ordering DESC"))
		mods = append(mods, bound(q.From, "<")...)
		mods = append(mods, bound(q.To, ">")...)
	} else {
		mods = append(mods, qm.OrderBy("e.topological_ordering, e.stream_ordering"))
		mods = append(mods, bound(q.From, ">")...)
		mods = append(mods, bound(q.To, "<")...)
	}
	if q.Limit > 0 {
		mods = append(mods, qm.Limit(q.Limit))
	}

	var rows []refRow
	if err := dbpg.NewQuery(mods...).Bind(ctx, r.db.Querier(ctx), &rows); err != nil {
		return nil, fmt.Errorf("repository: find relations: %w", err)
	}

	out := make([]entity.RelationRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, entity.RelationRef{
			ChildNID:       row.ChildNid,
			EventID:        row.EventID,
			ParentID:       row.ParentID,
			RelType:        row.RelType,
			EventType:      row.EventType,
			Sender:         row.Sender,
			OriginServerTS: row.OriginServerTS,
			Position: entity.Position{
				Topological: row.TopologicalOrdering,
				Stream:      row.StreamOrdering,
			},
			Disposition: entity.Disposition(row.Disposition),
		})
	}
	return out, nil
}

func bound(at *entity.Position, operator string) []qm.QueryMod {
	if at == nil {
		return nil
	}
	return []qm.QueryMod{qm.Where(
		"(e.topological_ordering, e.stream_ordering) "+operator+" (?, ?)",
		at.Topological, at.Stream)}
}

func asAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, value := range in {
		out = append(out, value)
	}
	return out
}
