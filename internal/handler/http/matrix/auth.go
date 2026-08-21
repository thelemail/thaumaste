package matrix

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
)

const clientAPIPrefix = "/_matrix/client/"

type callerKey struct{}

func withCaller(ctx context.Context, t entity.AccessToken) context.Context {
	return context.WithValue(ctx, callerKey{}, t)
}

func callerFrom(ctx context.Context) (entity.AccessToken, bool) {
	t, ok := ctx.Value(callerKey{}).(entity.AccessToken)
	return t, ok
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := tenantFrom(r.Context())
		if !ok {
			writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
			return
		}

		presented, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, codeMissingToken, "Missing access token")
			return
		}

		caller, err := h.tokens.Resolve(r.Context(), presented)
		switch {
		case err == nil:
		case errors.Is(err, entity.ErrTokenNotFound),
			errors.Is(err, entity.ErrTokenExpired),
			errors.Is(err, entity.ErrTokenRevoked):
			writeUnknownToken(w, "Invalid access token")
			return
		default:
			writeInternal(r.Context(), w, "Could not check the access token", err)
			return
		}

		if caller.TenantID != tenant.ID {
			writeUnknownToken(w, "Invalid access token")
			return
		}

		next.ServeHTTP(w, r.WithContext(withCaller(r.Context(), caller)))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if scheme, token, found := strings.Cut(header, " "); found && strings.EqualFold(scheme, "Bearer") {
		token = strings.TrimSpace(token)
		return token, token != ""
	}
	token := r.URL.Query().Get("access_token")
	return token, token != ""
}

func (h *Handler) unauthenticatedNotFound(w http.ResponseWriter, r *http.Request) {
	presented, ok := bearerToken(r)
	if !ok || !strings.HasPrefix(r.URL.Path, clientAPIPrefix) {
		writeError(w, http.StatusNotFound, codeUnrecognized, "Unrecognized request")
		return
	}
	if _, err := h.tokens.Resolve(r.Context(), presented); err != nil {
		writeUnknownToken(w, "Invalid access token")
		return
	}
	writeError(w, http.StatusNotFound, codeUnrecognized, "Unrecognized request")
}
