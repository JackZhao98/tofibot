package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/executor"
	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProgressReportPrompt is shared with the runtime's prompt budget accounting.
func ProgressReportPrompt(maxCalls int) string {
	if maxCalls <= 0 {
		return ""
	}
	return fmt.Sprintf("\n\nFor longer tasks, give a brief concrete public progress update before exceeding %d tool calls since the last one, then continue unfinished work. Private reasoning is not an update.\n", maxCalls)
}

// SkillTool represents an installed skill callable as a tool in the agent loop
type SkillTool struct {
	ID           string
	Name         string
	Description  string
	Instructions string
	Preload      bool                  // Inject the full body at session start; false = deferred (loaded on demand via tofi_load_skill)
	SkillDir     string                // Absolute path to skill directory on disk (empty if no scripts)
	BundledTools []ExtraBuiltinTool    // Tools that come with this skill — activated when skill is loaded
	DirectTools  []models.SkillToolDef // Direct tool definitions from manifest (skip sub-LLM, execute scripts directly)
}

// maxSkillCatalogDescChars caps each <available-skills> entry. The catalog is
// discovery-only — the full body loads on demand, so a long description just
// wastes turn-1 tokens.
const maxSkillCatalogDescChars = 250

// skillCatalogDesc collapses a skill description to a single budget-capped line
// for the <available-skills> catalog. Trims by rune so multibyte text is safe.
func skillCatalogDesc(desc string) string {
	desc = strings.TrimSpace(strings.ReplaceAll(desc, "\n", " "))
	r := []rune(desc)
	if len(r) > maxSkillCatalogDescChars {
		return string(r[:maxSkillCatalogDescChars-1]) + "…"
	}
	return desc
}

// ExtraBuiltinTool allows registering additional built-in tools with custom handlers.
// Deferred tools are hidden from the LLM until surfaced via tofi_tool_search —
// use this for tools that only matter in rare situations (disk cleanup, etc.)
// so they don't bloat the default context.
type ExtraBuiltinTool struct {
	Schema  provider.Tool
	Handler func(args map[string]interface{}) (string, error)
	// HandlerCtx is preferred for callers that need request cancellation. The
	// legacy Handler remains supported for existing integrations.
	HandlerCtx func(context.Context, map[string]interface{}) (string, error)
	Deferred   bool
	Hint       string // comma/space-separated keywords for tofi_tool_search
}

const maxDirectToolResultChars = 50000

// MaxStepsWithProgressReports is the hard loop guard for tasks that report
// progress between tool batches. Persistence must accept the same turn range.
const MaxStepsWithProgressReports = 300

// MaxAssistantTurnIndex includes the reserved receipt repair and final reply
// after the normal loop budget. Indices count all model turns, not just reports.
const MaxAssistantTurnIndex = MaxStepsWithProgressReports + 2

func progressReportReminder(limit int) string {
	return fmt.Sprintf("You have used %d tool calls since the last user-visible progress update. Briefly report a concrete finding, completed action, or blocker to the user before calling more tools. If the task is unfinished, continue working after that update; do not treat this as a request for a final answer.", limit)
}

func repeatedDiscoveryTool(name string) bool {
	switch name {
	case "list_computers", "list_mcp_servers", "search_mcp_tools", "list_skills", "tofi_tool_search", "workspace_capabilities":
		return true
	}
	return false
}

// AgentConfig holds the configuration required to run an autonomous agent
type AgentConfig struct {
	Ctx             context.Context   // Optional: cancellation context (nil = context.Background())
	Provider        provider.Provider // LLM provider (handles all API format differences)
	Model           string            // Model name (for context window, cost calculation)
	ReasoningEffort string            // Optional provider reasoning effort
	System          string
	Prompt          string
	Messages        []provider.Message                                     // Optional: full conversation history (overrides Prompt if non-empty)
	MCPServers      []MCPServerConfig                                      // Active MCP server connections
	SessionID       string                                                 // Session/task identifier for streaming callbacks
	SkillTools      []SkillTool                                            // Installed skills (deferred — loaded on-demand via tofi_load_skill)
	PreloadedSkills []string                                               // Skills to pre-activate at start (from previous turns in same session)
	ExtraTools      []ExtraBuiltinTool                                     // Core built-in tools (always available)
	SandboxDir      string                                                 // Sandbox directory for shell command execution (optional)
	DeferFileTools  bool                                                   // Hide file tools from the initial tool list; reachable via tofi_tool_search
	UserDir         string                                                 // User persistent directory for installed tools (optional)
	Executor        executor.Executor                                      // Sandbox executor (nil = use legacy functions)
	SecretEnv       map[string]string                                      // Extra env vars injected into sandbox commands (skill secrets)
	OnStreamChunk   func(sessionID, delta string)                          // Optional: called with each content delta during streaming
	OnThinkingChunk func(sessionID, delta string)                          // Optional: called with each reasoning/thinking delta during streaming
	OnToolCall      func(toolName, input, output string, durationMs int64) // Optional: called after each tool execution
	// OnAssistantTurn is called for each completed non-final assistant turn with
	// non-empty public content, before any associated tools are executed. The
	// content is sanitized for public delivery; the agent's internal message
	// remains unchanged.
	OnAssistantTurn   func(turnIndex int, content string)
	MaxContextTokens  int                                                       // 0 = auto-detect from model name
	OnContextCompact  func(summary string, originalTokens, compactedTokens int) // Optional: called when context is compacted
	OnProgress        func(status string, progress int, message string)         // Generic progress update
	OnStepStart       func(toolName, args string)                               // Generic step start
	OnStepDone        func(toolName, result string, durationMs int64)           // Generic step done
	LiveUsage         *provider.Usage                                           // Optional: updated in real-time during agent loop for tools to read
	OnContextEstimate func(estimatedInput int)                                  // Estimated input for the next model call, after pre-call compaction
	OnUsage           func(inputTokens, outputTokens int64)                     // Provider-reported usage for one model call
	OnCompact         func(originalTokens, compactedTokens int)                 // Successful in-run context compaction
	Hooks             *Hooks                                                    // Optional: pre/post hooks for tool calls, API calls, compaction
	OnMessage         func(msg provider.Message)                                // Optional: called immediately after each assistant/tool message is appended. Used for incremental chat session persistence so a browser refresh or hold doesn't lose in-progress turns. Internal synthetic user continuations are NOT emitted.
	// BeforeModelCall runs at the safe boundary immediately before a provider
	// request. Callers may use it to observe durable steering input without
	// interrupting an already-streaming response or an in-flight tool.
	BeforeModelCall func() error
	// BeforeFinalResponse optionally reviews cleaned, non-empty text-only final
	// content. An empty reminder accepts it; an error aborts. A non-empty reminder
	// requests one continuation with the existing history, without emitting the
	// internal reminder through OnMessage. The draft is promoted through
	// OnAssistantTurn once. Review is skipped after one repair or on cancellation.
	BeforeFinalResponse func(content string) (reminder string, err error)
	// FinalResponseRepairTools permits one reserved repair after normal run
	// budget exhaustion. That request exposes and executes only the named tools,
	// suitable for durable completion receipts that must not replay task effects.
	FinalResponseRepairTools []string
	// Per-run budget caps. Zero = no limit. When exceeded, the loop injects a
	// "wrap up now" directive and denies further tool calls, forcing the model
	// to produce a final answer with what it already has. Prevents a single
	// runaway "deep research" loop from burning through the user's daily quota.
	MaxRunCost     float64       // Optional: max real USD cost for this run (uses Tracker.TotalCost())
	MaxRunLLMCalls int           // Optional: max number of LLM API calls before forced wrap-up
	MaxRunDuration time.Duration // Optional: max wall-clock duration before forced wrap-up
	// A visible assistant turn resets this counter. Extra calls in one model
	// batch are returned as unexecuted tool results until the model reports.
	MaxToolCallsBetweenReports int
	// UserWaitDuration reports time spent in backend-owned human-input waits.
	// Only the sequential ToolsOnly runtime uses it; it never relaxes call/cost caps.
	UserWaitDuration func() time.Duration
	AskUserFn        func(question string, options []string) (string, error) // Optional: callback to ask user a question (Chat mode). Nil = tool not registered.
	IsSubAgent       bool                                                    // True when running as a sub-agent (prevents recursive spawning)
	// OnSubAgentEvent forwards live sub-agent activity (chunks, tool starts,
	// tool completions) up to the parent's emit channel. The sub-agent's
	// own loop wires its hooks through this so the parent UI can render a
	// live progress view inside the SubAgentRunCard. nil = no forwarding.
	OnSubAgentEvent func(eventType string, data map[string]interface{})
	// ToolsOnly prevents the loop from registering any built-in, skill, shell,
	// file, task, or MCP tools. Callers provide the complete tool surface via
	// ExtraTools. This is used by the product runtime adapter so backend policy
	// remains the authority for available tools.
	ToolsOnly bool
	// Continuation restores a previously suspended ToolsOnly loop. It is
	// agent-owned protocol state: callers must validate their outer run identity
	// before supplying it. ResumeResult is inserted only for its waiting tool.
	Continuation        *Continuation
	ResumeResult        string
	ResumeOutcome       *tooloutcome.Outcome
	ResolveToolIdentity func(name, arguments string) tooloutcome.Identity
}

type MCPServerConfig struct {
	Name          string
	Command       string            // stdio transport: executable to spawn (mutually exclusive with URL)
	Args          []string          // stdio transport args
	Env           map[string]string // stdio transport env
	URL           string            // remote transport: StreamableHTTP endpoint (e.g. https://agent.robinhood.com/mcp/trading)
	Headers       map[string]string // remote transport: static headers (e.g. Authorization)
	OAuth         *MCPOAuthConfig   // remote transport: OAuth 2.1 (overrides Headers auth)
	ToolAllowlist map[string]bool   // empty exposes all tools; otherwise only named tools reach the model
	ToolDenylist  map[string]bool   // named tools are hidden after any allowlist is applied
}

// MCPOAuthConfig carries an authorization handler for a remote MCP server.
// The official SDK refreshes tokens through this handler.
type MCPOAuthConfig struct {
	Handler auth.OAuthHandler
}

// AgentResult holds the result of an agent loop execution.
type AgentResult struct {
	Content         string
	TotalUsage      provider.Usage
	TotalCost       float64
	Model           string
	LLMCalls        int
	LoadedSkills    []string              // Skills that were loaded during this agent loop (for persistence)
	Messages        []provider.Message    // All new messages from this turn (assistant + tool calls + tool responses)
	ModelBreakdown  map[string]ModelUsage // Per-model token/cost breakdown
	Trace           *Trace                // Execution trace for observability (nil if not recorded)
	Suspended       bool                  // True when a tool requested durable human input.
	QuestionID      string                // Backend-owned question identity for Suspended results.
	Continuation    *Continuation         // Present only when Suspended is true.
	BudgetExhausted bool
	BudgetReason    string
}

// RunAgentLoop executes the autonomous agent loop (ReAct)
// It manages MCP clients, tools, and the LLM interaction loop.
func RunAgentLoop(cfg AgentConfig, ctx *models.ExecutionContext) (returned *AgentResult, returnedErr error) {
	budgetReason := ""
	defer func() {
		if returned != nil && !returned.Suspended && budgetReason != "" {
			returned.BudgetExhausted, returned.BudgetReason = true, budgetReason
		}
	}()
	if cfg.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	runCtx := cfg.Ctx
	if runCtx == nil {
		runCtx = context.Background()
	}

	// 1. Initialize MCP Clients
	var activeClients []*mcp.ClientSession
	var cleanups []func()

	// Cleanup all clients on exit
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	if !cfg.ToolsOnly {
		for _, serverCfg := range cfg.MCPServers {
			ctx.Log("[Agent] Connecting to MCP server: %s", serverCfg.Name)
			cli, cleanup, err := setupClient(runCtx, serverCfg, ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to MCP server '%s': %v", serverCfg.Name, err)
			}
			activeClients = append(activeClients, cli)
			cleanups = append(cleanups, cleanup)
		}
	}

	// 2. Handshake & List Tools from ALL clients
	var allTools []provider.Tool
	clientMap := make(map[string]*mcp.ClientSession) // Map tool name to client

	for i, cli := range activeClients {
		// Connect already completed the strict 2026-07-28 handshake. Visit every
		// tools/list page so larger servers do not silently lose tools.
		var listed []*mcp.Tool
		for tool, err := range cli.Tools(runCtx, nil) {
			if err != nil {
				return nil, fmt.Errorf("failed to list tools for server %d: %w", i, err)
			}
			listed = append(listed, tool)
		}
		converted := convertTools(listed)
		for _, t := range converted {
			allowlist := cfg.MCPServers[i].ToolAllowlist
			if len(allowlist) > 0 && !allowlist[t.Name] {
				continue
			}
			if cfg.MCPServers[i].ToolDenylist[t.Name] {
				continue
			}
			clientMap[t.Name] = cli
			allTools = append(allTools, t)
		}
	}

	// Add built-in 'wait' tool
	if cfg.ToolsOnly {
		// Alpha runtime callers explicitly own the complete tool surface.
	} else {
		allTools = append(allTools, provider.Tool{
			Name:        "tofi_wait",
			Description: "Wait for a specified number of seconds. Use this when waiting for page loads, animations, or dynamic content rendering.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"seconds": map[string]interface{}{
						"type":        "number",
						"description": "Number of seconds to wait (e.g., 2.5)",
					},
				},
				"required": []string{"seconds"},
			},
		})
	}

	// tofi_update_progress was removed — never wired to anything visible in
	// the web UI; the AI's narration in chunks already communicates progress.

	// Unified tool registry — replaces the old extraHandlers map
	registry := NewToolRegistry()
	if !cfg.ToolsOnly {
		visionTool := buildViewImageTool()
		registry.Register(visionTool)
		allTools = append(allTools, visionTool.Schema())
	}

	// Register core built-in tools. Deferred ones go into the registry
	// but NOT into allTools — they surface via tofi_tool_search only.
	for _, et := range cfg.ExtraTools {
		registry.Register(WrapExtraBuiltin(et))
		if !et.Deferred {
			allTools = append(allTools, et.Schema)
		}
	}

	// Track which skills have been loaded (persisted across turns via session)
	loadedSkills := make(map[string]bool)

	// preloadedInstructions: full bodies of preload:true / already-loaded skills,
	// injected under "## Active Skills". availableSkills: the name+description
	// catalog of deferred skills, injected under "<available-skills>" so the
	// model can discover and load them on demand.
	var preloadedInstructions []string
	var availableSkills []string

	// Register skill tools — deferred loading pattern (like Claude Code)
	// Skills are listed by name+description in <available-skills> section of system prompt.
	// Full Instructions loaded on-demand via tofi_load_skill tool.
	if !cfg.ToolsOnly && len(cfg.SkillTools) > 0 {
		// Build skill lookup map
		skillMap := make(map[string]*SkillTool)
		for i := range cfg.SkillTools {
			skillMap[cfg.SkillTools[i].Name] = &cfg.SkillTools[i]
		}

		// PreloadedSkills handling moved into the eager-load loop below — that
		// path also registers DirectTools (script-backed tools defined in
		// SKILL.md's `tools:` block). The previous Pre-activate loop only
		// registered BundledTools and then short-circuited tofi_load_skill,
		// so DirectTools like web_search / web_fetch never reached the model.

		// Skill loader closure — does all the side effects (registers bundled
		// tools, sets up symlinks, registers DirectTools, etc) and returns the
		// SKILL.md instructions so callers can decide where to surface them.
		// Used in two places:
		//   1. Eager preload at startup (this turn) → instructions go into the
		//      system prompt so the model sees them immediately.
		//   2. tofi_load_skill ExecuteFunc → kept as a backwards-compat no-op
		//      (returns "already active") since everything is preloaded now.
		loadSkillByName := func(_ context.Context, args map[string]interface{}) (string, error) {
			name := strings.TrimSpace(fmt.Sprintf("%v", args["name"]))
			skill, ok := skillMap[name]
			if !ok {
				// Try fuzzy match
				for k, v := range skillMap {
					if strings.EqualFold(k, name) || strings.Contains(strings.ToLower(k), strings.ToLower(name)) {
						skill = v
						name = k
						ok = true
						break
					}
				}
			}
			if !ok {
				var available []string
				for k, v := range skillMap {
					available = append(available, fmt.Sprintf("- %s: %s", k, v.Description))
				}
				return "Skill not found: " + name + "\n\nAvailable skills:\n" + strings.Join(available, "\n"), nil
			}

			// Already loaded — return short confirmation instead of full instructions
			if loadedSkills[name] {
				return fmt.Sprintf("Skill '%s' is already loaded. Its tools are available — use them directly.", name), nil
			}
			loadedSkills[name] = true

			// Activate bundled tools (if any)
			var activatedTools []string
			for _, bt := range skill.BundledTools {
				if !registry.Has(bt.Schema.Name) {
					registry.Register(WrapExtraBuiltin(bt))
					allTools = append(allTools, bt.Schema)
					activatedTools = append(activatedTools, bt.Schema.Name)
				}
			}

			// If skill has scripts, copy them into the sandbox + register tools.
			if skill.SkillDir != "" {
				// Make scripts available at skills/{name}/ inside the sandbox.
				// gVisor cannot follow symlinks to host paths outside /work, so
				// this must copy the files rather than symlink the host skill dir.
				if cfg.SandboxDir != "" {
					if err := copySkillToSandbox(cfg.SandboxDir, name, skill.SkillDir); err != nil {
						ctx.Log("[Skill:%s] Warning: failed to copy scripts into sandbox: %v", name, err)
					} else {
						ctx.Log("[Skill:%s] Copied scripts into sandbox: skills/%s/", name, name)
					}
				}

				if len(skill.DirectTools) > 0 {
					// Direct tool registration — each tool maps to a script, no sub-LLM needed
					for _, toolDef := range skill.DirectTools {
						// Skip if already registered
						alreadyRegistered := false
						for _, t := range allTools {
							if t.Name == toolDef.Name {
								alreadyRegistered = true
								break
							}
						}
						if alreadyRegistered {
							continue
						}

						// Build JSON Schema from params
						properties := map[string]interface{}{}
						var required []string
						for paramName, param := range toolDef.Params {
							prop := map[string]interface{}{
								"type":        param.Type,
								"description": param.Description,
							}
							if param.Default != nil {
								prop["default"] = param.Default
							}
							properties[paramName] = prop
							if param.Required {
								required = append(required, paramName)
							}
						}
						// This is consumed by the agent runtime, never passed to the script.
						// The model must choose its reading budget for every direct-tool call.
						properties["max_result_chars"] = map[string]interface{}{
							"type":        "integer",
							"minimum":     1,
							"maximum":     maxDirectToolResultChars,
							"description": fmt.Sprintf("Maximum characters to read from this result for this call (1-%d). Choose the smallest amount that can answer the task; use a larger value for long-form research.", maxDirectToolResultChars),
						}
						required = append(required, "max_result_chars")

						schema := provider.Tool{
							Name:        toolDef.Name,
							Description: toolDef.Description,
							Parameters: map[string]interface{}{
								"type":       "object",
								"properties": properties,
								"required":   required,
							},
						}

						// Capture for closure. In sandboxed execution, run scripts
						// through the sandbox-local skills/{name} symlink; host
						// absolute paths are not mounted inside gVisor.
						capturedScript := filepath.Join(skill.SkillDir, toolDef.Script)
						if cfg.SandboxDir != "" {
							capturedScript = filepath.Join("skills", name, toolDef.Script)
						}
						capturedName := toolDef.Name
						capturedParams := toolDef.Params

						allTools = append(allTools, schema)
						registry.Register(&FuncTool{
							ToolName:   capturedName,
							ToolSchema: schema,
							ExecuteFunc: func(_ context.Context, args map[string]interface{}) (string, error) {
								maxResultChars, err := directToolResultChars(args)
								if err != nil {
									return "", err
								}
								cmdParts := []string{"python3", shellQuote(capturedScript)}

								// First positional arg: "query" or "url"
								if q, ok := args["query"].(string); ok {
									cmdParts = append(cmdParts, shellQuote(q))
								} else if u, ok := args["url"].(string); ok {
									cmdParts = append(cmdParts, shellQuote(u))
								}

								// Named params as flags
								for paramName, paramDef := range capturedParams {
									if paramName == "query" || paramName == "url" || paramName == "max_result_chars" {
										continue
									}
									val, exists := args[paramName]
									if !exists {
										continue
									}
									flagName := strings.ReplaceAll(paramName, "_", "-")
									switch paramDef.Type {
									case "boolean":
										if b, ok := val.(bool); ok && b {
											cmdParts = append(cmdParts, "--"+flagName)
										}
									case "integer":
										if n, ok := val.(float64); ok {
											cmdParts = append(cmdParts, fmt.Sprintf("--%s", flagName), fmt.Sprintf("%d", int(n)))
										}
									default: // string
										if s, ok := val.(string); ok && s != "" {
											cmdParts = append(cmdParts, "--"+flagName, shellQuote(s))
										}
									}
								}
								if capturedName == "web_fetch" {
									if _, set := args["max_chars"]; !set {
										cmdParts = append(cmdParts, "--max-chars", fmt.Sprintf("%d", maxResultChars))
									}
								}

								cmd := strings.Join(cmdParts, " ")
								timeoutSec := classifyTimeout(cmd, 0)
								execInstance := cfg.Executor
								if execInstance == nil {
									execInstance = executor.NewExecutor("")
								}
								output, execErr := execInstance.Execute(
									runCtx,
									cfg.SandboxDir,
									cfg.UserDir,
									cmd,
									timeoutSec,
									cfg.SecretEnv,
								)
								result := ShellResult{Stdout: output}
								if execErr != nil {
									result.Stderr = execErr.Error()
									result.ExitCode = 1
								}
								result.Interpretation = interpretExitCode(cmd, result.ExitCode)
								return smartTruncate(result.FormatForAgent(), maxResultChars), nil
							},
						})
						activatedTools = append(activatedTools, capturedName)
						ctx.Log("[Skill:%s] Registered direct tool: %s → %s", name, capturedName, capturedScript)
					}
				} else {
					// Fallback: register run_skill__ for skills without direct tools
					runToolName := "run_skill__" + sanitizeToolName(name)
					alreadyRegistered := false
					for _, t := range allTools {
						if t.Name == runToolName {
							alreadyRegistered = true
							break
						}
					}
					if !alreadyRegistered {
						allTools = append(allTools, provider.Tool{
							Name:        runToolName,
							Description: fmt.Sprintf("Execute the '%s' skill: %s", name, skill.Description),
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"input": map[string]interface{}{
										"type":        "string",
										"description": "The input/request to send to this skill",
									},
								},
								"required": []string{"input"},
							},
						})
						activatedTools = append(activatedTools, runToolName)
					}
				}
			}

			// Replace relative script paths with absolute paths so AI doesn't need to guess
			instructions := skill.Instructions
			if skill.SkillDir != "" {
				// Only replace the relative prefix "skills/{name}/" — single pass to avoid double-replace
				relativePrefix := "skills/" + name + "/"
				absolutePrefix := skill.SkillDir + "/"
				instructions = strings.ReplaceAll(instructions, relativePrefix, absolutePrefix)
			}

			result := fmt.Sprintf("# Skill: %s\n\n%s", name, instructions)
			if len(activatedTools) > 0 {
				result += fmt.Sprintf("\n\n---\nActivated tools: %s\nThese tools are now callable.", strings.Join(activatedTools, ", "))
			}
			return result, nil
		}

		// Register tofi_load_skill — the real loader. A deferred skill's full
		// SKILL.md body (and any bundled tools) is pulled in only when the model
		// calls this, having decided from the skill's catalog description that it
		// is relevant to the task.
		registry.Register(&FuncTool{
			ToolName:        "tofi_load_skill",
			ToolDisplayName: "Load Skill",
			ToolSchema: provider.Tool{
				Name: "tofi_load_skill",
				Description: "Load a skill's full instructions and tools when its description in <available-skills> matches your task. " +
					"Once loaded, its guidance and tools stay available for the rest of the session.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Skill name (e.g. 'market-sense')",
						},
					},
					"required": []string{"name"},
				},
			},
			ExecuteFunc: loadSkillByName,
		})

		// Progressive disclosure: preload only skills flagged preload:true (their
		// tools must be hot on turn 1) plus skills already loaded earlier in this
		// session (cfg.PreloadedSkills). Every other skill stays deferred — it
		// appears in the <available-skills> catalog (name + description) and its
		// body is pulled in on demand via tofi_load_skill.
		preloadSet := make(map[string]bool)
		for _, name := range cfg.PreloadedSkills {
			preloadSet[strings.TrimSpace(name)] = true
		}
		for skillName, st := range skillMap {
			if st.Preload || preloadSet[skillName] {
				result, err := loadSkillByName(runCtx, map[string]interface{}{"name": skillName})
				if err == nil && result != "" {
					preloadedInstructions = append(preloadedInstructions, result)
				}
			} else {
				availableSkills = append(availableSkills, fmt.Sprintf("- %s: %s", skillName, skillCatalogDesc(st.Description)))
			}
		}
		// Sort for stable system-prompt ordering (matters for prompt cache).
		sort.Strings(preloadedInstructions)
		sort.Strings(availableSkills)

		// Declare tofi_load_skill to the model only when there is a deferred
		// skill to load — the <available-skills> catalog tells the model to call
		// it, so it must be in the tool list, not just the registry.
		if len(availableSkills) > 0 {
			if ls := registry.Get("tofi_load_skill"); ls != nil {
				allTools = append(allTools, ls.Schema())
			}
		}

		// Register tofi_tool_search (searches all deferred tools including skills)
		registry.Register(buildToolSearchTool(registry))
	}

	// Register tofi_shell + file tools (if sandbox is configured)
	if !cfg.ToolsOnly && cfg.SandboxDir != "" {
		allTools = append(allTools, provider.Tool{
			Name: "tofi_shell",
			Description: "Execute a shell command in an isolated sandbox directory (macOS). " +
				"Use this to run python3, node, npx, curl, git clone, etc. " +
				"Install packages with 'python3 -m pip install <pkg>' (NEVER bare 'pip'). " +
				"For multi-line Python use heredoc: python3 <<'PYEOF'\\n...\\nPYEOF. " +
				"The sandbox is isolated — packages persist across tasks.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute (e.g., 'npx create-react-app myapp', 'uv run script.py')",
					},
					"timeout": map[string]interface{}{
						"type":        "number",
						"description": "Timeout in seconds (default: 60, max: 120)",
					},
				},
				"required": []string{"command"},
			},
		})

		// Register file tools. When deferred (trading-desk sessions), they
		// stay out of the model's tool list until surfaced on demand via
		// tofi_tool_search — e.g. to handle a user-uploaded file.
		for _, ft := range buildFileTools(cfg.SandboxDir, cfg.DeferFileTools) {
			registry.Register(ft)
			if !ft.Deferred() {
				allTools = append(allTools, ft.Schema())
			}
		}
	}

	// tofi_tool_search must be registered AND declared whenever any deferred
	// tool exists — without it deferred tools are unreachable. It used to be
	// registered only alongside skills, and its schema was never declared to
	// the provider at all.
	if !cfg.ToolsOnly && registry.Get("tofi_tool_search") == nil && len(registry.DeferredTools()) > 0 {
		registry.Register(buildToolSearchTool(registry))
	}
	if !cfg.ToolsOnly {
		if ts := registry.Get("tofi_tool_search"); ts != nil {
			allTools = append(allTools, ts.Schema())
		}
	}

	// Validate all tools before use
	allTools = validateTools(allTools)
	declaredTools := make(map[string]bool, len(allTools))
	for _, t := range allTools {
		declaredTools[t.Name] = true
	}

	// Log all registered tool names for debugging
	var toolNames []string
	for _, t := range allTools {
		toolNames = append(toolNames, t.Name)
	}
	ctx.Log("[Agent] Registered %d tools: %s", len(allTools), strings.Join(toolNames, ", "))
	ctx.Log("[Agent] Tools: %d core, %d skills (deferred)", len(cfg.ExtraTools), len(cfg.SkillTools))

	// 3. Prepare System Prompt
	if cfg.System == "" {
		cfg.System = "You are an autonomous intelligent agent."
	}
	systemPrompt := cfg.System
	systemPrompt += ProgressReportPrompt(cfg.MaxToolCallsBetweenReports)

	// Preloaded skills (preload:true or loaded earlier this session): full body
	// injected so their tools are callable immediately.
	if len(preloadedInstructions) > 0 {
		systemPrompt += "\n\n## Active Skills\n\nThese skills are loaded and ready. Their tools are already callable — just use them directly.\n\n"
		systemPrompt += strings.Join(preloadedInstructions, "\n\n---\n\n")
		systemPrompt += "\n\n"
	}

	// Deferred skills: only their name + description, so the model can decide to
	// load the full body on demand via tofi_load_skill.
	if len(availableSkills) > 0 {
		systemPrompt += "\n\n<available-skills>\nThese skills are available but not yet loaded. When a skill's description matches your task, call tofi_load_skill with its name to pull in its full instructions and tools.\n\n"
		systemPrompt += strings.Join(availableSkills, "\n")
		systemPrompt += "\n</available-skills>\n\n"
	}

	// Tool usage rules apply whenever shell + skill tools are in play.
	if len(cfg.SkillTools) > 0 {
		systemPrompt += `
## Tool Usage Rules
- Do not fetch or scrape web pages with tofi_shell. Use an available web-fetch skill or another authorized web tool.
- tofi_shell output is smart-truncated (head + tail preserved). Install/build commands get extended timeout (5min). Long commands auto-background after 15s.
- A URL written in text is not visual input. When the user asks you to inspect, interpret, compare, or analyze an image at a URL, call tofi_view_image before answering.
`
	}

	// appendAndEmit appends a message to the slice and fires OnMessage if set.
	// Used for every assistant/tool message so downstream (e.g. chat session
	// persistence) can checkpoint incrementally instead of waiting for the
	// full loop to return. Synthetic user continuations bypass this and use
	// a plain append to avoid polluting the persisted conversation.
	assistantTurnIndex := 0
	toolCallsSinceReport := 0
	reportRequired := false
	seenDiscoveryResults := map[[32]byte]bool{}
	toolArgsByCallID := map[string]string{}
	cycleSawTool, cycleSawNovel, cycleOnlyDiscovery := false, false, true
	stalledDiscoveryCycles := 0
	stalledDiscoveryStop := false
	var recoveryLedger []ToolRecoveryRecord
	recoveryCalls := map[string]provider.ToolCall{}
	recoveryIdentities := map[string]tooloutcome.Identity{}
	recoveryEvidence := map[string][]tooloutcome.Identity{}
	recoveryBlocked := map[string]bool{}
	resolveIdentity := func(name, args string) tooloutcome.Identity {
		if cfg.ResolveToolIdentity != nil {
			return cfg.ResolveToolIdentity(name, args)
		}
		return tooloutcome.DefaultIdentity(name, json.RawMessage(args))
	}
	if cfg.Continuation != nil {
		assistantTurnIndex = cfg.Continuation.AssistantTurnIndex
		toolCallsSinceReport = cfg.Continuation.ToolCallsSinceReport
		reportRequired = cfg.Continuation.ReportRequired || cfg.MaxToolCallsBetweenReports > 0 && toolCallsSinceReport >= cfg.MaxToolCallsBetweenReports
	}
	notifyAssistantTurn := func(msg provider.Message, nonFinal bool) {
		if msg.Role == "assistant" {
			assistantTurnIndex++
			if nonFinal && cfg.OnAssistantTurn != nil {
				// Do not expose model reasoning tags to the public callback. Keep
				// msg.Content unchanged for the provider-facing transcript.
				if content := strings.TrimSpace(stripThinkTags(msg.Content)); content != "" {
					cfg.OnAssistantTurn(assistantTurnIndex, content)
					toolCallsSinceReport = 0
					reportRequired = false
					cycleSawTool, cycleSawNovel, cycleOnlyDiscovery = false, false, true
				}
			}
		}
	}
	appendAndEmit := func(list []provider.Message, msg provider.Message) []provider.Message {
		if cfg.ToolsOnly {
			for _, call := range msg.ToolCalls {
				recoveryCalls[call.ID] = call
			}
			if msg.Role == "tool" {
				if o := msg.ToolOutcome; o != nil && recoveryStatus(o.Status) && !recoveryBlocked[msg.ToolCallID] {
					if call, ok := recoveryCalls[msg.ToolCallID]; ok {
						identity, ok := recoveryIdentities[call.ID]
						if !ok {
							identity = resolveIdentity(call.Name, call.Arguments)
						}
						var evidence []tooloutcome.Identity
						if observed := recoveryEvidence[call.ID]; len(observed) > 1 {
							evidence = append(evidence, observed[1:]...)
						}
						recoveryLedger = append(recoveryLedger, ToolRecoveryRecord{Call: call, Outcome: *o, Identity: &identity, Evidence: evidence})
					}
				}
			}
		}
		if msg.Role == "tool" && cfg.MaxToolCallsBetweenReports > 0 && !strings.HasPrefix(msg.Content, "Tool not executed:") {
			if args, ok := toolArgsByCallID[msg.ToolCallID]; ok {
				cycleSawTool = true
				if repeatedDiscoveryTool(msg.ToolName) {
					fingerprint := sha256.Sum256([]byte(msg.ToolName + "\x00" + args + "\x00" + msg.Content))
					if !seenDiscoveryResults[fingerprint] {
						seenDiscoveryResults[fingerprint] = true
						cycleSawNovel = true
					}
				} else {
					cycleOnlyDiscovery = false
				}
			}
		}
		notifyAssistantTurn(msg, len(msg.ToolCalls) > 0)
		if cfg.OnMessage != nil {
			cfg.OnMessage(msg)
		}
		return append(list, msg)
	}

	// Build messages. A continuation carries the exact provider transcript up
	// through the unresolved waiting tool. Only the answered tool result and
	// explicit stale-call skips are appended before asking the model again.
	var messages []provider.Message
	if cfg.Continuation != nil {
		var err error
		messages, err = resumeContinuation(cfg.Continuation, cfg.ResumeResult, cfg.ResumeOutcome)
		if err != nil {
			return nil, fmt.Errorf("resume continuation: %w", err)
		}
		if cfg.OnMessage != nil {
			for _, msg := range messages[len(cfg.Continuation.Messages):] {
				cfg.OnMessage(msg)
			}
		}
		if reportRequired {
			messages = append(messages, provider.Message{Role: "user", Content: progressReportReminder(cfg.MaxToolCallsBetweenReports)})
		}
	} else if len(cfg.Messages) > 0 {
		messages = make([]provider.Message, len(cfg.Messages))
		copy(messages, cfg.Messages)
	} else {
		messages = []provider.Message{
			{Role: "user", Content: cfg.Prompt},
		}
	}

	if cfg.ToolsOnly {
		if cfg.Continuation != nil && cfg.Continuation.ToolRecovery != nil {
			recoveryLedger = append([]ToolRecoveryRecord(nil), cfg.Continuation.ToolRecovery...)
		} else {
			recoveryLedger = toolRecoveryRecords(messages)
		}
		for i := range recoveryLedger {
			if recoveryLedger[i].Identity == nil {
				identity := resolveIdentity(recoveryLedger[i].Call.Name, recoveryLedger[i].Call.Arguments)
				recoveryLedger[i].Identity = &identity
			}
		}
		for _, msg := range messages {
			for _, call := range msg.ToolCalls {
				recoveryCalls[call.ID] = call
			}
		}
		if cfg.Continuation != nil {
			for _, msg := range messages[len(cfg.Continuation.Messages):] {
				if msg.ToolOutcome != nil && recoveryStatus(msg.ToolOutcome.Status) {
					if call, ok := recoveryCalls[msg.ToolCallID]; ok {
						identity := resolveIdentity(call.Name, call.Arguments)
						recoveryLedger = append(recoveryLedger, ToolRecoveryRecord{Call: call, Identity: &identity, Outcome: *msg.ToolOutcome})
					}
				}
			}
		}

	}

	// 4. Start Loop — AgentState drives the entire execution
	loopCtx := runCtx
	maxSteps := 30
	if cfg.MaxToolCallsBetweenReports > 0 {
		maxSteps = MaxStepsWithProgressReports
	}
	emptyResponseStreak := 0

	state := NewAgentState(systemPrompt, messages, loadedSkills, cfg.Model)

	// Transcript for crash recovery
	// Alpha ToolsOnly runs let the backend own durable history. Do not create
	// the legacy ~/.tofi transcript as an implicit side effect.
	if cfg.SessionID != "" && !cfg.ToolsOnly {
		if t, err := NewTranscript(cfg.SessionID, cfg.UserDir); err == nil {
			state = state.WithTranscript(t)
			defer func() {
				if state.Transcript != nil {
					state.Transcript.Clean()
				}
			}()
		}
	}

	// Background task manager for auto-backgrounding long shell commands
	bgManager := NewBackgroundTaskManager()

	// Register task management tools (task_status + ask_user)
	if !cfg.ToolsOnly {
		for _, tt := range buildTaskTools(bgManager, cfg.AskUserFn) {
			registry.Register(tt)
			allTools = append(allTools, tt.Schema())
		}
	}

	// tofi_sub_agent was removed 2026-07-08: persistent child threads +
	// parallel fan-out (tofi_open_thread / tofi_dispatch_threads, server
	// layer) replaced the ephemeral in-loop sub-agent.

	// Per-run budget tracking. Once any budget is crossed we flip
	// budgetWrapUp=true, inject a "wrap up now" user directive, and disallow
	// further tool calls. If the model still tries to call tools on the next
	// iteration we force-terminate with whatever text content we have.
	runStart := time.Now()
	budgetWrapUp := false
	finalResponseRepaired := false
	finalRepairPending := false
	finalRepairReserved := false
	finalRepairFinalPending := false
	if cfg.Continuation != nil {
		runStart = runStart.Add(-time.Duration(cfg.Continuation.ActiveElapsedNanos))
		budgetWrapUp = cfg.Continuation.BudgetWrapUp
		if budgetWrapUp {
			budgetReason = "active run budget exhausted before suspension"
		}
		finalResponseRepaired = cfg.Continuation.FinalResponseRepaired
		finalRepairPending = cfg.Continuation.FinalRepairPending
		finalRepairReserved = cfg.Continuation.FinalRepairReserved
		finalRepairFinalPending = cfg.Continuation.FinalRepairFinalPending
		state.TotalUsage = cfg.Continuation.TotalUsage
		state.LLMCalls = cfg.Continuation.LLMCalls
		state.Step = cfg.Continuation.Step
		state.Tracker.RestoreModelBreakdown(cfg.Continuation.ModelUsage)
	}

	finalRepairToolNames := make(map[string]bool, len(cfg.FinalResponseRepairTools))
	for _, name := range cfg.FinalResponseRepairTools {
		if declaredTools[name] {
			finalRepairToolNames[name] = true
		}
	}

	for !state.Phase.IsTerminal() {
		if cfg.BeforeModelCall != nil {
			if err := cfg.BeforeModelCall(); err != nil {
				if loopCtx.Err() != nil {
					state = state.WithCancelled()
					return state.ToResult(cfg.Model), nil
				}
				return nil, fmt.Errorf("before model call: %w", err)
			}
		}
		repairRequest := finalRepairPending && finalRepairReserved
		finalRepairFinalRequest := finalRepairFinalPending
		if finalRepairPending && !finalRepairReserved && loopCtx.Err() == nil {
			// Check before context preparation too: compaction may call the model.
			if exceeded, reason := checkRunBudget(&cfg, state.LLMCalls, state.Tracker.TotalCost(), runStart); exceeded {
				return nil, fmt.Errorf("final response repair budget exhausted: %s", reason)
			}
		}
		// Deferred tools activated via tofi_tool_search in a previous
		// iteration must be declared to the provider, otherwise the model
		// is told "these tools are now available" but physically cannot
		// call them (providers restrict calls to declared tools).
		for _, sch := range registry.ActivatedDeferredSchemas() {
			if !declaredTools[sch.Name] {
				allTools = append(allTools, sch)
				declaredTools[sch.Name] = true
			}
		}

		state.Step++
		if state.Step > maxSteps && !(repairRequest && state.Step == maxSteps+1) && !(finalRepairFinalRequest && state.Step == maxSteps+2) {
			state = state.WithError(fmt.Errorf("maximum agent steps exceeded without a final response"))
			break
		}

		// Check for cancellation before starting a new LLM call
		if loopCtx.Err() != nil {
			ctx.Log("[Agent] Cancelled by client.")
			state = state.WithCancelled()
			break
		}

		state = state.WithPhase(PhaseThinking)
		state.Trace.RecordPhaseChange(state.Step, PhaseInit, PhaseThinking)

		// Extract messages from state for this iteration (written back at end of loop)
		messages := state.Messages

		// Micro-compact: trim old tool results that LLM has already consumed
		if len(messages) > 8 {
			messages = microCompact(messages, 6)
		}

		// Pre-call context budget check — compact proactively before hitting the limit
		estimatedInput := EstimateContextUsage(systemPrompt, messages, allTools)
		if cfg.OnContextEstimate != nil {
			cfg.OnContextEstimate(estimatedInput)
		}
		if !repairRequest && !finalRepairFinalRequest && state.Tracker.ShouldCompact(estimatedInput, 0.80) && len(messages) > 4 {
			ctx.Log("[Agent] Pre-call compaction triggered: estimated %d tokens > 80%% of %d window", estimatedInput, state.Tracker.ContextWindow())
			cfg.Hooks.callPreCompact(len(messages), estimatedInput)
			originalTokens := estimatedInput
			originalCount := len(messages)
			summary, compactErr := compactMessages(loopCtx, cfg.Provider, cfg.Model, cfg.ReasoningEffort, messages)
			if compactErr != nil {
				ctx.Log("[Agent] Pre-call compaction failed: %v", compactErr)
			} else {
				messages = compactAndRebuild(messages, summary)
				// Reset InitialMsgCount so NewMessages() tracks only post-compaction additions
				state = state.WithCompactedMessages(messages)
				compactedTokens := EstimateContextUsage(systemPrompt, messages, allTools)
				estimatedInput = compactedTokens
				cfg.Hooks.callPostCompact(originalCount, len(messages), originalTokens, compactedTokens)
				if cfg.OnCompact != nil {
					cfg.OnCompact(originalTokens, compactedTokens)
				}
				if cfg.OnContextEstimate != nil {
					cfg.OnContextEstimate(compactedTokens)
				}
				ctx.Log("[Agent] Pre-call compacted to %d messages (~%d tokens)", len(messages), compactedTokens)
			}
		}

		// Pre-API hook
		if err := cfg.Hooks.callPreAPICall(state.Step, len(messages), estimatedInput); err != nil {
			ctx.Log("[Agent] PreAPICall hook blocked: %v", err)
			return nil, fmt.Errorf("pre-API hook: %w", err)
		}
		// Ordinary repairs get no extra call/time/cost allowance. A caller may
		// reserve one repair that exposes only a narrow receipt tool; recheck the
		// ordinary path after callbacks and context preparation, and never start
		// either request after cancellation.
		if finalRepairPending {
			if loopCtx.Err() != nil {
				state = state.WithCancelled()
				return state.ToResult(cfg.Model), nil
			}
			if !finalRepairReserved {
				if exceeded, reason := checkRunBudget(&cfg, state.LLMCalls, state.Tracker.TotalCost(), runStart); exceeded {
					return nil, fmt.Errorf("final response repair budget exhausted: %s", reason)
				}
			}
			finalRepairPending = false
		}
		if finalRepairFinalPending {
			finalRepairFinalPending = false
		}

		// Checkpoint before API call (crash recovery)
		if state.Transcript != nil {
			state.Transcript.Checkpoint(state.Step, PhaseThinking, messages, state.TotalUsage, state.LLMCalls)
		}

		requestTools := allTools
		if repairRequest {
			requestTools = make([]provider.Tool, 0, len(finalRepairToolNames))
			for _, tool := range allTools {
				if finalRepairToolNames[tool.Name] {
					requestTools = append(requestTools, tool)
				}
			}
		} else if finalRepairFinalRequest {
			requestTools = nil
		}
		req := &provider.ChatRequest{
			Model:           cfg.Model,
			ReasoningEffort: cfg.ReasoningEffort,
			System:          systemPrompt,
			Messages:        messages,
			Tools:           requestTools,
		}

		apiStart := time.Now()
		var resp *provider.ChatResponse
		var err error

		if cfg.OnStreamChunk != nil {
			// Streaming mode — wrap callback to filter out <think> blocks
			firstThinkTag := true
			filter := &thinkStreamFilter{
				forward: func(delta string) {
					cfg.OnStreamChunk(cfg.SessionID, delta)
				},
				onThinking: func(delta string) {
					if firstThinkTag {
						ctx.Log("[Agent] Received <think> tag stream")
						firstThinkTag = false
					}
					if cfg.OnThinkingChunk != nil {
						cfg.OnThinkingChunk(cfg.SessionID, delta)
					}
				},
			}
			firstReasoning := true
			resp, err = cfg.Provider.ChatStream(loopCtx, req, func(delta provider.StreamDelta) {
				if delta.Content != "" {
					filter.Write(delta.Content)
				}
				if delta.Reasoning != "" {
					if firstReasoning {
						ctx.Log("[Agent] Received reasoning/thinking stream")
						firstReasoning = false
					}
					if cfg.OnThinkingChunk != nil {
						cfg.OnThinkingChunk(cfg.SessionID, delta.Reasoning)
					}
				}
			})
		} else {
			// Non-streaming mode
			resp, err = cfg.Provider.Chat(loopCtx, req)
		}

		if err != nil {
			// If cancelled by client (ESC), return partial results instead of error
			if loopCtx.Err() != nil {
				ctx.Log("[Agent] Cancelled by client.")
				lastContent := ""
				if resp != nil {
					lastContent = resp.Content
					state = state.RecordAPICall(cfg.Model, resp.Usage)
					if cfg.OnUsage != nil {
						cfg.OnUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens)
					}
				}
				state = state.WithMessages(messages)
				state = state.WithCancelled()
				state.Result = lastContent
				return state.ToResult(cfg.Model), nil
			}
			state.Trace.RecordError(state.Step, err)
			state = state.WithMessages(messages)
			state = state.WithError(err)
			return nil, fmt.Errorf("LLM call failed: %v", err)
		}

		apiDuration := time.Since(apiStart)
		state = state.RecordAPICall(cfg.Model, resp.Usage)
		if cfg.OnUsage != nil {
			cfg.OnUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens)
		}
		state.Trace.RecordAPICall(state.Step, cfg.Model, resp.Usage, resp, apiDuration)
		cfg.Hooks.callPostAPICall(state.Step, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.HasToolCalls())
		if cfg.LiveUsage != nil {
			*cfg.LiveUsage = state.TotalUsage
		}
		if stalledDiscoveryStop {
			finalContent := strings.TrimSpace(stripThinkTags(resp.Content))
			if resp.HasToolCalls() || finalContent == "" {
				finalContent = "I stopped because two consecutive rounds of tool-directory queries returned no new information. Please clarify what to try next or let me use another source."
			}
			messages = appendAndEmit(messages, provider.Message{Role: "assistant", Content: finalContent})
			state = state.WithMessages(messages).WithResult(finalContent)
			return state.ToResult(cfg.Model), nil
		}
		if finalRepairFinalRequest && !resp.HasToolCalls() && stripThinkTags(resp.Content) == "" {
			return nil, errors.New("final receipt repair returned no final answer")
		}
		if !resp.HasToolCalls() && stripThinkTags(resp.Content) == "" {
			emptyResponseStreak++
			if cfg.MaxToolCallsBetweenReports > 0 && emptyResponseStreak >= 5 {
				return nil, errors.New("model produced five consecutive empty responses")
			}
		} else {
			emptyResponseStreak = 0
		}
		if reportRequired && strings.TrimSpace(stripThinkTags(resp.Content)) == "" {
			// Progress is a presentation concern, not execution authorization.
			// A model's missing narration must not discard useful tool calls or
			// consume three extra model turns. Runtime activity remains visible.
			if cfg.OnProgress != nil {
				cfg.OnProgress("running", 0, "Work is continuing; completed tool results are available in the activity history.")
			}
			reportRequired = false
			toolCallsSinceReport = 0
			cycleSawTool, cycleSawNovel, cycleOnlyDiscovery = false, false, true
		}

		// Per-run budget guard. After each LLM call we check whether this
		// specific run has crossed any of its caps. The first time it does,
		// we flip budgetWrapUp, strip any pending tool calls from the
		// assistant response, and inject a user directive instructing the
		// model to produce a final answer now. If the next LLM call STILL
		// returns tool calls or no visible answer, we force-terminate
		// with whatever text content is available — no more tool execution,
		// no more LLM calls — so one runaway research loop can't burn through
		// the user's day.
		// Empty or reasoning-only responses also need another model call,
		// so they must honor the same budget as tool continuations. If the
		// model already produced a visible tool-call-free final answer, let
		// the normal termination path handle it — even over budget, accepting the final
		// answer is the friendliest outcome.
		if finalRepairFinalRequest && resp.HasToolCalls() {
			return nil, errors.New("final receipt repair must end without additional tools")
		}
		if !repairRequest && (resp.HasToolCalls() || stripThinkTags(resp.Content) == "") {
			if exceeded, reason := checkRunBudget(&cfg, state.LLMCalls, state.Tracker.TotalCost(), runStart); exceeded {
				if !budgetWrapUp {
					budgetReason = reason
					budgetWrapUp = true
					ctx.Log("[Agent] Per-run budget exceeded (%s) — injecting wrap-up directive", reason)
					// Keep the assistant text but drop tool calls, so the
					// history is consistent (no dangling tool_call without
					// a matching tool result).
					// The budget wrap-up response is a completed non-final
					// assistant turn even though its tool calls are deliberately
					// discarded before the next model request.
					wrapUpMessage := provider.Message{
						Role:    "assistant",
						Content: resp.Content,
					}
					notifyAssistantTurn(wrapUpMessage, true)
					if cfg.OnMessage != nil {
						cfg.OnMessage(wrapUpMessage)
					}
					messages = append(messages, wrapUpMessage)
					// Synthetic user directive — intentionally NOT emitted
					// via OnMessage so it doesn't clutter the persisted
					// session with system bookkeeping the user never typed.
					messages = append(messages, provider.Message{
						Role:    "user",
						Content: fmt.Sprintf("You have reached this run's budget (%s). Do not call any more tools. Provide your best final answer now using only the information you already have.", reason),
					})
					// Persist the appends into state BEFORE continuing so
					// the next iteration's `messages := state.Messages`
					// reload doesn't lose what we just wrote.
					state = state.WithMessages(messages)
					continue
				}
				// Already in wrap-up mode without a visible final answer.
				// Force termination to honor the cap.
				ctx.Log("[Agent] Model ignored wrap-up directive — forcing termination")
				finalContent := stripThinkTags(resp.Content)
				if finalContent == "" {
					finalContent = fmt.Sprintf("[Run stopped — %s. Partial results only; no final answer produced.]", reason)
				}
				messages = appendAndEmit(messages, provider.Message{
					Role:    "assistant",
					Content: finalContent,
				})
				state = state.WithMessages(messages)
				state = state.WithResult(finalContent)
				return state.ToResult(cfg.Model), nil
			}
		}
		// Append Assistant Message
		assistantMsg := provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		toolArgsByCallID = make(map[string]string, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			toolArgsByCallID[call.ID] = call.Arguments
		}
		messages = appendAndEmit(messages, assistantMsg)
		if repairRequest && resp.HasToolCalls() {
			// The reserved request may execute one receipt tool, followed by one
			// tool-free final response. Preserve that final-only state across a
			// suspension without granting another tool round.
			finalRepairFinalPending = true
		}

		// Log Thinking
		if resp.Reasoning != "" {
			ctx.Log("<think>\n%s\n</think>", resp.Reasoning)
		}
		if resp.Content != "" {
			ctx.Log("<think>\n%s\n</think>", resp.Content)
		}

		// Check for Termination
		if !resp.HasToolCalls() {
			// Strip <think> tags — if the model only returned thinking, it's not a real answer
			cleanContent := stripThinkTags(resp.Content)

			if cleanContent != "" {
				if cfg.BeforeFinalResponse != nil && !finalResponseRepaired {
					if loopCtx.Err() != nil {
						state = state.WithMessages(messages).WithCancelled()
						return state.ToResult(cfg.Model), nil
					}
					exceeded, _ := checkRunBudget(&cfg, state.LLMCalls, state.Tracker.TotalCost(), runStart)
					canUseNormalRepair := !budgetWrapUp && !exceeded && state.Step < maxSteps
					reminder, err := cfg.BeforeFinalResponse(cleanContent)
					if err != nil {
						return nil, fmt.Errorf("before final response: %w", err)
					}
					if loopCtx.Err() != nil {
						state = state.WithMessages(messages).WithCancelled()
						return state.ToResult(cfg.Model), nil
					}
					// The callback itself can consume the remaining time budget.
					exceeded, _ = checkRunBudget(&cfg, state.LLMCalls, state.Tracker.TotalCost(), runStart)
					canUseNormalRepair = canUseNormalRepair && !exceeded
					canUseReservedRepair := !canUseNormalRepair && len(finalRepairToolNames) > 0
					if strings.TrimSpace(reminder) != "" && (canUseNormalRepair || canUseReservedRepair) {
						finalResponseRepaired = true
						finalRepairReserved = canUseReservedRepair
						// appendAndEmit already counted and emitted this assistant
						// message. Promote the same turn; do not count or emit it twice.
						if cfg.OnAssistantTurn != nil {
							cfg.OnAssistantTurn(assistantTurnIndex, cleanContent)
						}
						if loopCtx.Err() != nil {
							state = state.WithMessages(messages).WithCancelled()
							return state.ToResult(cfg.Model), nil
						}
						messages = append(messages, provider.Message{Role: "user", Content: reminder})
						state = state.WithMessages(messages)
						finalRepairPending = true
						continue
					}
				}
				ctx.Log("[Agent] Finished.")
				state = state.WithMessages(messages)
				state = state.WithResult(cleanContent)
				return state.ToResult(cfg.Model), nil
			}

			// Empty, whitespace-only and reasoning-only responses need a continuation.
			ctx.Log("[Agent] Model returned no visible answer, prompting to continue...")
			messages = append(messages, provider.Message{
				Role:    "user",
				Content: "Please continue. Use the available tools to get the information needed, then provide your answer.",
			})
			// The next iteration reloads state.Messages; retain both the response
			// and the recovery prompt instead of repeating the original request.
			state = state.WithMessages(messages)
			continue
		}

		// Execute Tools — try parallel path for concurrency-safe batches
		if !repairRequest && !cfg.ToolsOnly && canExecuteInParallel(resp.ToolCalls) {
			ctx.Log("[Agent] Executing %d tools in parallel", len(resp.ToolCalls))
			results := executeToolsParallel(resp.ToolCalls, func(tc provider.ToolCall) (string, error) {
				var argsMap map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Arguments), &argsMap); err != nil {
					return "", fmt.Errorf("error parsing arguments for %s: %w", tc.Name, err)
				}

				// Registry tools (core + skill + activated deferred)
				if tool := registry.Get(tc.Name); tool != nil {
					result, err := tool.Execute(loopCtx, argsMap)
					if err != nil {
						return "", err
					}
					return result, nil
				}

				// MCP tools
				cli, exists := clientMap[tc.Name]
				if !exists {
					return "", fmt.Errorf("tool '%s' not found", tc.Name)
				}

				toolResult, err := cli.CallTool(loopCtx, &mcp.CallToolParams{Name: tc.Name, Arguments: argsMap})
				if err != nil {
					return "", err
				}

				var sb strings.Builder
				for _, c := range toolResult.Content {
					switch v := c.(type) {
					case *mcp.TextContent:
						sb.WriteString(v.Text)
					case *mcp.ImageContent:
						sb.WriteString(fmt.Sprintf("[Image: %s]", v.MIMEType))
					case *mcp.EmbeddedResource:
						if v.Resource != nil {
							sb.WriteString(fmt.Sprintf("[Resource: %s]", v.Resource.URI))
						}
					default:
						sb.WriteString("[Unknown Content]")
					}
				}
				if toolResult.IsError {
					return "", errors.New(sb.String())
				}
				return sb.String(), nil
			}, 5)

			// Append results in order + fire callbacks
			for _, r := range results {
				messages = appendAndEmit(messages, provider.Message{
					Role:       "tool",
					Content:    r.Content,
					ToolFailed: r.Failed,
					ToolCallID: r.CallID,
					ToolName:   r.ToolName,
				})
				ctx.Log("[Parallel:%s] %s", r.ToolName, truncate(r.Content, 200))
				if cfg.OnToolCall != nil {
					cfg.OnToolCall(r.ToolName, "", r.Content, 0)
				}
			}
		} else {
			// Sequential execution (original path) for non-concurrent-safe tools
			reservedReceiptAttempted := false
			for _, tc := range resp.ToolCalls {
				fnName := tc.Name
				fnArgs := tc.Arguments
				callID := tc.ID
				if repairRequest && (!finalRepairToolNames[fnName] || reservedReceiptAttempted) {
					refusal := tooloutcome.New(tooloutcome.Permanent, "reserved_repair_refused", "not_executed", "Final review repair permits only one declared completion receipt attempt. This call was not executed.", "explain_blocker")
					messages = appendAndEmit(messages, provider.Message{Role: "tool", Content: refusal.JSON(), ToolCallID: callID, ToolName: fnName, ToolFailed: true, ToolOutcome: &refusal})
					continue
				}
				if repairRequest {
					reservedReceiptAttempted = true
				}

				toolCallsSinceReport++
				if err := loopCtx.Err(); err != nil {
					return nil, err
				}
				if cfg.ToolsOnly && registry.Get(fnName) == nil {
					messages = appendAndEmit(messages, provider.Message{
						Role: "tool", Content: fmt.Sprintf("Tool '%s' is not available in this run.", fnName), ToolCallID: callID, ToolName: fnName, ToolFailed: true,
					})
					continue
				}

				ctx.Log("<tool_call name=\" %s \">\n%s\n</tool_call>", fnName, fnArgs)

				// Log step start (skip internal tools like wait and update_progress)
				toolStartTime := time.Now()
				if fnName != "tofi_wait" && cfg.OnStepStart != nil {
					argsStr := fnArgs
					if len(argsStr) > 1000 {
						argsStr = argsStr[:1000] + "..."
					}
					cfg.OnStepStart(fnName, argsStr)
				}

				if cfg.ToolsOnly {
					identity := resolveIdentity(fnName, fnArgs)
					recoveryIdentities[callID] = identity
					if blocked := toolRecoveryIdentityGuard(recoveryLedger, identity); blocked != nil {
						recoveryBlocked[callID] = true
						messages = appendAndEmit(messages, provider.Message{Role: "tool", Content: blocked.JSON(), ToolCallID: callID, ToolName: fnName, ToolOutcome: blocked, ToolFailed: true})
						continue
					}
				}
				// Parse Args
				var argsMap map[string]interface{}
				if err := json.Unmarshal([]byte(fnArgs), &argsMap); err != nil {
					outcome := tooloutcome.New(tooloutcome.Validation, "invalid_json", "not_executed", fmt.Sprintf("Error parsing arguments for %s: %v", fnName, err), "repair_arguments")
					errMsg := outcome.JSON()
					messages = appendAndEmit(messages, provider.Message{
						Role:        "tool",
						Content:     errMsg,
						ToolOutcome: &outcome, ToolFailed: true,
						ToolCallID: callID,
						ToolName:   fnName,
					})
					ctx.Log("[Error] %s", errMsg)
					continue
				}

				// PreToolCall hook — can modify args or block execution
				if modifiedArgs, hookErr := cfg.Hooks.callPreToolCall(fnName, argsMap); hookErr != nil {
					errMsg := fmt.Sprintf("PreToolCall hook blocked %s: %v", fnName, hookErr)
					messages = appendAndEmit(messages, provider.Message{
						Role: "tool", Content: errMsg, ToolCallID: callID, ToolName: fnName, ToolFailed: true,
					})
					ctx.Log("[Hook] %s", errMsg)
					continue
				} else {
					argsMap = modifiedArgs
				}

				// Hooks may change execution arguments. Recheck the effective operation
				// immediately before dispatch and record this exact identity in the ledger.
				if cfg.ToolsOnly {
					raw, encodeErr := json.Marshal(argsMap)
					if encodeErr != nil {
						return nil, encodeErr
					}
					identity := resolveIdentity(fnName, string(raw))
					recoveryIdentities[callID] = identity
					if blocked := toolRecoveryIdentityGuard(recoveryLedger, identity); blocked != nil {
						recoveryBlocked[callID] = true
						messages = appendAndEmit(messages, provider.Message{Role: "tool", Content: blocked.JSON(), ToolCallID: callID, ToolName: fnName, ToolOutcome: blocked, ToolFailed: true})
						continue
					}
				}

				// markStepDone is a helper to update the step status after tool execution
				markStepDone := func(result string) {
					durationMs := time.Since(toolStartTime).Milliseconds()
					// PostToolCall hook — can modify output
					if modified, hookErr := cfg.Hooks.callPostToolCall(fnName, argsMap, result); hookErr != nil {
						ctx.Log("[Hook] PostToolCall error for %s: %v", fnName, hookErr)
					} else {
						result = modified
					}
					// Record in trace
					state.Trace.RecordToolExec(state.Step, fnName, fnArgs, result, true, time.Since(toolStartTime))
					if fnName != "tofi_wait" && cfg.OnStepDone != nil {
						cfg.OnStepDone(fnName, result, durationMs)
					}
					if cfg.OnToolCall != nil {
						cfg.OnToolCall(fnName, fnArgs, result, durationMs)
					}
				}

				// Handle Built-in 'wait'
				if !cfg.ToolsOnly && fnName == "tofi_wait" {
					secVal := 0.0
					if s, ok := argsMap["seconds"].(float64); ok {
						secVal = s
					}
					ctx.Log("[Wait] Sleeping for %.1f seconds...", secVal)
					time.Sleep(time.Duration(secVal * float64(time.Second)))

					messages = appendAndEmit(messages, provider.Message{
						Role:       "tool",
						Content:    fmt.Sprintf("Waited for %.1f seconds.", secVal),
						ToolCallID: callID,
						ToolName:   fnName,
					})
					continue
				}

				// Handle Built-in 'tofi_shell'
				if !cfg.ToolsOnly && fnName == "tofi_shell" && cfg.SandboxDir != "" {
					command, _ := argsMap["command"].(string)

					// Detect skill script in command → override display name for OnToolCall
					if displayName := detectSkillFromCommand(command); displayName != "" {
						origMarkStepDone := markStepDone
						markStepDone = func(result string) {
							durationMs := time.Since(toolStartTime).Milliseconds()
							if cfg.OnStepDone != nil {
								cfg.OnStepDone(displayName, result, durationMs)
							}
							if cfg.OnToolCall != nil {
								cfg.OnToolCall(displayName, fnArgs, result, durationMs)
							}
						}
						_ = origMarkStepDone // suppress unused warning
					}

					// Destructive command detection (AST-based)
					destructLevel, destructWarning := DetectDestructiveAST(command)
					if destructLevel >= DestructiveCommand {
						ctx.Log("[Shell] ⚠️  Destructive command detected: %s", destructWarning)
						// TODO: in Chat mode, ask user for confirmation via Hooks
						// For now, log the warning and proceed (Agent Run is unattended).
					}
					// No pre-execution command validation — the defense is the sandbox
					// itself (gVisor syscall filter + mount isolation). DevExecutor
					// runs are local-dev only and explicitly unsafe.

					// Classify timeout based on command type
					requestedTimeout := 0
					if t, ok := argsMap["timeout"].(float64); ok && t > 0 {
						requestedTimeout = int(t)
					}
					timeout := classifyTimeout(command, requestedTimeout)

					// Execute with auto-backgrounding for long commands
					var shellResult ShellResult
					execInstance := cfg.Executor
					if execInstance == nil {
						execInstance = executor.NewExecutor("")
					}

					if bgManager != nil {
						shellResult = bgManager.RunWithAutoBackground(
							loopCtx, execInstance,
							cfg.SandboxDir, cfg.UserDir, command, timeout, cfg.SecretEnv,
							func(status string) {
								if cfg.OnProgress != nil {
									cfg.OnProgress("running", 0, status)
								}
							},
						)
					} else {
						// Direct execution (no background manager)
						output, execErr := execInstance.Execute(loopCtx, cfg.SandboxDir, cfg.UserDir, command, timeout, cfg.SecretEnv)
						shellResult = ShellResult{
							Stdout:     output,
							DurationMs: time.Since(toolStartTime).Milliseconds(),
						}
						if execErr != nil {
							shellResult.Stderr = execErr.Error()
							shellResult.ExitCode = 1
							if strings.Contains(execErr.Error(), "timed out") {
								shellResult.TimedOut = true
							}
						}
						shellResult.Interpretation = interpretExitCode(command, shellResult.ExitCode)
					}

					// Format result with smart truncation
					resultMsg := shellResult.FormatForAgent()
					resultMsg = smartTruncate(resultMsg, 4000)

					ctx.Log("[Shell] %s → %s (exit=%d, %dms)", truncate(command, 80), truncate(resultMsg, 200), shellResult.ExitCode, shellResult.DurationMs)
					messages = appendAndEmit(messages, provider.Message{
						Role:       "tool",
						Content:    resultMsg,
						ToolFailed: shellResult.ExitCode != 0 || shellResult.TimedOut,
						ToolCallID: callID,
						ToolName:   fnName,
					})
					markStepDone(resultMsg)
					continue
				}

				// Handle registry tools (core + skill + activated deferred)
				if tool := registry.Get(fnName); tool != nil {
					executionCtx := loopCtx
					if cfg.ToolsOnly {
						executionCtx = tooloutcome.WithBoundary(loopCtx, func(identity tooloutcome.Identity) *tooloutcome.Outcome {
							blocked := toolRecoveryIdentityGuard(recoveryLedger, identity)
							if blocked != nil {
								recoveryBlocked[callID] = true
							}
							return blocked
						}, func(identity tooloutcome.Identity) {
							// The first dispatch identity is immutable. Later observations can
							// add evidence, but cannot replace or narrow its uncertainty fence.
							if len(recoveryEvidence[callID]) == 0 {
								recoveryIdentities[callID] = identity
							}
							for _, prior := range recoveryEvidence[callID] {
								if prior == identity {
									return
								}
							}
							recoveryEvidence[callID] = append(recoveryEvidence[callID], identity)
						})
					}
					result, err := tool.Execute(executionCtx, argsMap)
					resultMsg := ""
					var outcome *tooloutcome.Outcome
					if err != nil {
						if questionID, suspended := suspensionQuestionID(err); suspended && cfg.ToolsOnly {
							return newSuspendedResult(
								state, &cfg, cfg.Model, messages, assistantTurnIndex, runStart,
								budgetWrapUp, finalResponseRepaired, finalRepairPending, finalRepairReserved, finalRepairFinalPending,
								toolCallsSinceReport, toolCallsSinceReport >= cfg.MaxToolCallsBetweenReports && cfg.MaxToolCallsBetweenReports > 0,
								questionID, tc, recoveryLedger,
							), nil
						}
						resultMsg = tooloutcome.ModelResult(err)
						if classified, ok := tooloutcome.FromError(err); ok {
							outcome = &classified
						}
					} else {
						resultMsg = result
						// If skill returned commands (code blocks), hint agent to execute them
						if !cfg.ToolsOnly && strings.Contains(result, "```") {
							resultMsg += "\n\n[This skill returned suggested commands. Execute them using tofi_shell to get actual results — do NOT relay these instructions to the user.]"
						}
					}
					imageURLs := visualImageURLs(fnName, argsMap, resultMsg)
					if (fnName == "computer_action" || fnName == "computer_desktop" || fnName == "computer_browser") && err == nil {
						resultMsg, imageURLs = computerScreenshot(resultMsg)
					}
					ctx.Log("[ExtraTool:%s] %s", fnName, truncate(resultMsg, 200))
					messages = appendAndEmit(messages, provider.Message{
						Role:        "tool",
						Content:     resultMsg,
						ImageURLs:   imageURLs,
						ToolOutcome: outcome, ToolFailed: err != nil,
						ToolCallID: callID,
						ToolName:   fnName,
					})
					markStepDone(resultMsg)
					continue
				}

				// Handle skill tools (sub-LLM call with skill instructions)
				if strings.HasPrefix(fnName, "run_skill__") {
					skillKey := strings.TrimPrefix(fnName, "run_skill__")
					var matchedSkill *SkillTool
					for i := range cfg.SkillTools {
						if sanitizeToolName(cfg.SkillTools[i].Name) == skillKey {
							matchedSkill = &cfg.SkillTools[i]
							break
						}
					}
					if matchedSkill != nil {
						input, _ := argsMap["input"].(string)
						ctx.Log("[Skill:%s] Executing with input: %s", matchedSkill.Name, truncate(input, 100))

						// 如果 skill 有脚本目录，在沙箱中创建 symlink
						var symlinkErr string
						if matchedSkill.SkillDir != "" && cfg.SandboxDir != "" {
							symlinkDir := filepath.Join(cfg.SandboxDir, "skills")
							os.MkdirAll(symlinkDir, 0755)
							link := filepath.Join(symlinkDir, matchedSkill.Name)
							if _, err := os.Lstat(link); os.IsNotExist(err) {
								if err := os.Symlink(matchedSkill.SkillDir, link); err != nil {
									symlinkErr = fmt.Sprintf("Failed to symlink skill scripts: %v", err)
									ctx.Log("[Skill:%s] Warning: %s", matchedSkill.Name, symlinkErr)
								} else {
									ctx.Log("[Skill:%s] Symlinked scripts: skills/%s/ → %s", matchedSkill.Name, matchedSkill.Name, matchedSkill.SkillDir)
								}
							}
						}

						result, err := executeSkillSubCall(cfg.Provider, cfg.Model, *matchedSkill, input)
						resultMsg := ""
						if err != nil {
							// Build diagnostic info for the agent
							var diag strings.Builder
							diag.WriteString(fmt.Sprintf("Skill '%s' execution failed: %v\n", matchedSkill.Name, err))
							diag.WriteString("\nDiagnostics:\n")
							// Check scripts directory
							if matchedSkill.SkillDir != "" {
								scriptsDir := filepath.Join(matchedSkill.SkillDir, "scripts")
								if _, statErr := os.Stat(scriptsDir); statErr != nil {
									diag.WriteString(fmt.Sprintf("- Scripts directory: MISSING (%s)\n", scriptsDir))
								} else {
									diag.WriteString(fmt.Sprintf("- Scripts directory: exists (%s)\n", scriptsDir))
								}
							} else {
								diag.WriteString("- Scripts directory: N/A (no bundled scripts)\n")
							}
							if symlinkErr != "" {
								diag.WriteString(fmt.Sprintf("- Symlink: FAILED (%s)\n", symlinkErr))
							}
							diag.WriteString("\nSuggestion: Try installing missing dependencies with tofi_shell, or write your own code to accomplish the goal.")
							resultMsg = diag.String()
						} else {
							resultMsg = result
						}
						ctx.Log("[Skill:%s] Result: %s", matchedSkill.Name, truncate(resultMsg, 200))
						messages = appendAndEmit(messages, provider.Message{
							Role:       "tool",
							Content:    resultMsg,
							ToolFailed: err != nil,
							ToolCallID: callID,
							ToolName:   fnName,
						})
						markStepDone(resultMsg)
					} else {
						messages = appendAndEmit(messages, provider.Message{
							Role:       "tool",
							Content:    fmt.Sprintf("Skill '%s' not found", skillKey),
							ToolFailed: true,
							ToolCallID: callID,
							ToolName:   fnName,
						})
					}
					continue
				}

				// Find appropriate MCP client
				cli, exists := clientMap[fnName]
				if !exists {
					errMsg := fmt.Sprintf("Tool '%s' not found.", fnName)
					messages = appendAndEmit(messages, provider.Message{
						Role:       "tool",
						Content:    errMsg,
						ToolFailed: true,
						ToolCallID: callID,
						ToolName:   fnName,
					})
					ctx.Log("[Error] %s", errMsg)
					continue
				}

				// Execute via MCP Client
				toolResult, err := cli.CallTool(loopCtx, &mcp.CallToolParams{Name: fnName, Arguments: argsMap})

				var outputText string
				if err != nil {
					outputText = fmt.Sprintf("Tool execution error: %v", err)
					ctx.Log("[Result] Error: %v", err)
				} else {
					var sb strings.Builder
					for _, c := range toolResult.Content {
						switch v := c.(type) {
						case *mcp.TextContent:
							sb.WriteString(v.Text)
						case *mcp.ImageContent:
							sb.WriteString(fmt.Sprintf("[Image: %s]", v.MIMEType))
						case *mcp.EmbeddedResource:
							if v.Resource != nil {
								sb.WriteString(fmt.Sprintf("[Resource: %s]", v.Resource.URI))
							}
						default:
							sb.WriteString("[Unknown Content]")
						}
					}
					outputText = sb.String()
					ctx.Log("[Result] %s", truncate(outputText, 100))
				}

				messages = appendAndEmit(messages, provider.Message{
					Role:       "tool",
					Content:    outputText,
					ToolFailed: err != nil || (toolResult != nil && toolResult.IsError),
					ToolCallID: callID,
					ToolName:   fnName,
				})
				markStepDone(outputText)
			}
		} // end sequential execution else block
		if cfg.MaxToolCallsBetweenReports > 0 && toolCallsSinceReport >= cfg.MaxToolCallsBetweenReports {
			if cycleSawTool && cycleOnlyDiscovery && !cycleSawNovel {
				stalledDiscoveryCycles++
			} else {
				stalledDiscoveryCycles = 0
			}
			if stalledDiscoveryCycles >= 2 {
				stalledDiscoveryStop = true
				messages = append(messages, provider.Message{Role: "user", Content: "Two consecutive 15-call intervals repeated the same tool-directory queries and results without new information. Stop calling tools. Explain the concrete blocker and what has already been checked; give a final answer now."})
			} else {
				reportRequired = true
				messages = append(messages, provider.Message{Role: "user", Content: progressReportReminder(cfg.MaxToolCallsBetweenReports)})
			}
		}

		// Post-call context compaction — use actual API-reported token count
		if !repairRequest && !finalRepairFinalRequest && resp.Usage.InputTokens > int64(float64(state.Tracker.ContextWindow())*0.80) && len(messages) > 4 {
			ctx.Log("[Agent] Post-call compaction triggered: %d tokens > 80%% of %d window", resp.Usage.InputTokens, state.Tracker.ContextWindow())
			originalTokens := int(resp.Usage.InputTokens)
			cfg.Hooks.callPreCompact(len(messages), originalTokens)

			summary, compactErr := compactMessages(loopCtx, cfg.Provider, cfg.Model, cfg.ReasoningEffort, messages)
			if compactErr != nil {
				ctx.Log("[Agent] Compaction failed: %v", compactErr)
			} else {
				originalCount := len(messages)
				messages = compactAndRebuild(messages, summary)
				// Reset InitialMsgCount so NewMessages() tracks only post-compaction additions
				state = state.WithCompactedMessages(messages)

				compactedTokens := EstimateContextUsage(systemPrompt, messages, allTools)
				cfg.Hooks.callPostCompact(originalCount, len(messages), originalTokens, compactedTokens)
				if cfg.OnCompact != nil {
					cfg.OnCompact(originalTokens, compactedTokens)
				}
				if cfg.OnContextCompact != nil {
					cfg.OnContextCompact(summary, originalTokens, compactedTokens)
				}
				ctx.Log("[Agent] Compacted: %d messages → %d messages (%d → ~%d tokens)", originalCount, len(messages), originalTokens, compactedTokens)
			}
		}

		// Write messages back to state at end of iteration
		state = state.WithMessages(messages)
	}

	// Loop exited via state machine — return result
	if state.Err != nil {
		return nil, fmt.Errorf("agent loop failed: %w", state.Err)
	}
	return state.ToResult(cfg.Model), nil
}

// compactMessages uses the same LLM to generate a concise summary of the conversation.
func compactMessages(ctx context.Context, p provider.Provider, model, reasoningEffort string, messages []provider.Message) (string, error) {
	var conversationText strings.Builder
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		// For compaction input, truncate long tool results to save context
		content := msg.Content
		if msg.Role == "tool" && len(content) > 500 {
			content = content[:500] + "\n[... truncated for summarization]" + lazyKnowledgeReloadHint(msg.ToolName)
		}
		conversationText.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, content))
	}

	req := &provider.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		System:          "You are a precise assistant that creates structured conversation summaries. Output in the same language as the conversation.",
		Messages: []provider.Message{
			{Role: "user", Content: fmt.Sprintf(
				"Summarize the following conversation. You MUST preserve:\n"+
					"1. The current task goal and what the user originally asked for\n"+
					"2. Key decisions made and their reasoning\n"+
					"3. Important results, data, file paths, and code outputs\n"+
					"4. What was accomplished so far (completed steps)\n"+
					"5. What still needs to be done (pending steps)\n"+
					"6. Any errors encountered and how they were resolved\n\n"+
					"Format as structured sections. Be concise but complete.\n\n"+
					"Conversation:\n%s", conversationText.String())},
		},
	}

	resp, err := p.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// Lazy capability results carry reusable schemas or scoped instructions. A
// truncated preview is not sufficient for reuse; keep a bounded reload route
// instead of retaining every discovered schema or guide in context.
func lazyKnowledgeReloadHint(toolName string) string {
	switch toolName {
	case "search_mcp_tools":
		return "\n[Schema context was compacted. Before calling an MCP tool whose complete input schema is no longer visible, use search_mcp_tools on its known server to reload that schema. Do not guess arguments; prior discovery does not grant authorization.]"
	case "read_skill", "read_skill_file", "read_workflow_guide", "computer_help":
		return "\n[Scoped instructions were compacted. If this guidance is still relevant and its full instructions are no longer visible, reload it using " + toolName + " with the prior selection before continuing dependent work. Reloading does not grant authorization.]"
	default:
		return ""
	}
}

// microCompact performs lightweight in-place trimming of old tool results
// that the LLM has already seen and acted upon. This reduces context usage
// without needing a full LLM-powered summarization pass.
//
// Strategy: tool results older than the last N messages get truncated to
// a short summary (first 200 chars + "[full output was N chars]").
// The LLM has already consumed and responded to these results,
// so the full text is no longer needed.
func microCompact(messages []provider.Message, keepRecentCount int) []provider.Message {
	if keepRecentCount <= 0 {
		keepRecentCount = 6 // keep last 3 pairs (assistant + tool) intact
	}

	if len(messages) <= keepRecentCount {
		return messages
	}

	result := make([]provider.Message, len(messages))
	copy(result, messages)

	cutoff := len(messages) - keepRecentCount

	for i := 0; i < cutoff; i++ {
		msg := &result[i]
		if msg.Role != "tool" {
			continue
		}
		if len(msg.Content) <= 300 {
			continue
		}

		// Preserve first 200 chars as a preview
		preview := msg.Content[:200]
		// Find a clean break point (newline)
		if idx := strings.LastIndex(preview, "\n"); idx > 100 {
			preview = preview[:idx]
		}
		msg.Content = fmt.Sprintf("%s\n\n[... %d chars of output omitted — already processed by assistant above]",
			preview, len(msg.Content)) + lazyKnowledgeReloadHint(msg.ToolName)
	}

	return result
}

// compactAndRebuild takes the full message list and a summary, finds a safe cut point
// that preserves complete tool call/result sequences, and rebuilds the message list
// with the summary prepended.
func compactAndRebuild(messages []provider.Message, summary string) []provider.Message {
	keepFrom := len(messages) - 2
	if keepFrom < 1 {
		keepFrom = 1
	}
	for i := keepFrom; i >= 1; i-- {
		if messages[i].Role == "tool" {
			for j := i - 1; j >= 1; j-- {
				if messages[j].Role == "assistant" && len(messages[j].ToolCalls) > 0 {
					keepFrom = j
					break
				}
			}
			break
		} else if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			keepFrom = i
			break
		}
	}

	kept := make([]provider.Message, len(messages[keepFrom:]))
	copy(kept, messages[keepFrom:])
	result := []provider.Message{
		{Role: "user", Content: fmt.Sprintf("<context_summary>\n%s\n</context_summary>\n\nThe above is a summary of our conversation so far. Please continue from where we left off.", summary)},
	}
	// Preserve reload routes independently of the model-written summary. Only
	// omitted tool results need this reminder; complete recent results remain.
	for _, name := range []string{"search_mcp_tools", "read_skill", "read_skill_file", "read_workflow_guide", "computer_help"} {
		hint := lazyKnowledgeReloadHint(name)
		for _, msg := range messages[:keepFrom] {
			if (msg.Role == "tool" && msg.ToolName == name) || strings.Contains(msg.Content, hint) {
				result[0].Content += hint
				break
			}
		}
	}
	return append(result, kept...)
}

// stripThinkTags removes <think>...</think> blocks from LLM content.
// Some models emit chain-of-thought in <think> tags which should not be shown to users.
func stripThinkTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			// Unclosed tag — strip from <think> to end
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// thinkStreamFilter wraps a streaming callback to suppress <think> blocks in real-time,
// redirecting thinking content to onThinking instead.
type thinkStreamFilter struct {
	forward    func(string)
	onThinking func(string) // called with content inside <think> blocks
	buf        strings.Builder
	inside     bool
}

func (f *thinkStreamFilter) Write(delta string) {
	f.buf.WriteString(delta)
	text := f.buf.String()

	for {
		if f.inside {
			end := strings.Index(text, "</think>")
			if end == -1 {
				// Still inside think block
				// Check for partial closing tag at end (e.g., "</thi")
				holdback := partialTagSuffix(text, "</think>")
				toForward := text[:len(text)-len(holdback)]
				if toForward != "" && f.onThinking != nil {
					f.onThinking(toForward)
				}
				f.buf.Reset()
				f.buf.WriteString(holdback)
				return
			}
			// Forward thinking content before </think>
			if end > 0 && f.onThinking != nil {
				f.onThinking(text[:end])
			}
			text = text[end+len("</think>"):]
			f.inside = false
		}

		start := strings.Index(text, "<think>")
		if start == -1 {
			// No think tag — but check for partial opening tag at end (e.g., "<thi")
			holdback := partialTagSuffix(text, "<think>")
			toForward := text[:len(text)-len(holdback)]
			if toForward != "" {
				f.forward(toForward)
			}
			f.buf.Reset()
			f.buf.WriteString(holdback)
			return
		}

		// Forward content before <think>
		if start > 0 {
			f.forward(text[:start])
		}
		text = text[start+len("<think>"):]
		f.inside = true
	}
}

// partialTagSuffix checks if text ends with a partial prefix of tag.
// e.g., text="hello<thi", tag="<think>" → returns "<thi"
func partialTagSuffix(text, tag string) string {
	for i := 1; i < len(tag); i++ {
		suffix := tag[:i]
		if strings.HasSuffix(text, suffix) {
			return suffix
		}
	}
	return ""
}

// ---------------- Helpers ----------------

const latestMCPProtocolVersion = "2026-07-28"

type staticHeaderTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t staticHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	for k, v := range t.headers {
		switch http.CanonicalHeaderKey(k) {
		case "Mcp-Protocol-Version", "Mcp-Session-Id":
			continue
		}
		copyReq.Header.Set(k, v)
	}
	return t.base.RoundTrip(copyReq)
}

func newStrictMCPClient() *mcp.Client {
	cli := mcp.NewClient(&mcp.Implementation{Name: "tofi-agent", Version: "1.0.0"}, nil)
	// The SDK intentionally retries legacy initialize after a failed discover.
	// Product policy is latest-only, so block the retry before it reaches a peer.
	cli.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "initialize" {
				return nil, fmt.Errorf("MCP server does not support required protocol %s", latestMCPProtocolVersion)
			}
			return next(ctx, method, req)
		}
	})
	return cli
}

func connectStrictMCP(ctx context.Context, tr mcp.Transport) (*mcp.ClientSession, error) {
	cs, err := newStrictMCPClient().Connect(ctx, tr, &mcp.ClientSessionOptions{ProtocolVersion: latestMCPProtocolVersion})
	if err != nil {
		return nil, err
	}
	if result := cs.InitializeResult(); result == nil || result.ProtocolVersion != latestMCPProtocolVersion {
		_ = cs.Close()
		return nil, fmt.Errorf("MCP server did not negotiate required protocol %s", latestMCPProtocolVersion)
	}
	return cs, nil
}

func setupClient(runCtx context.Context, cfg MCPServerConfig, ctx *models.ExecutionContext) (*mcp.ClientSession, func(), error) {
	// Remote MCP server (StreamableHTTP), optionally OAuth-authenticated.
	if cfg.URL != "" {
		tr := &mcp.StreamableClientTransport{Endpoint: cfg.URL}
		if cfg.OAuth != nil {
			if cfg.OAuth.Handler == nil {
				return nil, nil, fmt.Errorf("OAuth handler is required for MCP server %s", cfg.Name)
			}
			tr.OAuthHandler = cfg.OAuth.Handler
		}
		if len(cfg.Headers) > 0 {
			headers := make(map[string]string, len(cfg.Headers))
			for k, v := range cfg.Headers {
				if cfg.OAuth != nil && strings.EqualFold(k, "Authorization") {
					continue
				}
				headers[k] = v
			}
			tr.HTTPClient = &http.Client{Transport: staticHeaderTransport{base: http.DefaultTransport, headers: headers}}
		}
		cli, err := connectStrictMCP(runCtx, tr)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to connect remote MCP server %s: %w", cfg.Name, err)
		}
		cleanup := func() {
			ctx.Log("[Debug] Closing remote MCP client (%s)...", cfg.Name)
			if err := cli.Close(); err != nil {
				ctx.Log("[Warn] Failed to close remote MCP client: %v", err)
			}
		}
		return cli, cleanup, nil
	}

	// Ensure workspace exists (Artifacts directory)
	// Many MCP servers (like fs-server) will fail to start if the root directory is missing.
	if err := os.MkdirAll(ctx.Paths.Artifacts, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create artifacts directory: %v", err)
	}

	workspacePath, _ := filepath.Abs(ctx.Paths.Artifacts)

	processedArgs := make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		processedArgs[i] = strings.ReplaceAll(arg, "{{workspace}}", workspacePath)
	}

	// Construct environment variables
	env := os.Environ()
	for k, v := range cfg.Env {
		processedVal := strings.ReplaceAll(v, "{{workspace}}", workspacePath)
		env = append(env, fmt.Sprintf("%s=%s", k, processedVal))
	}

	cmd := exec.Command(cfg.Command, processedArgs...)
	cmd.Env = env
	tr := &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}
	cli, err := connectStrictMCP(runCtx, tr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect stdio MCP server %s: %w", cfg.Name, err)
	}

	cleanup := func() {
		ctx.Log("[Debug] Closing MCP client (%s)...", cfg.Name)

		if err := cli.Close(); err != nil {
			ctx.Log("[Warn] Failed to close MCP client: %v", err)
		}
	}
	return cli, cleanup, nil
}

func convertTools(mcpTools []*mcp.Tool) []provider.Tool {
	var result []provider.Tool
	for _, t := range mcpTools {
		var schemaMap map[string]interface{}
		jsonBytes, err := json.Marshal(t.InputSchema)
		if err == nil {
			_ = json.Unmarshal(jsonBytes, &schemaMap)
		}
		if schemaMap == nil {
			schemaMap = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}

		typeVal, hasType := schemaMap["type"]
		isObject := !hasType || (hasType && typeVal == "object")

		if isObject {
			if _, hasProps := schemaMap["properties"]; !hasProps {
				schemaMap["properties"] = map[string]interface{}{}
				schemaMap["type"] = "object"
			}
		}

		// Sanitize tool name for compatibility (a-z, 0-9, _, -)
		name := sanitizeToolName(t.Name)
		if name == "" {
			log.Printf("[Warn] Skipping tool with empty name (original: %q)", t.Name)
			continue
		}
		// Max function name length is 64
		if len(name) > 64 {
			name = name[:64]
		}

		// Ensure description is not empty
		desc := t.Description
		if desc == "" {
			desc = "Tool: " + name
		}
		// Truncate overly long descriptions
		if len(desc) > 1024 {
			desc = desc[:1021] + "..."
		}

		result = append(result, provider.Tool{
			Name:        name,
			Description: desc,
			Parameters:  schemaMap,
		})
	}
	return result
}

// validateTools checks and fixes tool definitions before use.
func validateTools(tools []provider.Tool) []provider.Tool {
	var valid []provider.Tool
	for _, t := range tools {
		// Ensure name is valid
		if t.Name == "" {
			continue
		}
		if len(t.Name) > 64 {
			t.Name = t.Name[:64]
		}

		// Ensure parameters is a valid object schema
		if t.Parameters == nil {
			t.Parameters = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		if params, ok := t.Parameters.(map[string]interface{}); ok {
			if _, hasType := params["type"]; !hasType {
				params["type"] = "object"
			}
			if _, hasProps := params["properties"]; !hasProps {
				params["properties"] = map[string]interface{}{}
			}
		}

		// Ensure description exists
		if t.Description == "" {
			t.Description = "Tool: " + t.Name
		}
		if len(t.Description) > 1024 {
			t.Description = t.Description[:1021] + "..."
		}

		valid = append(valid, t)
	}
	return valid
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// smartTruncate intelligently truncates command output by preserving
// the head and tail (most useful parts) and summarizing the middle.
func smartTruncate(output string, maxChars int) string {
	if len(output) <= maxChars {
		return output
	}

	lines := strings.Split(output, "\n")

	// For very few lines that are just long, do simple truncation
	if len(lines) <= 10 {
		return output[:maxChars] + "\n\n[truncated, total " + fmt.Sprintf("%d", len(output)) + " chars]"
	}

	// Keep first 30% of budget for head, last 30% for tail, 40% buffer
	headBudget := maxChars * 3 / 10
	tailBudget := maxChars * 3 / 10

	// Build head: take lines from the start until budget exhausted
	var headLines []string
	headUsed := 0
	for _, line := range lines {
		if headUsed+len(line)+1 > headBudget {
			break
		}
		headLines = append(headLines, line)
		headUsed += len(line) + 1
	}

	// Build tail: take lines from the end until budget exhausted
	var tailLines []string
	tailUsed := 0
	for i := len(lines) - 1; i >= len(headLines); i-- {
		if tailUsed+len(lines[i])+1 > tailBudget {
			break
		}
		tailLines = append([]string{lines[i]}, tailLines...)
		tailUsed += len(lines[i]) + 1
	}

	omitted := len(lines) - len(headLines) - len(tailLines)
	if omitted <= 0 {
		// Budgets covered everything, just truncate normally
		return output[:maxChars] + "\n\n[truncated, total " + fmt.Sprintf("%d", len(output)) + " chars]"
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(headLines, "\n"))
	sb.WriteString(fmt.Sprintf("\n\n... [%d lines omitted, %d total lines, %d total chars] ...\n\n", omitted, len(lines), len(output)))
	sb.WriteString(strings.Join(tailLines, "\n"))
	return sb.String()
}

func directToolResultChars(args map[string]interface{}) (int, error) {
	raw, ok := args["max_result_chars"]
	if !ok {
		return 0, fmt.Errorf("max_result_chars is required for direct tools (1-%d)", maxDirectToolResultChars)
	}

	value, ok := raw.(float64)
	if !ok || value != float64(int(value)) {
		return 0, fmt.Errorf("max_result_chars must be an integer between 1 and %d", maxDirectToolResultChars)
	}
	chars := int(value)
	if chars < 1 || chars > maxDirectToolResultChars {
		return 0, fmt.Errorf("max_result_chars must be between 1 and %d", maxDirectToolResultChars)
	}
	return chars, nil
}

const maxWebFetchVisualImages = 3

func visualImageURLs(toolName string, args map[string]interface{}, result string) []string {
	if toolName == "tofi_view_image" {
		imageURL, _ := args["image_url"].(string)
		if isVisualImageURL(imageURL) {
			return []string{imageURL}
		}
		return nil
	}
	if toolName != "web_fetch" {
		return nil
	}
	_, imageSection, found := strings.Cut(result, "--- Images ---")
	if !found {
		return nil
	}

	urls := make([]string, 0, maxWebFetchVisualImages)
	seen := make(map[string]bool)
	for _, line := range strings.Split(imageSection, "\n") {
		start := strings.Index(line, "http")
		if start < 0 {
			continue
		}
		imageURL := strings.TrimRight(strings.TrimSpace(line[start:]), ".,;)")
		if !isVisualImageURL(imageURL) || seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		urls = append(urls, imageURL)
		if len(urls) == maxWebFetchVisualImages {
			break
		}
	}
	return urls
}

// detectSkillFromCommand checks if a shell command runs a skill script
// and returns a display name like "web-search" or "web-fetch".
// Returns empty string if the command is not a skill script.
func detectSkillFromCommand(command string) string {
	// Match patterns like:
	//   python3 /path/to/skills/web-search/scripts/search.py ...
	//   python3 skills/web-search/scripts/news.py ...
	idx := strings.Index(command, "skills/")
	if idx == -1 {
		return ""
	}
	rest := command[idx+len("skills/"):]
	slashIdx := strings.Index(rest, "/")
	if slashIdx == -1 {
		return ""
	}
	return rest[:slashIdx]
}

// sanitizeToolName converts a skill name to a valid tool function name
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// shellQuote wraps a string in single quotes with proper escaping for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func copySkillToSandbox(sandboxDir, skillName, skillDir string) error {
	target := filepath.Join(sandboxDir, "skills", skillName)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("clear target: %w", err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("create target: %w", err)
	}
	return copyDir(skillDir, target)
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeToolName(name string) string {
	result := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)
	return strings.ToLower(result)
}

// executeSkillSubCall runs a skill by doing a sub-LLM call with the skill's instructions.
// Returns structured output: message (informational) + commands (to execute in sandbox).
func executeSkillSubCall(p provider.Provider, model string, skill SkillTool, input string) (string, error) {
	instructions := skill.Instructions

	// 展开 {baseDir} 占位符为沙箱内的相对路径
	if skill.SkillDir != "" {
		relativePath := "skills/" + skill.Name
		instructions = strings.ReplaceAll(instructions, "{baseDir}", relativePath)
	}

	// 根据是否有脚本目录，生成不同的约束块
	var constraintBlock string
	if skill.SkillDir != "" {
		constraintBlock = fmt.Sprintf(`
SKILL SCRIPTS AVAILABLE at: skills/%s/
- This skill has bundled scripts. Reference them using the relative path above.
  Example: python3 skills/%s/scripts/xxx.py --help
- You may also use inline code (python3 <<'PYEOF'...PYEOF for multi-line, python3 -c "..." for trivial one-liners only)
- The skills/ directory is READ-ONLY — do NOT write files into it`, skill.Name, skill.Name)
	} else {
		constraintBlock = `
CRITICAL constraints on commands:
- NO script files (no "python3 xxx.py", no "node script.js") — you have NO local files
- Only use: curl, python3 <<'PYEOF', python3 -c "...", node -e "...", jq, sed, grep -E, xmllint, etc.
- For multi-line Python: ALWAYS use heredoc (python3 <<'PYEOF'...PYEOF), NEVER cram complex code into python3 -c "..."
- Install packages with: python3 -m pip install <pkg> (NEVER bare "pip")
- For web scraping: prefer RSS feeds (curl + xmllint/sed/grep) or python3 with urllib`
	}

	systemPrompt := instructions + `

---
You are being invoked as a skill by an agent with shell execution capability on macOS.
You MUST respond with a JSON object in this exact format:
{"message": "brief explanation", "commands": ["cmd1", "cmd2"]}

Rules:
- "message": Short description of the result or what the commands do
- "commands": Array of shell commands to execute. Empty [] if you can answer directly.
- Each command must be a single, self-contained shell command
- Return raw JSON only — no markdown code blocks
- NO placeholder API keys (no "YOUR_API_KEY", no "YOUR_TOKEN") — only use free/public endpoints
- NO grep -P (Perl regex) — macOS grep does not support it. Use grep -E or sed instead
` + constraintBlock

	req := &provider.ChatRequest{
		Model:  model,
		System: systemPrompt,
		Messages: []provider.Message{
			{Role: "user", Content: input},
		},
	}

	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("skill sub-call LLM failed for '%s': %v. The skill's sub-LLM call could not complete. Try accomplishing the goal with tofi_shell directly", skill.Name, err)
	}
	content := resp.Content
	if content == "" {
		return "Skill returned empty response", nil
	}

	// Try to parse as JSON — extract message and commands
	content = strings.TrimSpace(content)
	// Strip markdown code fences if LLM wrapped it anyway
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			// Remove first and last lines (``` markers)
			content = strings.Join(lines[1:len(lines)-1], "\n")
			content = strings.TrimSpace(content)
		}
	}

	var parsed struct {
		Message  string   `json:"message"`
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err == nil && (parsed.Message != "" || len(parsed.Commands) > 0) {
		// Successfully parsed structured response
		var result strings.Builder
		if parsed.Message != "" {
			result.WriteString(parsed.Message)
		}
		if len(parsed.Commands) > 0 {
			if result.Len() > 0 {
				result.WriteString("\n\n")
			}
			result.WriteString("[COMMANDS TO EXECUTE]\n")
			for _, cmd := range parsed.Commands {
				result.WriteString("$ " + cmd + "\n")
			}
			result.WriteString("\n[Execute these commands using tofi_shell to get actual results.]")
		}
		return result.String(), nil
	}

	// Fallback: LLM didn't return valid JSON, return raw content with hint
	if strings.Contains(content, "```") {
		return content + "\n\n[This skill returned suggested commands. Execute them using tofi_shell to get actual results.]", nil
	}
	return content, nil
}
