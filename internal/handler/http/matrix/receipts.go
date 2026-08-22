package matrix

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type receiptRequest struct {
	ThreadID string `json:"thread_id"`
}

type readMarkerRequest struct {
	FullyRead   string `json:"m.fully_read"`
	Read        string `json:"m.read"`
	ReadPrivate string `json:"m.read.private"`
}

func (h *Handler) sendReceipt(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body receiptRequest
	if !readJSON(w, r, &body) {
		return
	}

	err := h.receipts.Send(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID"),
		chi.URLParam(r, "receiptType"), roomParam(r, "eventID"), body.ThreadID)
	if err != nil {
		writeReceiptError(r, w, err, "Could not store the receipt")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) setReadMarkers(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body readMarkerRequest
	if !readJSON(w, r, &body) {
		return
	}

	err := h.receipts.Mark(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID"),
		entity.ReadMarker{FullyRead: body.FullyRead, Read: body.Read, ReadPrivate: body.ReadPrivate})
	if err != nil {
		writeReceiptError(r, w, err, "Could not move the read markers")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func writeReceiptError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	switch {
	case errors.Is(err, entity.ErrReceiptTypeUnknown), errors.Is(err, entity.ErrThreadUnknown):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	case errors.Is(err, entity.ErrNotInRoom):
		writeError(w, http.StatusForbidden, codeForbidden, err.Error())
	case errors.Is(err, entity.ErrRoomNotFound), errors.Is(err, entity.ErrEventNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}
