package app

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Tool tier settings routes. Contract: docs/agent-plan/mcp-tool-tiers.md. The
// tier of a tool is the provider's own readOnlyHint unless the owner decided
// otherwise; nothing here asks a model, so connecting a service and starting
// the server cost no model request.

func (s *Server) notifyMCPTiers() {
	if s.store != nil {
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
	}
}

type mcpToolTierResponse struct {
	extensions.MCPToolTierList
	Diagnostics []extensions.Diagnostic  `json:"diagnostics,omitempty"`
	Checked     bool                     `json:"checked"`
	Error       string                   `json:"error_class,omitempty"`
	Order       []extensions.MCPToolTier `json:"tier_order"`
}

func (s *Server) mcpToolTierResponse(server string) mcpToolTierResponse {
	return mcpToolTierResponse{MCPToolTierList: s.extensions.ListToolTiers(server), Order: []extensions.MCPToolTier{extensions.MCPTierReadOnly, extensions.MCPTierWrite, extensions.MCPTierHighRisk}}
}

// routeMCPToolTiers serves the Connections page: the tool list with tiers,
// the owner's per-tool override, and Refresh (a fresh tools/list, which
// re-records the provider's annotations).
func (s *Server) routeMCPToolTiers(w http.ResponseWriter, r *http.Request, p string) bool {
	rest, ok := strings.CutPrefix(p, "extensions/mcp/")
	if !ok {
		return false
	}
	name, tail, ok := strings.Cut(rest, "/tools")
	if !ok || name == "" || strings.Contains(name, "/") || tail != "" && !strings.HasPrefix(tail, "/") {
		return false
	}
	known := false
	if servers, err := s.extensions.ListMCP(); err == nil {
		for _, server := range servers {
			if server.Name == name {
				known = true
			}
		}
	}
	switch {
	case tail == "" && r.Method == http.MethodGet:
		if !known {
			writeErr(w, 404, "extensions", "MCP server not found")
			return true
		}
		writeJSON(w, 200, s.mcpToolTierResponse(name))
		return true
	case tail == "/refresh" && r.Method == http.MethodPost:
		if !known {
			writeErr(w, 404, "extensions", "MCP server not found")
			return true
		}
		inspection := s.extensions.InspectMCP(r.Context(), name)
		out := s.mcpToolTierResponse(name)
		out.Checked, out.Diagnostics = len(inspection.Diagnostics) == 0, inspection.Diagnostics
		if !out.Checked {
			out.Error = "failed"
			if inspection.AuthRequired {
				out.Error = "auth"
			}
		}
		s.notifyMCPTiers()
		writeJSON(w, 200, out)
		return true
	case strings.HasPrefix(tail, "/") && len(tail) > 1 && r.Method == http.MethodPut:
		tool, err := url.PathUnescape(tail[1:])
		if err != nil || tool == "" {
			writeErr(w, 400, "invalid_request", "invalid tool name")
			return true
		}
		var in struct {
			Tier string `json:"tier"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", e.Error())
			return true
		}
		if in.Tier != "" && !extensions.ValidMCPToolTier(in.Tier) {
			writeErr(w, 400, "invalid_request", "tier must be read_only, write, high_risk or empty")
			return true
		}
		view, err := s.extensions.SetToolTierOverride(name, tool, in.Tier)
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, 404, "extensions", "MCP tool not found")
			return true
		}
		if err != nil {
			writeErr(w, 400, "extensions", err.Error())
			return true
		}
		if in.Tier == string(extensions.MCPTierHighRisk) {
			// "Always ask me" ends a standing allowance for this tool for good:
			// lowering the tier again does not bring it back.
			if err := s.store.revokeStandingAllowancesForTool(name, tool, "tier_raised"); err != nil {
				writeErr(w, 500, "storage", err.Error())
				return true
			}
		}
		s.notifyMCPTiers()
		writeJSON(w, 200, map[string]any{"ok": true, "tool": view, "summary": s.extensions.ListToolTiers(name)})
		return true
	}
	return false
}
