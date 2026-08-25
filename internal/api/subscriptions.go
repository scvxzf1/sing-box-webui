package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sing-box-webui/internal/latency"
	"sing-box-webui/internal/subscription"
)

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrfToken": s.csrfToken})
}

func (s *Server) subscriptionsCollection(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.subscriptions.List()})
	case http.MethodPost:
		var input subscription.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		item, err := s.subscriptions.Create(ctx, input)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) subscriptionsOrder(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	items, err := s.subscriptions.Reorder(input.IDs)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) subscriptionItem(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		item, err := s.subscriptions.Get(id)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPatch:
		var input subscription.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		item, err := s.subscriptions.Update(id, input)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := s.subscriptions.Delete(id); err != nil {
			writeDomainError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) refreshSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := s.subscriptions.Refresh(ctx, r.PathValue("id")); err != nil {
		writeDomainError(w, r, err)
		return
	}
	item, err := s.subscriptions.Get(r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) activateSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	item, err := s.subscriptions.Activate(r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) selectNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	var input struct {
		NodeID string `json:"nodeId"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.NodeID == "" {
		writeError(w, r, http.StatusBadRequest, "request_invalid", "nodeId is required")
		return
	}
	item, err := s.subscriptions.SelectNode(r.PathValue("id"), input.NodeID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) importSubscriptionNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	var input subscription.ImportNodesInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	result, err := s.subscriptions.ImportNodes(r.PathValue("id"), input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) subscriptionNodeLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.subscriptions == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscriptions_unavailable", "Subscription storage is unavailable")
		return
	}
	result, err := s.subscriptions.NodeLink(r.PathValue("id"), r.PathValue("nodeId"))
	if err != nil {
		if errors.Is(err, subscription.ErrNodeNotFound) {
			writeError(w, r, http.StatusNotFound, "node_not_found", "Node not found")
			return
		}
		if errors.Is(err, subscription.ErrNotFound) {
			writeDomainError(w, r, err)
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "node_link_unavailable", "Node link is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) testNodeLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.latency == nil {
		writeError(w, r, http.StatusServiceUnavailable, "latency_unavailable", "Node latency testing is unavailable")
		return
	}
	var input struct {
		NodeIDs []string `json:"nodeIds"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
		return
	}
	if len(input.NodeIDs) > latency.MaxTargets {
		writeError(w, r, http.StatusBadRequest, "request_invalid", fmt.Sprintf("单次最多测试 %d 个节点", latency.MaxTargets))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	response, err := s.latency.Test(ctx, r.PathValue("id"), input.NodeIDs)
	if err != nil {
		if errors.Is(err, latency.ErrUnavailable) {
			writeError(w, r, http.StatusServiceUnavailable, "latency_unavailable", "sing-box 核心不可用，无法进行真实代理延迟测试")
			return
		}
		if errors.Is(err, latency.ErrBusy) {
			writeError(w, r, http.StatusConflict, "latency_busy", "测速任务已达到并发上限")
			return
		}
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, subscription.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "subscription_not_found", "Subscription not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
