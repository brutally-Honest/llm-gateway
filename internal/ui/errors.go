package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type envelope struct {
	Error errBody `json:"error"`
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeErr is the only error path of the API: the JSON envelope with its status.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Error: errBody{Code: code, Message: msg}})
}

// writeJSON answers 200 with v.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeStoreErr maps a store error: a deadline is 504 timeout, a cancelled request gets
// no body (the client is gone), anything else 500 store_error with a fixed message so
// no SQL text or path reaches the page.
func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "timeout", "the store did not answer in time")
	case errors.Is(err, context.Canceled):
		// Nothing to answer: the request was cancelled.
	default:
		writeErr(w, http.StatusInternalServerError, "store_error", "the store could not be read")
	}
}
