package matrix

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) Mount(r chi.Router) {
	r.Get("/_matrix/client/versions", h.versions)
	r.Get("/_matrix/client/v3/capabilities", h.capabilities)

	r.NotFound(notRecognized)
	r.MethodNotAllowed(notRecognized)
}

func notRecognized(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeUnrecognized, "Unrecognized request")
}
