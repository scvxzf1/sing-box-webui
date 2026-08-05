package api

import (
	"errors"
	"net/http"

	"sing-box-webui/internal/core"
)

type coreUpdateInput struct {
	Version string `json:"version"`
}

func (s *Server) coreStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.core == nil {
		writeError(w, r, http.StatusServiceUnavailable, "core_unavailable", "Core management is unavailable")
		return
	}
	info, err := s.core.Info(r.Context())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "core_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) updateCore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.core == nil {
		writeError(w, r, http.StatusServiceUnavailable, "core_unavailable", "Core management is unavailable")
		return
	}
	if s.coreChangeBlocked(r) {
		writeError(w, r, http.StatusConflict, "core_busy", "请先停止代理，再更新 sing-box 核心")
		return
	}
	var input coreUpdateInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	info, err := s.core.Update(r.Context(), input.Version)
	if err != nil {
		writeCoreOperationError(w, r, "core_update_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) rollbackCore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.core == nil {
		writeError(w, r, http.StatusServiceUnavailable, "core_unavailable", "Core management is unavailable")
		return
	}
	if s.coreChangeBlocked(r) {
		writeError(w, r, http.StatusConflict, "core_busy", "请先停止代理，再回滚 sing-box 核心")
		return
	}
	info, err := s.core.Rollback(r.Context())
	if err != nil {
		writeCoreOperationError(w, r, "core_rollback_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) coreChangeBlocked(r *http.Request) bool {
	if s.control == nil {
		return false
	}
	state := s.control.Status(r.Context()).State
	return state != "stopped" && state != "failed"
}

func writeCoreOperationError(w http.ResponseWriter, r *http.Request, code string, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, core.ErrUpdateUnsupported) {
		status = http.StatusConflict
	}
	writeError(w, r, status, code, err.Error())
}
