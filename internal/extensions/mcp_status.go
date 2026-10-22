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
	// ErrorCode names a problem the person can fix ("api_disabled"); API and EnableURL then say which Google API
	// to enable and where. Set by a check, or by a tool call that came back "API not enabled".
	ErrorCode string `json:"error_code,omitempty"`
	API       string `json:"api,omitempty"`
	EnableURL string `json:"enable_url,omitempty"`
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
		if d := inspection.Diagnostics[0]; d.Code == "api_disabled" {
			st.ErrorCode, st.API, st.EnableURL = d.Code, d.API, d.EnableURL
		}
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

// noteToolFailure records "API not enabled" on a connection when a tool call came back with Google's structured
// SERVICE_DISABLED error. Nothing else is recorded from a failed call: the status holds no provider text.
func (m *Manager) noteToolFailure(name, url string, err error) {
	if err == nil {
		return
	}
	d := mcpInspectionDiagnostic(name, url, err)
	if d.Code != "api_disabled" {
		return
	}
	path := m.statusPath()
	if path == "" {
		return
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	all := m.loadStatuses()
	prev := all[name]
	st := MCPStatus{CheckedAt: m.clock().UTC(), URL: url, ErrorClass: "failed", ErrorCode: d.Code, API: d.API, EnableURL: d.EnableURL}
	if prev.URL == url {
		st.ToolCount = prev.ToolCount
	}
	all[name] = st
	_ = writeJSON0600(path, statusFile{Servers: all})
}
