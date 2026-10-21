package extensions

// Tool tiers decide how much review one remote MCP tool needs: a read_only
// tool executes directly, a write tool is reviewed per call, a high_risk tool
// asks a person every time. The contract is docs/agent-plan/mcp-tool-tiers.md.
//
// The provider's own annotation is the authority for read_only: a tool the
// server marks readOnlyHint=true executes without review, exactly as other
// MCP hosts treat it. Everything else is reviewed per call; only the owner
// can make a tool high_risk. No model is asked to classify anything.
//
// The store holds policy only (tool names, digests, observed annotations and
// owner overrides); it never holds a URL, header or credential. Writes take
// the Manager's configuration fence so a tier change orders against dispatch
// exactly like a settings edit.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type MCPToolTier string

const (
	MCPTierReadOnly MCPToolTier = "read_only"
	MCPTierWrite    MCPToolTier = "write"
	MCPTierHighRisk MCPToolTier = "high_risk"
)

// Tier sources, as the UI shows them: the owner set it, the provider marked
// the tool read only, or neither (reviewed each time).
const (
	MCPTierSourceOwner    = "owner"
	MCPTierSourceProvider = "provider"
	MCPTierSourceDefault  = "default"

	// A legacy trusted_read_only_tools entry binds to the first digest seen.
	mcpTierLegacyDigest = "legacy"

	maxTierToolsPerServer  = maxDiscoveredTools
	maxTierDescriptionRune = 512
	maxTierFileBytes       = 4 << 20
	// Version 1 files (model classification, background queue) are read and
	// rewritten as version 2 on load: the dropped fields do not survive.
	tierFileVersion       = 2
	tierFileLegacyVersion = 1
)

func ValidMCPToolTier(s string) bool {
	switch MCPToolTier(s) {
	case MCPTierReadOnly, MCPTierWrite, MCPTierHighRisk:
		return true
	}
	return false
}

func mcpTierRank(t MCPToolTier) int {
	switch t {
	case MCPTierReadOnly:
		return 0
	case MCPTierHighRisk:
		return 2
	}
	return 1 // write, and anything unknown reads as write
}

// MCPToolAnnotations is the untrusted remote tools/list annotation set as
// last seen. Present says whether the remote sent any annotations object.
type MCPToolAnnotations struct {
	Present     bool  `json:"present"`
	ReadOnly    bool  `json:"read_only_hint"`
	Destructive *bool `json:"destructive_hint,omitempty"`
}

// MCPToolDescriptor is what observation records.
type MCPToolDescriptor struct {
	Server      string             `json:"server"`
	RemoteName  string             `json:"name"`
	Description string             `json:"description"`
	InputSchema map[string]any     `json:"input_schema"`
	Annotations MCPToolAnnotations `json:"annotations"`
	Digest      string             `json:"digest"`
}

// MCPToolDigest binds every tier decision to the exact tool it was made for.
func MCPToolDigest(d MCPToolDescriptor) string {
	raw, err := json.Marshal(struct {
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Schema      map[string]any     `json:"input_schema"`
		Annotations MCPToolAnnotations `json:"annotations"`
	}{d.RemoteName, d.Description, d.InputSchema, d.Annotations})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type mcpToolTierRecord struct {
	Digest             string `json:"digest"`
	Description        string `json:"description,omitempty"`
	AnnotationsPresent bool   `json:"annotations_present,omitempty"`
	ReadOnlyHint       bool   `json:"read_only_hint,omitempty"`
	DestructiveHint    *bool  `json:"destructive_hint,omitempty"`
	// Duplicated marks a remote name listed more than once in one tools/list.
	Duplicated     bool   `json:"duplicated,omitempty"`
	Override       string `json:"override,omitempty"`
	OverrideDigest string `json:"override_digest,omitempty"`
}

type mcpToolTierServer struct {
	MigratedTrusted bool                          `json:"migrated_trusted"`
	Tools           map[string]*mcpToolTierRecord `json:"tools"`
}

type mcpToolTierFile struct {
	Version int                           `json:"version"`
	Servers map[string]*mcpToolTierServer `json:"servers"`
}

type mcpToolTierStore struct {
	mu      sync.RWMutex
	path    string
	servers map[string]*mcpToolTierServer
}

func newMCPToolTierStore(configPath string) *mcpToolTierStore {
	s := &mcpToolTierStore{servers: map[string]*mcpToolTierServer{}}
	if strings.TrimSpace(configPath) == "" {
		return s
	}
	s.path = filepath.Join(filepath.Dir(configPath), "mcp-tool-tiers.json")
	raw, err := os.ReadFile(s.path)
	if err != nil || len(raw) > maxTierFileBytes {
		return s
	}
	var f mcpToolTierFile
	if json.Unmarshal(raw, &f) != nil || f.Version != tierFileVersion && f.Version != tierFileLegacyVersion {
		return s
	}
	for name, srv := range f.Servers {
		if srv == nil || !validExtensionName(name) {
			continue
		}
		if srv.Tools == nil {
			srv.Tools = map[string]*mcpToolTierRecord{}
		}
		for tool, rec := range srv.Tools {
			if rec == nil || tool == "" || len(tool) > maxCatalogToolNameBytes {
				delete(srv.Tools, tool)
				continue
			}
			if !ValidMCPToolTier(rec.Override) {
				rec.Override, rec.OverrideDigest = "", ""
			}
			if f.Version == tierFileLegacyVersion {
				// A version 1 record carries the first listing's annotations
				// and never said whether the name was listed more than once
				// (the model classifier, not the annotation, decided then).
				// Carrying its readOnlyHint into version 2 would make a
				// duplicated listing read_only until the server is listed
				// again, and a call through a cached schema needs no fresh
				// listing. The provider's mark is re-recorded by the next
				// tools/list; until then the tool is reviewed. Digest,
				// description and owner override survive.
				rec.AnnotationsPresent, rec.ReadOnlyHint, rec.DestructiveHint, rec.Duplicated = false, false, nil, false
			}
		}
		s.servers[name] = srv
	}
	if f.Version == tierFileLegacyVersion {
		// Rewrite at once so the model-classification fields and the queue
		// state of the old format are gone, not merely ignored. Owner
		// overrides (including migrated trusted_read_only_tools) are kept.
		s.mu.Lock()
		_ = s.saveLocked()
		s.mu.Unlock()
	}
	return s
}

// saveLocked persists atomically with mode 0600. Callers hold mu.
func (s *mcpToolTierStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(mcpToolTierFile{Version: tierFileVersion, Servers: s.servers}, "", " ")
	if err != nil {
		return err
	}
	if len(raw) > maxTierFileBytes {
		return errors.New("tool tier file limit exceeded")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-tool-tiers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func (s *mcpToolTierStore) serverLocked(name string) *mcpToolTierServer {
	srv := s.servers[name]
	if srv == nil {
		srv = &mcpToolTierServer{Tools: map[string]*mcpToolTierRecord{}}
		s.servers[name] = srv
	}
	return srv
}

// migrateTrustedLocked turns legacy trusted_read_only_tools into owner
// overrides once per server. Until then the legacy list is honoured virtually
// (see resolve), so a config written before this code ran keeps its policy.
func (s *mcpToolTierStore) migrateTrustedLocked(name string, cfg MCPServerConfig) {
	srv := s.serverLocked(name)
	if srv.MigratedTrusted {
		return
	}
	for _, tool := range cfg.TrustedReadOnlyTools {
		if tool == "" || len(tool) > maxCatalogToolNameBytes {
			continue
		}
		rec := srv.Tools[tool]
		if rec == nil {
			rec = &mcpToolTierRecord{}
			srv.Tools[tool] = rec
		}
		if rec.Override == "" {
			rec.Override, rec.OverrideDigest = string(MCPTierReadOnly), mcpTierLegacyDigest
		}
	}
	srv.MigratedTrusted = true
}

// MCPToolTierView is the resolved policy for one tool, as the UI and the gate
// see it. Binding changes whenever anything that produced Tier changes.
type MCPToolTierView struct {
	RemoteName      string      `json:"name"`
	Description     string      `json:"description,omitempty"`
	Tier            MCPToolTier `json:"tier"`
	Source          string      `json:"source"`
	Override        string      `json:"override,omitempty"`
	Changed         bool        `json:"changed,omitempty"`
	Duplicated      bool        `json:"duplicated,omitempty"`
	ReadOnlyHint    bool        `json:"read_only_hint,omitempty"`
	DestructiveHint *bool       `json:"destructive_hint,omitempty"`
	Binding         string      `json:"-"`
	// Digest is the tool definition's digest (name, description, input schema,
	// annotations) as last observed; empty when the tool was never observed.
	// It does not include the owner's override, unlike Binding.
	Digest string `json:"-"`
}

// resolveMCPToolTier is the single place the contract's precedence lives: a
// fresh owner override wins; a stale one only tightens; the provider's
// readOnlyHint makes an unambiguous tool read_only; everything else is write.
func resolveMCPToolTier(name string, rec *mcpToolTierRecord, legacyTrusted bool) MCPToolTierView {
	view := MCPToolTierView{RemoteName: name, Tier: MCPTierWrite, Source: MCPTierSourceDefault}
	override, overrideDigest := "", ""
	if rec != nil {
		view.Description, view.Duplicated = rec.Description, rec.Duplicated
		view.Digest = rec.Digest
		view.ReadOnlyHint, view.DestructiveHint = rec.ReadOnlyHint, rec.DestructiveHint
		// A name listed more than once is one tools/call target with several
		// schemas; no single annotation describes it, so the provider's mark
		// does not apply. destructiveHint never changes the tier: it is passed
		// to the reviewer as evidence.
		if rec.AnnotationsPresent && rec.ReadOnlyHint && !rec.Duplicated {
			view.Tier, view.Source = MCPTierReadOnly, MCPTierSourceProvider
		}
		override, overrideDigest = rec.Override, rec.OverrideDigest
	}
	if override == "" && legacyTrusted {
		override, overrideDigest = string(MCPTierReadOnly), mcpTierLegacyDigest
	}
	if ValidMCPToolTier(override) {
		view.Override = override
		fresh := overrideDigest == mcpTierLegacyDigest || rec != nil && overrideDigest == rec.Digest
		if fresh {
			view.Tier, view.Source = MCPToolTier(override), MCPTierSourceOwner
		} else {
			view.Changed = true
			if mcpTierRank(MCPToolTier(override)) > mcpTierRank(view.Tier) {
				view.Tier, view.Source = MCPToolTier(override), MCPTierSourceOwner
			}
		}
	}
	raw, _ := json.Marshal(struct {
		Record *mcpToolTierRecord
		Legacy bool
		Tier   MCPToolTier
		Source string
	}{rec, legacyTrusted, view.Tier, view.Source})
	sum := sha256.Sum256(raw)
	view.Binding = hex.EncodeToString(sum[:])
	return view
}

func (s *mcpToolTierStore) resolve(server string, cfg MCPServerConfig, tool string) MCPToolTierView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rec *mcpToolTierRecord
	legacy := false
	if srv := s.servers[server]; srv != nil {
		rec = srv.Tools[tool]
		legacy = !srv.MigratedTrusted && trustedReadOnlyTool(cfg, tool)
	} else {
		legacy = trustedReadOnlyTool(cfg, tool)
	}
	return resolveMCPToolTier(tool, rec, legacy)
}

// MCPToolTier resolves the current policy for one remote tool of a configured
// server. It reads the configuration only for the legacy trusted list.
func (m *Manager) MCPToolTier(server, tool string) MCPToolTierView {
	m.mu.RLock()
	servers, _ := loadServers(m.cfg.MCPConfigPath)
	m.mu.RUnlock()
	return m.tiers.resolve(server, servers[server], tool)
}

func (m *Manager) toolReadOnly(server string, cfg MCPServerConfig, tool string) bool {
	return m.tiers.resolve(server, cfg, tool).Tier == MCPTierReadOnly
}

// MCPCallTier resolves the tier of one proposal against the current disk
// state. current is false when the configuration fingerprint or the tier
// binding the proposal was built with no longer matches. A proposal without a
// binding (never produced by discovery) is reviewed as write and is current
// only in its configuration part; it can never be read_only or high_risk.
func (m *Manager) MCPCallTier(call MCPCallApproval) (MCPToolTier, bool) {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	cfg, ok := servers[call.Server]
	if err != nil || !ok || call.ConfigVersion == "" || metadataFingerprint(call.Server, cfg) != call.ConfigVersion {
		return MCPTierWrite, false
	}
	if call.TierBinding == "" {
		return MCPTierWrite, true
	}
	view := m.tiers.resolve(call.Server, cfg, call.Tool)
	if view.Binding != call.TierBinding {
		return MCPTierWrite, false
	}
	return view.Tier, true
}

// MCPConfigVersion is the identity fingerprint of a configured server (its
// name, address, transport and headers), or false when no such server exists.
// A standing allowance binds to it: any change of what the name dials voids
// the allowance.
func (m *Manager) MCPConfigVersion(server string) (string, bool) {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	cfg, ok := servers[server]
	if err != nil || !ok {
		return "", false
	}
	return metadataFingerprint(server, cfg), true
}

// MCPServerIdentity is what a standing allowance binds to besides the
// configuration fingerprint: a digest of the credential target (address and
// OAuth client, scopes and discovery URLs), so that connecting the same name
// to another client or destination voids it. Nothing secret leaves: only the
// digest does.
func (m *Manager) MCPServerIdentity(server string) (fingerprint, credential string, ok bool) {
	m.mcpConfigFence.RLock()
	defer m.mcpConfigFence.RUnlock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	cfg, found := servers[server]
	if err != nil || !found {
		return "", "", false
	}
	sum := sha256.Sum256([]byte(credentialTargetKey(cfg)))
	return metadataFingerprint(server, cfg), hex.EncodeToString(sum[:]), true
}

func (m *Manager) tierBindingCurrent(call MCPCallApproval, cfg MCPServerConfig) bool {
	if call.TierBinding == "" {
		return true
	}
	return m.tiers.resolve(call.Server, cfg, call.Tool).Binding == call.TierBinding
}

// ObserveTools records one complete tools/list for a server: digests, hints
// and descriptions move, tools no longer offered are dropped, and legacy
// trusted names are migrated once. No model is consulted; the provider's
// annotations recorded here are what resolve reads.
func (m *Manager) ObserveTools(server string, cfg MCPServerConfig, tools []MCPToolDescriptor) error {
	if !validExtensionName(server) {
		return errors.New("invalid MCP server name")
	}
	if len(tools) > maxTierToolsPerServer {
		return errors.New("MCP server exposed too many tools")
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	if cfg.URL == "" {
		// A caller without the configuration in hand (tests, maintenance)
		// still migrates the legacy trusted list from the saved file.
		if servers, err := loadServers(m.cfg.MCPConfigPath); err == nil {
			cfg = servers[server]
		}
	}
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	before, _ := json.Marshal(s.servers[server])
	s.migrateTrustedLocked(server, cfg)
	srv := s.serverLocked(server)
	observed := make(map[string]bool, len(tools))
	// A remote name listed more than once is one tools/call target with
	// several run-facing schemas. Its digest covers every listing (an owner
	// override binds to the whole set) and the provider's readOnlyHint does
	// not apply to it (write unless the owner decides).
	listings := map[string][]string{}
	for _, d := range tools {
		if d.RemoteName == "" || len(d.RemoteName) > maxCatalogToolNameBytes {
			continue
		}
		if digest := MCPToolDigest(MCPToolDescriptor{RemoteName: d.RemoteName, Description: d.Description, InputSchema: d.InputSchema, Annotations: d.Annotations}); digest != "" {
			listings[d.RemoteName] = append(listings[d.RemoteName], digest)
		}
	}
	for _, d := range tools {
		if d.RemoteName == "" || len(d.RemoteName) > maxCatalogToolNameBytes || observed[d.RemoteName] {
			continue
		}
		d.Server = server
		if d.Digest == "" {
			d.Digest = MCPToolDigest(d)
		}
		if d.Digest == "" {
			continue
		}
		duplicated := len(listings[d.RemoteName]) > 1
		if duplicated {
			all := append([]string(nil), listings[d.RemoteName]...)
			sort.Strings(all)
			sum := sha256.Sum256([]byte(strings.Join(all, "\n")))
			d.Digest = hex.EncodeToString(sum[:])
		}
		observed[d.RemoteName] = true
		rec := srv.Tools[d.RemoteName]
		if rec == nil {
			rec = &mcpToolTierRecord{}
			srv.Tools[d.RemoteName] = rec
		}
		if rec.OverrideDigest == mcpTierLegacyDigest {
			rec.OverrideDigest = d.Digest
		}
		rec.Digest = d.Digest
		rec.Description = truncateUTF8(strings.TrimSpace(d.Description), maxTierDescriptionRune)
		rec.AnnotationsPresent, rec.ReadOnlyHint, rec.DestructiveHint = d.Annotations.Present, d.Annotations.ReadOnly, d.Annotations.Destructive
		rec.Duplicated = duplicated
	}
	for name, rec := range srv.Tools {
		// A migrated legacy name the server never offered stays until it is
		// offered; a known tool that vanished from the list is dropped.
		if !observed[name] && rec.OverrideDigest != mcpTierLegacyDigest {
			delete(srv.Tools, name)
		}
	}
	after, _ := json.Marshal(srv)
	if string(before) != string(after) {
		return s.saveLocked()
	}
	return nil
}

// SetToolTierOverride binds an owner decision to the tool's current digest;
// an empty tier clears the override. The tool must have been observed.
func (m *Manager) SetToolTierOverride(server, tool, tier string) (MCPToolTierView, error) {
	if !validExtensionName(server) {
		return MCPToolTierView{}, errors.New("invalid MCP server name")
	}
	if tier != "" && !ValidMCPToolTier(tier) {
		return MCPToolTierView{}, errors.New("invalid tool tier")
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := s.servers[server]
	if srv == nil || srv.Tools[tool] == nil || srv.Tools[tool].Digest == "" {
		return MCPToolTierView{}, os.ErrNotExist
	}
	rec := srv.Tools[tool]
	if tier == "" {
		rec.Override, rec.OverrideDigest = "", ""
	} else {
		rec.Override, rec.OverrideDigest = tier, rec.Digest
	}
	if err := s.saveLocked(); err != nil {
		return MCPToolTierView{}, err
	}
	return resolveMCPToolTier(tool, rec, false), nil
}

// MCPToolTierList is the per-service summary the settings page shows.
type MCPToolTierList struct {
	Tools    []MCPToolTierView `json:"tools"`
	ReadOnly int               `json:"read_only"`
	Write    int               `json:"write"`
	HighRisk int               `json:"high_risk"`
}

func (m *Manager) ListToolTiers(server string) MCPToolTierList {
	m.mu.RLock()
	servers, _ := loadServers(m.cfg.MCPConfigPath)
	m.mu.RUnlock()
	cfg := servers[server]
	s := m.tiers
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := MCPToolTierList{Tools: []MCPToolTierView{}}
	srv := s.servers[server]
	names := map[string]bool{}
	if srv != nil {
		for name, rec := range srv.Tools {
			if rec.Digest != "" {
				names[name] = true
			}
		}
	}
	if srv == nil || !srv.MigratedTrusted {
		for _, name := range cfg.TrustedReadOnlyTools {
			names[name] = true
		}
	}
	for name := range names {
		var rec *mcpToolTierRecord
		if srv != nil {
			rec = srv.Tools[name]
		}
		legacy := (srv == nil || !srv.MigratedTrusted) && trustedReadOnlyTool(cfg, name)
		view := resolveMCPToolTier(name, rec, legacy)
		out.Tools = append(out.Tools, view)
		switch view.Tier {
		case MCPTierReadOnly:
			out.ReadOnly++
		case MCPTierHighRisk:
			out.HighRisk++
		default:
			out.Write++
		}
	}
	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].RemoteName < out.Tools[j].RemoteName })
	return out
}

// RunToolReadOnly maps a run tool name (mcp_<server>__<tool>, as recorded in
// tool activity) back to its tier. Unknown names are not read-only.
func (m *Manager) RunToolReadOnly(runToolName string) bool {
	m.mu.RLock()
	servers, _ := loadServers(m.cfg.MCPConfigPath)
	m.mu.RUnlock()
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	server := mcpServerForToolName(names, runToolName)
	if server == "" {
		return false
	}
	s := m.tiers
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv := s.servers[server]
	cfg := servers[server]
	candidates := map[string]bool{}
	if srv != nil {
		for name := range srv.Tools {
			candidates[name] = true
		}
	}
	for _, name := range cfg.TrustedReadOnlyTools {
		candidates[name] = true
	}
	// Run names are sanitized (case-folded, punctuation mapped to '_'), so
	// two distinct remote tools can share one run name; discovery suffixes
	// the second. The recorded step does not say which one ran, so an
	// ambiguous name is never an observation.
	matched := ""
	for name := range candidates {
		if !strings.EqualFold(sanitizeToolName("mcp_"+server+"__"+name), runToolName) {
			continue
		}
		if matched != "" {
			return false
		}
		matched = name
	}
	if matched == "" {
		return false
	}
	var rec *mcpToolTierRecord
	if srv != nil {
		rec = srv.Tools[matched]
	}
	legacy := (srv == nil || !srv.MigratedTrusted) && trustedReadOnlyTool(cfg, matched)
	return resolveMCPToolTier(matched, rec, legacy).Tier == MCPTierReadOnly
}

// clearToolTiersLocked forgets a server's policy. Callers hold the
// configuration fence (credential target change or deletion).
func (m *Manager) clearToolTiersLocked(server string) error {
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[server]; !ok {
		return nil
	}
	delete(s.servers, server)
	return s.saveLocked()
}

// mcpDispatchReadOnlyKey carries the proposal's resolved tier into the
// transport layer, which must not take the configuration fence itself.
type mcpDispatchReadOnlyKey struct{}
