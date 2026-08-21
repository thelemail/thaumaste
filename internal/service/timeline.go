package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Timeline interface {
	Render(ctx context.Context, v entity.TimelineView, scanned []entity.StoredEvent) ([]entity.ClientEvent, error)
	Enriched(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent) ([]entity.ClientEvent, error)
	Single(ctx context.Context, v entity.TimelineView, stored entity.StoredEvent) (entity.ClientEvent, error)
}
