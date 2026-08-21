package matrix

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (h *Handler) setAccountData(w http.ResponseWriter, r *http.Request) {
	h.writeAccountData(w, r, "")
}

func (h *Handler) setRoomAccountData(w http.ResponseWriter, r *http.Request) {
	h.writeAccountData(w, r, roomParam(r, "roomID"))
}

func (h *Handler) writeAccountData(w http.ResponseWriter, r *http.Request, roomID string) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var content json.RawMessage
	if !readJSON(w, r, &content) {
		return
	}

	err := h.accountData.Set(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), roomID, chi.URLParam(r, "type"), content)
	if err != nil {
		writeAccountDataError(r, w, err, "Could not store the account data")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) getAccountData(w http.ResponseWriter, r *http.Request) {
	h.readAccountData(w, r, "")
}

func (h *Handler) getRoomAccountData(w http.ResponseWriter, r *http.Request) {
	h.readAccountData(w, r, roomParam(r, "roomID"))
}

func (h *Handler) readAccountData(w http.ResponseWriter, r *http.Request, roomID string) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	found, err := h.accountData.Get(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), roomID, chi.URLParam(r, "type"))
	if err != nil {
		writeAccountDataError(r, w, err, "Could not read the account data")
		return
	}
	writeRaw(w, http.StatusOK, found.Content)
}

type tagsResponse struct {
	Tags map[string]json.RawMessage `json:"tags"`
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	tags, err := h.accountData.Tags(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), roomParam(r, "roomID"))
	if err != nil {
		writeAccountDataError(r, w, err, "Could not list the tags")
		return
	}
	if tags.Tags == nil {
		tags.Tags = map[string]json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, tagsResponse{Tags: tags.Tags})
}

func (h *Handler) setTag(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var order json.RawMessage
	if !readJSON(w, r, &order) {
		return
	}

	err := h.accountData.SetTag(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), roomParam(r, "roomID"), chi.URLParam(r, "tag"), order)
	if err != nil {
		writeAccountDataError(r, w, err, "Could not set the tag")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	err := h.accountData.DeleteTag(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), roomParam(r, "roomID"), chi.URLParam(r, "tag"))
	if err != nil {
		writeAccountDataError(r, w, err, "Could not remove the tag")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func writeAccountDataError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	switch {
	case errors.Is(err, entity.ErrAccountDataForeign):
		writeError(w, http.StatusForbidden, codeForbidden, err.Error())
	case errors.Is(err, entity.ErrAccountDataReserved):
		writeError(w, http.StatusMethodNotAllowed, codeBadJSON, err.Error())
	case errors.Is(err, entity.ErrAccountDataNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, entity.ErrRoomNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, entity.ErrAccountDataTooLarge):
		writeError(w, http.StatusBadRequest, codeTooLarge, err.Error())
	case errors.Is(err, entity.ErrAccountDataShape), errors.Is(err, entity.ErrTagInvalid):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}
