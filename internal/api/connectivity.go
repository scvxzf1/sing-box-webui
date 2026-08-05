package api

import (
	"errors"
	"net/http"

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
			writeConnectivityError(w, r, err)
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
			writeConnectivityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, target)
	case http.MethodDelete:
		if err := s.connectivity.Delete(id); err != nil {
			writeConnectivityError(w, r, err)
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
	response, err := s.connectivity.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		writeConnectivityError(w, r, err)
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
	result, err := s.connectivity.Diagnose(r.Context(), input)
	if err != nil {
		writeConnectivityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeConnectivityError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, connectivity.ErrInvalidProvider) {
		writeError(w, r, http.StatusBadRequest, "diagnostic_provider_invalid", err.Error())
		return
	}
	if errors.Is(err, connectivity.ErrProxyStopped) {
		writeError(w, r, http.StatusConflict, "proxy_not_running", err.Error())
		return
	}
	if errors.Is(err, connectivity.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "connectivity_target_not_found", "Connectivity target not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
