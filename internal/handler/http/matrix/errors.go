package matrix

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

type errorCode string

const (
	codeUnrecognized errorCode = "M_UNRECOGNIZED"
	codeUnknown      errorCode = "M_UNKNOWN"
	codeNotFound     errorCode = "M_NOT_FOUND"
	codeForbidden    errorCode = "M_FORBIDDEN"
	codeMissingToken errorCode = "M_MISSING_TOKEN"
	codeUnknownToken errorCode = "M_UNKNOWN_TOKEN"
	codeUserInUse    errorCode = "M_USER_IN_USE"
	codeInvalidUser  errorCode = "M_INVALID_USERNAME"
	codeBadJSON      errorCode = "M_BAD_JSON"
	codeNotJSON      errorCode = "M_NOT_JSON"
	codeMissingParam errorCode = "M_MISSING_PARAM"
	codeInvalidParam errorCode = "M_INVALID_PARAM"
	codeLimitExceed  errorCode = "M_LIMIT_EXCEEDED"
	codeDeactivated  errorCode = "M_USER_DEACTIVATED"
	codeWeakPassword errorCode = "M_WEAK_PASSWORD"
	codeBadRoomVer   errorCode = "M_UNSUPPORTED_ROOM_VERSION"
	codeBadRoomState errorCode = "M_INVALID_ROOM_STATE"
	codeRoomInUse    errorCode = "M_ROOM_IN_USE"
	codeBadAlias     errorCode = "M_BAD_ALIAS"
	codeBadState     errorCode = "M_BAD_STATE"
	codeCannotGrant  errorCode = "M_UNABLE_TO_GRANT_JOIN"
	codeTooLarge     errorCode = "M_TOO_LARGE"
	codeDuplicate    errorCode = "M_DUPLICATE_ANNOTATION"
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

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code errorCode, msg string) {
	writeJSON(w, status, errorEnvelope{ErrCode: code, Error: msg})
}

func writeInternal(ctx context.Context, w http.ResponseWriter, msg string, err error) {
	slog.ErrorContext(ctx, msg, "error", err)
	writeError(w, http.StatusInternalServerError, codeUnknown, msg)
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	seconds := int64(math.Ceil(retryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeJSON(w, http.StatusTooManyRequests, rateLimitEnvelope{
		errorEnvelope: errorEnvelope{ErrCode: codeLimitExceed, Error: "Too many requests"},
		RetryAfterMS:  retryAfter.Milliseconds(),
	})
}

type rateLimitEnvelope struct {
	errorEnvelope
	RetryAfterMS int64 `json:"retry_after_ms"`
}

func writeUnknownToken(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnauthorized, unknownTokenEnvelope{
		errorEnvelope: errorEnvelope{ErrCode: codeUnknownToken, Error: msg},
		SoftLogout:    false,
	})
}

const maxRequestBytes = 1 << 20

func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeNotJSON, "Could not read the request body")
		return false
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if !utf8.Valid(raw) {
		writeError(w, http.StatusBadRequest, codeNotJSON, "Request body is not valid UTF-8")
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if !json.Valid(raw) {
			writeError(w, http.StatusBadRequest, codeNotJSON, "Request body is not valid JSON")
			return false
		}
		writeError(w, http.StatusBadRequest, codeBadJSON, "Request body is not a JSON object")
		return false
	}
	return true
}
