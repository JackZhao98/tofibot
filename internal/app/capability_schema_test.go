package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func TestRecentCapabilitySchemasScopeAgeAndUsedToolPriority(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("one", "", "model")
	other, _ := s.CreateBot("two", "", "model")
	schema := func(name string) extensions.CachedMCPTool {
		return extensions.CachedMCPTool{Name: name, Server: "fixture", RemoteName: name, Description: "Fixture capability", SchemaVersion: strings.Repeat("a", 64), Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
	}
	insert := func(id, bot, conv, when, status string, tools ...extensions.CachedMCPTool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"tools": tools})
		_, e := s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,result,status,started_at,updated_at) VALUES(?,?,'prior',?,'search_mcp_tools',?,?,?,?)`, conv, bot, id, string(body), status, when, when)
		if e != nil {
			t.Fatal(e)
		}
	}
	before := "2026-09-29T12:00:00Z"
	insert("valid", b.ID, b.DMConversationID, "2026-09-29T11:00:00Z", "completed", schema("mcp_a"), schema("mcp_b"), schema("mcp_c"), schema("mcp_used"))
	insert("other-bot", other.ID, b.DMConversationID, "2026-09-29T11:59:00Z", "completed", schema("mcp_other_bot"))
	insert("other-conv", b.ID, other.DMConversationID, "2026-09-29T11:59:00Z", "completed", schema("mcp_other_conv"))
	insert("expired", b.ID, b.DMConversationID, "2026-09-28T11:59:00Z", "completed", schema("mcp_expired"))
	insert("failed", b.ID, b.DMConversationID, "2026-09-29T11:58:00Z", "failed", schema("mcp_failed"))
	insert("future", b.ID, b.DMConversationID, "2026-09-29T12:00:00.1Z", "completed", schema("mcp_future"))
	_, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,status,started_at,updated_at) VALUES(?,?,'prior','used','call_mcp_tool','{"name":"mcp_used"}','completed','2026-09-29T11:01:00Z','2026-09-29T11:01:00Z')`, b.DMConversationID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := s.recentCapabilitySchemas(b.DMConversationID, b.ID, before)
	if len(got) != 3 || got[0].Name != "mcp_used" {
		t.Fatalf("used schema should win bounded cache: %+v", got)
	}
	for _, v := range got {
		if v.Name != "mcp_used" && v.Name != "mcp_a" && v.Name != "mcp_b" {
			t.Fatalf("out-of-scope cache: %+v", v)
		}
	}
	if got := s.recentCapabilitySchemas(b.DMConversationID, b.ID, "bad-time"); len(got) != 0 {
		t.Fatal("invalid boundary accepted")
	}
}

func TestRecentCapabilitySchemasSkipOversizedAndMalformed(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("one", "", "model")
	valid := extensions.CachedMCPTool{Name: "mcp_valid", Server: "fixture", RemoteName: "valid", SchemaVersion: strings.Repeat("a", 64), Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
	oversized := valid
	oversized.Name = "mcp_oversized"
	oversized.Parameters = map[string]any{"type": "object", "description": strings.Repeat("x", maxRecentSchemaBytes+1)}
	malformed := valid
	malformed.Name = "mcp_malformed"
	malformed.Parameters = map[string]any{"type": "array"}
	body, _ := json.Marshal(map[string]any{"tools": []extensions.CachedMCPTool{oversized, malformed, valid}})
	_, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,result,status,started_at,updated_at) VALUES(?,?,'r','c','search_mcp_tools',?,'completed','2026-09-29T10:00:00Z','2026-09-29T10:00:00Z')`, b.DMConversationID, b.ID, string(body))
	if err != nil {
		t.Fatal(err)
	}
	got := s.recentCapabilitySchemas(b.DMConversationID, b.ID, "2026-09-29T11:00:00Z")
	if len(got) != 1 || got[0].Name != "mcp_valid" {
		t.Fatalf("unsafe schema or valid item lost: %+v", got)
	}
}
