package relation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const uniqueViolation = "23505"

const findRelationsSQL = `
	SELECT er.child_nid, er.parent_id, er.rel_type, er.sender,
	       e.event_id, e.origin_server_ts, e.topological_ordering, e.stream_ordering,
	       e.disposition, t.event_type
	  FROM event_relations er
	  JOIN events e ON e.event_nid = er.child_nid
	  JOIN event_types t ON t.event_type_nid = er.event_type_nid
	 WHERE er.room_nid = $1`

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

func (r *repo) Find(ctx context.Context, roomNID int64, q entity.RelationQuery) ([]entity.RelationRef, error) {
	if q.ParentIDs != nil && len(q.ParentIDs) == 0 {
		return nil, nil
	}

	args := []any{roomNID}
	query := findRelationsSQL
	placeholder := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	if len(q.ParentIDs) > 0 {
		slots := make([]string, 0, len(q.ParentIDs))
		for _, parentID := range q.ParentIDs {
			slots = append(slots, placeholder(parentID))
		}
		query += " AND er.parent_id IN (" + strings.Join(slots, ", ") + ")"
	}
	if q.RelType != "" {
		query += " AND er.rel_type = " + placeholder(q.RelType)
	}
	if q.EventType != "" {
		query += " AND t.event_type = " + placeholder(q.EventType)
	}

	after, before := ">", "<"
	order := " ORDER BY e.topological_ordering, e.stream_ordering"
	if q.Backwards {
		after, before = "<", ">"
		order = " ORDER BY e.topological_ordering DESC, e.stream_ordering DESC"
	}
	if q.From != nil {
		query += " AND (e.topological_ordering, e.stream_ordering) " + after +
			" (" + placeholder(q.From.Topological) + ", " + placeholder(q.From.Stream) + ")"
	}
	if q.To != nil {
		query += " AND (e.topological_ordering, e.stream_ordering) " + before +
			" (" + placeholder(q.To.Topological) + ", " + placeholder(q.To.Stream) + ")"
	}
	query += order
	if q.Limit > 0 {
		query += " LIMIT " + placeholder(q.Limit)
	}

	rows, err := r.db.Querier(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: find relations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.RelationRef
	for rows.Next() {
		var ref entity.RelationRef
		var disposition string
		if err := rows.Scan(&ref.ChildNID, &ref.ParentID, &ref.RelType, &ref.Sender,
			&ref.EventID, &ref.OriginServerTS, &ref.Position.Topological, &ref.Position.Stream,
			&disposition, &ref.EventType); err != nil {
			return nil, fmt.Errorf("repository: scan relation: %w", err)
		}
		ref.Disposition = entity.Disposition(disposition)
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: find relations: %w", err)
	}
	return out, nil
}
