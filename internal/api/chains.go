package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"sing-box-webui/internal/latency"
	"sing-box-webui/internal/proxychain"
)

func (s *Server) chainsCollection(w http.ResponseWriter, r *http.Request) {
	if s.chains == nil {
		writeError(w, r, http.StatusServiceUnavailable, "chains_unavailable", "Proxy chain storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.chains.List()})
	case http.MethodPost:
		var input proxychain.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		chain, err := s.chains.Create(input)
		if err != nil {
			writeChainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, chain)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) chainItem(w http.ResponseWriter, r *http.Request) {
	if s.chains == nil {
		writeError(w, r, http.StatusServiceUnavailable, "chains_unavailable", "Proxy chain storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		chain, err := s.chains.Get(id)
		if err != nil {
			writeChainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, chain)
	case http.MethodPatch:
		var input proxychain.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		chain, err := s.chains.Update(id, input)
		if err != nil {
			writeChainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, chain)
	case http.MethodDelete:
		if s.control != nil {
			runtime := s.control.Status(r.Context())
			if runtime.State == "running" && runtime.TargetType == "chain" && runtime.ChainID == id {
				writeError(w, r, http.StatusConflict, "chain_running", "Switch away from this proxy chain before deleting it")
				return
			}
		}
		if err := s.chains.Delete(id); err != nil {
			writeChainError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) testChainLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	tester, ok := s.latency.(interface {
		TestChain(context.Context, proxychain.Resolved) (latency.Response, error)
	})
	if !ok || s.chains == nil {
		writeError(w, r, http.StatusServiceUnavailable, "latency_unavailable", "Proxy chain latency testing is unavailable")
		return
	}
	chain, err := s.chains.Resolve(r.PathValue("id"))
	if err != nil {
		writeChainError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	response, err := tester.TestChain(ctx, chain)
	if err != nil {
		if errors.Is(err, latency.ErrUnavailable) {
			writeError(w, r, http.StatusServiceUnavailable, "latency_unavailable", "sing-box 核心不可用，无法进行链路测试")
			return
		}
		if errors.Is(err, latency.ErrBusy) {
			writeError(w, r, http.StatusConflict, "latency_busy", "测速任务已达到并发上限")
			return
		}
		writeChainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeChainError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, proxychain.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "chain_not_found", "Proxy chain not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
