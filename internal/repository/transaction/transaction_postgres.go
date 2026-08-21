package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/google/uuid"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Transaction {
	return &repo{db: db}
}

func (r *repo) Get(ctx context.Context, key entity.TransactionKey) (entity.EventTransaction, error) {
	row, err := dbpg.FindEventTransaction(ctx, r.db.Querier(ctx),
		key.TenantID.String(), key.UserID, key.DeviceID, key.Endpoint, key.RoomID, key.TxnID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.EventTransaction{}, repository.ErrTransactionNotFound
		}
		return entity.EventTransaction{}, fmt.Errorf("repository: get transaction: %w", err)
	}
	return toTransaction(row)
}

func (r *repo) ForEvent(ctx context.Context, sender entity.TransactionSender, eventID string) (entity.EventTransaction, error) {
	row, err := dbpg.EventTransactions(
		dbpg.EventTransactionWhere.EventID.EQ(eventID),
		dbpg.EventTransactionWhere.TenantID.EQ(sender.TenantID.String()),
		dbpg.EventTransactionWhere.UserID.EQ(sender.UserID),
		dbpg.EventTransactionWhere.DeviceID.EQ(sender.DeviceID),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.EventTransaction{}, repository.ErrTransactionNotFound
		}
		return entity.EventTransaction{}, fmt.Errorf("repository: get transaction by event: %w", err)
	}
	return toTransaction(row)
}

func (r *repo) Record(ctx context.Context, in entity.NewEventTransaction) error {
	row := dbpg.EventTransaction{
		TenantID: in.TenantID.String(),
		UserID:   in.UserID,
		DeviceID: in.DeviceID,
		Endpoint: in.Endpoint,
		RoomID:   in.RoomID,
		TXNID:    in.TxnID,
		EventID:  in.EventID,
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		return fmt.Errorf("repository: record transaction: %w", err)
	}
	return nil
}

func (r *repo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	deleted, err := dbpg.EventTransactions(
		dbpg.EventTransactionWhere.CreatedAt.LT(cutoff),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return 0, fmt.Errorf("repository: sweep transactions: %w", err)
	}
	return deleted, nil
}

func toTransaction(row *dbpg.EventTransaction) (entity.EventTransaction, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.EventTransaction{}, fmt.Errorf("repository: transaction tenant %q: %w", row.TenantID, err)
	}
	return entity.EventTransaction{
		TenantID: tenantID,
		UserID:   row.UserID,
		DeviceID: row.DeviceID,
		Endpoint: row.Endpoint,
		RoomID:   row.RoomID,
		TxnID:    row.TXNID,
		EventID:  row.EventID,
		Recorded: row.CreatedAt,
	}, nil
}
