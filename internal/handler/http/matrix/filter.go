package matrix

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type filterCreated struct {
	FilterID string `json:"filter_id"`
}

func (h *Handler) createFilter(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var document json.RawMessage
	if !readJSON(w, r, &document) {
		return
	}

	filterID, err := h.filters.Store(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), document)
	if err != nil {
		writeFilterError(r, w, err, "Could not store the filter")
		return
	}
	writeJSON(w, http.StatusOK, filterCreated{FilterID: filterID})
}

func (h *Handler) getFilter(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	filter, err := h.filters.Get(r.Context(), tenant.Scope(), caller.UserID,
		roomParam(r, "userID"), chi.URLParam(r, "filterID"))
	if err != nil {
		writeFilterError(r, w, err, "Could not read the filter")
		return
	}
	writeRaw(w, http.StatusOK, filter.Document)
}

func writeFilterError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	switch {
	case errors.Is(err, entity.ErrAccountDataForeign):
		writeError(w, http.StatusForbidden, codeForbidden, err.Error())
	case errors.Is(err, entity.ErrFilterNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, entity.ErrBadFilter), errors.Is(err, entity.ErrFilterTooLarge):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}

func (h *Handler) resolveFilter(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	if raw == "" || strings.HasPrefix(raw, "{") {
		return raw, true
	}

	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return "", false
	}
	stored, err := h.filters.Get(r.Context(), tenant.Scope(), caller.UserID, caller.UserID, raw)
	if err != nil {
		writeFilterError(r, w, err, "Could not read the filter")
		return "", false
	}
	projected, err := stored.Timeline().JSON()
	if err != nil {
		writeInternal(r.Context(), w, "Could not apply the filter", err)
		return "", false
	}
	return string(projected), true
}
