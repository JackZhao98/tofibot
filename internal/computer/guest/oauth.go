package guest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	oauthMaxState = 4096
	oauthMaxCode  = 8192
	oauthMaxError = 1024
)

type oauthSession struct {
	mu        sync.Mutex
	id        string
	path      string
	listener  net.Listener
	server    *http.Server
	state     string
	armed     bool
	status    string
	code      string
	err       string
	delivered bool
	stop      chan struct{}
	stopOnce  sync.Once
}

type oauthStartRequest struct{}
type oauthStartResponse struct {
	SessionID   string `json:"session_id"`
	RedirectURI string `json:"redirect_uri"`
}
type oauthArmRequest struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}
type oauthPollRequest struct {
	SessionID string `json:"session_id"`
}
type oauthCancelRequest struct {
	SessionID string `json:"session_id"`
}
type oauthPollResponse struct {
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	State  string `json:"state,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Service) handleOAuth(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/oauth/start":
		if r.Method != http.MethodPost {
			writeOAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.oauthStart(w, r)
	case "/v1/oauth/arm":
		if r.Method != http.MethodPost {
			writeOAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.oauthArm(w, r)
	case "/v1/oauth/poll":
		if r.Method != http.MethodPost {
			writeOAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.oauthPoll(w, r)
	case "/v1/oauth/cancel":
		if r.Method != http.MethodPost {
			writeOAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.oauthCancel(w, r)
	default:
		writeOAuthError(w, http.StatusNotFound, "not found")
	}
}

func (s *Service) oauthStart(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var req oauthStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeOAuthError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	if s.oauthClosed {
		writeOAuthError(w, http.StatusServiceUnavailable, "service is stopping")
		return
	}
	if s.oauth != nil && s.oauth.isPending() {
		writeOAuthError(w, http.StatusConflict, "oauth session already active")
		return
	}
	id, err := randomToken(24)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "unable to create session")
		return
	}
	pathToken, err := randomToken(32)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "unable to create callback")
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "unable to bind callback")
		return
	}
	sess := &oauthSession{id: id, path: "/oauth/callback/" + pathToken, listener: ln, status: "pending", stop: make(chan struct{})}
	sess.server = &http.Server{Handler: http.HandlerFunc(func(cw http.ResponseWriter, cr *http.Request) { s.oauthCallback(sess, cw, cr) }), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 * 1024}
	s.oauth = sess
	go func() { _ = sess.server.Serve(ln) }()
	go s.oauthExpire(sess)
	writeJSON(w, http.StatusOK, oauthStartResponse{SessionID: id, RedirectURI: "http://127.0.0.1:" + fmt.Sprint(ln.Addr().(*net.TCPAddr).Port) + sess.path})
}

func (s *Service) oauthArm(w http.ResponseWriter, r *http.Request) {
	var req oauthArmRequest
	if !decodeOAuthJSON(w, r, &req) || req.SessionID == "" || len(req.State) == 0 || len(req.State) > oauthMaxState {
		writeOAuthError(w, http.StatusBadRequest, "invalid session or state")
		return
	}
	s.oauthMu.Lock()
	sess := s.oauth
	s.oauthMu.Unlock()
	if sess == nil || sess.id != req.SessionID {
		writeOAuthError(w, http.StatusNotFound, "session not found")
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.status != "pending" {
		writeOAuthError(w, http.StatusGone, "session is no longer active")
		return
	}
	if sess.armed {
		writeOAuthError(w, http.StatusConflict, "session already armed")
		return
	}
	sess.state, sess.armed = req.State, true
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) oauthPoll(w http.ResponseWriter, r *http.Request) {
	var req oauthPollRequest
	if !decodeOAuthJSON(w, r, &req) || req.SessionID == "" {
		writeOAuthError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	s.oauthMu.Lock()
	sess := s.oauth
	s.oauthMu.Unlock()
	if sess == nil || sess.id != req.SessionID {
		writeOAuthError(w, http.StatusNotFound, "session not found")
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	resp := oauthPollResponse{Status: sess.status}
	if sess.status == "complete" && !sess.delivered {
		resp.Code, resp.State = sess.code, sess.state
		sess.delivered = true
		sess.code, sess.state = "", ""
	}
	if sess.status == "complete" && sess.delivered && resp.Code == "" {
		resp.Status = "expired"
	}
	if sess.status == "denied" {
		resp.Error = sess.err
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) oauthCancel(w http.ResponseWriter, r *http.Request) {
	var req oauthCancelRequest
	if !decodeOAuthJSON(w, r, &req) || req.SessionID == "" {
		writeOAuthError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	s.oauthMu.Lock()
	sess := s.oauth
	s.oauthMu.Unlock()
	if sess == nil || sess.id != req.SessionID {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	sess.mu.Lock()
	if sess.status == "pending" {
		sess.status, sess.err, sess.armed = "denied", "cancelled", false
	}
	sess.state, sess.code = "", ""
	sess.stopSession(false)
	sess.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) oauthCallback(sess *oauthSession, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path != sess.path {
		writeOAuthError(w, http.StatusNotFound, "not found")
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	sess.mu.Lock()
	if sess.status != "pending" {
		sess.mu.Unlock()
		writeOAuthError(w, http.StatusConflict, "callback already handled")
		return
	}
	if !sess.armed || state == "" || state != sess.state {
		sess.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid callback")
		return
	}
	code, oauthErr := q.Get("code"), q.Get("error")
	if (code == "" && oauthErr == "") || (code != "" && oauthErr != "") || len(code) > oauthMaxCode || len(oauthErr) > oauthMaxError {
		sess.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid callback")
		return
	}
	if oauthErr != "" {
		sess.status, sess.err = "denied", oauthErr
	} else {
		sess.status, sess.code = "complete", code
	}
	sess.stopSession(true)
	sess.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Authorization complete. You may return to the application."))
}

func (s *Service) oauthExpire(sess *oauthSession) {
	ttl := s.oauthTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	t := time.NewTimer(ttl)
	defer t.Stop()
	select {
	case <-t.C:
	case <-sess.stop:
		return
	}
	sess.mu.Lock()
	if sess.status == "pending" {
		sess.status, sess.armed = "expired", false
		sess.state, sess.code = "", ""
		sess.stopSession(false)
	}
	sess.mu.Unlock()
}

func (s *Service) closeOAuth() {
	s.oauthMu.Lock()
	s.oauthClosed = true
	sess := s.oauth
	s.oauthMu.Unlock()
	if sess != nil {
		sess.mu.Lock()
		sess.status = "expired"
		sess.armed = false
		sess.state, sess.code = "", ""
		sess.stopSession(false)
		sess.mu.Unlock()
	}
}

func (sess *oauthSession) stopSession(graceful bool) {
	sess.stopOnce.Do(func() {
		close(sess.stop)
		_ = sess.listener.Close()
		if sess.server != nil {
			if graceful {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					if sess.server.Shutdown(ctx) != nil {
						_ = sess.server.Close()
					}
				}()
			} else {
				_ = sess.server.Close()
			}
		}
	})
}
func (s *oauthSession) isPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status == "pending"
}
func decodeOAuthJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst) == nil
}
func writeOAuthError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
