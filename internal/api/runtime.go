package api

import (
	"net/http"

	"sing-box-webui/internal/control"
)

func (s *Server) runtimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.control == nil {
		writeError(w, r, http.StatusServiceUnavailable, "runtime_unavailable", "Runtime control is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.control.Status(r.Context()))
}

func (s *Server) applyRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.control == nil {
		writeError(w, r, http.StatusServiceUnavailable, "runtime_unavailable", "Runtime control is unavailable")
		return
	}
	var input control.ApplyInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	runtime, err := s.control.Apply(r.Context(), input)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runtime)
}

func (s *Server) stopRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.control == nil {
		writeError(w, r, http.StatusServiceUnavailable, "runtime_unavailable", "Runtime control is unavailable")
		return
	}
	runtime, err := s.control.Stop(r.Context())
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "stop_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runtime)
}
