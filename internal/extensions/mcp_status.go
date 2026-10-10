package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MCPStatus is the last known result of an explicit or background check. It is
// persisted so Settings can render a connection's state on first paint without
// probing the server. It holds no credentials or upstream error text.
type MCPStatus struct {
	CheckedAt  time.Time `json:"checked_at"`
	OK         bool      `json:"ok"`
	ToolCount  int       `json:"tool_count"`
	ErrorClass string    `json:"error_class,omitempty"` // "auth" or "failed"
	URL        string    `json:"url,omitempty"`         // status applies only while the endpoint is unchanged
}

type statusFile struct {
	Servers map[string]MCPStatus `json:"servers"`
}

func (m *Manager) statusPath() string {
	if strings.TrimSpace(m.cfg.MCPConfigPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.cfg.MCPConfigPath), ".mcp-status.json")
}

func (m *Manager) loadStatuses() map[string]MCPStatus {
	path := m.statusPath()
	out := map[string]MCPStatus{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var f statusFile
	if json.Unmarshal(b, &f) != nil || f.Servers == nil {
		return out
	}
	return f.Servers
}

// recordStatus stores the outcome of a check. A failed check keeps the last
// successful tool count so the card can still say what was there before.
func (m *Manager) recordStatus(name, url string, inspection MCPInspection) {
	path := m.statusPath()
	if path == "" {
		return
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	all := m.loadStatuses()
	prev := all[name]
	st := MCPStatus{CheckedAt: m.clock().UTC(), URL: url}
	switch {
	case len(inspection.Diagnostics) == 0:
		st.OK, st.ToolCount = true, inspection.ToolCount
	case inspection.AuthRequired:
		st.ErrorClass = "auth"
	default:
		st.ErrorClass = "failed"
	}
	if !st.OK && prev.URL == url {
		st.ToolCount = prev.ToolCount
	}
	all[name] = st
	_ = writeJSON0600(path, statusFile{Servers: all})
}

func (m *Manager) forgetStatus(name string) {
	path := m.statusPath()
	if path == "" {
		return
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	all := m.loadStatuses()
	if _, ok := all[name]; !ok {
		return
	}
	delete(all, name)
	_ = writeJSON0600(path, statusFile{Servers: all})
}
