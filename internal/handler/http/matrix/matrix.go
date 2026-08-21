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
	users        service.Users
	publicScheme string
	keyValidity  time.Duration
	clock        func() time.Time
}

func New(
	tenants service.Tenants,
	tokens service.Tokens,
	users service.Users,
	srv config.Server,
	sign config.Signing,
	clock func() time.Time,
) *Handler {
	if clock == nil {
		clock = time.Now
	}
	return &Handler{
		tenants:      tenants,
		tokens:       tokens,
		users:        users,
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

			r.Get("/_matrix/client/v3/login", h.loginFlows)
			r.Post("/_matrix/client/v3/login", h.login)
			r.Post("/_matrix/client/v3/register", h.register)
			r.Get("/_matrix/client/v3/register/available", h.registerAvailable)
			r.Post("/_matrix/client/v3/refresh", h.refresh)

			r.Get("/_matrix/client/v3/profile/{userID}", h.getProfile)
			r.Get("/_matrix/client/v3/profile/{userID}/{keyName}", h.getProfileField)

			r.Group(func(r chi.Router) {
				r.Use(h.authenticate)

				r.Get("/_matrix/client/v3/capabilities", h.capabilities)
				r.Get("/_matrix/client/v3/account/whoami", h.whoami)
				r.Post("/_matrix/client/v3/account/password", h.changePassword)
				r.Post("/_matrix/client/v3/account/deactivate", h.deactivate)

				r.Post("/_matrix/client/v3/logout", h.logout)
				r.Post("/_matrix/client/v3/logout/all", h.logoutAll)

				r.Get("/_matrix/client/v3/devices", h.listDevices)
				r.Get("/_matrix/client/v3/devices/{deviceID}", h.getDevice)
				r.Put("/_matrix/client/v3/devices/{deviceID}", h.renameDevice)
				r.Delete("/_matrix/client/v3/devices/{deviceID}", h.deleteDevice)
				r.Post("/_matrix/client/v3/delete_devices", h.deleteDevices)

				r.Put("/_matrix/client/v3/profile/{userID}/{keyName}", h.setProfileField)
			})
		})
	})

	r.NotFound(h.unauthenticatedNotFound)
	r.MethodNotAllowed(unrecognized(http.StatusMethodNotAllowed))
}

func unrecognized(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, status, codeUnrecognized, "Unrecognized request")
	}
}
