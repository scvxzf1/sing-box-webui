package api

import (
	"errors"
	"net/http"

	"sing-box-webui/internal/proxychannel"
)

func (s *Server) channelsCollection(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, r, http.StatusServiceUnavailable, "channels_unavailable", "Proxy channel storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": s.channels.List()})
	case http.MethodPost:
		var input proxychannel.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		channel, err := s.channels.Create(input)
		if err != nil {
			writeChannelError(w, r, err)
			return
		}
		if err := s.channels.Reload(); err != nil {
			s.writeInternalError(w, r, http.StatusConflict, "runtime_reapply_failed", "Proxy channel was saved but the running proxy could not reload it", err)
			return
		}
		writeJSON(w, http.StatusCreated, channel)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) channelItem(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, r, http.StatusServiceUnavailable, "channels_unavailable", "Proxy channel storage is unavailable")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		channel, err := s.channels.Get(id)
		if err != nil {
			writeChannelError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, channel)
	case http.MethodPatch:
		var input proxychannel.UpdateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", err.Error())
			return
		}
		channel, err := s.channels.Update(id, input)
		if err != nil {
			writeChannelError(w, r, err)
			return
		}
		if err := s.channels.Reload(); err != nil {
			s.writeInternalError(w, r, http.StatusConflict, "runtime_reapply_failed", "Proxy channel was saved but the running proxy could not reload it", err)
			return
		}
		writeJSON(w, http.StatusOK, channel)
	case http.MethodDelete:
		if err := s.channels.Delete(id); err != nil {
			writeChannelError(w, r, err)
			return
		}
		if err := s.channels.Reload(); err != nil {
			s.writeInternalError(w, r, http.StatusConflict, "runtime_reapply_failed", "Proxy channel was deleted but the running proxy could not reload it", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
	}
}

func (s *Server) channelCertificate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.channels == nil {
		writeError(w, r, http.StatusServiceUnavailable, "channels_unavailable", "Proxy channel storage is unavailable")
		return
	}
	content, err := s.channels.Certificate()
	if err != nil {
		s.writeInternalError(w, r, http.StatusInternalServerError, "certificate_unavailable", "Proxy channel certificate is unavailable", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="sing-box-webui-channel-ca.pem"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func writeChannelError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, proxychannel.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "channel_not_found", "Proxy channel not found")
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "operation_failed", err.Error())
}
