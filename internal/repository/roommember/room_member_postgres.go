package roommember

import (
	"context"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/uuid"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.RoomMember {
	return &repo{db: db}
}

func (r *repo) Upsert(ctx context.Context, in entity.NewRoomMembership) error {
	row := dbpg.RoomMembership{
		TenantID:   in.TenantID.String(),
		RoomNid:    in.RoomNID,
		UserID:     in.UserID,
		Membership: in.Membership,
		EventNid:   in.EventNID,
	}
	err := row.Upsert(ctx, r.db.Querier(ctx), true,
		[]string{
			dbpg.RoomMembershipColumns.TenantID,
			dbpg.RoomMembershipColumns.RoomNid,
			dbpg.RoomMembershipColumns.UserID,
		},
		boil.Whitelist(dbpg.RoomMembershipColumns.Membership, dbpg.RoomMembershipColumns.EventNid),
		boil.Infer())
	if err != nil {
		return fmt.Errorf("repository: upsert room membership: %w", err)
	}
	return nil
}

func (r *repo) ListForRoom(ctx context.Context, roomNID int64, membership string) ([]entity.RoomMembership, error) {
	rows, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
		qm.OrderBy(dbpg.RoomMembershipColumns.UserID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list room members: %w", err)
	}
	return toMemberships(rows)
}

func (r *repo) ListForUser(ctx context.Context, scope entity.TenantScope, userID, membership string) ([]entity.RoomMembership, error) {
	rows, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomMembershipWhere.UserID.EQ(userID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
		qm.Load(dbpg.RoomMembershipRels.RoomNidRoom),
		qm.OrderBy(dbpg.RoomMembershipColumns.RoomNid),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list rooms for user: %w", err)
	}
	return toMemberships(rows)
}

func (r *repo) CountForRoom(ctx context.Context, roomNID int64, membership string) (int64, error) {
	n, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
	).Count(ctx, r.db.Querier(ctx))
	if err != nil {
		return 0, fmt.Errorf("repository: count room members: %w", err)
	}
	return n, nil
}

func toMemberships(rows dbpg.RoomMembershipSlice) ([]entity.RoomMembership, error) {
	out := make([]entity.RoomMembership, 0, len(rows))
	for _, row := range rows {
		tenantID, err := uuid.Parse(row.TenantID)
		if err != nil {
			return nil, fmt.Errorf("parse membership tenant id: %w", err)
		}
		converted := entity.RoomMembership{
			TenantID:   tenantID,
			RoomNID:    row.RoomNid,
			UserID:     row.UserID,
			Membership: row.Membership,
			EventNID:   row.EventNid,
		}
		if row.R != nil && row.R.RoomNidRoom != nil {
			converted.RoomID = row.R.RoomNidRoom.RoomID
		}
		out = append(out, converted)
	}
	return out, nil
}
