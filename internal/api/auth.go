package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	authCookieName  = "sing_box_webui_session"
	sessionLifetime = 24 * time.Hour
)

type loginRequest struct {
	Token string `json:"token"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input loginRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, r, http.StatusBadRequest, "request_invalid", "The request body is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, r, http.StatusBadRequest, "request_invalid", "The request body is invalid")
		return
	}
	if s.webToken == "" || subtle.ConstantTimeCompare([]byte(input.Token), []byte(s.webToken)) != 1 {
		writeError(w, r, http.StatusUnauthorized, "token_invalid", "The access token is invalid")
		return
	}

	expires := time.Now().Add(sessionLifetime)
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: s.signSession(expires), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionLifetime.Seconds()), Expires: expires,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func (s *Server) authenticated(r *http.Request) bool {
	if s.webToken == "" {
		return true
	}
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= expiresUnix {
		return false
	}
	expected := s.sessionMAC(parts[0])
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	return err == nil && hmac.Equal(provided, expected)
}

func (s *Server) signSession(expires time.Time) string {
	payload := strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.sessionMAC(payload))
}

func (s *Server) sessionMAC(payload string) []byte {
	mac := hmac.New(sha256.New, s.sessionSecret[:])
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
