package filters

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	filters repository.Filter
}

func New(filters repository.Filter) service.Filters {
	return &srv{filters: filters}
}

func (s *srv) Store(ctx context.Context, scope entity.TenantScope, caller, target string,
	document []byte,
) (string, error) {
	if caller != target {
		return "", entity.ErrAccountDataForeign
	}
	filter, err := entity.ParseFilter(document)
	if err != nil {
		return "", err
	}
	in := entity.NewFilter{TenantID: scope.ID(), UserID: target, Filter: filter}
	if err := in.Validate(); err != nil {
		return "", err
	}
	return s.filters.Store(ctx, in)
}

func (s *srv) Get(ctx context.Context, scope entity.TenantScope, caller, target,
	filterID string,
) (entity.Filter, error) {
	if caller != target {
		return entity.Filter{}, entity.ErrAccountDataForeign
	}
	return s.filters.Get(ctx, scope, target, filterID)
}
