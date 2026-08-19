package api

import (
	"net/http"

	"sing-box-webui/internal/dnsprofile"
)

func (s *Server) dnsProfileResource(w http.ResponseWriter, r *http.Request) {
	if s.dns == nil {
		writeError(w, r, http.StatusServiceUnavailable, "dns_profile_unavailable", "DNS profile service is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.dns.Get())
	case http.MethodPut:
		var input dnsprofile.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		profile, err := s.dns.Update(input)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "dns_profile_invalid", err.Error())
			return
		}
		if err := s.dns.Reload(); err != nil {
			s.writeInternalError(w, r, http.StatusConflict, "runtime_reapply_failed", "DNS profile was saved but the running proxy could not reload it", err)
			return
		}
		writeJSON(w, http.StatusOK, profile)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}
