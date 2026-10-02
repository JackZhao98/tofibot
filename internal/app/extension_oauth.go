package app

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// A fixed loopback port lets manually configured Web OAuth clients register
// both the public server callback and the desktop callback. Keep in sync with
// the standalone desktop MCP OAuth client. The desktop refuses to fall back to another port.
const desktopOAuthRedirect = "http://127.0.0.1:43821/oauth/callback"

func (s *Server) webOAuthOrigin(r *http.Request) string {
	origin := s.publicOrigin
	if origin == "" && r.TLS != nil {
		origin = schemeHost(r)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	return strings.TrimRight(origin, "/")
}

func (s *Server) routeDesktopOAuth(w http.ResponseWriter, r *http.Request, p string) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/oauth/desktop/start") {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/oauth/desktop/start")
		sid, authorization, err := s.extensions.OAuthStart(r.Context(), name, desktopOAuthRedirect)
		if err != nil {
			writeErr(w, 400, "oauth", extensions.OAuthPublicError(err))
		} else {
			writeJSON(w, 200, map[string]any{"session_id": sid, "authorization_url": authorization, "redirect_uri": desktopOAuthRedirect})
		}
		return true
	}
	if p != "extensions/oauth/desktop/complete" && p != "extensions/oauth/desktop/cancel" {
		return false
	}
	// Both routes require the ordinary authenticated owner session. No client
	// secret, token or user-chosen redirect address crosses into the renderer.
	var in struct {
		SessionID string `json:"session_id"`
		Code      string `json:"code"`
		State     string `json:"state"`
	}
	if err := decodeExtensionJSON(r, &in); err != nil || in.SessionID == "" {
		writeErr(w, 400, "invalid_request", "session_id is required")
		return true
	}
	if strings.HasSuffix(p, "/cancel") {
		s.extensions.OAuthCancel(in.SessionID)
		writeJSON(w, 200, map[string]any{"ok": true})
		return true
	}
	if err := s.extensions.OAuthCallbackForRedirect(r.Context(), in.SessionID, in.Code, in.State, desktopOAuthRedirect); err != nil {
		writeErr(w, 400, "oauth", "Authorization could not be verified. Please connect again.")
		return true
	}
	_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
	writeJSON(w, 200, map[string]any{"ok": true})
	return true
}
