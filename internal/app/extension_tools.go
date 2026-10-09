package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type extensionToolOAuth struct {
	ClientID    string   `json:"client_id"`
	Scopes      []string `json:"scopes"`
	MetadataURL string   `json:"auth_server_metadata_url"`
}
type extensionToolArgs struct {
	Action         string              `json:"action"`
	Name           string              `json:"name"`
	URL            string              `json:"url"`
	Transport      *string             `json:"transport"`
	Headers        map[string]string   `json:"headers"`
	ToolAllowlist  *[]string           `json:"tool_allowlist"`
	ToolDenylist   *[]string           `json:"tool_denylist"`
	OAuth          *extensionToolOAuth `json:"oauth"`
	Files          map[string]string   `json:"files"`
	TestConnection bool                `json:"test_connection"`
}

func (s *Server) extensionManagementTools(c Conversation, r Run) []Tool {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	stringsSchema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	properties := map[string]any{
		"action":         map[string]any{"type": "string", "enum": []string{"mcp_list", "mcp_create", "mcp_update", "mcp_delete", "mcp_test", "oauth_required", "skill_list", "skill_install", "skill_delete"}},
		"name":           str("Existing or new MCP/skill name; required except list actions."),
		"url":            str("Complete HTTP(S) MCP endpoint; required for create/update. Do not copy a redacted URL from list output."),
		"transport":      map[string]any{"type": "string", "enum": []string{"streamable_http", "sse"}, "description": "MCP transport; omitted on update preserves the existing value. Defaults to streamable_http on create."},
		"headers":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "User-supplied headers. Omit to preserve existing headers; masked values preserve their existing secret. Never ask to read credentials."},
		"tool_allowlist": stringsSchema, "tool_denylist": stringsSchema,
		"oauth":           objectSchema(map[string]any{"client_id": str("Public OAuth client ID; omit for providers supporting dynamic registration. User completes authorization in Settings."), "scopes": stringsSchema, "auth_server_metadata_url": str("Public authorization-server metadata URL.")}, nil),
		"files":           map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Complete local skill files as UTF-8 strings, including SKILL.md with matching name; no downloading or execution. Installation does not overwrite an existing skill."},
		"test_connection": map[string]any{"type": "boolean", "description": "Must be true for mcp_test, only when the user requested connecting/testing. Other actions never test automatically."},
	}
	return []Tool{{Name: "manage_extensions", Description: "Manage the user's workspace-wide MCP servers and installed Skills using Tofi's existing settings. MCP servers are available to every Bot. Skills are available to every Bot unless the user limited a skill to selected Bots in Settings; skill_list shows only the skills the current Bot may use, and Bots cannot change skill access. Read/list first before changes. MCP update requires the complete desired URL; omitted policy lists, headers and OAuth retain existing values. Tool allow/deny lists are global server policies. Never expose credentials or perform OAuth authorization; tell the user to finish authorization in Settings. Test network connections only when explicitly requested using mcp_test with test_connection=true. Delete actions remove user configuration/skill files and require user intent.", Parameters: objectSchema(properties, []string{"action"}), Identity: extensionRecoveryIdentity, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		return s.executeExtensionManagement(ctx, c, r, raw)
	}}}
}

func (s *Server) extensionToolActive(ctx context.Context, c Conversation, r Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := s.store.GetRun(r.ID)
	if err != nil || current.Status != "running" || current.ConversationID != c.ID || current.BotID != r.BotID {
		return tooloutcome.New(tooloutcome.Denied, "run_inactive", "not_executed", "Run is no longer active.", "explain_blocker").Err()
	}
	conversation, err := s.store.GetConversation(c.ID)
	if err != nil || conversation.Archived {
		return tooloutcome.New(tooloutcome.Denied, "conversation_unavailable", "not_executed", "Conversation is unavailable or archived.", "explain_blocker").Err()
	}
	bot, err := s.store.GetBot(r.BotID)
	if err != nil || bot.Archived {
		return tooloutcome.New(tooloutcome.Denied, "bot_unavailable", "not_executed", "Bot is unavailable or archived.", "explain_blocker").Err()
	}
	return ctx.Err()
}

func (s *Server) executeExtensionManagement(ctx context.Context, c Conversation, r Run, raw json.RawMessage) (string, error) {
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return "", err
	}
	if s.extensions == nil {
		return "", tooloutcome.New(tooloutcome.Permanent, "extensions_unavailable", "not_executed", "Extensions are unavailable.", "explain_blocker").Err()
	}
	if len(raw) > 2<<20 {
		return "", tooloutcome.InvalidArguments("extension arguments exceed 2 MiB")
	}
	var x extensionToolArgs
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&x); err != nil {
		return "", tooloutcome.InvalidArguments("invalid extension arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", tooloutcome.InvalidArguments("expected one extension argument object")
	}
	if x.Action != "mcp_list" && x.Action != "skill_list" && strings.TrimSpace(x.Name) == "" {
		return "", tooloutcome.InvalidArguments("extension name is required")
	}
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return "", err
	}
	var result any
	var err error
	mutated := false
	switch x.Action {
	case "mcp_list":
		var servers []extensions.MCPServerView
		servers, err = s.extensions.ListMCP()
		if err == nil {
			for i := range servers {
				servers[i].URL = extensionPublicURL(servers[i].URL)
				if servers[i].OAuth != nil {
					servers[i].OAuth.AuthServerMetadataURL = extensionPublicURL(servers[i].OAuth.AuthServerMetadataURL)
				}
			}
			result = map[string]any{"servers": servers}
		}
	case "mcp_create", "mcp_update":
		u, parseErr := url.Parse(x.URL)
		if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "", tooloutcome.InvalidArguments("MCP create/update requires a complete HTTP(S) endpoint.")
		}
		config := extensions.MCPServerConfig{URL: x.URL, Headers: x.Headers, Transport: "streamable_http"}
		var existing *extensions.MCPServerView
		if x.Action == "mcp_update" {
			servers, listErr := s.extensions.ListMCP()
			if listErr != nil {
				return "", tooloutcome.New(tooloutcome.Permanent, "configuration_read_failed", "not_executed", "Extension configuration could not be read.", "explain_blocker").Err()
			}
			for i := range servers {
				if servers[i].Name == x.Name {
					existing = &servers[i]
					break
				}
			}
			if existing == nil {
				return "", tooloutcome.InvalidArguments("MCP server not found")
			}
			config.ToolAllowlist = existing.ToolAllowlist
			config.ToolDenylist = existing.ToolDenylist
			if existing.Transport != "" {
				config.Transport = existing.Transport
			}
		}
		if x.Transport != nil {
			if *x.Transport != "streamable_http" && *x.Transport != "sse" {
				return "", tooloutcome.InvalidArguments("unsupported MCP transport")
			}
			config.Transport = *x.Transport
		}
		if x.ToolAllowlist != nil {
			config.ToolAllowlist = *x.ToolAllowlist
		}
		if x.ToolDenylist != nil {
			config.ToolDenylist = *x.ToolDenylist
		}
		if x.OAuth != nil {
			config.OAuth = &extensions.OAuthConfig{ClientID: x.OAuth.ClientID, Scopes: x.OAuth.Scopes, AuthServerMetadataURL: x.OAuth.MetadataURL}
			// Only the existing manager's mask is used for updates. A newly
			// created public OAuth client never stores a pretend secret.
			if existing != nil && existing.OAuth != nil {
				config.OAuth.ClientSecret = existing.OAuth.ClientSecret
			}
		}
		if err = s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		err = s.extensions.SaveMCP(x.Name, config, x.Action == "mcp_update")
		mutated = err == nil
	case "mcp_delete":
		err = s.extensions.DeleteMCP(x.Name)
		mutated = err == nil
	case "mcp_test":
		if !x.TestConnection {
			return "", tooloutcome.New(tooloutcome.Denied, "connection_test_not_requested", "not_executed", "mcp_test requires explicit test_connection=true; do not test automatically", "explain_blocker").Err()
		}
		var servers []extensions.MCPServerView
		servers, err = s.extensions.ListMCP()
		if err != nil {
			break
		}
		found := false
		for _, server := range servers {
			if server.Name == x.Name {
				found = true
				if server.OAuth != nil && !server.OAuth.Connected {
					return extensionToolJSON(extensionOAuthRequired())
				}
			}
		}
		if !found {
			return "", tooloutcome.InvalidArguments("MCP server not found")
		}
		if err = s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		diagnostics := s.extensions.TestMCP(ctx, x.Name)
		if err = s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		result = map[string]any{"ok": len(diagnostics) == 0, "diagnostic_count": len(diagnostics)}
		if len(diagnostics) > 0 {
			result.(map[string]any)["message"] = "MCP connection or tool discovery failed. Check its settings and authorization."
		}
	case "oauth_required":
		var servers []extensions.MCPServerView
		servers, err = s.extensions.ListMCP()
		if err != nil {
			break
		}
		found := false
		for _, server := range servers {
			if server.Name == x.Name {
				found = true
			}
		}
		if !found {
			return "", tooloutcome.InvalidArguments("MCP server not found")
		}
		result = extensionOAuthRequired()
	case "skill_list":
		skills, diagnostics := s.extensions.SkillsForBot(ctx, r.BotID)
		result = map[string]any{"skills": skills, "diagnostic_count": len(diagnostics)}
	case "skill_install":
		files := make(map[string][]byte, len(x.Files))
		for name, text := range x.Files {
			files[name] = []byte(text)
		}
		if err = s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		err = s.extensions.InstallSkill(x.Name, files)
		mutated = err == nil
	case "skill_delete":
		err = s.deleteSkill(x.Name)
		mutated = err == nil
	default:
		return "", tooloutcome.InvalidArguments("unsupported extension action")
	}
	// Manager errors can contain file paths, malformed configuration or provider
	// details. Never forward those strings into model-visible tool results.
	if err != nil {
		if o, ok := tooloutcome.FromError(err); ok {
			o.Message = "Extension arguments were rejected before execution; check the name, endpoint, configuration or skill manifest in Settings."
			return "", o.Err()
		}
		return "", errors.New("extension operation failed; check the name, endpoint, configuration or skill manifest in Settings")
	}
	if mutated {
		_, syncErr := s.store.WorkspaceEvent(workspaceScopeConfig)
		result = map[string]any{"ok": true, "next_run": true, "sync_pending": syncErr != nil}
		if x.OAuth != nil {
			result.(map[string]any)["authorization"] = extensionOAuthRequired()
		}
		// Writes use existing synchronous manager APIs. Report an already committed
		// write truthfully even if cancellation arrives during its file operation.
	} else if err := s.extensionToolActive(ctx, c, r); err != nil {
		return "", err
	}
	return extensionToolJSON(result)
}

func extensionOAuthRequired() map[string]any {
	return map[string]any{"user_action_required": true, "message": "The user must complete OAuth authorization in Settings → Extensions. This tool does not access or return credentials."}
}
func extensionToolJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
func extensionPublicURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[redacted URL]"
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	if parsed.Fragment != "" {
		parsed.Fragment = "redacted"
	}
	return parsed.String()
}

func extensionRecoveryIdentity(raw json.RawMessage) tooloutcome.Identity {
	var x extensionToolArgs
	_ = json.Unmarshal(raw, &x)
	i := tooloutcome.OperationIdentity("workspace/extensions", x.Action, raw)
	switch x.Action {
	case "mcp_list", "skill_list", "oauth_required":
		i.Risk = tooloutcome.Observation
	case "mcp_create", "mcp_update", "mcp_delete":
		i.Risk = tooloutcome.TargetMutation
		i.Target = "mcp/" + x.Name
	case "skill_install", "skill_delete":
		i.Risk = tooloutcome.TargetMutation
		i.Target = "skill/" + x.Name
	}
	return i
}
