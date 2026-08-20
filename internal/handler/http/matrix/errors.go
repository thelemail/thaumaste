package matrix

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type errorCode string

const (
	codeUnrecognized errorCode = "M_UNRECOGNIZED"
	codeUnknown      errorCode = "M_UNKNOWN"
)

type errorEnvelope struct {
	ErrCode errorCode `json:"errcode"`
	Error   string    `json:"error"`
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
