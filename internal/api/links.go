package api

import (
	"net/http"
	"strconv"
	"strings"

	"sing-box-webui/internal/connmon"
)

// links returns a filtered, sorted, paged snapshot of observed proxy connections.
//
// Query parameters:
//   - search: substring match across host/node/network/type/chain (case-insensitive)
//   - active: "true"/"false" to filter to live or closed connections
//   - sort:   comma-separated keys, "-key" for descending
//     (host,url,node,upload,download,uploadRate,downloadRate,startedAt)
//   - offset, limit: pagination
func (s *Server) links(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.control == nil || s.control.Links() == nil {
		writeError(w, r, http.StatusServiceUnavailable, "links_unavailable", "Connection monitoring is unavailable")
		return
	}
	query := r.URL.Query()
	q := connmon.Query{
		Search: query.Get("search"),
		Sort:   parseOrdering(query.Get("sort")),
	}
	if active := query.Get("active"); active != "" {
		parsed, err := strconv.ParseBool(active)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "request_invalid", "The active parameter must be a boolean")
			return
		}
		q.Active = &parsed
	}
	q.Offset = parseNonNegativeInt(query.Get("offset"))
	q.Limit = parseNonNegativeInt(query.Get("limit"))
	writeJSON(w, http.StatusOK, s.control.Links().Query(q))
}

// clearLinks empties the connection cache without stopping the monitor.
func (s *Server) clearLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	if s.control == nil || s.control.Links() == nil {
		writeError(w, r, http.StatusServiceUnavailable, "links_unavailable", "Connection monitoring is unavailable")
		return
	}
	s.control.Links().Reset()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parseOrdering(raw string) []connmon.Ordering {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var ordering []connmon.Ordering
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		desc := false
		if strings.HasPrefix(field, "-") {
			desc = true
			field = strings.TrimPrefix(field, "-")
		}
		key := connmon.SortKey(field)
		if !validSortKey(key) {
			continue
		}
		ordering = append(ordering, connmon.Ordering{Key: key, Desc: desc})
	}
	return ordering
}

func validSortKey(key connmon.SortKey) bool {
	switch key {
	case connmon.SortHost, connmon.SortURL, connmon.SortNode, connmon.SortUpload, connmon.SortDownload,
		connmon.SortUploadRate, connmon.SortDownloadRate, connmon.SortStartedAt:
		return true
	default:
		return false
	}
}

func parseNonNegativeInt(raw string) int {
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}
