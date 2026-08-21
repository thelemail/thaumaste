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
	rooms        service.Rooms
	publicScheme string
	keyValidity  time.Duration
	clock        func() time.Time
}

func New(
	tenants service.Tenants,
	tokens service.Tokens,
	users service.Users,
	rooms service.Rooms,
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
		rooms:        rooms,
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

				r.Post("/_matrix/client/v3/createRoom", h.createRoom)
				r.Get("/_matrix/client/v3/joined_rooms", h.joinedRooms)

				r.Get("/_matrix/client/v3/rooms/{roomID}/state", h.roomState)
				r.Get("/_matrix/client/v3/rooms/{roomID}/state/{eventType}", h.roomStateEvent)
				r.Get("/_matrix/client/v3/rooms/{roomID}/state/{eventType}/", h.roomStateEvent)
				r.Get("/_matrix/client/v3/rooms/{roomID}/state/{eventType}/{stateKey}", h.roomStateEvent)
				r.Put("/_matrix/client/v3/rooms/{roomID}/state/{eventType}", h.setRoomState)
				r.Put("/_matrix/client/v3/rooms/{roomID}/state/{eventType}/", h.setRoomState)
				r.Put("/_matrix/client/v3/rooms/{roomID}/state/{eventType}/{stateKey}", h.setRoomState)
				r.Put("/_matrix/client/v3/rooms/{roomID}/send/{eventType}/{txnID}", h.sendMessage)
				r.Get("/_matrix/client/v3/rooms/{roomID}/event/{eventID}", h.roomEvent)
				r.Get("/_matrix/client/v3/rooms/{roomID}/messages", h.roomMessages)
				r.Get("/_matrix/client/v3/rooms/{roomID}/context/{eventID}", h.roomContext)
				r.Get("/_matrix/client/v3/rooms/{roomID}/joined_members", h.joinedMembers)
				r.Get("/_matrix/client/v3/rooms/{roomID}/members", h.members)

				r.Post("/_matrix/client/v3/join/{roomIDOrAlias}", h.joinRoom)
				r.Post("/_matrix/client/v3/knock/{roomIDOrAlias}", h.knockRoom)
				r.Post("/_matrix/client/v3/rooms/{roomIDOrAlias}/join", h.joinRoom)
				r.Post("/_matrix/client/v3/rooms/{roomIDOrAlias}/knock", h.knockRoom)
				r.Post("/_matrix/client/v3/rooms/{roomID}/leave", h.leaveRoom)
				r.Post("/_matrix/client/v3/rooms/{roomID}/forget", h.forgetRoom)
				r.Post("/_matrix/client/v3/rooms/{roomID}/invite", h.inviteUser)
				r.Post("/_matrix/client/v3/rooms/{roomID}/kick", h.kickUser)
				r.Post("/_matrix/client/v3/rooms/{roomID}/ban", h.banUser)
				r.Post("/_matrix/client/v3/rooms/{roomID}/unban", h.unbanUser)
				r.Get("/_matrix/client/v3/rooms/{roomID}/aliases", h.roomAliases)

				r.Get("/_matrix/client/v3/directory/room/{roomAlias}", h.resolveAlias)
				r.Put("/_matrix/client/v3/directory/room/{roomAlias}", h.createAlias)
				r.Delete("/_matrix/client/v3/directory/room/{roomAlias}", h.deleteAlias)
				r.Get("/_matrix/client/v3/directory/list/room/{roomID}", h.getVisibility)
				r.Put("/_matrix/client/v3/directory/list/room/{roomID}", h.setVisibility)

				r.Get("/_matrix/client/v3/publicRooms", h.publicRooms)
				r.Post("/_matrix/client/v3/publicRooms", h.searchPublicRooms)
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
