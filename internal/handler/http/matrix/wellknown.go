package matrix

import (
	"net/http"
)

type baseURL struct {
	BaseURL string `json:"base_url"`
}

type clientDiscovery struct {
	Homeserver baseURL `json:"m.homeserver"`
}

func (h *Handler) wellKnownClient(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantFrom(r.Context()); !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}
	writeJSON(w, http.StatusOK, clientDiscovery{
		Homeserver: baseURL{BaseURL: h.publicScheme + "://" + requestHost(r)},
	})
}
