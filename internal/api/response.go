package api

import (
	"encoding/json"
	"net/http"
)

type errorBody struct {
	Error     errorDetail `json:"error"`
	RequestID string      `json:"requestId"`
}

type errorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestID, _ := r.Context().Value(requestIDKey{}).(string)
	writeJSON(w, status, errorBody{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
		RequestID: requestID,
	})
}
