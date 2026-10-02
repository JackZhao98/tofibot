package agent

import (
	"github.com/JackZhao98/tofibot/internal/provider"
	"strings"
	"testing"
)

func TestLazyKnowledgeMicroCompactionKeepsReloadRoute(t *testing.T) {
	for _, name := range []string{"search_mcp_tools", "read_skill", "read_skill_file", "read_workflow_guide", "computer_help"} {
		t.Run(name, func(t *testing.T) {
			messages := []provider.Message{{Role: "user", Content: "Continue the assigned task"}, {Role: "tool", ToolName: name, Content: strings.Repeat("metadata ", 800) + "required_parameter_or_guide_rule"}}
			for i := 0; i < 8; i++ {
				messages = append(messages, provider.Message{Role: "assistant", Content: "progress"})
			}
			got := microCompact(messages, 6)
			if strings.Contains(got[1].Content, "required_parameter_or_guide_rule") || len(got[1].Content) > 600 {
				t.Fatal("retained excessive old knowledge")
			}
			if !strings.Contains(got[1].Content, lazyKnowledgeReloadHint(name)) {
				t.Fatal("missing reload route")
			}
			again := microCompact(got, 6)
			if strings.Count(again[1].Content, lazyKnowledgeReloadHint(name)) != 1 {
				t.Fatal("reload route duplicated or lost")
			}
		})
	}
}

func TestLazyKnowledgeReloadRouteSurvivesRepeatedFullCompaction(t *testing.T) {
	messages := []provider.Message{{Role: "user", Content: "Task"}, {Role: "tool", ToolName: "search_mcp_tools", Content: strings.Repeat("schema", 1000)}, {Role: "tool", ToolName: "read_workflow_guide", Content: strings.Repeat("guide", 1000)}, {Role: "assistant", Content: "progress"}, {Role: "user", Content: "Continue"}}
	for i := 0; i < 3; i++ {
		got := compactAndRebuild(messages, "Task remains unfinished.")
		for _, name := range []string{"search_mcp_tools", "read_workflow_guide"} {
			if strings.Count(got[0].Content, lazyKnowledgeReloadHint(name)) != 1 {
				t.Fatalf("round %d lost or duplicated %s reload route", i, name)
			}
		}
		if strings.Contains(got[0].Content, "schemaschema") || len(got[0].Content) > 1000 {
			t.Fatal("unbounded schema retention")
		}
		messages = append(got, provider.Message{Role: "assistant", Content: "progress"}, provider.Message{Role: "user", Content: "Continue"})
	}
}

func TestOrdinaryToolCompactionDoesNotAddCapabilityReload(t *testing.T) {
	if lazyKnowledgeReloadHint("call_mcp_tool") != "" {
		t.Fatal("ordinary output should not be treated as schema")
	}
}
