package matrix

import (
	"net/http"
)

type whoamiResponse struct {
	UserID string `json:"user_id"`
}

func (h *Handler) whoami(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, codeMissingToken, "Missing access token")
		return
	}
	writeJSON(w, http.StatusOK, whoamiResponse{UserID: caller.UserID})
}
