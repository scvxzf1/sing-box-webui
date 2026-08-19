package api

import (
	"errors"
	"net/http"

	"sing-box-webui/internal/nodepool"
)

func (s *Server) poolsCollection(w http.ResponseWriter, r *http.Request) {
	if s.pools == nil {
		writeError(w, r, http.StatusServiceUnavailable, "pools_unavailable", "Node pool storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.pools.List()})
	case http.MethodPost:
		var input nodepool.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		pool, err := s.pools.Create(input)
		if err != nil {
			writePoolError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, pool)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) poolsOrder(w http.ResponseWriter, r *http.Request) {
	if s.pools == nil {
		writeError(w, r, http.StatusServiceUnavailable, "pools_unavailable", "Node pool storage is unavailable")
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
	items, err := s.pools.Reorder(input.IDs)
	if err != nil {
		writePoolError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) poolItem(w http.ResponseWriter, r *http.Request) {
	if s.pools == nil {
		writeError(w, r, http.StatusServiceUnavailable, "pools_unavailable", "Node pool storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		pool, err := s.pools.Get(id)
		if err != nil {
			writePoolError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, pool)
	case http.MethodPatch:
		var input nodepool.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		pool, err := s.pools.Update(id, input)
		if err != nil {
			writePoolError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, pool)
	case http.MethodDelete:
		if err := s.pools.Delete(id); err != nil {
			writePoolError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func writePoolError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, nodepool.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "pool_not_found", "Node pool not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
