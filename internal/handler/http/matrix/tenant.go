package matrix

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
)

type tenantKey struct{}

func withTenant(ctx context.Context, t entity.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

func tenantFrom(ctx context.Context) (entity.Tenant, bool) {
	t, ok := ctx.Value(tenantKey{}).(entity.Tenant)
	return t, ok
}

func (h *Handler) resolveTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := h.tenants.ByHost(r.Context(), requestHost(r))
		if err != nil {
			if errors.Is(err, entity.ErrTenantNotFound) {
				writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
				return
			}
			writeInternal(r.Context(), w, "Could not resolve the server for this host", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withTenant(r.Context(), t)))
	})
}

func (h *Handler) requireActiveTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := tenantFrom(r.Context())
		if !ok {
			writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
			return
		}
		if !t.Active() {
			writeError(w, http.StatusForbidden, codeForbidden, "This server is suspended")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestHost prefers the forwarded host, because in production the client-server API is reached
// through an edge that terminates TLS for the tenant's own domain and proxies onward.
func requestHost(r *http.Request) string {
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host, _, _ = strings.Cut(forwarded, ",")
	}
	return entity.NormaliseHost(strings.TrimSpace(host))
}
