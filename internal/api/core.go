package api

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	info, err := s.core.Info(ctx)
	if err != nil {
		s.writeInternalError(w, r, http.StatusServiceUnavailable, "core_unavailable", "Core information is unavailable", err)
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	info, err := s.core.Update(ctx, input.Version)
	if err != nil {
		s.writeCoreOperationError(w, r, "core_update_failed", err)
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.core.Rollback(ctx)
	if err != nil {
		s.writeCoreOperationError(w, r, "core_rollback_failed", err)
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

func (s *Server) writeCoreOperationError(w http.ResponseWriter, r *http.Request, code string, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, core.ErrUpdateUnsupported) {
		writeError(w, r, http.StatusConflict, code, "Core update is not supported in this environment")
		return
	}
	s.writeInternalError(w, r, status, code, "Core operation failed", err)
}
