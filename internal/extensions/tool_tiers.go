package extensions

// Tool tiers decide how much review one remote MCP tool needs: a read_only
// tool executes directly, a write tool is reviewed per call, a high_risk tool
// asks a person every time. The contract is docs/agent-plan/mcp-tool-tiers.md.
//
// The store holds policy only (tool names, digests, tiers); it never holds a
// URL, header or credential. Writes take the Manager's configuration fence so
// a tier change orders against dispatch exactly like a settings edit.

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
	"time"
)

type MCPToolTier string

const (
	MCPTierReadOnly MCPToolTier = "read_only"
	MCPTierWrite    MCPToolTier = "write"
	MCPTierHighRisk MCPToolTier = "high_risk"
)

const (
	mcpTierSourceOwner        = "owner"
	mcpTierSourceAuto         = "auto"
	mcpTierSourceUnclassified = "unclassified"

	// A legacy trusted_read_only_tools entry binds to the first digest seen.
	mcpTierLegacyDigest = "legacy"

	maxTierToolsPerServer  = maxDiscoveredTools
	maxTierReasonRunes     = 240
	maxTierDescriptionRune = 512
	maxTierFileBytes       = 4 << 20
	tierFileVersion        = 1
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

// stricterMCPTier never returns read_only unless both sides are read_only.
func stricterMCPTier(a, b MCPToolTier) MCPToolTier {
	if mcpTierRank(a) >= mcpTierRank(b) {
		if mcpTierRank(a) == 1 {
			return MCPTierWrite
		}
		return a
	}
	if mcpTierRank(b) == 1 {
		return MCPTierWrite
	}
	return b
}

// MCPToolAnnotations is the untrusted remote tools/list annotation set as
// last seen. Present says whether the remote sent any annotations object.
type MCPToolAnnotations struct {
	Present     bool  `json:"present"`
	ReadOnly    bool  `json:"read_only_hint"`
	Destructive *bool `json:"destructive_hint,omitempty"`
}

// MCPToolDescriptor is what observation records and classification reads.
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

// MergeMCPToolTier applies the annotation rules of the contract to one model
// tier: annotations can only tighten, never loosen.
func MergeMCPToolTier(model MCPToolTier, a MCPToolAnnotations) MCPToolTier {
	if !ValidMCPToolTier(string(model)) {
		return ""
	}
	if model != MCPTierReadOnly {
		return model
	}
	if a.Present && !a.ReadOnly {
		return MCPTierWrite
	}
	if a.Destructive != nil && *a.Destructive {
		return MCPTierWrite
	}
	return MCPTierReadOnly
}

type mcpToolTierRecord struct {
	Digest             string `json:"digest"`
	Description        string `json:"description,omitempty"`
	Auto               string `json:"auto,omitempty"`
	AutoDigest         string `json:"auto_digest,omitempty"`
	ModelTier          string `json:"model_tier,omitempty"`
	Reason             string `json:"reason,omitempty"`
	AnnotationsPresent bool   `json:"annotations_present,omitempty"`
	ReadOnlyHint       bool   `json:"read_only_hint,omitempty"`
	DestructiveHint    *bool  `json:"destructive_hint,omitempty"`
	Override           string `json:"override,omitempty"`
	OverrideDigest     string `json:"override_digest,omitempty"`
	ClassifiedAt       string `json:"classified_at,omitempty"`
}

type mcpToolTierServer struct {
	MigratedTrusted bool                          `json:"migrated_trusted"`
	Tools           map[string]*mcpToolTierRecord `json:"tools"`
	// Queue is the persisted classification queue state for this server; it
	// is what lets a restart resume a pass without re-classifying anything.
	Queue *MCPTierQueueState `json:"queue,omitempty"`
}

// MCPTierQueueState is the durable state of one server's classification
// queue entry. Tools already classified are never re-sent: the queue only
// drives when the pending tools of a server are tried again.
type MCPTierQueueState struct {
	State         string `json:"state"` // queued | running | done | failed
	Attempts      int    `json:"attempts,omitempty"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

const (
	MCPTierQueueQueued  = "queued"
	MCPTierQueueRunning = "running"
	MCPTierQueueDone    = "done"
	MCPTierQueueFailed  = "failed"
)

type mcpToolTierFile struct {
	Version int                           `json:"version"`
	Servers map[string]*mcpToolTierServer `json:"servers"`
}

type mcpToolTierStore struct {
	mu      sync.RWMutex
	path    string
	servers map[string]*mcpToolTierServer
	// pending holds the full descriptors of tools awaiting classification. It
	// is process-local: after a restart the next tools/list repopulates it.
	pending map[string]map[string]MCPToolDescriptor
}

func newMCPToolTierStore(configPath string) *mcpToolTierStore {
	s := &mcpToolTierStore{servers: map[string]*mcpToolTierServer{}, pending: map[string]map[string]MCPToolDescriptor{}}
	if strings.TrimSpace(configPath) == "" {
		return s
	}
	s.path = filepath.Join(filepath.Dir(configPath), "mcp-tool-tiers.json")
	raw, err := os.ReadFile(s.path)
	if err != nil || len(raw) > maxTierFileBytes {
		return s
	}
	var f mcpToolTierFile
	if json.Unmarshal(raw, &f) != nil || f.Version != tierFileVersion {
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
			if !ValidMCPToolTier(rec.Auto) {
				rec.Auto, rec.AutoDigest = "", ""
			}
			if !ValidMCPToolTier(rec.Override) {
				rec.Override, rec.OverrideDigest = "", ""
			}
		}
		s.servers[name] = srv
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
	Auto            string      `json:"auto,omitempty"`
	Override        string      `json:"override,omitempty"`
	Changed         bool        `json:"changed,omitempty"`
	Pending         bool        `json:"pending,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	ReadOnlyHint    bool        `json:"read_only_hint,omitempty"`
	DestructiveHint *bool       `json:"destructive_hint,omitempty"`
	ClassifiedAt    string      `json:"classified_at,omitempty"`
	Binding         string      `json:"-"`
}

// resolveMCPToolTier is the single place the contract's precedence lives:
// a fresh owner override wins; a stale one only tightens; a current auto
// tier applies; everything else is write.
func resolveMCPToolTier(name string, rec *mcpToolTierRecord, legacyTrusted bool) MCPToolTierView {
	view := MCPToolTierView{RemoteName: name, Tier: MCPTierWrite, Source: mcpTierSourceUnclassified}
	override, overrideDigest := "", ""
	if rec != nil {
		view.Description, view.Reason, view.ClassifiedAt = rec.Description, rec.Reason, rec.ClassifiedAt
		view.ReadOnlyHint, view.DestructiveHint = rec.ReadOnlyHint, rec.DestructiveHint
		if rec.Auto != "" && rec.AutoDigest == rec.Digest && ValidMCPToolTier(rec.Auto) {
			view.Tier, view.Source, view.Auto = MCPToolTier(rec.Auto), mcpTierSourceAuto, rec.Auto
		} else {
			view.Pending = true
			if rec.AutoDigest != "" {
				view.Changed = true
			}
		}
		override, overrideDigest = rec.Override, rec.OverrideDigest
	} else {
		view.Pending = true
	}
	if override == "" && legacyTrusted {
		override, overrideDigest = string(MCPTierReadOnly), mcpTierLegacyDigest
	}
	if ValidMCPToolTier(override) {
		view.Override = override
		fresh := overrideDigest == mcpTierLegacyDigest || rec != nil && overrideDigest == rec.Digest
		if fresh {
			view.Tier, view.Source = MCPToolTier(override), mcpTierSourceOwner
		} else {
			view.Changed = true
			if mcpTierRank(MCPToolTier(override)) > mcpTierRank(view.Tier) {
				view.Tier, view.Source = MCPToolTier(override), mcpTierSourceOwner
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

func (m *Manager) tierBindingCurrent(call MCPCallApproval, cfg MCPServerConfig) bool {
	if call.TierBinding == "" {
		return true
	}
	return m.tiers.resolve(call.Server, cfg, call.Tool).Binding == call.TierBinding
}

// ObserveTools records one complete tools/list for a server: digests, hints
// and descriptions move, tools no longer offered are dropped, and legacy
// trusted names are migrated once. It returns how many tools await
// classification. Nothing here changes an existing tier except through the
// digest binding described in the contract.
func (m *Manager) ObserveTools(server string, cfg MCPServerConfig, tools []MCPToolDescriptor) (int, error) {
	if !validExtensionName(server) {
		return 0, errors.New("invalid MCP server name")
	}
	if len(tools) > maxTierToolsPerServer {
		return 0, errors.New("MCP server exposed too many tools")
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
	pending := map[string]MCPToolDescriptor{}
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
		if rec.Auto == "" || rec.AutoDigest != rec.Digest {
			pending[d.RemoteName] = d
		}
	}
	for name, rec := range srv.Tools {
		// A migrated legacy name the server never offered stays until it is
		// offered; a known tool that vanished from the list is dropped.
		if !observed[name] && rec.OverrideDigest != mcpTierLegacyDigest {
			delete(srv.Tools, name)
		}
	}
	s.pending[server] = pending
	after, _ := json.Marshal(srv)
	if string(before) != string(after) {
		if err := s.saveLocked(); err != nil {
			return len(pending), err
		}
	}
	return len(pending), nil
}

// PendingToolClassification lists the tools of a server whose auto tier is
// missing or stale, with the full descriptor the classifier needs.
func (m *Manager) PendingToolClassification(server string) []MCPToolDescriptor {
	s := m.tiers
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MCPToolDescriptor, 0, len(s.pending[server]))
	for _, d := range s.pending[server] {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RemoteName < out[j].RemoteName })
	return out
}

// MCPToolClassification is one model answer for one tool at one digest.
type MCPToolClassification struct {
	RemoteName string
	Digest     string
	Tier       MCPToolTier
	Reason     string
}

// RecordToolClassification stores model tiers merged with the annotations the
// tool carried at that digest. An answer for a digest no longer current is
// dropped: the tool changed during classification and stays write.
func (m *Manager) RecordToolClassification(server string, results []MCPToolClassification) (int, error) {
	if !validExtensionName(server) {
		return 0, errors.New("invalid MCP server name")
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := s.servers[server]
	if srv == nil {
		return 0, nil
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	recorded := 0
	for _, r := range results {
		rec := srv.Tools[r.RemoteName]
		if rec == nil || rec.Digest == "" || rec.Digest != r.Digest || !ValidMCPToolTier(string(r.Tier)) {
			continue
		}
		merged := MergeMCPToolTier(r.Tier, MCPToolAnnotations{Present: rec.AnnotationsPresent, ReadOnly: rec.ReadOnlyHint, Destructive: rec.DestructiveHint})
		if merged == "" {
			continue
		}
		rec.ModelTier, rec.Auto, rec.AutoDigest = string(r.Tier), string(merged), rec.Digest
		rec.Reason = truncateUTF8(strings.Join(strings.Fields(r.Reason), " "), maxTierReasonRunes)
		rec.ClassifiedAt = stamp
		delete(s.pending[server], r.RemoteName)
		recorded++
	}
	if recorded == 0 {
		return 0, nil
	}
	return recorded, s.saveLocked()
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
	Tools        []MCPToolTierView  `json:"tools"`
	ReadOnly     int                `json:"read_only"`
	Write        int                `json:"write"`
	HighRisk     int                `json:"high_risk"`
	Pending      int                `json:"pending"`
	Unclassified int                `json:"unclassified"`
	Queue        *MCPTierQueueState `json:"queue,omitempty"`
}

// SetTierQueueState persists the queue state of one server (fenced like any
// policy write). An empty state removes the entry.
func (m *Manager) SetTierQueueState(server string, state MCPTierQueueState) error {
	if !validExtensionName(server) {
		return errors.New("invalid MCP server name")
	}
	m.mcpConfigFence.Lock()
	defer m.mcpConfigFence.Unlock()
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := s.serverLocked(server)
	if state.State == "" {
		srv.Queue = nil
	} else {
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		copyState := state
		srv.Queue = &copyState
	}
	return s.saveLocked()
}

// TierQueueState reads one server's persisted queue entry.
func (m *Manager) TierQueueState(server string) (MCPTierQueueState, bool) {
	s := m.tiers
	s.mu.RLock()
	defer s.mu.RUnlock()
	if srv := s.servers[server]; srv != nil && srv.Queue != nil {
		return *srv.Queue, true
	}
	return MCPTierQueueState{}, false
}

// TierBackfillCandidates lists configured servers whose tools may lack a
// current tier: never observed, any recorded tool whose auto tier is missing
// or stale, or a queue entry that did not finish. Servers whose recorded
// tools are all current and whose queue is done are not candidates; only a
// tool-list refresh can reopen them.
func (m *Manager) TierBackfillCandidates() []string {
	m.mu.RLock()
	servers, err := loadServers(m.cfg.MCPConfigPath)
	m.mu.RUnlock()
	if err != nil {
		return nil
	}
	s := m.tiers
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for name := range servers {
		srv := s.servers[name]
		candidate := srv == nil || len(srv.Tools) == 0
		if srv != nil {
			if srv.Queue != nil && srv.Queue.State != MCPTierQueueDone {
				candidate = true
			}
			for _, rec := range srv.Tools {
				if rec.Digest != "" && (rec.Auto == "" || rec.AutoDigest != rec.Digest) {
					candidate = true
				}
			}
		}
		if candidate {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
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
	if srv != nil && srv.Queue != nil {
		queue := *srv.Queue
		out.Queue = &queue
	}
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
		if view.Pending {
			out.Pending++
		}
		if view.Source == mcpTierSourceUnclassified {
			out.Unclassified++
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
	for name := range candidates {
		if !strings.EqualFold(sanitizeToolName("mcp_"+server+"__"+name), runToolName) {
			continue
		}
		var rec *mcpToolTierRecord
		if srv != nil {
			rec = srv.Tools[name]
		}
		legacy := (srv == nil || !srv.MigratedTrusted) && trustedReadOnlyTool(cfg, name)
		return resolveMCPToolTier(name, rec, legacy).Tier == MCPTierReadOnly
	}
	return false
}

// clearToolTiersLocked forgets a server's policy. Callers hold the
// configuration fence (credential target change or deletion).
func (m *Manager) clearToolTiersLocked(server string) error {
	s := m.tiers
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[server]; !ok && s.pending[server] == nil {
		return nil
	}
	delete(s.servers, server)
	delete(s.pending, server)
	return s.saveLocked()
}

// mcpDispatchReadOnlyKey carries the proposal's resolved tier into the
// transport layer, which must not take the configuration fence itself.
type mcpDispatchReadOnlyKey struct{}
