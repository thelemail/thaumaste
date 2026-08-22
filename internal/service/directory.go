package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Directory interface {
	Search(ctx context.Context, scope entity.TenantScope, caller string, in entity.DirectorySearch) ([]entity.DirectoryResult, bool, error)
}
