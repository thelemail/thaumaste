package matrix_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
)

type countingRelations struct {
	inner repository.Relation
	calls *atomic.Int64
}

func (c countingRelations) Insert(ctx context.Context, in entity.NewEventRelation) error {
	return c.inner.Insert(ctx, in)
}

func (c countingRelations) Delete(ctx context.Context, childNID int64) error {
	return c.inner.Delete(ctx, childNID)
}

func (c countingRelations) Find(ctx context.Context, roomNID int64, q entity.RelationQuery) ([]entity.RelationRef, error) {
	c.calls.Add(1)
	return c.inner.Find(ctx, roomNID, q)
}

type countingEvents struct {
	repository.Event
	calls *atomic.Int64
}

func (c countingEvents) GetManyByNID(ctx context.Context, eventNIDs []int64) ([]entity.StoredEvent, error) {
	c.calls.Add(1)
	return c.Event.GetManyByNID(ctx, eventNIDs)
}

func (s *server) countQueries(t *testing.T, during func()) int64 {
	t.Helper()
	before := s.queries.Load()
	during()
	return s.queries.Load() - before
}
