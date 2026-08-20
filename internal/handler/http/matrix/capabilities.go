package matrix

import "net/http"

const defaultRoomVersion = "12"

type roomVersionsCapability struct {
	Default   string            `json:"default"`
	Available map[string]string `json:"available"`
}

type booleanCapability struct {
	Enabled bool `json:"enabled"`
}

type capabilitySet struct {
	RoomVersions    roomVersionsCapability `json:"m.room_versions"`
	ChangePassword  booleanCapability      `json:"m.change_password"`
	SetDisplayname  booleanCapability      `json:"m.set_displayname"`
	SetAvatarURL    booleanCapability      `json:"m.set_avatar_url"`
	ThreePIDChanges booleanCapability      `json:"m.3pid_changes"`
}

type capabilitiesResponse struct {
	Capabilities capabilitySet `json:"capabilities"`
}

func (h *Handler) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		Capabilities: capabilitySet{
			RoomVersions: roomVersionsCapability{
				Default:   defaultRoomVersion,
				Available: map[string]string{defaultRoomVersion: "stable"},
			},
		},
	})
}
