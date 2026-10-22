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
	if s.routeSkillAccess(w, r, p) || s.routeBuiltinSkillEnabled(w, r, p) {
		return true
	}
	if s.routeVMOAuth(w, r, p) {
		return true
	}
	if s.routeDesktopOAuth(w, r, p) {
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
		origin := s.webOAuthOrigin(r)
		if origin == "" || (r.TLS == nil && r.Header.Get("Origin") != origin) || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			writeErr(w, 400, "oauth_https_required", "网页授权需要 HTTPS。HTTP 直连请使用共享电脑授权，或改用 HTTPS / Tofi 客户端。")
			return true
		}
		redirect := origin + "/api/extensions/mcp/" + name + "/oauth/callback"
		sid, u, e := s.extensions.OAuthStart(r.Context(), name, redirect)
		if e != nil {
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
	case p == "extensions/skills" && r.Method == http.MethodGet:
		v, d := s.extensions.ListSkills()
		access, err := s.store.skillAccessMap()
		if err != nil {
			writeErr(w, 500, "internal", "skill access unavailable")
			return true
		}
		active, err := s.store.activeBotSet()
		if err != nil {
			writeErr(w, 500, "internal", "skill access unavailable")
			return true
		}
		off, err := s.store.builtinSkillsOff(r.Context())
		if err != nil {
			writeErr(w, 500, "internal", "skill settings unavailable")
			return true
		}
		out := make([]skillWithAccess, 0, len(v))
		for _, x := range v {
			view := accessViewFor(access, x.Name)
			if view.Mode == "selected" {
				// Rows for archived or deleted Bots are kept but not shown.
				kept := []string{}
				for _, id := range view.BotIDs {
					if active[id] {
						kept = append(kept, id)
					}
				}
				view.BotIDs = kept
			}
			item := skillWithAccess{SkillView: x, Access: view}
			if x.Builtin {
				on := !off[x.Name]
				item.Enabled = &on
			}
			out = append(out, item)
		}
		writeJSON(w, 200, map[string]any{"skills": out, "diagnostics": d})
		return true
	case p == "extensions/skills" && r.Method == http.MethodPost:
		var in struct {
			Name  string                     `json:"name"`
			Files map[string]json.RawMessage `json:"files"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", e.Error())
			return true
		}
		files := map[string][]byte{}
		for p, b := range in.Files {
			var text string
			if e := json.Unmarshal(b, &text); e != nil {
				writeErr(w, 400, "invalid_request", "files must be UTF-8 strings")
				return true
			}
			files[p] = []byte(text)
		}
		if e := s.extensions.InstallSkill(in.Name, files); e != nil {
			writeErr(w, 400, "extensions", e.Error())
		} else {
			s.writeExtensionSaved(w, http.StatusCreated)
		}
		return true
	case strings.HasPrefix(p, "extensions/skills/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(p, "extensions/skills/")
		if e := s.deleteSkill(name); errors.Is(e, extensions.ErrBuiltinSkill) {
			writeErr(w, 400, "builtin_skill", e.Error())
		} else if e != nil {
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
