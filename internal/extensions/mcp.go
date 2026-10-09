package extensions

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultToolTimeout      = 30 * time.Second
	defaultDiscoveryTimeout = 10 * time.Second
	maxToolResult           = 50_000
	maxDescription          = 1_024
	maxDiscoveredTools      = 1_000
	maxSkillPromptBytes     = 50_000
)

// MCPServerConfig is the deliberately small remote-only subset accepted by
// the collaboration runtime. Local stdio is intentionally not started here.
type MCPServerConfig struct {
	URL string `json:"url"`
	// Transport is "streamable_http" (default). Legacy "sse" configs remain
	// readable but are rejected because MCP 2026-07-28 removed HTTP+SSE.
	Transport     string            `json:"transport,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	ToolAllowlist []string          `json:"tool_allowlist,omitempty"`
	ToolDenylist  []string          `json:"tool_denylist,omitempty"`
	// TrustedReadOnlyTools is an owner-reviewed list of exact remote tool names.
	// MCP annotations are untrusted hints and never grant this exemption.
	TrustedReadOnlyTools []string            `json:"trusted_read_only_tools,omitempty"`
	BotAllowlists        map[string][]string `json:"bot_allowlists"`
	OAuth                *OAuthConfig        `json:"oauth,omitempty"`
}

type serverFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	Servers    map[string]MCPServerConfig `json:"servers"`
}

type Diagnostic struct {
	Server  string `json:"server,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type Config struct {
	// HTTPTransport resolves service-owned private endpoints. A nil result
	// retains ordinary remote HTTP; errors must never fall back to remote DNS.
	HTTPTransport    func(string) (http.RoundTripper, error)
	MCPConfigPath    string
	SkillsDir        string
	ToolTimeout      time.Duration
	DiscoveryTimeout time.Duration
	MaxToolResult    int
	// ExpandToolQuery is an optional, bounded AI fallback for lexical misses.
	// Search remains usable when it is nil or returns an error.
	ExpandToolQuery func(context.Context, string) ([]string, error)
	// ServerUsable may hide a configured server from a run while its setup is
	// incomplete (for example, no connected account). Nil offers every server.
	ServerUsable func(context.Context, string) bool
	// SkillAccess reports which of the named skills the Bot may use. Skills
	// are workspace-wide unless restricted to selected Bots. Nil permits every
	// skill; a returned error makes the run drop every skill (fail closed).
	SkillAccess func(ctx context.Context, botID string, names []string) (map[string]bool, error)
}

type Manager struct {
	mcpConfigFence     sync.RWMutex // Dispatch/configuration fence, separate from OAuth cache locking.
	cfg                Config
	mu                 sync.RWMutex
	oauth              map[string]oauthSession
	oauthState         map[string]string
	tokenStores        map[string]*FileTokenStore
	schemaCacheKey     [32]byte
	schemaCacheEnabled bool
	catalog            *MCPCatalogCache
	metadata           *MCPCatalogCache
	metadataDisk       *PersistentMCPMetadataIndex
	// readiness memoizes recent successful MCP handshakes per server and
	// configuration fingerprint; see mcp_session.go.
	readiness mcpReadinessCache
	// now is a test seam for readiness freshness. Nil means time.Now.
	now func() time.Time
}

// CachedMCPTool is a bounded schema reference that a caller recovered from a
// prior successful search. It remains unusable unless this Manager accepts it
// for the current configuration snapshot.
type CachedMCPTool struct {
	Name          string         `json:"name"`
	Server        string         `json:"server"`
	RemoteName    string         `json:"remote_name"`
	Description   string         `json:"description"`
	Parameters    map[string]any `json:"input_schema"`
	SchemaVersion string         `json:"schema_version"`
}

// MCPCallApproval is an execution-time request for one exact tool call.
// Approval must occur before the remote CallTool request is sent.
type MCPCallApproval struct {
	Description   string // Untrusted remote metadata, never policy authority.
	Schema        json.RawMessage
	Server        string
	Tool          string
	ConfigVersion string
	Arguments     json.RawMessage
	// Recheck is a backend-owned readiness callback; it cannot grant approval.
	Recheck func(context.Context) error
	// OnClaim only disables transport retries after a durable backend claim.
	// It cannot grant permission and is never populated by remote metadata.
	OnClaim func()
}

type mcpClaimedDispatchKey struct{}

type MCPCallGate func(context.Context, MCPCallApproval) error

// MCPCallCurrent checks the private configuration without returning credentials.
func (m *Manager) MCPCallCurrent(call MCPCallApproval) bool {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	return m.mcpCallCurrentLocked(call)
}

// MCPCallRequiresHuman reads the existing host execution policy for this exact
// configuration. Remote descriptions, schemas and annotations cannot exempt a
// call from human confirmation. The second result is false for stale setup.
func (m *Manager) MCPCallRequiresHuman(call MCPCallApproval) (bool, bool) {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	cfg, ok := servers[call.Server]
	if err != nil || !ok || call.ConfigVersion == "" || metadataFingerprint(call.Server, cfg) != call.ConfigVersion {
		return true, false
	}
	return !trustedReadOnlyTool(cfg, call.Tool), true
}

func (m *Manager) mcpCallCurrentLocked(call MCPCallApproval) bool {
	servers, err := loadServers(m.cfg.MCPConfigPath)
	cfg, ok := servers[call.Server]
	return err == nil && ok && call.ConfigVersion != "" && metadataFingerprint(call.Server, cfg) == call.ConfigVersion
}

// Hold the configuration fence through dispatch so a settings edit cannot
// redirect a claimed proposal. No human/model wait takes this lock.
func (m *Manager) executeCurrentMCPCall(call MCPCallApproval, execute func() (string, error)) (string, error) {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	if !m.mcpCallCurrentLocked(call) {
		return "", tooloutcome.New(tooloutcome.NeedApproval, "mcp_config_changed", "not_executed", "MCP configuration changed; request a fresh human review.", "explain_blocker").Err()
	}
	return execute()
}

type mcpToolSource struct {
	server        string
	remoteName    string
	schemaVersion string
}

type Prepared struct {
	Tools       []runtime.Tool
	Diagnostics []Diagnostic
	// Instructions are the enabled skills' bodies for direct inclusion in the
	// run system prompt. Skill tools remain available for references/files.
	Instructions string
	close        func() error
}

func NewManager(cfg Config) *Manager {
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = defaultToolTimeout
	}
	if cfg.DiscoveryTimeout <= 0 {
		cfg.DiscoveryTimeout = defaultDiscoveryTimeout
	}
	if cfg.MaxToolResult <= 0 || cfg.MaxToolResult > maxToolResult {
		cfg.MaxToolResult = maxToolResult
	}
	m := &Manager{cfg: cfg, tokenStores: map[string]*FileTokenStore{}, catalog: NewMCPCatalogCache(0, 0, 0), metadata: NewMCPCatalogCache(0, 0, 10*time.Minute)}
	if cfg.MCPConfigPath != "" {
		if root, err := filepath.Abs(filepath.Join(filepath.Dir(cfg.MCPConfigPath), "mcp-tool-index")); err == nil {
			m.metadataDisk, _ = NewPersistentMCPMetadataIndex(root)
		}
	}
	// The opaque per-process key keeps configuration fingerprints out of model
	// context. A restart conservatively drops all persisted schema references.
	if _, err := rand.Read(m.schemaCacheKey[:]); err == nil {
		m.schemaCacheEnabled = true
	}
	return m
}

func (m *Manager) schemaVersion(name string, cfg MCPServerConfig) string {
	if !m.schemaCacheEnabled {
		return ""
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	h := hmac.New(sha256.New, m.schemaCacheKey[:])
	_, _ = h.Write([]byte(requiredMCPProtocolVersion))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(b)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// A stable fingerprint allows private metadata-only index files to survive an
// App restart. The original URL, headers and credentials never enter the file.
func metadataFingerprint(name string, cfg MCPServerConfig) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write([]byte(requiredMCPProtocolVersion))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(b)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (m *Manager) invalidateCatalogs(name string) {
	m.catalog.InvalidateServer(name)
	m.metadata.InvalidateServer(name)
	if m.metadataDisk != nil {
		_ = m.metadataDisk.InvalidateServer(name)
	}
}

func (p *Prepared) Close() error {
	if p == nil || p.close == nil {
		return nil
	}
	return p.close()
}

// Prepare loads configured remote servers and the configured skill root. A
// broken individual server becomes a diagnostic and leaves other tools usable.
// No configuration means an empty tool set and no network activity.
func (m *Manager) Prepare(ctx context.Context) (*Prepared, error) {
	return m.prepare(ctx, "", false)
}

// PrepareForBot creates a run-scoped immutable snapshot. Explicit per-Bot MCP
// policies are applied before any remote connection is made.
func (m *Manager) PrepareForBot(ctx context.Context, botID string) (*Prepared, error) {
	return m.prepare(ctx, botID, true)
}

func (m *Manager) prepare(ctx context.Context, botID string, scoped bool) (*Prepared, error) {
	return m.prepareMode(ctx, botID, scoped, false, nil, nil, false)
}

func (m *Manager) prepareMode(ctx context.Context, botID string, scoped, discoverable bool, cachedTools []CachedMCPTool, gate MCPCallGate, enforceApproval bool) (*Prepared, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &Prepared{Tools: make([]runtime.Tool, 0), Diagnostics: make([]Diagnostic, 0)}
	clients := make([]*mcp.ClientSession, 0)
	usedNames := map[string]int{}
	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, m.cfg.DiscoveryTimeout)
	defer discoveryCancel()
	closeAll := func() error {
		var first error
		for _, c := range clients {
			if err := c.Close(); err != nil && first == nil {
				first = err
			}
		}
		clients = nil
		return first
	}
	p.close = closeAll

	m.mu.RLock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	var skills []Skill
	var skillDiags []Diagnostic
	if m.cfg.SkillsDir != "" {
		skills, skillDiags = loadSkillsMode(m.cfg.SkillsDir, discoverable)
		skills, skillDiags = m.filterSkillsForBot(ctx, botID, skills, skillDiags)
	}
	servers = cloneServers(servers)
	m.mu.RUnlock()
	if m.cfg.ServerUsable != nil {
		for name := range servers {
			if !m.cfg.ServerUsable(ctx, name) {
				delete(servers, name)
			}
		}
	}
	if err != nil {
		p.Diagnostics = append(p.Diagnostics, Diagnostic{Message: publicDiagnostic(err)})
	} else {
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cfg := servers[name]
			if err := ctx.Err(); err != nil {
				_ = p.Close()
				return nil, err
			}
			if err := discoveryCtx.Err(); err != nil {
				p.Diagnostics = append(p.Diagnostics, Diagnostic{Message: "MCP discovery deadline exceeded"})
				break
			}
			// Discoverable runs expose a small search facade. Defer MCP
			// initialization and tools/list until the model actually searches.
			if discoverable {
				continue
			}
			tools, cli, err := m.prepareServer(ctx, discoveryCtx, name, cfg, nil, usedNames, nil)
			if err != nil {
				p.Diagnostics = append(p.Diagnostics, mcpInspectionDiagnostic(name, cfg.URL, err))
				continue
			}
			clients = append(clients, cli)
			p.Tools = append(p.Tools, tools...)
		}
	}
	if m.cfg.SkillsDir != "" {
		// Installed skills are workspace-wide by default and may be restricted
		// to selected Bots (Config.SkillAccess). skills was already filtered
		// for botID above, so the tools and index built below can never name,
		// list or read a skill this Bot may not use. The old per-Bot
		// .enabled.json state file is intentionally ignored.
		p.Diagnostics = append(p.Diagnostics, skillDiags...)
		if discoverable {
			lazyTools, lazyClose := lazyDiscoverableMCPTools(ctx, m, servers, m.cfg.DiscoveryTimeout, m.cfg.MaxToolResult, cachedTools, gate, enforceApproval)
			oldClose := p.close
			p.close = func() error {
				e1 := lazyClose()
				e2 := oldClose()
				if e1 != nil {
					return e1
				}
				return e2
			}
			p.Tools = lazyTools
			p.Tools = append(p.Tools, discoverableSkillTools(ctx, skills, m.cfg.MaxToolResult)...)
			p.Instructions = discoveryInstructions(servers, skills)
			if err := ctx.Err(); err != nil {
				p.Close()
				return nil, err
			}
			return p, nil
		}
		p.Tools = append(p.Tools, skillTools(skills, m.cfg.MaxToolResult)...)
		var instructions strings.Builder
		usedBytes := 0
		for _, skill := range skills {
			if skill.Body == "" {
				continue
			}
			prefix := fmt.Sprintf("\n\n## Skill: %s\n", skill.Name)
			remaining := maxSkillPromptBytes - usedBytes
			if remaining <= len(prefix) {
				p.Diagnostics = append(p.Diagnostics, Diagnostic{Server: skill.Name, Message: "skill prompt omitted: aggregate prompt limit reached"})
				break
			}
			body := skill.Body
			available := remaining - len(prefix)
			if len(body) > available {
				body = truncateUTF8(body, available)
				p.Diagnostics = append(p.Diagnostics, Diagnostic{Server: skill.Name, Message: "skill prompt truncated at aggregate prompt limit"})
			}
			instructions.WriteString(prefix)
			instructions.WriteString(body)
			usedBytes = instructions.Len()
			if usedBytes >= maxSkillPromptBytes {
				break
			}
		}
		p.Instructions = instructions.String()
	}
	if discoverable {
		p.Instructions = discoveryInstructions(servers, nil)
		lazyTools, lazyClose := lazyDiscoverableMCPTools(ctx, m, servers, m.cfg.DiscoveryTimeout, m.cfg.MaxToolResult, cachedTools, gate, enforceApproval)
		oldClose := p.close
		p.close = func() error {
			e1 := lazyClose()
			e2 := oldClose()
			if e1 != nil {
				return e1
			}
			return e2
		}
		p.Tools = lazyTools
		p.Tools = append(p.Tools, discoverableSkillTools(ctx, nil, m.cfg.MaxToolResult)...)
		if err := ctx.Err(); err != nil {
			p.Close()
			return nil, err
		}
	}
	return p, nil
}

// filterSkillsForBot drops skills the Bot may not use. When the access lookup
// fails every skill is dropped: unknown must never be treated as allowed.
func (m *Manager) filterSkillsForBot(ctx context.Context, botID string, skills []Skill, diags []Diagnostic) ([]Skill, []Diagnostic) {
	if m.cfg.SkillAccess == nil || len(skills) == 0 {
		return skills, diags
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	allowed, err := m.cfg.SkillAccess(ctx, botID, names)
	if err != nil {
		log.Printf("skill access lookup failed; skills withheld from run: %v", err)
		return nil, append(diags, Diagnostic{Message: "skills unavailable: access check failed"})
	}
	kept := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if allowed[s.Name] {
			kept = append(kept, s)
		}
	}
	return kept, diags
}

// SkillsForBot lists the skills the Bot may use, failing closed on error.
func (m *Manager) SkillsForBot(ctx context.Context, botID string) ([]SkillView, []Diagnostic) {
	m.mu.RLock()
	s, d := loadSkills(m.cfg.SkillsDir)
	m.mu.RUnlock()
	s, d = m.filterSkillsForBot(ctx, botID, s, d)
	out := make([]SkillView, 0, len(s))
	for _, x := range s {
		out = append(out, SkillView{Name: x.Name, Description: x.Description})
	}
	return out, d
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes && utf8.ValidString(s) {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && len(string(runes)) > maxBytes {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func cloneServers(in map[string]MCPServerConfig) map[string]MCPServerConfig {
	if in == nil {
		return nil
	}
	out := make(map[string]MCPServerConfig, len(in))
	for name, cfg := range in {
		cfg.Headers = cloneStringMap(cfg.Headers)
		cfg.ToolAllowlist = append([]string(nil), cfg.ToolAllowlist...)
		cfg.ToolDenylist = append([]string(nil), cfg.ToolDenylist...)
		cfg.TrustedReadOnlyTools = append([]string(nil), cfg.TrustedReadOnlyTools...)
		if cfg.BotAllowlists != nil {
			cfg.BotAllowlists = make(map[string][]string, len(cfg.BotAllowlists))
			for bot, tools := range in[name].BotAllowlists {
				cfg.BotAllowlists[bot] = append([]string(nil), tools...)
			}
		}
		if cfg.OAuth != nil {
			oauth := *cfg.OAuth
			oauth.Scopes = append([]string(nil), cfg.OAuth.Scopes...)
			cfg.OAuth = &oauth
		}
		out[name] = cfg
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func loadServers(path string) (map[string]MCPServerConfig, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]MCPServerConfig{}, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]MCPServerConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read MCP config: %w", err)
	}
	var f serverFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse MCP config: %w", err)
	}
	if len(f.MCPServers) > 0 && len(f.Servers) > 0 {
		return nil, errors.New("MCP config must use one of mcpServers or servers")
	}
	if f.MCPServers != nil {
		return f.MCPServers, nil
	}
	return f.Servers, nil
}

// A service-owned transport may need to boot its fixed endpoint. Preparation
// uses its own bounded deadline before the ordinary catalog deadline starts.
func (m *Manager) prepareTransport(ctx context.Context, cfg MCPServerConfig) (bool, error) {
	if m.cfg.HTTPTransport == nil {
		return false, nil
	}
	tr, err := m.cfg.HTTPTransport(cfg.URL)
	if err != nil {
		return false, err
	}
	if ready, ok := tr.(interface{ Prepare(context.Context) error }); ok {
		return true, ready.Prepare(ctx)
	}
	return false, nil
}

func (m *Manager) prepareServer(runCtx, discoveryCtx context.Context, name string, cfg MCPServerConfig, botAllow []string, usedNames map[string]int, sources map[string]mcpToolSource) ([]runtime.Tool, *mcp.ClientSession, error) {
	prepared, err := m.prepareTransport(runCtx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare MCP endpoint: %w", err)
	}
	if prepared {
		var cancel context.CancelFunc
		discoveryCtx, cancel = context.WithTimeout(runCtx, m.cfg.DiscoveryTimeout)
		defer cancel()
	}
	cli, err := m.openMCPClient(runCtx, discoveryCtx, name, cfg)
	if err != nil {
		return nil, nil, err
	}
	closeOnError := func(e error) ([]runtime.Tool, *mcp.ClientSession, error) {
		_ = cli.Close()
		return nil, nil, e
	}
	var listTools []*mcp.Tool
	request := mcp.ListToolsParams{}
	seenCursors := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxDiscoveredTools {
			return closeOnError(errors.New("MCP tools/list exceeded page limit"))
		}
		pageResult, listErr := cli.ListTools(discoveryCtx, &request)
		if listErr != nil {
			return closeOnError(fmt.Errorf("list tools: %w", listErr))
		}
		listTools = append(listTools, pageResult.Tools...)
		if len(listTools) > maxDiscoveredTools {
			return closeOnError(errors.New("MCP server exposed too many tools"))
		}
		cursor := pageResult.NextCursor
		if cursor == "" {
			break
		}
		if seenCursors[cursor] {
			return closeOnError(errors.New("MCP tools/list returned a repeated cursor"))
		}
		seenCursors[cursor] = true
		request.Cursor = cursor
	}
	allow := make(map[string]bool, len(cfg.ToolAllowlist))
	for _, x := range cfg.ToolAllowlist {
		allow[x] = true
	}
	deny := make(map[string]bool, len(cfg.ToolDenylist))
	for _, x := range cfg.ToolDenylist {
		deny[x] = true
	}
	bot := make(map[string]bool, len(botAllow))
	for _, x := range botAllow {
		bot[x] = true
	}
	result := make([]runtime.Tool, 0, len(listTools))
	for _, remote := range listTools {
		if !mcpToolAllowed(allow, deny, bot, remote.Name) {
			continue
		}
		toolName := uniqueToolName("mcp_"+name+"__"+remote.Name, usedNames)
		params := toolSchema(remote)
		if sources != nil {
			sources[toolName] = mcpToolSource{server: name, remoteName: remote.Name, schemaVersion: m.schemaVersion(name, cfg)}
		}
		r := remote
		result = append(result, runtime.Tool{Name: toolName, Description: boundedDescription(remote.Description, toolName), Parameters: params, CheckReadiness: m.mcpMethodReadiness(name, cfg), Execute: func(callCtx context.Context, args json.RawMessage) (string, error) {
			return m.callMCPTool(callCtx, cli, r.Name, args, trustedReadOnlyTool(cfg, r.Name))
		}})
	}
	return result, cli, nil
}

func (m *Manager) openMCPClient(runCtx, discoveryCtx context.Context, name string, cfg MCPServerConfig) (*mcp.ClientSession, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("MCP URL is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("MCP URL must be an http or https endpoint")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if mode == "" {
		mode = "streamable_http"
	}
	if mode == "sse" || mode == "legacy_sse" {
		return nil, errMCPLegacySSE
	}
	if mode != "streamable_http" {
		return nil, fmt.Errorf("unsupported MCP transport %q", cfg.Transport)
	}
	baseTransport := http.DefaultTransport
	if m.cfg.HTTPTransport != nil {
		resolved, err := m.cfg.HTTPTransport(cfg.URL)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			baseTransport = resolved
		}
	}
	transport := &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: &http.Client{Transport: mcpHeaderTransport{headers: cloneStringMap(cfg.Headers), base: baseTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if cfg.OAuth != nil {
		transport.OAuthHandler = storedMCPOAuth{store: m.tokenStore(name, cfg)}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "tofi", Version: "1"}, &mcp.ClientOptions{
		Capabilities:           &mcp.ClientCapabilities{},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { m.invalidateCatalogs(name) },
	})
	// The SDK normally falls back to initialize on discover failure. Block
	// that outgoing request before it reaches the transport, and preserve the
	// original auth/network error for actionable diagnostics.
	var discoverErr error
	client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "initialize" || method == "notifications/initialized" {
				if discoverErr != nil && !isMCPProtocolFailure(discoverErr) {
					return nil, discoverErr
				}
				return nil, errMCPProtocolIncompatible
			}
			if method == "server/discover" {
				if req.GetParams().GetMeta()[mcp.MetaKeyProtocolVersion] != requiredMCPProtocolVersion {
					return nil, errMCPProtocolIncompatible
				}
				result, err := next(ctx, method, req)
				discoverErr = err
				if err == nil {
					res, ok := result.(*mcp.DiscoverResult)
					if !ok || !contains(res.SupportedVersions, requiredMCPProtocolVersion) {
						discoverErr = errMCPProtocolIncompatible
						return nil, discoverErr
					}
				}
				return result, err
			}
			return next(ctx, method, req)
		}
	})
	cli, err := client.Connect(discoveryCtx, transport, &mcp.ClientSessionOptions{ProtocolVersion: requiredMCPProtocolVersion})
	if err != nil {
		m.invalidateMCPReadiness(name)
		return nil, fmt.Errorf("connect MCP: %w", err)
	}
	if cli.InitializeResult().ProtocolVersion != requiredMCPProtocolVersion {
		_ = cli.Close()
		m.invalidateMCPReadiness(name)
		return nil, errMCPProtocolIncompatible
	}
	// A completed server/discover handshake is the readiness proof that
	// call-time checks may reuse for a short, configuration-bound window.
	m.markMCPReady(name, cfg)
	// Connect's context bounds discovery, while the prepared session belongs
	// to the run. Its cancellation must close even lazy stateless sessions.
	stop := context.AfterFunc(runCtx, func() { _ = cli.Close() })
	go func() { _ = cli.Wait(); stop() }()

	return cli, nil
}

func mcpToolAllowed(allow, deny, bot map[string]bool, name string) bool {
	return !(len(allow) > 0 && !allow[name] || deny[name] || len(bot) > 0 && !bot[name] && !bot["*"])
}

func (m *Manager) callMCPTool(callCtx context.Context, cli *mcp.ClientSession, name string, args json.RawMessage, readOnly bool) (string, error) {
	return m.callMCPToolObserved(callCtx, cli, name, args, readOnly, mcpCallObserver{})
}

// mcpCallObserver lets a run-scoped session learn whether the endpoint
// answered. It never changes the outcome returned to the model.
type mcpCallObserver struct {
	// connectionFailed runs when tools/call failed below the JSON-RPC layer.
	connectionFailed func()
	// reached runs when the endpoint answered tools/call at all.
	reached func()
}

func (m *Manager) callMCPToolObserved(callCtx context.Context, cli *mcp.ClientSession, name string, args json.RawMessage, readOnly bool, observe mcpCallObserver) (string, error) {
	if callCtx == nil {
		callCtx = context.Background()
	}
	if claimed, _ := callCtx.Value(mcpClaimedDispatchKey{}).(bool); claimed {
		readOnly = false
	}
	callCtx, cancel := context.WithTimeout(callCtx, m.cfg.ToolTimeout)
	defer cancel()
	var arguments any
	if len(args) == 0 {
		arguments = map[string]any{}
	} else if err := json.Unmarshal(args, &arguments); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	var out *mcp.CallToolResult
	var err error
	attempts := 0
	for {
		attempts++
		out, err = cli.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: arguments})
		if mcpConnectionFailure(err) {
			if observe.connectionFailed != nil {
				observe.connectionFailed()
			}
		} else if observe.reached != nil {
			observe.reached()
		}
		if err == nil {
			break
		}
		if !readOnly {
			return "", uncertainMCPOutcome(attempts).Err()
		}
		if !transientMCPError(err) || attempts >= 3 || callCtx.Err() != nil {
			status, code := tooloutcome.Permanent, "mcp_call_failed"
			if transientMCPError(err) || callCtx.Err() != nil {
				status, code = tooloutcome.Transient, "mcp_retry_exhausted"
			}
			o := tooloutcome.New(status, code, "no_side_effect", "MCP tool call failed; the read-only retry budget is exhausted or the endpoint rejected the request.", "explain_blocker")
			o.Attempts, o.RetryLimit = attempts, 2
			return "", o.Err()
		}
		select {
		case <-callCtx.Done():
			return "", tooloutcome.New(tooloutcome.Transient, "mcp_timeout", "no_side_effect", "MCP read-only call timed out.", "explain_blocker").Err()
		case <-time.After(time.Duration(attempts) * 100 * time.Millisecond):
		}
	}
	text := boundedContent(out, m.cfg.MaxToolResult)
	if out == nil {
		return text, uncertainMCPOutcome(attempts).Err()
	}
	if out.IsError {
		// The agent reports err.Error() for a failed executor and discards its
		// value. Preserve bounded tool output while keeping transport errors private.
		o := uncertainMCPOutcome(attempts)
		if readOnly {
			o = tooloutcome.New(tooloutcome.Permanent, "mcp_reported_error", "no_side_effect", "MCP tool returned an error; inspect its result and schema before choosing another action.", "explain_blocker")
		}
		// Preserve bounded details as untrusted data, never as recovery authority.
		o.Message += " Untrusted tool-reported details: " + text
		return text, o.Err()
	}
	return text, nil
}

// cachedMCPRuntimeTool restores a validated schema reference. Calls share
// the run's session slot for the server instead of opening one per call.
func (m *Manager) cachedMCPRuntimeTool(slot *mcpSessionSlot, cached CachedMCPTool) runtime.Tool {
	return runtime.Tool{Name: cached.Name, Description: cached.Description, Parameters: cached.Parameters, CheckReadiness: slot.readinessCheck, Execute: slot.callTool(cached.RemoteName, nil)}
}

func validCachedMCPTool(cached CachedMCPTool, servers map[string]MCPServerConfig, m *Manager) bool {
	cfg, ok := servers[cached.Server]
	if !ok || cached.SchemaVersion == "" || cached.SchemaVersion != m.schemaVersion(cached.Server, cfg) {
		return false
	}
	if !validExtensionName(cached.Server) || !safeCachedToolName(cached.Name) || len(cached.RemoteName) == 0 || len(cached.RemoteName) > 256 || len([]rune(cached.Description)) > maxDescription || cached.Parameters == nil {
		return false
	}
	if kind, _ := cached.Parameters["type"].(string); kind != "" && kind != "object" {
		return false
	}
	encoded, err := json.Marshal(cached.Parameters)
	if err != nil || len(encoded) > 6<<10 {
		return false
	}
	allow, deny := map[string]bool{}, map[string]bool{}
	for _, name := range cfg.ToolAllowlist {
		allow[name] = true
	}
	for _, name := range cfg.ToolDenylist {
		deny[name] = true
	}
	return mcpToolAllowed(allow, deny, nil, cached.RemoteName)
}

func safeCachedToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 || !strings.HasPrefix(name, "mcp_") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func toolSchema(t *mcp.Tool) map[string]any {
	b, _ := json.Marshal(t.InputSchema)
	var out map[string]any
	if json.Unmarshal(b, &out) != nil || out == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return out
}

func boundedDescription(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		s = "Tool: " + fallback
	}
	r := []rune(s)
	if len(r) > maxDescription {
		return string(r[:maxDescription-3]) + "..."
	}
	return s
}

// sanitizeToolName is the character mapping uniqueToolName applies before
// length limits and collision suffixes.
func sanitizeToolName(raw string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, raw))
}

func uniqueToolName(raw string, used map[string]int) string {
	raw = sanitizeToolName(raw)
	if raw == "" {
		raw = "mcp_tool"
	}
	if len([]rune(raw)) > 64 {
		raw = string([]rune(raw)[:64])
	}
	base := raw
	for used[raw] > 0 {
		used[base]++
		suffix := fmt.Sprintf("__%d", used[base])
		limit := 64 - len([]rune(suffix))
		if limit < 1 {
			limit = 1
		}
		r := []rune(base)
		if len(r) > limit {
			r = r[:limit]
		}
		raw = string(r) + suffix
	}
	used[raw]++
	return raw
}

func boundedContent(result *mcp.CallToolResult, max int) string {
	if result == nil {
		return "MCP returned an empty result"
	}
	var b strings.Builder
	for _, c := range result.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			b.WriteByte('\n')
		}
	}
	if result.StructuredContent != nil {
		if raw, err := json.Marshal(result.StructuredContent); err == nil {
			b.Write(raw)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "MCP returned no text content"
	}
	r := []rune(out)
	if len(r) > max {
		return string(r[:max]) + "\n[truncated]"
	}
	return out
}
