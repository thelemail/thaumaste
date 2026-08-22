package matrix

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type sendToDeviceRequest struct {
	Messages map[string]map[string]json.RawMessage `json:"messages"`
}

func (h *Handler) sendToDevice(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if caller.DeviceID == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Sending to a device requires a device")
		return
	}

	var body sendToDeviceRequest
	if !readJSON(w, r, &body) {
		return
	}

	err := h.toDevice.Send(r.Context(), tenant.Scope(), entity.ToDeviceSend{
		TenantID: tenant.ID,
		Sender:   caller.UserID,
		DeviceID: caller.DeviceID,
		Type:     chi.URLParam(r, "eventType"),
		TxnID:    chi.URLParam(r, "txnID"),
		Messages: body.Messages,
	})
	if err != nil {
		var limited entity.RateLimited
		switch {
		case errors.Is(err, entity.ErrToDeviceEmpty):
			writeError(w, http.StatusBadRequest, codeMissingParam, err.Error())
		case errors.Is(err, entity.ErrToDeviceTooBig):
			writeError(w, http.StatusBadRequest, codeTooLarge, err.Error())
		case errors.As(err, &limited):
			writeRateLimited(w, limited.RetryAfter)
		case errors.As(err, &validation.Errors{}):
			writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
		default:
			writeInternal(r.Context(), w, "Could not send the to-device messages", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

type keyChangesResponse struct {
	Changed []string `json:"changed"`
	Left    []string `json:"left"`
}

func (h *Handler) keyChanges(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	if !query.Has("from") || !query.Has("to") {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Both from and to are required")
		return
	}
	from, err := entity.ParseSyncToken(query.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown from token")
		return
	}
	to, err := entity.ParseSyncToken(query.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown to token")
		return
	}

	lists, err := h.deviceLists.ChangedSince(r.Context(), tenant.Scope(), caller.UserID,
		from.Cursors(), to.Cursors())
	if err != nil {
		writeInternal(r.Context(), w, "Could not read the device list changes", err)
		return
	}
	writeJSON(w, http.StatusOK, keyChangesResponse{
		Changed: orEmptyStrings(lists.Changed),
		Left:    orEmptyStrings(lists.Left),
	})
}
