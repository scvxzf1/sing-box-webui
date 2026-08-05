package api

import (
	"errors"
	"net/http"

	"sing-box-webui/internal/routing"
)

func (s *Server) rulePoolsCollection(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "rules_unavailable", "Routing rule storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.rules.ListPools()})
	case http.MethodPost:
		var input routing.CreatePoolInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		pool, err := s.rules.CreatePool(input)
		if err != nil {
			writeRulePoolError(w, r, err)
			return
		}
		if !s.reapplyRules(w, r) {
			return
		}
		writeJSON(w, http.StatusCreated, pool)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) rulePoolItem(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "rules_unavailable", "Routing rule storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPatch:
		var input routing.UpdatePoolInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		pool, err := s.rules.UpdatePool(id, input)
		if err != nil {
			writeRulePoolError(w, r, err)
			return
		}
		if !s.reapplyRules(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, pool)
	case http.MethodDelete:
		if err := s.rules.DeletePool(id); err != nil {
			writeRulePoolError(w, r, err)
			return
		}
		if !s.reapplyRules(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) rulePoolsOrder(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "rules_unavailable", "Routing rule storage is unavailable")
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
	pools, err := s.rules.ReorderPools(input.IDs)
	if err != nil {
		writeRulePoolError(w, r, err)
		return
	}
	if !s.reapplyRules(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": pools})
}

func writeRulePoolError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, routing.ErrPoolNotFound) {
		writeError(w, r, http.StatusNotFound, "rule_pool_not_found", "Routing rule pool not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
