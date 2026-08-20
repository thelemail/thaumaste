package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			slog.ErrorContext(r.Context(), "panic serving request",
				"error", rec,
				"stack", string(debug.Stack()),
				"request_id", RequestIDFrom(r.Context()),
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN","error":"Internal server error"}`))
		}()
		next.ServeHTTP(w, r)
	})
}
