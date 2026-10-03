package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func (s *Server) localRunnerConfig() (string, string, error) {
	if s.isolatedWorkspace && s.microVM != nil {
		return computer.RunnerOrigin, "", nil
	}
	base := strings.TrimRight(s.localRunnerURL, "/")
	path := s.localRunnerTokenFile
	if !s.isolatedWorkspace {
		if base == "" {
			base = strings.TrimRight(os.Getenv("TOFI_MCP_RUNNER_URL"), "/")
		}
		if path == "" {
			path = os.Getenv("TOFI_MCP_RUNNER_TOKEN_FILE")
		}
	}
	if base == "" || path == "" {
		return "", "", errors.New("local MCP Runner is not configured")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", errors.New("invalid local MCP Runner URL")
	}
	token, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return "", "", errors.New("local MCP Runner is not ready")
	}
	return base, strings.TrimSpace(string(token)), nil
}

func (s *Server) localMCPTransport(endpoint string) (http.RoundTripper, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Host != "account-computer" {
		return nil, nil
	}
	if !s.isolatedWorkspace || s.microVM == nil {
		return nil, errors.New("account Runner transport unavailable")
	}
	return s.microVM.RunnerTransport(), nil
}

func (s *Server) runnerRequest(r *http.Request, method, path string, body any) ([]byte, int, error) {
	base, token, err := s.localRunnerConfig()
	if err != nil {
		return nil, 0, err
	}
	var reader io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return nil, 0, e
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, base+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 4 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	client.Transport, err = s.localMCPTransport(base)
	if err != nil {
		return nil, 0, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return data, response.StatusCode, err
}

func (s *Server) routeLocalMCP(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "extensions/local-mcp") {
		return false
	}
	if p == "extensions/local-mcp" && r.Method == http.MethodGet {
		data, status, err := s.runnerRequest(r, http.MethodGet, "/v1/plugins", nil)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
			return true
		}
		if status != http.StatusOK {
			writeErr(w, http.StatusBadGateway, "runner", "local MCP Runner is unavailable")
			return true
		}
		var result map[string]any
		if json.Unmarshal(data, &result) != nil {
			writeErr(w, http.StatusBadGateway, "runner", "invalid Runner response")
			return true
		}
		result["available"] = true
		writeJSON(w, http.StatusOK, result)
		return true
	}
	if p == "extensions/local-mcp/install" && r.Method == http.MethodPost {
		var input mcprunner.InstallRequest
		if err := decodeExtensionJSON(r, &input); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		data, status, err := s.runnerRequest(r, http.MethodPost, "/v1/plugins", input)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "runner", err.Error())
			return true
		}
		if status != http.StatusCreated {
			writeErr(w, http.StatusBadRequest, "runner", strings.TrimSpace(string(data)))
			return true
		}
		if err := s.attachLocalMCP(input.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, "runner", fmt.Sprintf("plugin installed but could not be attached: %v", err))
			return true
		}
		s.writeExtensionSaved(w, http.StatusCreated)
		return true
	}
	if p == "extensions/local-mcp/gog/oauth/status" && r.Method == http.MethodGet {
		data, status, err := s.runnerRequest(r, http.MethodGet, "/v1/plugins/gog/gog/status", nil)
		if err != nil || status != http.StatusOK {
			writeErr(w, http.StatusBadGateway, "runner", "Google connection status unavailable")
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return true
	}
	if p == "extensions/local-mcp/gog/oauth/check" && r.Method == http.MethodPost {
		data, status, err := s.runnerRequest(r, http.MethodPost, "/v1/plugins/gog/gog/check", map[string]any{})
		if err != nil {
			writeErr(w, 502, "runner", err.Error())
			return true
		}
		if status != http.StatusOK {
			writeErr(w, 502, "runner", strings.TrimSpace(string(data)))
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return true
	}
	if p == "extensions/local-mcp/gog/oauth/disconnect" && r.Method == http.MethodPost {
		data, status, err := s.runnerRequest(r, http.MethodPost, "/v1/plugins/gog/gog/disconnect", map[string]any{})
		if err != nil {
			writeErr(w, 502, "runner", err.Error())
			return true
		}
		if status != http.StatusOK {
			writeErr(w, 502, "runner", strings.TrimSpace(string(data)))
			return true
		}
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return true
	}
	if p == "extensions/local-mcp/gog/oauth/start" && r.Method == http.MethodPost {
		origin := s.webOAuthOrigin(r)
		if origin == "" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			writeErr(w, 400, "oauth_https_required", "Google Web OAuth requires the configured HTTPS origin")
			return true
		}
		var input struct {
			Email           string          `json:"email"`
			Scope           string          `json:"scope"`
			CredentialsJSON json.RawMessage `json:"credentials_json"`
		}
		if err := decodeExtensionJSON(r, &input); err != nil {
			writeErr(w, 400, "invalid_request", err.Error())
			return true
		}
		request := mcprunner.GogStartRequest{Email: input.Email, Scope: input.Scope, CredentialsJSON: input.CredentialsJSON, RedirectURI: origin + "/api/extensions/local-mcp/gog/oauth/callback"}
		data, status, err := s.runnerRequest(r, http.MethodPost, "/v1/plugins/gog/gog/start", request)
		if err != nil {
			writeErr(w, 502, "runner", err.Error())
			return true
		}
		if status != http.StatusOK {
			writeErr(w, 400, "runner", strings.TrimSpace(string(data)))
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return true
	}
	if p == "extensions/local-mcp/gog/oauth/callback" && r.Method == http.MethodGet {
		origin := s.webOAuthOrigin(r)
		page := oauthServicePage(extensions.OAuthServiceIdentity{Name: "Google (gogcli)"})
		page.State, page.Message = "failed", "无法完成 Google 授权，请返回 Tofi 重试。"
		if origin == "" {
			writeOAuthCompletion(w, 400, page)
			return true
		}
		fullURL := origin + r.URL.RequestURI()
		_, status, err := s.runnerRequest(r, http.MethodPost, "/v1/plugins/gog/gog/finish", mcprunner.GogFinishRequest{RedirectedURL: fullURL})
		if err == nil && status == http.StatusOK {
			page.State, page.Message = "success", "Google 授权已保存。请返回 Tofi 验证 Gmail 工具。"
			_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
			writeOAuthCompletion(w, 200, page)
			return true
		}
		writeOAuthCompletion(w, 400, page)
		return true
	}
	if strings.HasPrefix(p, "extensions/local-mcp/") {
		id := strings.TrimPrefix(p, "extensions/local-mcp/")
		if r.Method == http.MethodPost && strings.HasSuffix(id, "/attach") {
			id = strings.TrimSuffix(id, "/attach")
			if !localMCPID(id) {
				http.NotFound(w, r)
				return true
			}
			if err := s.attachLocalMCP(id); err != nil {
				writeErr(w, 400, "runner", err.Error())
			} else {
				s.writeExtensionSaved(w, 200)
			}
			return true
		}
		if !localMCPID(id) {
			http.NotFound(w, r)
			return true
		}
		if r.Method == http.MethodDelete {
			data, status, err := s.runnerRequest(r, http.MethodDelete, "/v1/plugins/"+id, nil)
			if err != nil {
				writeErr(w, 502, "runner", err.Error())
				return true
			}
			if status != http.StatusOK {
				writeErr(w, 409, "runner", strings.TrimSpace(string(data)))
				return true
			}
			if err := s.extensions.DeleteMCP("local_" + id); err != nil && !errors.Is(err, os.ErrNotExist) {
				writeErr(w, 500, "runner", err.Error())
				return true
			}
			s.writeExtensionSaved(w, http.StatusOK)
			return true
		}
	}
	http.NotFound(w, r)
	return true
}

func localMCPID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func (s *Server) attachLocalMCP(id string) error {
	if !localMCPID(id) {
		return errors.New("invalid plugin ID")
	}
	base, token, err := s.localRunnerConfig()
	if err != nil {
		return err
	}
	cfg := extensions.MCPServerConfig{URL: base + "/mcp/" + id, Transport: "streamable_http"}
	if token != "" {
		cfg.Headers = map[string]string{"Authorization": "Bearer " + token}
	}
	// The private status response supplies operator-owned adapter provenance.
	// Binding it to the URL changes existing cache/config/approval fingerprints
	// without granting any read-only exemption or changing approval semantics.
	statusRequest, err := http.NewRequest(http.MethodGet, base+"/v1/plugins", nil)
	if err != nil {
		return err
	}
	data, status, err := s.runnerRequest(statusRequest, http.MethodGet, "/v1/plugins", nil)
	if err != nil || status != http.StatusOK {
		return errors.New("local MCP Runner identity is unavailable")
	}
	var installed struct {
		Plugins []mcprunner.Status `json:"plugins"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return errors.New("invalid local MCP Runner identity")
	}
	found := false
	for _, plugin := range installed.Plugins {
		if plugin.ID != id {
			continue
		}
		found = true
		if plugin.AdapterIdentity != "" {
			if len(plugin.AdapterIdentity) != 64 || strings.Trim(plugin.AdapterIdentity, "0123456789abcdef") != "" {
				return errors.New("invalid local MCP adapter identity")
			}
			u, _ := url.Parse(cfg.URL)
			query := u.Query()
			query.Set(mcprunner.AdapterIdentityQuery, plugin.AdapterIdentity)
			u.RawQuery = query.Encode()
			cfg.URL = u.String()
		}
		break
	}
	if !found {
		return errors.New("local MCP plugin is not installed")
	}
	views, err := s.extensions.ListMCP()
	if err != nil {
		return err
	}
	updating := false
	for _, view := range views {
		if view.Name == "local_"+id {
			updating = true
			break
		}
	}
	return s.extensions.SaveMCP("local_"+id, cfg, updating)
}
