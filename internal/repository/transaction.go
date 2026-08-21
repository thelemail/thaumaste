package repository

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrTransactionNotFound = errors.New("repository: transaction not found")

type Transaction interface {
	Get(ctx context.Context, key entity.TransactionKey) (entity.EventTransaction, error)
	ForEvents(ctx context.Context, sender entity.TransactionSender, eventIDs []string) (map[string]string, error)
	Record(ctx context.Context, in entity.NewEventTransaction) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
