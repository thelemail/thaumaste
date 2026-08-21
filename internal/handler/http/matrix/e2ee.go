package matrix

import (
	"encoding/json"
	"errors"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

const unsignedKey = "unsigned"

type keyUploadRequest struct {
	DeviceKeys   json.RawMessage            `json:"device_keys"`
	OneTimeKeys  map[string]json.RawMessage `json:"one_time_keys"`
	FallbackKeys map[string]json.RawMessage `json:"fallback_keys"`
}

type keyUploadResponse struct {
	OneTimeKeyCounts map[string]int `json:"one_time_key_counts"`
}

type keyQueryRequest struct {
	Timeout    int                 `json:"timeout"`
	DeviceKeys map[string][]string `json:"device_keys"`
}

type keyQueryResponse struct {
	DeviceKeys map[string]map[string]json.RawMessage `json:"device_keys"`
	Failures   map[string]any                        `json:"failures"`
}

type keyClaimRequest struct {
	Timeout     int                          `json:"timeout"`
	OneTimeKeys map[string]map[string]string `json:"one_time_keys"`
}

type keyClaimResponse struct {
	OneTimeKeys map[string]map[string]json.RawMessage `json:"one_time_keys"`
	Failures    map[string]any                        `json:"failures"`
}

func (h *Handler) uploadKeys(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if caller.DeviceID == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "To upload keys you must authenticate with a device")
		return
	}

	var body keyUploadRequest
	if !readJSON(w, r, &body) {
		return
	}

	counts, err := h.keys.Upload(r.Context(), tenant.Scope(), entity.KeyUpload{
		TenantID:     tenant.ID,
		UserID:       caller.UserID,
		DeviceID:     caller.DeviceID,
		DeviceKeys:   body.DeviceKeys,
		OneTimeKeys:  body.OneTimeKeys,
		FallbackKeys: body.FallbackKeys,
	})
	if err != nil {
		writeKeyError(r, w, err, "Could not upload the keys")
		return
	}
	if counts == nil {
		counts = map[string]int{}
	}
	writeJSON(w, http.StatusOK, keyUploadResponse{OneTimeKeyCounts: counts})
}

func (h *Handler) queryKeys(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body keyQueryRequest
	if !readJSON(w, r, &body) {
		return
	}

	found, err := h.keys.Query(r.Context(), tenant.Scope(), caller.UserID,
		entity.KeyQuery{Devices: body.DeviceKeys})
	if err != nil {
		writeKeyError(r, w, err, "Could not query the keys")
		return
	}

	out := make(map[string]map[string]json.RawMessage, len(found))
	for userID, devices := range found {
		rendered := make(map[string]json.RawMessage, len(devices))
		for deviceID, key := range devices {
			raw, err := renderDeviceKey(key)
			if err != nil {
				writeInternal(r.Context(), w, "Could not render the keys", err)
				return
			}
			rendered[deviceID] = raw
		}
		out[userID] = rendered
	}
	writeJSON(w, http.StatusOK, keyQueryResponse{DeviceKeys: out, Failures: map[string]any{}})
}

func renderDeviceKey(key entity.DeviceKey) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(key.KeyJSON, &fields); err != nil {
		return nil, err
	}
	unsigned := map[string]string{}
	if key.DisplayName != "" {
		unsigned["device_display_name"] = key.DisplayName
	}
	raw, err := json.Marshal(unsigned)
	if err != nil {
		return nil, err
	}
	fields[unsignedKey] = raw
	return json.Marshal(fields)
}

func (h *Handler) claimKeys(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body keyClaimRequest
	if !readJSON(w, r, &body) {
		return
	}

	claimed, err := h.keys.Claim(r.Context(), tenant.Scope(), entity.KeyClaim{Devices: body.OneTimeKeys})
	if err != nil {
		writeKeyError(r, w, err, "Could not claim the keys")
		return
	}

	out := map[string]map[string]json.RawMessage{}
	for _, key := range claimed {
		devices, ok := out[key.UserID]
		if !ok {
			devices = map[string]json.RawMessage{}
			out[key.UserID] = devices
		}
		raw, err := json.Marshal(map[string]json.RawMessage{key.KeyID.String(): key.KeyJSON})
		if err != nil {
			writeInternal(r.Context(), w, "Could not render the keys", err)
			return
		}
		devices[key.DeviceID] = raw
	}
	writeJSON(w, http.StatusOK, keyClaimResponse{OneTimeKeys: out, Failures: map[string]any{}})
}

func writeKeyError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	switch {
	case errors.Is(err, entity.ErrKeyIdentityMismatch):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	case errors.Is(err, entity.ErrDeviceKeyMalformed), errors.Is(err, entity.ErrKeyIDMalformed):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	case errors.Is(err, entity.ErrOneTimeKeyConflict), errors.Is(err, entity.ErrFallbackKeyConflict):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	case errors.Is(err, entity.ErrTooManyOneTimeKeys), errors.Is(err, entity.ErrTooManyKeyTargets):
		writeError(w, http.StatusBadRequest, codeTooLarge, err.Error())
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}
