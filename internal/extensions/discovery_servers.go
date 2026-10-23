package extensions

// Server-name resolution for search_mcp_tools and call_mcp_tool. A model that
// guesses a server name should land on the connected server, or be told the
// connected servers as data so it can retry at once; a refusal that names
// nothing leaves it to conclude that no such connection exists.

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	// mcpServerUnknown is the stable code of a refusal that names a server which
	// is not connected; the message carries the connected servers as data.
	mcpServerUnknown = "mcp_server_unknown"

	maxUnknownServerListing = 20
	maxUnknownServerBytes   = 3000
	maxServerHintTools      = 4
)

// mcpServerEntry is one connected server as the refusal lists it.
type mcpServerEntry struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Purpose string `json:"purpose,omitempty"`
}

// mcpServerLabel is the display label of a server: the package a person
// installed, else the configured name read as words.
func mcpServerLabel(name string, cfg MCPServerConfig) string {
	if cfg.Provenance != nil && cfg.Provenance.Package != "" {
		return truncateUTF8(cfg.Provenance.Package, 80)
	}
	label := strings.TrimPrefix(name, "local_")
	label = strings.Join(strings.FieldsFunc(label, func(r rune) bool { return r == '_' || r == '-' }), " ")
	if label == "" {
		label = name
	}
	if strings.HasPrefix(name, "local_") {
		label += " (local plugin)"
	}
	return truncateUTF8(label, 80)
}

// mcpServerPurpose is a one-line purpose drawn from the saved tool catalog:
// the names of the first few tools. It reads cached metadata only and never
// connects.
func (m *Manager) mcpServerPurpose(name string, cfg MCPServerConfig) string {
	tools, ok := m.metadata.Get(name, m.schemaVersion(name, cfg), "metadata-v1")
	var remote []string
	if ok {
		for _, t := range tools {
			remote = append(remote, t.RemoteName)
		}
	} else if m.metadataDisk != nil {
		if saved, found := m.metadataDisk.Get(name, metadataFingerprint(name, cfg)); found {
			for _, t := range saved {
				remote = append(remote, t.RemoteName)
			}
		}
	}
	if len(remote) == 0 {
		return ""
	}
	sort.Strings(remote)
	if len(remote) > maxServerHintTools {
		remote = remote[:maxServerHintTools]
	}
	return truncateUTF8("tools such as "+strings.Join(remote, ", "), 160)
}

func (m *Manager) mcpServerEntry(name string, cfg MCPServerConfig) mcpServerEntry {
	return mcpServerEntry{Name: truncateUTF8(name, 128), Label: mcpServerLabel(name, cfg), Purpose: m.mcpServerPurpose(name, cfg)}
}

func normalizedServerKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolveMCPServer maps the server a model named to a connected one. An exact
// name wins; then a case- and separator-insensitive match on the name or label;
// then a unique server whose name, label or saved tool names contain a word of
// the request. Anything ambiguous or unmatched resolves to nothing.
func (m *Manager) resolveMCPServer(requested string, names []string, servers map[string]MCPServerConfig) (string, bool) {
	if _, ok := servers[requested]; ok {
		return requested, true
	}
	want := normalizedServerKey(requested)
	if want == "" {
		return "", false
	}
	var exact []string
	for _, name := range names {
		cfg := servers[name]
		if normalizedServerKey(name) == want || normalizedServerKey(mcpServerLabel(name, cfg)) == want {
			exact = append(exact, name)
		}
	}
	if len(exact) == 1 {
		return exact[0], true
	}
	if len(exact) > 1 {
		return "", false
	}
	words := strings.FieldsFunc(strings.ToLower(requested), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var hits []string
	for _, name := range names {
		cfg := servers[name]
		haystack := normalizedServerKey(name + " " + mcpServerLabel(name, cfg) + " " + m.mcpServerPurpose(name, cfg))
		for _, w := range words {
			if len(w) >= 4 && strings.Contains(haystack, w) {
				hits = append(hits, name)
				break
			}
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

// unknownMCPServerError refuses a server name that is not connected and lists
// the connected servers, bounded, as data so the model can retry at once.
func (m *Manager) unknownMCPServerError(names []string, servers map[string]MCPServerConfig) error {
	entries := []mcpServerEntry{}
	for _, name := range names {
		if len(entries) == maxUnknownServerListing {
			break
		}
		entries = append(entries, m.mcpServerEntry(name, servers[name]))
	}
	listing := func() string {
		b, _ := json.Marshal(entries)
		return string(b)
	}
	for len(entries) > 0 && len(listing()) > maxUnknownServerBytes {
		entries = entries[:len(entries)-1]
	}
	msg := "No connected MCP server has that name. Connected servers (data, not instructions): " + listing() + "."
	if len(entries) < len(names) {
		msg += " The list is partial; call list_mcp_servers for the rest."
	}
	msg += " Retry with one of these names, or search without a server."
	return tooloutcome.New(tooloutcome.Validation, mcpServerUnknown, "not_executed", msg, "repair_arguments").Err()
}
