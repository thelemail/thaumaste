package matrix

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
)

type deviceResponse struct {
	DeviceID    string `json:"device_id"`
	DisplayName string `json:"display_name,omitempty"`
	LastSeenIP  string `json:"last_seen_ip,omitempty"`
	LastSeenTS  int64  `json:"last_seen_ts,omitempty"`
}

func toDeviceResponse(d entity.Device) deviceResponse {
	out := deviceResponse{
		DeviceID:    d.DeviceID,
		DisplayName: d.DisplayName,
		LastSeenIP:  d.LastSeenIP,
	}
	if d.LastSeenTS != nil {
		out.LastSeenTS = d.LastSeenTS.UnixMilli()
	}
	return out
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	devices, err := h.users.Devices(r.Context(), tenant.Scope(), caller.UserID)
	if err != nil {
		writeInternal(r.Context(), w, "Could not list the devices", err)
		return
	}
	out := make([]deviceResponse, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceResponse(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (h *Handler) getDevice(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	device, err := h.users.Device(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "deviceID"))
	if err != nil {
		if errors.Is(err, entity.ErrDeviceNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "Unknown device")
			return
		}
		writeInternal(r.Context(), w, "Could not read the device", err)
		return
	}
	writeJSON(w, http.StatusOK, toDeviceResponse(device))
}

type renameDeviceRequest struct {
	DisplayName *string `json:"display_name"`
}

func (h *Handler) renameDevice(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in renameDeviceRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.DisplayName == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	err := h.users.RenameDevice(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "deviceID"), *in.DisplayName)
	if err != nil {
		if errors.Is(err, entity.ErrDeviceNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "Unknown device")
			return
		}
		writeInternal(r.Context(), w, "Could not rename the device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

type deleteDeviceRequest struct {
	Auth    *authDict `json:"auth"`
	Devices []string  `json:"devices"`
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	h.deleteDevicesNamed(w, r, []string{chi.URLParam(r, "deviceID")})
}

func (h *Handler) deleteDevices(w http.ResponseWriter, r *http.Request) {
	h.deleteDevicesNamed(w, r, nil)
}

func (h *Handler) deleteDevicesNamed(w http.ResponseWriter, r *http.Request, fromPath []string) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in deleteDeviceRequest
	if !readJSON(w, r, &in) {
		return
	}
	if _, done := h.requireUIA(w, r, in.Auth, tenant, entity.UIAKindDeleteDevice, caller.UserID); !done {
		return
	}

	devices := fromPath
	if devices == nil {
		devices = in.Devices
	}
	if err := h.users.DeleteDevices(r.Context(), tenant.Scope(), caller.UserID, devices); err != nil {
		writeInternal(r.Context(), w, "Could not delete the devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}
