package rooms

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) allow(ctx context.Context, scope entity.TenantScope, sender, roomID string) error {
	if !s.limits.Enabled() {
		return nil
	}

	scoped := []struct {
		key   string
		limit int
	}{
		{"send:user:" + scope.ID().String() + ":" + sender, s.limits.PerUser},
		{"send:room:" + roomID, s.limits.PerRoom},
		{"send:tenant:" + scope.ID().String(), s.limits.PerTenant},
	}

	for _, each := range scoped {
		if each.limit <= 0 {
			continue
		}
		verdict, err := s.limiter.Allow(ctx, each.key, each.limit, s.limits.Window)
		if err != nil {
			return err
		}
		if !verdict.Allowed {
			return entity.RateLimited{RetryAfter: verdict.ResetAt.Sub(s.clock().UTC())}
		}
	}
	return nil
}
