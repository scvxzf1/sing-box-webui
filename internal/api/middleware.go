package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
)

type requestIDKey struct{}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		r = r.WithContext(withRequestID(r.Context(), requestID))

		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Request-ID", requestID)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}

		if !s.allowedHost(r.Host) {
			writeError(w, r, http.StatusBadRequest, "invalid_host", "The request host is not allowed")
			return
		}

		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			writeError(w, r, http.StatusForbidden, "cross_site_request", "Cross-site requests are not allowed")
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" && !s.allowedOrigin(origin) {
			writeError(w, r, http.StatusForbidden, "invalid_origin", "The request origin is not allowed")
			return
		}
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/auth/login" {
			if isUnsafeMethod(r.Method) && r.Header.Get("Origin") == "" {
				writeError(w, r, http.StatusForbidden, "origin_required", "An allowed browser origin is required")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !s.authenticated(r) {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		if isUnsafeMethod(r.Method) {
			if r.Header.Get("Origin") == "" {
				writeError(w, r, http.StatusForbidden, "origin_required", "An allowed browser origin is required")
				return
			}
			provided := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrfToken)) != 1 {
				writeError(w, r, http.StatusForbidden, "csrf_invalid", "The CSRF token is invalid")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func (s *Server) allowedHost(host string) bool {
	return strings.EqualFold(host, s.address)
}

func (s *Server) allowedOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return false
	}

	return strings.EqualFold(parsed.Host, s.address) || origin == s.devOrigin
}

func newRequestID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(bytes[:])
}

func newCSRFToken() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}
