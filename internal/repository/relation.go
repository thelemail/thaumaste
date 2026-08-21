package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrRelationExists = errors.New("repository: relation already exists")

type Relation interface {
	Insert(ctx context.Context, in entity.NewEventRelation) error
	Delete(ctx context.Context, childNID int64) error
	Find(ctx context.Context, roomNID int64, q entity.RelationQuery) ([]entity.RelationRef, error)
}
