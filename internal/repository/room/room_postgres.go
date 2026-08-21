package room

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const uniqueViolation = "23505"

type repo struct {
	db     *postgres.Client
	events repository.Event
}

func New(db *postgres.Client, events repository.Event) repository.Room {
	return &repo{db: db, events: events}
}

func (r *repo) Create(ctx context.Context, in entity.NewRoom) (entity.Room, error) {
	row := dbpg.Room{
		TenantID:    in.TenantID.String(),
		RoomID:      in.RoomID,
		RoomVersion: string(in.Version),
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return entity.Room{}, repository.ErrRoomAlreadyExists
		}
		return entity.Room{}, fmt.Errorf("repository: create room: %w", err)
	}
	return toRoom(&row)
}

func (r *repo) GetByRoomID(ctx context.Context, roomID string) (entity.Room, error) {
	row, err := dbpg.Rooms(dbpg.RoomWhere.RoomID.EQ(roomID)).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Room{}, repository.ErrRoomNotFound
		}
		return entity.Room{}, fmt.Errorf("repository: get room: %w", err)
	}
	return toRoom(row)
}

func (r *repo) SetCreateEvent(ctx context.Context, roomNID, eventNID int64) error {
	row := dbpg.Room{RoomNid: roomNID, CreateEventNid: null.Int64From(eventNID)}
	n, err := row.Update(ctx, r.db.Querier(ctx), boil.Whitelist(dbpg.RoomColumns.CreateEventNid))
	if err != nil {
		return fmt.Errorf("repository: set create event: %w", err)
	}
	if n == 0 {
		return repository.ErrRoomNotFound
	}
	return nil
}

func (r *repo) SetVisibility(ctx context.Context, roomNID int64, visibility string) error {
	row := dbpg.Room{RoomNid: roomNID, Visibility: visibility}
	n, err := row.Update(ctx, r.db.Querier(ctx), boil.Whitelist(dbpg.RoomColumns.Visibility))
	if err != nil {
		return fmt.Errorf("repository: set room visibility: %w", err)
	}
	if n == 0 {
		return repository.ErrRoomNotFound
	}
	return nil
}

func (r *repo) ListPublic(ctx context.Context, scope entity.TenantScope) ([]entity.Room, error) {
	rows, err := dbpg.Rooms(
		dbpg.RoomWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomWhere.Visibility.EQ(entity.VisibilityPublic),
		qm.OrderBy(dbpg.RoomColumns.RoomNid),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list public rooms: %w", err)
	}
	return toRooms(rows)
}

func (r *repo) ListForTenant(ctx context.Context, scope entity.TenantScope) ([]entity.Room, error) {
	rows, err := dbpg.Rooms(
		dbpg.RoomWhere.TenantID.EQ(scope.ID().String()),
		qm.OrderBy(dbpg.RoomColumns.RoomNid),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list rooms: %w", err)
	}

	return toRooms(rows)
}

func toRooms(rows dbpg.RoomSlice) ([]entity.Room, error) {
	out := make([]entity.Room, 0, len(rows))
	for _, row := range rows {
		converted, err := toRoom(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) Extremities(ctx context.Context, roomNID int64) ([]entity.StoredEvent, error) {
	room, err := dbpg.Rooms(
		dbpg.RoomWhere.RoomNid.EQ(roomNID),
		qm.Load(dbpg.RoomRels.EventNidEvents),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrRoomNotFound
		}
		return nil, fmt.Errorf("repository: list extremities: %w", err)
	}
	if room.R == nil {
		return nil, nil
	}

	all, err := r.events.ListForRoom(ctx, roomNID)
	if err != nil {
		return nil, err
	}
	byNID := make(map[int64]entity.StoredEvent, len(all))
	for _, e := range all {
		byNID[e.NID] = e
	}

	out := make([]entity.StoredEvent, 0, len(room.R.EventNidEvents))
	for _, related := range room.R.EventNidEvents {
		if e, ok := byNID[related.EventNid]; ok {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b entity.StoredEvent) int { return int(a.NID - b.NID) })
	return out, nil
}

func (r *repo) ReplaceExtremities(ctx context.Context, roomNID int64, eventNIDs []int64) error {
	room := dbpg.Room{RoomNid: roomNID}
	related := make([]*dbpg.Event, 0, len(eventNIDs))
	for _, nid := range eventNIDs {
		related = append(related, &dbpg.Event{EventNid: nid})
	}
	if err := room.SetEventNidEvents(ctx, r.db.Querier(ctx), false, related...); err != nil {
		return fmt.Errorf("repository: replace extremities: %w", err)
	}
	return nil
}

func toRoom(row *dbpg.Room) (entity.Room, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.Room{}, fmt.Errorf("parse room tenant id: %w", err)
	}
	return entity.Room{
		NID:        row.RoomNid,
		TenantID:   tenantID,
		RoomID:     row.RoomID,
		Version:    entity.RoomVersionID(row.RoomVersion),
		Visibility: row.Visibility,
		CreatedAt:  row.CreatedAt,
	}, nil
}
