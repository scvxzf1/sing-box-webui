package api

import (
	"context"
	"net/http"
	"time"

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
	runtime := s.control.Status(r.Context())
	if runtime.LastError != "" {
		runtime.LastError = "The managed process reported an error; see server logs for details"
	}
	writeJSON(w, http.StatusOK, runtime)
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
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	runtime, err := s.control.Apply(ctx, input)
	if err != nil {
		s.writeInternalError(w, r, http.StatusUnprocessableEntity, "apply_failed", "Proxy configuration could not be applied", err)
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
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	runtime, err := s.control.Stop(ctx)
	if err != nil {
		s.writeInternalError(w, r, http.StatusUnprocessableEntity, "stop_failed", "Proxy could not be stopped", err)
		return
	}
	writeJSON(w, http.StatusOK, runtime)
}
