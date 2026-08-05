package api

import (
	"net/http"

	"sing-box-webui/internal/trafficpolicy"
)

func (s *Server) trafficPolicyStatus(w http.ResponseWriter, r *http.Request) {
	if s.trafficPolicy == nil {
		writeError(w, r, http.StatusServiceUnavailable, "traffic_policy_unavailable", "Traffic policy service is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.trafficPolicy.Get())
	case http.MethodPut:
		var input trafficpolicy.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		result, err := s.trafficPolicy.Update(r.Context(), input)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "traffic_policy_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}
