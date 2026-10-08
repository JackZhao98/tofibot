package extensions

// Management APIs deliberately live beside the read-only runtime loader. They
// write only user-owned configuration; a running Prepared snapshot is never
// mutated and therefore picks changes up on its next Prepare call.
import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"golang.org/x/oauth2"
)

const maskedSecret = "••••••••"
const maxInstallFiles = 256

type MCPServerView struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Transport     string            `json:"transport,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	ToolAllowlist []string          `json:"tool_allowlist,omitempty"`
	ToolDenylist  []string          `json:"tool_denylist,omitempty"`
	// Retained for reading legacy config files; per-Bot grants are ignored.
	BotAllowlists map[string][]string `json:"-"`
	OAuth         *OAuthView          `json:"oauth,omitempty"`
}
type OAuthView struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	AuthServerMetadataURL string   `json:"auth_server_metadata_url,omitempty"`
	Connected             bool     `json:"connected"`
}
type SkillView struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     map[string]bool `json:"enabled,omitempty"`
}

// SetSkillEnabled stores the per-Bot activation switch separately from the
// skill source, so changing one Bot never rewrites user supplied content.
func (m *Manager) SetSkillEnabled(botID, name string, enabled bool) error {
	if botID == "" || !skillNamePattern.MatchString(name) {
		return errors.New("invalid skill or bot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := os.Stat(filepath.Join(m.cfg.SkillsDir, name)); ok != nil {
		return os.ErrNotExist
	}
	p := filepath.Join(m.cfg.SkillsDir, ".enabled.json")
	state, e := loadSkillState(m.cfg.SkillsDir)
	if e != nil {
		return e
	}
	if state[botID] == nil {
		state[botID] = map[string]bool{}
	}
	state[botID][name] = enabled
	return writeJSON0600(p, state)
}

func loadSkillState(root string) (map[string]map[string]bool, error) {
	state := map[string]map[string]bool{}
	if strings.TrimSpace(root) == "" {
		return state, nil
	}
	b, err := os.ReadFile(filepath.Join(root, ".enabled.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func validExtensionName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return s != "." && s != ".."
}
func maskedHeaders(h map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if strings.TrimSpace(v) != "" {
			out[k] = maskedSecret
		}
	}
	return out
}
func copyHeaders(in map[string]string, old map[string]string) map[string]string {
	if in == nil {
		return cloneStringMap(old)
	}
	out := map[string]string{}
	for k, v := range in {
		if v == maskedSecret || v == "" {
			if x, ok := old[k]; ok {
				out[k] = x
			}
			continue
		}
		out[k] = v
	}
	return out
}

func (m *Manager) ListMCP() ([]MCPServerView, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, e := loadServers(m.cfg.MCPConfigPath)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	names := make([]string, 0, len(cfg))
	for n := range cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]MCPServerView, 0, len(names))
	for _, n := range names {
		c := cfg[n]
		var oauth *OAuthView
		if c.OAuth != nil {
			oauth = &OAuthView{ClientID: c.OAuth.ClientID, Scopes: append([]string(nil), c.OAuth.Scopes...), AuthServerMetadataURL: c.OAuth.AuthServerMetadataURL}
			if c.OAuth.ClientSecret != "" {
				oauth.ClientSecret = maskedSecret
			}
			if tokenFileConnected(m.tokenPath(n)) {
				oauth.Connected = true
			}
		}
		out = append(out, MCPServerView{Name: n, URL: c.URL, Transport: c.Transport, Headers: maskedHeaders(c.Headers), ToolAllowlist: append([]string(nil), c.ToolAllowlist...), ToolDenylist: append([]string(nil), c.ToolDenylist...), BotAllowlists: c.BotAllowlists, OAuth: oauth})
	}
	return out, nil
}

func tokenFileConnected(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var token Token
	return json.Unmarshal(b, &token) == nil && (token.AccessToken != "" || token.RefreshToken != "")
}

func (m *Manager) SaveMCP(name string, c MCPServerConfig, updating bool) error {
	if !validExtensionName(name) {
		return tooloutcome.InvalidArguments("invalid MCP server name")
	}
	u, e := url.Parse(c.URL)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return tooloutcome.InvalidArguments("MCP URL must be an http or https endpoint")
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, e := loadServers(m.cfg.MCPConfigPath)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if cfg == nil {
		cfg = map[string]MCPServerConfig{}
	}
	old, exists := cfg[name]
	if updating && !exists {
		return tooloutcome.InvalidArguments("MCP server not found")
	}
	if !updating && exists {
		return tooloutcome.InvalidArguments("MCP server already exists")
	}
	// Ordinary settings updates cannot grant an exemption. Changing the target
	// clears previously reviewed read-only tool names.
	c.TrustedReadOnlyTools = append([]string(nil), old.TrustedReadOnlyTools...)
	c.Headers = copyHeaders(c.Headers, old.Headers)
	if c.OAuth == nil {
		c.OAuth = old.OAuth
	} else if c.OAuth.ClientSecret == maskedSecret {
		if old.OAuth != nil {
			c.OAuth.ClientSecret = old.OAuth.ClientSecret
		} else {
			c.OAuth.ClientSecret = ""
		}
	}
	if c.OAuth != nil && old.OAuth != nil {
		// Management clients do not know the internal provenance field. Preserve
		// it only when they retain the same registered client; changing client ID
		// means the user supplied a new client and must clear provenance.
		if c.OAuth.ClientID == old.OAuth.ClientID && c.OAuth.ClientSecret == old.OAuth.ClientSecret {
			c.OAuth.DynamicClientRedirectURI = old.OAuth.DynamicClientRedirectURI
		} else {
			c.OAuth.DynamicClientRedirectURI = ""
		}
	} else if c.OAuth != nil {
		c.OAuth.DynamicClientRedirectURI = ""
	}
	// Never accept provenance supplied by a caller for a newly created OAuth
	// client. It is derived only after a successful local DCR flow.
	if !exists && c.OAuth != nil {
		c.OAuth.DynamicClientRedirectURI = ""
	}
	if exists && credentialTargetChanged(old, c) {
		c.TrustedReadOnlyTools = nil
	}
	// An empty client_id is valid when the provider advertises OAuth dynamic
	// client registration. StartOAuth performs registration on first connect and
	// persists the returned credentials after rechecking this configuration.
	cfg[name] = c
	if err := writeJSON0600(m.cfg.MCPConfigPath, serverFile{MCPServers: cfg}); err != nil {
		return err
	}
	m.invalidateCatalogs(name)
	if exists && credentialTargetChanged(old, c) {
		return m.invalidateCredentialLocked(name)
	}
	return nil
}

func credentialTargetChanged(a, b MCPServerConfig) bool {
	return credentialTargetKey(a) != credentialTargetKey(b)
}

func credentialTargetKey(cfg MCPServerConfig) string {
	if cfg.OAuth == nil {
		return cfg.URL + "\x00no-oauth"
	}
	return strings.Join([]string{cfg.URL, cfg.OAuth.ClientID, cfg.OAuth.ClientSecret, cfg.OAuth.AuthServerMetadataURL, cfg.OAuth.DynamicClientRedirectURI, strings.Join(cfg.OAuth.Scopes, "\x00")}, "\x01")
}

func (m *Manager) DeleteMCP(name string) error {
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, e := loadServers(m.cfg.MCPConfigPath)
	if e != nil {
		return e
	}
	if _, ok := cfg[name]; !ok {
		return os.ErrNotExist
	}
	invalidateErr := m.invalidateCredentialLocked(name)
	delete(cfg, name)
	if err := writeJSON0600(m.cfg.MCPConfigPath, serverFile{MCPServers: cfg}); err != nil {
		return err
	}
	m.invalidateCatalogs(name)
	return invalidateErr
}

type MCPInspection struct {
	ToolCount    int          `json:"tool_count"`
	AuthRequired bool         `json:"auth_required,omitempty"`
	Diagnostics  []Diagnostic `json:"diagnostics,omitempty"`
}

// InspectMCP connects to an MCP server and reports its discovered tool count
// together with diagnostics suitable for the settings UI.
func (m *Manager) InspectMCP(ctx context.Context, name string) MCPInspection {
	m.mu.RLock()
	cfg, e := loadServers(m.cfg.MCPConfigPath)
	m.mu.RUnlock()
	if e != nil {
		return MCPInspection{Diagnostics: []Diagnostic{{Server: name, Message: e.Error()}}}
	}
	c, ok := cfg[name]
	if !ok {
		return MCPInspection{Diagnostics: []Diagnostic{{Server: name, Message: "MCP server not found"}}}
	}
	discovery, cancel := context.WithTimeout(ctx, m.cfg.DiscoveryTimeout)
	defer cancel()
	tools, cli, e := m.prepareServer(ctx, discovery, name, c, nil, map[string]int{}, nil)
	if e != nil {
		authRequired := c.OAuth != nil && oauthAuthorizationRequired(e)
		return MCPInspection{AuthRequired: authRequired, Diagnostics: []Diagnostic{mcpInspectionDiagnostic(name, c.URL, e)}}
	}
	defer cli.Close()
	return MCPInspection{ToolCount: len(tools)}
}

func oauthAuthorizationRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoToken) || errors.Is(err, ErrOAuthAuthorizationRequired) || errors.Is(err, ErrAuthorizationRequired) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid_token") || strings.Contains(message, "status 401")
}

// TestMCP retains the original diagnostics-only API for existing callers.
func (m *Manager) TestMCP(ctx context.Context, name string) []Diagnostic {
	return m.InspectMCP(ctx, name).Diagnostics
}

func writeJSON0600(path string, v any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("MCP config path is not configured")
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".mcp-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(append(b, '\n'))
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

// InstallSkill installs a complete user supplied skill directory. Paths are
// relative, bounded, and created with private permissions.
func (m *Manager) InstallSkill(name string, files map[string][]byte) error {
	if !skillNamePattern.MatchString(name) {
		return tooloutcome.InvalidArguments("invalid skill name")
	}
	if len(files) == 0 || len(files) > maxInstallFiles {
		return tooloutcome.InvalidArguments("invalid skill file count")
	}
	root := m.cfg.SkillsDir
	if root == "" {
		return tooloutcome.New(tooloutcome.Permanent, "skills_unavailable", "not_executed", "Skills directory is not configured.", "explain_blocker").Err()
	}
	total := 0
	for p, b := range files {
		total += len(b)
		if !validSkillInstallPath(p) || total > maxSkillFile {
			return tooloutcome.InvalidArguments("unsafe or oversized skill file path")
		}
	}
	if _, ok := files["SKILL.md"]; !ok {
		return tooloutcome.InvalidArguments("SKILL.md is required")
	}
	manifest, err := parseSkill(files["SKILL.md"])
	if err != nil {
		return tooloutcome.InvalidArguments("invalid skill manifest")
	}
	if manifest.Name != name {
		return tooloutcome.InvalidArguments("skill directory name must match SKILL.md name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	dir := filepath.Join(root, name)
	if _, err := os.Lstat(dir); err == nil {
		return errors.New("skill already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0700); err != nil {
		return err
	}
	for p, b := range files {
		dst := filepath.Join(stage, filepath.FromSlash(p))
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		if e := os.WriteFile(dst, b, 0600); e != nil {
			return e
		}
	}
	if _, err := os.Stat(filepath.Join(stage, "SKILL.md")); err != nil {
		return err
	}
	return os.Rename(stage, dir)
}

func validSkillInstallPath(path string) bool {
	if path == "" || path == "." || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || strings.Contains(path, "\\") || filepath.IsAbs(path) {
		return false
	}
	if filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func (m *Manager) ListSkills() ([]SkillView, []Diagnostic) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, d := loadSkills(m.cfg.SkillsDir)
	state, err := loadSkillState(m.cfg.SkillsDir)
	if err != nil {
		d = append(d, Diagnostic{Message: "read skill activation state: " + publicDiagnostic(err)})
	}
	out := make([]SkillView, 0, len(s))
	for _, x := range s {
		enabled := map[string]bool{}
		for bot, skills := range state {
			if skills[x.Name] {
				enabled[bot] = true
			}
		}
		out = append(out, SkillView{Name: x.Name, Description: x.Description, Enabled: enabled})
	}
	return out, d
}
func (m *Manager) DeleteSkill(name string) error {
	if !skillNamePattern.MatchString(name) {
		return errors.New("invalid skill name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Join(m.cfg.SkillsDir, name)
	real, _ := filepath.EvalSymlinks(m.cfg.SkillsDir)
	if real == "" || !within(real, filepath.Join(real, name)) {
		return errors.New("invalid skills root")
	}
	return os.RemoveAll(p)
}

type OAuthMetadata struct {
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}
type OAuthConfig struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	AuthServerMetadataURL string   `json:"auth_server_metadata_url,omitempty"`
	// DynamicClientRedirectURI is internal provenance for credentials obtained
	// through DCR. It lets a later loopback callback change trigger a new
	// registration without treating a user-supplied client as replaceable.
	DynamicClientRedirectURI string `json:"dynamic_client_redirect_uri,omitempty"`
}
type FileTokenStore struct {
	Path      string
	target    string
	fileMu    sync.Mutex
	refreshMu sync.Mutex
	active    atomic.Bool
	refresh   func(context.Context, string) (*Token, error)
	failedKey string
	failedErr error
}

var errCredentialInvalidated = errors.New("OAuth credential generation is no longer active")

func newFileTokenStore(path, target string, refresh func(context.Context, string) (*Token, error)) *FileTokenStore {
	s := &FileTokenStore{Path: path, target: target, refresh: refresh}
	s.active.Store(true)
	return s
}

func (s *FileTokenStore) GetToken(ctx context.Context) (*Token, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !s.active.Load() {
		return nil, errCredentialInvalidated
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if !s.active.Load() {
		return nil, errCredentialInvalidated
	}
	t, e := s.readToken()
	if e != nil {
		return nil, e
	}
	if !t.IsExpired() || t.RefreshToken == "" || s.refresh == nil {
		return t, nil
	}
	refreshKey := t.RefreshToken + "\x00" + t.AccessToken + "\x00" + t.ExpiresAt.UTC().Format(time.RFC3339Nano)
	if s.failedKey == refreshKey && s.failedErr != nil {
		return nil, s.failedErr
	}
	refreshed, e := s.refresh(ctx, t.RefreshToken)
	if e != nil {
		s.failedKey = refreshKey
		s.failedErr = fmt.Errorf("refresh OAuth token: %w", e)
		return nil, s.failedErr
	}
	s.failedKey, s.failedErr = "", nil
	return refreshed, nil
}

func (s *FileTokenStore) readToken() (*Token, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	b, e := os.ReadFile(s.Path)
	if os.IsNotExist(e) {
		return nil, ErrNoToken
	}
	if e != nil {
		return nil, e
	}
	var t Token
	e = json.Unmarshal(b, &t)
	return &t, e
}
func (s *FileTokenStore) SaveToken(ctx context.Context, t *Token) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if !s.active.Load() {
		return errCredentialInvalidated
	}
	return writeJSON0600(s.Path, t)
}

func (s *FileTokenStore) invalidate() error {
	s.active.Store(false)
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func DiscoverOAuth(ctx context.Context, endpoint string) (OAuthMetadata, error) {
	out, _, e := discoverOAuthMetadata(ctx, endpoint)
	return out, e
}

var errOAuthAdvertisedIssuerUnavailable = errors.New("OAuth protected resource metadata advertised authorization servers whose metadata was unavailable")

// discoverOAuthMetadata follows RFC 8414 path insertion and also accepts
// deployments that publish authorization-server metadata at the origin. The
// latter is common for a single hosted MCP endpoint whose resource URL has a
// path (for example, /mcp).
func discoverOAuthMetadata(ctx context.Context, endpoint string) (OAuthMetadata, string, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return OAuthMetadata{}, "", errors.New("invalid OAuth endpoint")
	}
	u.RawQuery, u.Fragment = "", ""
	path := strings.Trim(u.EscapedPath(), "/")
	root := u.Scheme + "://" + u.Host
	// RFC 9728 protected-resource metadata is authoritative when present.
	// Follow its authorization_servers before trying origin-level fallbacks;
	// this preserves deployments whose authorization server is external.
	prmCandidates := []string{root + "/.well-known/oauth-protected-resource"}
	if path != "" {
		prmCandidates = append([]string{root + "/.well-known/oauth-protected-resource/" + path}, prmCandidates...)
	}
	advertisedServers := false
	for _, prmURL := range prmCandidates {
		var prm struct {
			AuthorizationServers []string `json:"authorization_servers"`
		}
		status, decodeErr := fetchOAuthJSON(ctx, prmURL, &prm)
		if status/100 != 2 || decodeErr != nil || len(prm.AuthorizationServers) == 0 {
			continue
		}
		advertisedServers = true
		for _, issuer := range prm.AuthorizationServers {
			for _, candidate := range oauthMetadataCandidates(issuer) {
				var metadata OAuthMetadata
				status, decodeErr := fetchOAuthJSON(ctx, candidate, &metadata)
				if status/100 == 2 && decodeErr == nil && metadata.AuthorizationEndpoint != "" && metadata.TokenEndpoint != "" {
					return metadata, candidate, nil
				}
			}
		}
	}
	if advertisedServers {
		return OAuthMetadata{}, "", errOAuthAdvertisedIssuerUnavailable
	}
	candidates := []string{
		root + "/.well-known/oauth-authorization-server",
	}
	if path != "" {
		candidates = append([]string{
			root + "/.well-known/oauth-authorization-server/" + path,
			root + "/.well-known/openid-configuration/" + path,
			root + "/" + path + "/.well-known/openid-configuration",
		}, candidates...)
	}
	var lastStatus int
	for _, candidate := range candidates {
		var metadata OAuthMetadata
		status, decodeErr := fetchOAuthJSON(ctx, candidate, &metadata)
		lastStatus = status
		if status/100 != 2 {
			continue
		}
		if decodeErr != nil {
			return OAuthMetadata{}, "", decodeErr
		}
		if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
			return metadata, "", errors.New("OAuth metadata missing endpoints")
		}
		return metadata, candidate, nil
	}
	return OAuthMetadata{}, "", fmt.Errorf("OAuth metadata: HTTP %d", lastStatus)
}

func oauthMetadataCandidates(issuer string) []string {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	path := strings.Trim(u.EscapedPath(), "/")
	root := u.Scheme + "://" + u.Host
	if path == "" {
		return []string{root + "/.well-known/oauth-authorization-server"}
	}
	return []string{root + "/.well-known/oauth-authorization-server/" + path, root + "/.well-known/oauth-authorization-server"}
}

func fetchOAuthJSON(ctx context.Context, endpoint string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
}

// OAuthPublicError converts provider and transport failures into safe,
// actionable text for the settings API. It intentionally does not echo URLs,
// response bodies, client credentials, or authorization codes.
func OAuthPublicError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return "OAuth connection could not be started because the provider timed out. Check the MCP endpoint and try again."
	case strings.Contains(msg, "redirect"):
		return "OAuth connection could not be started because HTTP redirects must use localhost. Use HTTPS for a network address, or connect through localhost."
	case strings.Contains(msg, "metadata"), strings.Contains(msg, "registration"), strings.Contains(msg, "client_id"):
		return "OAuth connection could not be started because the provider's OAuth discovery or client registration failed. Verify the MCP endpoint and try again."
	default:
		return "OAuth connection could not be started. Verify the MCP endpoint and try again."
	}
}
func PKCE() (verifier, challenge string, err error) {
	verifier = oauth2.GenerateVerifier()
	challenge = oauth2.S256ChallengeFromVerifier(verifier)
	return
}

// OAuthFlow is a small server-side delegated flow. State and PKCE verifier
// stay in memory; tokens are persisted through the supplied TokenStore.
type OAuthFlow struct {
	Handler               *OAuthHandler
	State, Verifier       string
	Metadata              AuthServerMetadata
	AuthServerMetadataURL string
	// RegisteredClient records credentials obtained through dynamic client
	// registration. They are persisted by Manager only after a configuration
	// generation check; the flow itself never writes configuration.
	RegisteredClient bool
	ClientID         string
	ClientSecret     string
}

func StartOAuth(ctx context.Context, cfg OAuthFlowConfig, endpoint ...string) (*OAuthFlow, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ValidateRedirectURI(cfg.RedirectURI); err != nil {
		return nil, err
	}
	// Resolve authorization metadata before starting the flow so providers
	// publishing it at the origin
	// still work when the MCP resource itself has a path.
	if strings.TrimSpace(cfg.AuthServerMetadataURL) == "" && len(endpoint) > 0 && strings.TrimSpace(endpoint[0]) != "" {
		if _, metadataURL, discoverErr := discoverOAuthMetadata(ctx, endpoint[0]); discoverErr == nil {
			cfg.AuthServerMetadataURL = metadataURL
		} else if errors.Is(discoverErr, errOAuthAdvertisedIssuerUnavailable) {
			return nil, discoverErr
		}
	}
	h := NewOAuthHandler(cfg)
	if len(endpoint) > 0 {
		h.SetBaseURL(endpoint[0])
	}
	md, e := h.GetServerMetadata(ctx)
	if e != nil {
		return nil, e
	}
	if md == nil {
		return nil, errors.New("OAuth server metadata unavailable")
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, errors.New("OAuth metadata missing endpoints")
	}
	registered := false
	if strings.TrimSpace(cfg.ClientID) == "" {
		if md.RegistrationEndpoint == "" {
			return nil, errors.New("OAuth client_id is empty and server does not support dynamic client registration")
		}
		if e = h.RegisterClient(ctx, "Tofi"); e != nil {
			return nil, e
		}
		if strings.TrimSpace(h.GetClientID()) == "" {
			return nil, errors.New("dynamic client registration returned an empty client_id")
		}
		registered = true
	}
	state, e := generateOAuthState()
	if e != nil {
		return nil, e
	}
	ver := oauth2.GenerateVerifier()
	h.SetExpectedState(state)
	return &OAuthFlow{Handler: h, State: state, Verifier: ver, Metadata: *md, AuthServerMetadataURL: cfg.AuthServerMetadataURL, RegisteredClient: registered, ClientID: h.GetClientID(), ClientSecret: h.GetClientSecret()}, nil
}
func (f *OAuthFlow) AuthorizationURL(ctx context.Context) (string, error) {
	if f == nil || f.Handler == nil {
		return "", errors.New("OAuth flow is not initialized")
	}
	u, err := f.Handler.GetAuthorizationURL(ctx, f.State, oauth2.S256ChallengeFromVerifier(f.Verifier))
	if err != nil {
		return "", err
	}
	// Google only returns refresh tokens reliably when offline access is
	// requested. Restrict these parameters to Google's verified authorization
	// host; arbitrary providers must receive the URL generated by oauth2.
	parsed, parseErr := url.Parse(u)
	if parseErr == nil && strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "accounts.google.com") {
		q := parsed.Query()
		q.Set("access_type", "offline")
		q.Set("include_granted_scopes", "true")
		q.Set("prompt", "consent")
		parsed.RawQuery = q.Encode()
		return parsed.String(), nil
	}
	return u, nil
}
func (f *OAuthFlow) Callback(ctx context.Context, code, state string) error {
	if f == nil || f.Handler == nil {
		return errors.New("OAuth flow is not initialized")
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(f.State)) != 1 {
		return errors.New("OAuth state mismatch")
	}
	return f.Handler.ProcessAuthorizationResponse(ctx, code, state, f.Verifier)
}
func (f *OAuthFlow) Refresh(ctx context.Context, refresh string) (*Token, error) {
	if f == nil || f.Handler == nil {
		return nil, errors.New("OAuth flow is not initialized")
	}
	return f.Handler.RefreshToken(ctx, refresh)
}

type oauthSession struct {
	Flow           *OAuthFlow
	Server         string
	Store          *FileTokenStore
	Expires        time.Time
	Pending        *dynamicOAuthClient
	ExchangeStore  TokenStore
	SnapshotTarget string
	RedirectURI    string
}

type dynamicOAuthClient struct {
	ID, Secret, RedirectURI, MetadataURL string
}

func (m *Manager) OAuthStart(ctx context.Context, name, redirect string) (string, string, error) {
	if !validExtensionName(name) {
		return "", "", errors.New("invalid MCP server name")
	}
	m.mu.Lock()
	m.pruneOAuthSessionsLocked(time.Now())
	cfgs, e := loadServers(m.cfg.MCPConfigPath)
	m.mu.Unlock()
	if e != nil {
		return "", "", e
	}
	c, ok := cfgs[name]
	if !ok || c.OAuth == nil {
		return "", "", errors.New("OAuth is not configured")
	}
	if e = ValidateRedirectURI(redirect); e != nil {
		return "", "", e
	}
	store := m.tokenStore(name, c)
	if !store.active.Load() {
		return "", "", errCredentialInvalidated
	}
	clientID, clientSecret := c.OAuth.ClientID, c.OAuth.ClientSecret
	// Automatically registered clients are bound to their redirect URI. A new
	// guest callback must receive a new registration; user-supplied clients are
	// never silently replaced.
	needsFreshRegistration := c.OAuth.DynamicClientRedirectURI != "" && c.OAuth.DynamicClientRedirectURI != redirect
	if needsFreshRegistration {
		clientID, clientSecret = "", ""
	}
	var exchangeStore TokenStore = store
	if clientID == "" {
		// DCR exchanges use an isolated in-memory store. The persistent store
		// continues serving the previous refresh token until commit succeeds.
		exchangeStore = NewMemoryTokenStore()
	}
	f, e := StartOAuth(ctx, OAuthFlowConfig{ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirect, Scopes: c.OAuth.Scopes, AuthServerMetadataURL: c.OAuth.AuthServerMetadataURL, TokenStore: exchangeStore, PKCEEnabled: true}, c.URL)
	if e != nil {
		return "", "", e
	}
	sid, e := generateOAuthState()
	if e != nil {
		return "", "", e
	}
	u, e := f.AuthorizationURL(ctx)
	if e != nil {
		return "", "", e
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	m.mu.Lock()
	current, currentErr := loadServers(m.cfg.MCPConfigPath)
	if !store.active.Load() || m.tokenStores[name] != store || currentErr != nil || credentialTargetChanged(c, current[name]) {
		// A concurrent DCR may have already persisted a newer client on this
		// same store. Do not invalidate that winning generation when this stale
		// contender observes the changed config.
		if m.tokenStores[name] == store && (currentErr != nil || !credentialTargetChanged(c, current[name]) || store.target != credentialTargetKey(current[name])) {
			_ = m.invalidateCredentialLocked(name)
		}
		m.mu.Unlock()
		return "", "", errCredentialInvalidated
	}
	var pending *dynamicOAuthClient
	if f.RegisteredClient {
		if f.ClientID == "" {
			m.mu.Unlock()
			return "", "", errors.New("dynamic client registration returned an empty client_id")
		}
		updated := current[name]
		if updated.OAuth == nil {
			m.mu.Unlock()
			return "", "", errCredentialInvalidated
		}
		pending = &dynamicOAuthClient{ID: f.ClientID, Secret: f.ClientSecret, RedirectURI: redirect, MetadataURL: f.AuthServerMetadataURL}
	} else if f.AuthServerMetadataURL != "" && current[name].OAuth != nil && current[name].OAuth.AuthServerMetadataURL == "" {
		// Persist resolved discovery even when the user supplied a client ID.
		// Otherwise refresh and subsequent MCP connects would repeat the broken
		// path-only discovery performed by the
		updated := current[name]
		updated.OAuth.AuthServerMetadataURL = f.AuthServerMetadataURL
		current[name] = updated
		if err := writeJSON0600(m.cfg.MCPConfigPath, serverFile{MCPServers: current}); err != nil {
			m.mu.Unlock()
			return "", "", err
		}
		oauth := *updated.OAuth
		oauth.Scopes = append([]string(nil), updated.OAuth.Scopes...)
		store.refreshMu.Lock()
		store.target = credentialTargetKey(updated)
		store.refresh = func(refreshCtx context.Context, refreshToken string) (*Token, error) {
			h := NewOAuthHandler(OAuthFlowConfig{ClientID: oauth.ClientID, ClientSecret: oauth.ClientSecret, Scopes: oauth.Scopes, AuthServerMetadataURL: oauth.AuthServerMetadataURL, TokenStore: store})
			h.SetBaseURL(updated.URL)
			return h.RefreshToken(refreshCtx, refreshToken)
		}
		store.refreshMu.Unlock()
		c = updated
	}
	if m.oauth == nil {
		m.oauth = map[string]oauthSession{}
	}
	if m.oauthState == nil {
		m.oauthState = map[string]string{}
	}
	m.oauth[sid] = oauthSession{Flow: f, Server: name, Store: store, Expires: time.Now().Add(10 * time.Minute), Pending: pending, ExchangeStore: exchangeStore, SnapshotTarget: store.target, RedirectURI: redirect}
	m.oauthState[f.State] = sid
	m.mu.Unlock()
	return sid, u, nil
}
func (m *Manager) OAuthCallback(ctx context.Context, sid, code, state string) error {
	return m.OAuthCallbackForRedirect(ctx, sid, code, state, "")
}

// OAuthCallbackForRedirect prevents a delegated desktop completion from
// consuming a flow that was started for a different callback channel.
func (m *Manager) OAuthCallbackForRedirect(ctx context.Context, sid, code, state, redirect string) error {
	m.mu.Lock()
	if sid == "" {
		sid = m.oauthState[state]
	}
	s, ok := m.oauth[sid]
	if !ok || time.Now().After(s.Expires) {
		delete(m.oauth, sid)
		if ok {
			delete(m.oauthState, s.Flow.State)
		} else {
			delete(m.oauthState, state)
		}
		m.mu.Unlock()
		return errors.New("OAuth session expired")
	}
	if (redirect != "" && s.RedirectURI != redirect) || subtle.ConstantTimeCompare([]byte(state), []byte(s.Flow.State)) != 1 {
		m.mu.Unlock()
		return errors.New("OAuth state mismatch")
	}
	if !s.Store.active.Load() || m.tokenStores[s.Server] != s.Store {
		delete(m.oauth, sid)
		delete(m.oauthState, s.Flow.State)
		m.mu.Unlock()
		return errCredentialInvalidated
	}
	delete(m.oauth, sid)
	delete(m.oauthState, s.Flow.State)
	m.mu.Unlock()
	if err := s.Flow.Callback(ctx, code, state); err != nil {
		return err
	}
	if s.Pending == nil {
		return nil
	}
	if s.ExchangeStore == nil {
		return errCredentialInvalidated
	}
	newToken, err := s.ExchangeStore.GetToken(ctx)
	if err != nil {
		return err
	}
	// DCR credentials are not persisted until the authorization-code exchange
	// succeeds. This keeps the previous refresh credentials usable if the user
	// abandons or fails the new loopback authorization.
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := loadServers(m.cfg.MCPConfigPath)
	if err != nil || current[s.Server].OAuth == nil || !s.Store.active.Load() || m.tokenStores[s.Server] != s.Store || s.SnapshotTarget != credentialTargetKey(current[s.Server]) {
		return errCredentialInvalidated
	}
	// Serialize the whole commit with refreshes. Otherwise an in-flight refresh
	// using the old client could save a token after this callback's new token.
	s.Store.refreshMu.Lock()
	defer s.Store.refreshMu.Unlock()
	oldServers := cloneServers(current)
	updated := current[s.Server]
	updated.OAuth.ClientID = s.Pending.ID
	updated.OAuth.ClientSecret = s.Pending.Secret
	updated.OAuth.DynamicClientRedirectURI = s.Pending.RedirectURI
	if updated.OAuth.AuthServerMetadataURL == "" {
		updated.OAuth.AuthServerMetadataURL = s.Pending.MetadataURL
	}
	current[s.Server] = updated
	if err := writeJSON0600(m.cfg.MCPConfigPath, serverFile{MCPServers: current}); err != nil {
		return err
	}
	if err := s.Store.SaveToken(ctx, newToken); err != nil {
		_ = writeJSON0600(m.cfg.MCPConfigPath, serverFile{MCPServers: oldServers})
		return err
	}
	oauth := *updated.OAuth
	oauth.Scopes = append([]string(nil), updated.OAuth.Scopes...)
	s.Store.target = credentialTargetKey(updated)
	s.Store.refresh = func(refreshCtx context.Context, refreshToken string) (*Token, error) {
		h := NewOAuthHandler(OAuthFlowConfig{ClientID: oauth.ClientID, ClientSecret: oauth.ClientSecret, Scopes: oauth.Scopes, AuthServerMetadataURL: oauth.AuthServerMetadataURL, TokenStore: s.Store})
		h.SetBaseURL(updated.URL)
		return h.RefreshToken(refreshCtx, refreshToken)
	}
	return nil
}

// OAuthCancel discards a pending delegated flow without touching persisted
// credentials. It is safe to call repeatedly for the same session.
func (m *Manager) OAuthCancel(sid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.oauth[sid]; ok {
		delete(m.oauth, sid)
		if s.Flow != nil {
			delete(m.oauthState, s.Flow.State)
		}
	}
}
func (m *Manager) OAuthDisconnect(name string) error {
	if !validExtensionName(name) {
		return errors.New("invalid MCP server name")
	}
	m.mu.Lock()
	cfgs, cfgErr := loadServers(m.cfg.MCPConfigPath)
	c := cfgs[name]
	store := m.tokenStores[name]
	if store == nil {
		store = m.newTokenStoreLocked(name, c)
	}
	token, tokenErr := store.readToken()
	removeErr := m.invalidateCredentialLocked(name)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var revokeErr error
	if cfgErr == nil && tokenErr == nil && c.OAuth != nil {
		oauth := c.OAuth
		h := NewOAuthHandler(OAuthFlowConfig{ClientID: oauth.ClientID, ClientSecret: oauth.ClientSecret, AuthServerMetadataURL: oauth.AuthServerMetadataURL, TokenStore: NewMemoryTokenStore()})
		h.SetBaseURL(c.URL)
		if metadata, err := h.GetServerMetadata(ctx); err == nil && metadata != nil && metadata.RevocationEndpoint != "" {
			value := token.RefreshToken
			if value == "" {
				value = token.AccessToken
			}
			form := url.Values{"token": {value}, "client_id": {oauth.ClientID}}
			if oauth.ClientSecret != "" {
				form.Set("client_secret", oauth.ClientSecret)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RevocationEndpoint, strings.NewReader(form.Encode()))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode/100 != 2 {
						err = fmt.Errorf("revocation endpoint returned HTTP %d", resp.StatusCode)
					}
				}
				revokeErr = err
			}
		}
	}
	if removeErr != nil {
		return removeErr
	}
	return revokeErr
}

func (m *Manager) tokenPath(name string) string {
	return filepath.Join(filepath.Dir(m.cfg.MCPConfigPath), ".oauth-"+name+".json")
}

func (m *Manager) tokenStore(name string, cfg MCPServerConfig) *FileTokenStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, err := loadServers(m.cfg.MCPConfigPath)
	if err != nil || credentialTargetChanged(cfg, live[name]) {
		stale := newFileTokenStore(m.tokenPath(name), credentialTargetKey(cfg), nil)
		stale.active.Store(false)
		return stale
	}
	if s := m.tokenStores[name]; s != nil {
		if s.target == credentialTargetKey(cfg) && s.active.Load() {
			return s
		}
		_ = m.invalidateCredentialLocked(name)
	}
	return m.newTokenStoreLocked(name, cfg)
}

func (m *Manager) newTokenStoreLocked(name string, cfg MCPServerConfig) *FileTokenStore {
	s := newFileTokenStore(m.tokenPath(name), credentialTargetKey(cfg), nil)
	if cfg.OAuth != nil {
		oauth := *cfg.OAuth
		oauth.Scopes = append([]string(nil), cfg.OAuth.Scopes...)
		s.refresh = func(ctx context.Context, refreshToken string) (*Token, error) {
			h := NewOAuthHandler(OAuthFlowConfig{ClientID: oauth.ClientID, ClientSecret: oauth.ClientSecret, Scopes: oauth.Scopes, AuthServerMetadataURL: oauth.AuthServerMetadataURL, TokenStore: s})
			h.SetBaseURL(cfg.URL)
			return h.RefreshToken(ctx, refreshToken)
		}
	}
	m.tokenStores[name] = s
	return s
}

func (m *Manager) invalidateCredentialLocked(name string) error {
	var removeErr error
	if store := m.tokenStores[name]; store != nil {
		delete(m.tokenStores, name)
		removeErr = store.invalidate()
	} else {
		removeErr = os.Remove(m.tokenPath(name))
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	}
	for sid, session := range m.oauth {
		if session.Server == name {
			delete(m.oauth, sid)
			delete(m.oauthState, session.Flow.State)
		}
	}
	return removeErr
}

func (m *Manager) pruneOAuthSessionsLocked(now time.Time) {
	for sid, session := range m.oauth {
		if now.After(session.Expires) {
			delete(m.oauth, sid)
			delete(m.oauthState, session.Flow.State)
		}
	}
}

func publicDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	// Remote response bodies can contain provider details. Keep diagnostics
	// useful while avoiding reflecting authorization material into the API.
	s := err.Error()
	lower := strings.ToLower(s)
	for _, marker := range []string{"authorization:", "bearer ", "access_token", "refresh_token", "client_secret"} {
		if strings.Contains(lower, marker) {
			return "extension request failed; inspect server logs"
		}
	}
	return s
}
