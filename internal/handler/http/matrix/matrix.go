package matrix

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/service"
)

type Handler struct {
	tenants      service.Tenants
	tokens       service.Tokens
	publicScheme string
	keyValidity  time.Duration
	clock        func() time.Time
}

func New(tenants service.Tenants, tokens service.Tokens, srv config.Server, sign config.Signing, clock func() time.Time) *Handler {
	if clock == nil {
		clock = time.Now
	}
	return &Handler{
		tenants:      tenants,
		tokens:       tokens,
		publicScheme: srv.PublicScheme,
		keyValidity:  sign.KeyValidity,
		clock:        clock,
	}
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/_matrix/client/versions", h.versions)

	r.Group(func(r chi.Router) {
		r.Use(h.resolveTenant)

		r.Get("/_matrix/key/v2/server", h.serverKeys)
		r.Get("/.well-known/matrix/client", h.wellKnownClient)

		r.Group(func(r chi.Router) {
			r.Use(h.requireActiveTenant)
			r.Use(h.authenticate)

			r.Get("/_matrix/client/v3/capabilities", h.capabilities)
			r.Get("/_matrix/client/v3/account/whoami", h.whoami)
		})
	})

	r.NotFound(unrecognized(http.StatusNotFound))
	r.MethodNotAllowed(unrecognized(http.StatusMethodNotAllowed))
}

func unrecognized(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, status, codeUnrecognized, "Unrecognized request")
	}
}
