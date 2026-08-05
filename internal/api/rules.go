package api

import (
	"errors"
	"net/http"

	"sing-box-webui/internal/routing"
)

func (s *Server) rulesCollection(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "rules_unavailable", "Routing rule storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.rules.List()})
	case http.MethodPost:
		var input routing.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		rule, err := s.rules.Create(input)
		if err != nil {
			writeRuleError(w, r, err)
			return
		}
		if !s.reapplyRules(w, r) {
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) ruleItem(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "rules_unavailable", "Routing rule storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPatch:
		var input routing.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		rule, err := s.rules.Update(id, input)
		if err != nil {
			writeRuleError(w, r, err)
			return
		}
		if !s.reapplyRules(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		if err := s.rules.Delete(id); err != nil {
			writeRuleError(w, r, err)
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

func (s *Server) rulesOrder(w http.ResponseWriter, r *http.Request) {
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
	rules, err := s.rules.Reorder(input.IDs)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	if !s.reapplyRules(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rules})
}

func (s *Server) reapplyRules(w http.ResponseWriter, r *http.Request) bool {
	if s.control == nil {
		return true
	}
	if _, err := s.control.ReapplyRules(r.Context()); err != nil {
		writeError(w, r, http.StatusConflict, "runtime_reapply_failed", "Rule was saved but the running proxy could not reload it: "+err.Error())
		return false
	}
	return true
}

func writeRuleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, routing.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "rule_not_found", "Routing rule not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
