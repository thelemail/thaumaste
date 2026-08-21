package matrix

import "net/http"

var supportedVersions = []string{"v1.16"}

var unstableFeatures = map[string]bool{"org.matrix.simplified_msc3575": true}

type versionsResponse struct {
	Versions         []string        `json:"versions"`
	UnstableFeatures map[string]bool `json:"unstable_features"`
}

func (h *Handler) versions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionsResponse{
		Versions:         supportedVersions,
		UnstableFeatures: unstableFeatures,
	})
}
