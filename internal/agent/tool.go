package agent

import (
	"context"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// ToolDef is the unified interface for all tools in the agent loop.
// Each tool declares its schema, execution behavior, and safety properties.
// This enables the scheduler to make informed decisions about parallel execution,
// result size limits, and permission checks.
type ToolDef interface {
	// Name returns the tool's internal ID (e.g. "tofi_read"). Must be unique.
	Name() string

	// DisplayName returns the user-facing name for TUI/Web (e.g. "Read File").
	DisplayName() string

	// Schema returns the provider.Tool definition for LLM registration.
	Schema() provider.Tool

	// Execute runs the tool with parsed arguments.
	Execute(ctx context.Context, args map[string]interface{}) (string, error)

	// ConcurrencySafe returns true if this tool can run in parallel with others.
	ConcurrencySafe() bool

	// ReadOnly returns true if the tool does not modify any state.
	ReadOnly() bool

	// MaxResultSize returns the maximum number of characters to keep from the result.
	// 0 means no limit (use default truncation).
	MaxResultSize() int

	// Deferred returns true if this tool's schema should NOT be sent to the LLM
	// unless explicitly activated (via tofi_tool_search or direct activation).
	Deferred() bool

	// SearchHint returns keyword hints for tool search matching.
	// Example: "web search internet browse" for a web search tool.
	// Empty string means search only matches on Name() and Schema().Description.
	SearchHint() string
}

// ToolRegistry manages a collection of tools with lookup, schema generation,
// and deferred tool activation.
type ToolRegistry struct {
	tools     map[string]ToolDef
	order     []string        // preserves registration order
	activated map[string]bool // deferred tools that have been activated
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:     make(map[string]ToolDef),
		activated: make(map[string]bool),
	}
}

// Register adds a tool to the registry. Panics on duplicate name.
func (r *ToolRegistry) Register(tool ToolDef) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		panic("duplicate tool registration: " + name)
	}
	r.tools[name] = tool
	r.order = append(r.order, name)
}

// Get returns a tool by name, or nil if not found.
func (r *ToolRegistry) Get(name string) ToolDef {
	return r.tools[name]
}

// Has returns true if a tool with the given name is registered.
func (r *ToolRegistry) Has(name string) bool {
	_, ok := r.tools[name]
	return ok
}

// Schemas returns ALL tool schemas (including deferred). Used for validation.
func (r *ToolRegistry) Schemas() []provider.Tool {
	schemas := make([]provider.Tool, 0, len(r.order))
	for _, name := range r.order {
		schemas = append(schemas, r.tools[name].Schema())
	}
	return schemas
}

// ActiveSchemas returns schemas for tools that should be sent to the LLM:
// non-deferred tools + deferred tools that have been activated.
func (r *ToolRegistry) ActiveSchemas() []provider.Tool {
	schemas := make([]provider.Tool, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		if !tool.Deferred() || r.activated[name] {
			schemas = append(schemas, tool.Schema())
		}
	}
	return schemas
}

// ActivatedDeferredSchemas returns schemas of deferred tools that HAVE been
// activated (via tofi_tool_search). The agent loop appends these to the
// provider tool list so the model can actually call them.
func (r *ToolRegistry) ActivatedDeferredSchemas() []provider.Tool {
	var schemas []provider.Tool
	for _, name := range r.order {
		tool := r.tools[name]
		if tool.Deferred() && r.activated[name] {
			schemas = append(schemas, tool.Schema())
		}
	}
	return schemas
}

// DeferredTools returns all deferred tools that have NOT been activated yet.
func (r *ToolRegistry) DeferredTools() []ToolDef {
	var result []ToolDef
	for _, name := range r.order {
		tool := r.tools[name]
		if tool.Deferred() && !r.activated[name] {
			result = append(result, tool)
		}
	}
	return result
}

// Activate marks a deferred tool as active so its schema is included in ActiveSchemas().
func (r *ToolRegistry) Activate(name string) {
	if _, ok := r.tools[name]; ok {
		r.activated[name] = true
	}
}

// IsActivated returns true if a deferred tool has been activated.
func (r *ToolRegistry) IsActivated(name string) bool {
	return r.activated[name]
}

// Names returns all registered tool names in order.
func (r *ToolRegistry) Names() []string {
	result := make([]string, len(r.order))
	copy(result, r.order)
	return result
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	return len(r.tools)
}

// DisplayNameFor returns the display name for a tool, or the internal name if not found.
func (r *ToolRegistry) DisplayNameFor(name string) string {
	if tool, ok := r.tools[name]; ok {
		return tool.DisplayName()
	}
	return name
}

// AllConcurrencySafe returns true if all given tool names are concurrency-safe.
func (r *ToolRegistry) AllConcurrencySafe(names []string) bool {
	for _, name := range names {
		tool := r.tools[name]
		if tool == nil || !tool.ConcurrencySafe() {
			return false
		}
	}
	return len(names) > 1
}

// SearchResult holds a tool match from Search().
type SearchResult struct {
	Name        string
	Description string
	Score       int
}

// Search finds deferred tools matching the query keywords.
// Returns results sorted by relevance score (highest first).
func (r *ToolRegistry) Search(query string) []SearchResult {
	keywords := splitKeywords(query)
	if len(keywords) == 0 {
		return nil
	}

	var results []SearchResult

	for _, name := range r.order {
		tool := r.tools[name]
		if !tool.Deferred() {
			continue
		}

		score := scoreTool(tool, keywords)
		if score > 0 {
			desc := tool.Schema().Description
			results = append(results, SearchResult{
				Name:        name,
				Description: desc,
				Score:       score,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// scoreTool computes relevance score for a tool against search keywords.
func scoreTool(tool ToolDef, keywords []string) int {
	score := 0
	name := strings.ToLower(tool.Name())
	hint := strings.ToLower(tool.SearchHint())
	desc := strings.ToLower(tool.Schema().Description)

	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			score += 3
		}
		if hint != "" && strings.Contains(hint, kw) {
			score += 4
		}
		if strings.Contains(desc, kw) {
			score += 2
		}
	}
	return score
}

// splitKeywords normalizes a query string into lowercase keywords.
func splitKeywords(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var result []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if len(w) > 1 { // skip single chars
			result = append(result, w)
		}
	}
	return result
}

// FuncTool is a simple ToolDef implementation backed by a function.
// Use for quick tool creation without defining a full struct.
type FuncTool struct {
	ToolName        string
	ToolDisplayName string
	ToolSchema      provider.Tool
	ExecuteFunc     func(ctx context.Context, args map[string]interface{}) (string, error)
	IsConcurrent    bool
	IsReadOnlyTool  bool
	MaxResultChars  int
	IsDeferred      bool   // true = deferred tool (not sent to LLM until activated)
	Hint            string // search keywords for tofi_tool_search
}

func (f *FuncTool) Name() string { return f.ToolName }
func (f *FuncTool) DisplayName() string {
	if f.ToolDisplayName != "" {
		return f.ToolDisplayName
	}
	return f.ToolName
}
func (f *FuncTool) Schema() provider.Tool { return f.ToolSchema }
func (f *FuncTool) ConcurrencySafe() bool { return f.IsConcurrent }
func (f *FuncTool) ReadOnly() bool        { return f.IsReadOnlyTool }
func (f *FuncTool) MaxResultSize() int    { return f.MaxResultChars }
func (f *FuncTool) Deferred() bool        { return f.IsDeferred }
func (f *FuncTool) SearchHint() string    { return f.Hint }

func (f *FuncTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return f.ExecuteFunc(ctx, args)
}

// WrapExtraBuiltin converts a legacy ExtraBuiltinTool into a ToolDef.
// Used during migration — new tools should implement ToolDef directly.
func WrapExtraBuiltin(et ExtraBuiltinTool) ToolDef {
	return &FuncTool{
		ToolName:   et.Schema.Name,
		ToolSchema: et.Schema,
		IsDeferred: et.Deferred,
		Hint:       et.Hint,
		ExecuteFunc: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if et.HandlerCtx != nil {
				return et.HandlerCtx(ctx, args)
			}
			return et.Handler(args)
		},
	}
}
