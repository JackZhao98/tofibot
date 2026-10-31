package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// routeExtensions exposes the UI-facing extension contract. Secret values are
// never returned; clients send the masked value (or omit it) to retain it.
func (s *Server) routeExtensions(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "extensions") {
		return false
	}
	if s.extensions == nil {
		writeErr(w, 503, "extensions_unavailable", "extensions unavailable")
		return true
	}
	if s.routeRepoStars(w, r, p) {
		return true
	}
	if s.routeGoogle(w, r, p) {
		return true
	}
	if s.routeLocalMCP(w, r, p) {
		return true
	}
	if s.routeSkillLibrary(w, r, p) {
		return true
	}
	if s.routeVMOAuth(w, r, p) {
		return true
	}
	if s.routeDesktopOAuth(w, r, p) || s.routePasteOAuth(w, r, p) {
		return true
	}
	if s.routeMCPToolTiers(w, r, p) {
		return true
	}
	switch {
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/oauth/pending") && r.Method == http.MethodGet:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/oauth/pending")
		page := oauthServicePage(extensions.OAuthServiceIdentity{})
		// Unlike the callback, this route retains ordinary owner authentication.
		if servers, err := s.extensions.ListMCP(); err == nil {
			for _, server := range servers {
				if server.Name == name {
					page = oauthServicePage(extensions.OAuthServiceIdentity{Name: server.Name, URL: server.URL})
					break
				}
			}
		}
		page.State, page.MessageKey, page.Lang = "pending", "msg_pending", s.oauthPageLanguage(r)
		writeOAuthCompletion(w, http.StatusOK, page)
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/oauth/start") && r.Method == http.MethodPost:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/oauth/start")
		// A provider that accepts only 127.0.0.1 redirects never gets the public-origin callback,
		// neither registered nor requested (the Manager refuses it too).
		if s.extensions.OAuthLoopbackOnly(name) {
			writeErr(w, 400, "oauth_loopback_only", "This connection signs in only through a 127.0.0.1 redirect. Use the Bot computer or the paste-back step.")
			return true
		}
		origin := s.webOAuthOrigin(r)
		if origin == "" || (r.TLS == nil && r.Header.Get("Origin") != origin) || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			writeErr(w, 400, "oauth_https_required", "Web authorization requires HTTPS. Over plain HTTP, authorize from the Bot computer or use HTTPS or the Tofi client.")
			return true
		}
		redirect := origin + "/api/extensions/mcp/" + name + "/oauth/callback"
		sid, u, e := s.extensions.OAuthStart(r.Context(), name, redirect)
		if errors.Is(e, extensions.ErrOAuthLoopbackRequired) {
			// The provider refused the public callback at registration; the connection is now marked loopback-only
			// and the client signs in through the paste-back route instead.
			writeErr(w, 409, "oauth_loopback_required", "This service accepts only a 127.0.0.1 redirect. Sign in with the paste-back step.")
		} else if e != nil {
			writeErr(w, 400, "oauth", extensions.OAuthPublicError(e))
		} else {
			writeJSON(w, 200, map[string]any{"session_id": sid, "authorization_url": u})
		}
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/oauth/callback") && r.Method == http.MethodGet:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/oauth/callback")
		redirect := ""
		if origin := s.webOAuthOrigin(r); origin != "" {
			redirect = origin + "/api/extensions/mcp/" + name + "/oauth/callback"
		}
		q := r.URL.Query()
		identity, err := s.extensions.OAuthWebCallback(r.Context(), name, q.Get("session_id"), q.Get("code"), q.Get("state"), redirect, q.Get("error") != "")
		page := oauthServicePage(identity)
		page.State, page.MessageKey, page.Lang = "failed", "msg_callback_invalid", s.oauthPageLanguage(r)
		status := http.StatusBadRequest
		if errors.Is(err, extensions.ErrOAuthDenied) {
			page.State, page.MessageKey = "denied", "msg_denied"
			if page.APIURL != "" {
				page.Setup = "audience"
			}
		} else if err == nil {
			_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
			// Read the tools right away: a working connection says so and shows nothing else; a missing API or
			// test user is named with the one place that fixes it.
			oauthVerifiedPage(&page, s.extensions.InspectMCP(r.Context(), name))
			status = http.StatusOK
		}
		writeOAuthCompletion(w, status, page)
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/oauth/disconnect") && r.Method == http.MethodPost:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/oauth/disconnect")
		e := s.extensions.OAuthDisconnect(name)
		// Local credentials may be removed even when provider revocation fails.
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			writeErr(w, 400, "oauth", "OAuth credentials were removed locally, but provider revocation failed")
		} else {
			writeJSON(w, 200, map[string]any{"ok": true})
		}
		return true
	case p == "extensions/mcp" && r.Method == http.MethodGet:
		v, e := s.extensions.ListMCP()
		if e != nil {
			writeErr(w, 500, "extensions", "extension configuration could not be read")
		} else {
			writeJSON(w, 200, map[string]any{"servers": v})
		}
		return true
	case p == "extensions/mcp" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		var in struct {
			Name          string                  `json:"name"`
			URL           string                  `json:"url"`
			Transport     string                  `json:"transport"`
			Headers       map[string]string       `json:"headers"`
			ToolAllowlist []string                `json:"tool_allowlist"`
			ToolDenylist  []string                `json:"tool_denylist"`
			OAuth         *extensions.OAuthConfig `json:"oauth"`
			// GoogleClient: sign in with the workspace's saved Google OAuth client instead of asking for one again.
			GoogleClient bool `json:"google_client"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", e.Error())
			return true
		}
		if in.GoogleClient && !s.useWorkspaceGoogleClient(w, &in.OAuth) {
			return true
		}
		e := s.extensions.SaveMCP(in.Name, extensions.MCPServerConfig{URL: in.URL, Transport: in.Transport, Headers: in.Headers, ToolAllowlist: in.ToolAllowlist, ToolDenylist: in.ToolDenylist, OAuth: in.OAuth}, r.Method == http.MethodPut)
		if e != nil {
			writeErr(w, 400, "extensions", e.Error())
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/bots") && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		s.routeConnectionBots(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/bots"))
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/enabled") && r.Method == http.MethodPut:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/enabled")
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil || in.Enabled == nil {
			writeErr(w, 400, "invalid_request", "enabled is required")
			return true
		}
		if e := s.extensions.SetMCPEnabled(name, *in.Enabled); e != nil {
			if errors.Is(e, os.ErrNotExist) {
				writeErr(w, 404, "extensions", "MCP server not found")
			} else {
				writeErr(w, 500, "extensions", "extension configuration could not be written")
			}
			return true
		}
		s.writeExtensionSaved(w, http.StatusOK)
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && r.Method == http.MethodPut:
		name := strings.TrimPrefix(p, "extensions/mcp/")
		var in struct {
			URL           string                  `json:"url"`
			Transport     string                  `json:"transport"`
			Headers       map[string]string       `json:"headers"`
			ToolAllowlist []string                `json:"tool_allowlist"`
			ToolDenylist  []string                `json:"tool_denylist"`
			OAuth         *extensions.OAuthConfig `json:"oauth"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", e.Error())
			return true
		}
		e := s.extensions.SaveMCP(name, extensions.MCPServerConfig{URL: in.URL, Transport: in.Transport, Headers: in.Headers, ToolAllowlist: in.ToolAllowlist, ToolDenylist: in.ToolDenylist, OAuth: in.OAuth}, true)
		if e != nil {
			writeErr(w, 400, "extensions", e.Error())
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && strings.HasSuffix(p, "/test") && r.Method == http.MethodPost:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/mcp/"), "/test")
		inspection := s.extensions.InspectMCP(r.Context(), name)
		writeJSON(w, 200, map[string]any{"ok": len(inspection.Diagnostics) == 0, "tool_count": inspection.ToolCount, "auth_required": inspection.AuthRequired, "diagnostics": inspection.Diagnostics})
		return true
	case strings.HasPrefix(p, "extensions/mcp/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(p, "extensions/mcp/")
		if e := s.deleteMCPServer(name); e != nil {
			writeErr(w, 404, "extensions", e.Error())
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	}
	return false
}

func decodeExtensionJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("request body required")
	}
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// Extension files and OAuth credentials are persisted by their existing manager.
// This event contains only an invalidation scope, never configuration or tokens.
func (s *Server) writeExtensionSaved(w http.ResponseWriter, status int) {
	_, err := s.store.WorkspaceEvent(workspaceScopeConfig)
	if err != nil {
		writeJSON(w, status, map[string]any{"ok": true, "next_run": true, "sync_pending": true})
		return
	}
	writeJSON(w, status, map[string]any{"ok": true, "next_run": true})
}
