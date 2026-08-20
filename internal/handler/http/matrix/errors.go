package matrix

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

type errorCode string

const (
	codeUnrecognized errorCode = "M_UNRECOGNIZED"
	codeUnknown      errorCode = "M_UNKNOWN"
	codeNotFound     errorCode = "M_NOT_FOUND"
	codeForbidden    errorCode = "M_FORBIDDEN"
	codeMissingToken errorCode = "M_MISSING_TOKEN"
	codeUnknownToken errorCode = "M_UNKNOWN_TOKEN"
)

type errorEnvelope struct {
	ErrCode errorCode `json:"errcode"`
	Error   string    `json:"error"`
}

type unknownTokenEnvelope struct {
	errorEnvelope
	SoftLogout bool `json:"soft_logout"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN","error":"encode failed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

func writeError(w http.ResponseWriter, status int, code errorCode, msg string) {
	writeJSON(w, status, errorEnvelope{ErrCode: code, Error: msg})
}

func writeInternal(ctx context.Context, w http.ResponseWriter, msg string, err error) {
	slog.ErrorContext(ctx, msg, "error", err)
	writeError(w, http.StatusInternalServerError, codeUnknown, msg)
}

func writeUnknownToken(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnauthorized, unknownTokenEnvelope{
		errorEnvelope: errorEnvelope{ErrCode: codeUnknownToken, Error: msg},
		SoftLogout:    false,
	})
}
