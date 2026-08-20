package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"sing-box-webui/internal/connectivity"
)

func (s *Server) connectivityCollection(w http.ResponseWriter, r *http.Request) {
	if s.connectivity == nil {
		writeError(w, r, http.StatusServiceUnavailable, "connectivity_unavailable", "Connectivity testing is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.connectivity.List()})
	case http.MethodPost:
		var input connectivity.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		target, err := s.connectivity.Create(input)
		if err != nil {
			s.writeConnectivityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, target)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) connectivityItem(w http.ResponseWriter, r *http.Request) {
	if s.connectivity == nil {
		writeError(w, r, http.StatusServiceUnavailable, "connectivity_unavailable", "Connectivity testing is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPatch:
		var input connectivity.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		target, err := s.connectivity.Update(id, input)
		if err != nil {
			s.writeConnectivityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, target)
	case http.MethodDelete:
		if err := s.connectivity.Delete(id); err != nil {
			s.writeConnectivityError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

// connectivityTest handles POST /api/v1/connectivity/test (all targets) and
// POST /api/v1/connectivity/{id}/test (single target).
func (s *Server) connectivityTest(w http.ResponseWriter, r *http.Request) {
	if s.connectivity == nil {
		writeError(w, r, http.StatusServiceUnavailable, "connectivity_unavailable", "Connectivity testing is unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	response, err := s.connectivity.Test(ctx, r.PathValue("id"))
	if err != nil {
		s.writeConnectivityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) connectivityDiagnostic(w http.ResponseWriter, r *http.Request) {
	if s.connectivity == nil {
		writeError(w, r, http.StatusServiceUnavailable, "connectivity_unavailable", "Connectivity testing is unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	var input connectivity.DiagnosticInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := s.connectivity.Diagnose(ctx, input)
	if err != nil {
		s.writeConnectivityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeConnectivityError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, connectivity.ErrInvalidProvider) {
		writeError(w, r, http.StatusBadRequest, "diagnostic_provider_invalid", "Diagnostic provider is invalid")
		return
	}
	if errors.Is(err, connectivity.ErrProxyStopped) {
		writeError(w, r, http.StatusConflict, "proxy_not_running", "Proxy is not running")
		return
	}
	if errors.Is(err, connectivity.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "connectivity_target_not_found", "Connectivity target not found")
		return
	}
	s.writeInternalError(w, r, http.StatusUnprocessableEntity, "operation_failed", "Connectivity operation failed", err)
}
