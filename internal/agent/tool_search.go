package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// buildToolSearchTool creates the tofi_tool_search tool that searches
// deferred tools by keyword and activates matching ones.
func buildToolSearchTool(registry *ToolRegistry) ToolDef {
	return &FuncTool{
		ToolName:        "tofi_tool_search",
		ToolDisplayName: "Tool Search",
		ToolSchema: provider.Tool{
			Name:        "tofi_tool_search",
			Description: "Search for available tools by keyword. Use this when you need a capability not in your current tool set. Matching tools are automatically activated for use.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search keywords describing the capability you need (e.g. 'web search', 'memory save', 'file upload')",
					},
				},
				"required": []string{"query"},
			},
		},
		ExecuteFunc: func(_ context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			if query == "" {
				return "Error: query is required", nil
			}

			results := registry.Search(query)
			if len(results) == 0 {
				// List all available deferred tools as fallback
				deferred := registry.DeferredTools()
				if len(deferred) == 0 {
					return "No deferred tools available.", nil
				}
				var lines []string
				lines = append(lines, fmt.Sprintf("No tools matched '%s'. Available deferred tools:", query))
				for _, t := range deferred {
					lines = append(lines, fmt.Sprintf("- %s: %s", t.Name(), t.Schema().Description))
				}
				return strings.Join(lines, "\n"), nil
			}

			// Activate matched tools and build response
			var lines []string
			lines = append(lines, fmt.Sprintf("Found %d matching tool(s):", len(results)))
			for _, r := range results {
				registry.Activate(r.Name)
				lines = append(lines, fmt.Sprintf("- %s: %s (activated)", r.Name, r.Description))
			}
			lines = append(lines, "\nThese tools are now available for use.")
			return strings.Join(lines, "\n"), nil
		},
		IsConcurrent:   true,
		IsReadOnlyTool: true,
	}
}
