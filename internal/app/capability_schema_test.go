package app

import (
	"encoding/json"
	"sort"
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

// A run resumed after an approval replays its own transcript, so the schemas
// its earlier search_mcp_tools calls returned count as shown, although they are
// newer than the run's start. Other runs, other Bots and failed or truncated
// searches do not count.
func TestRunSearchedCapabilitySchemasCoverTheRunsOwnSearches(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("one", "", "model")
	other, _ := s.CreateBot("two", "", "model")
	schema := func(name string) extensions.CachedMCPTool {
		return extensions.CachedMCPTool{Name: name, Server: "fixture", RemoteName: name, Description: "Fixture capability", SchemaVersion: strings.Repeat("a", 64), Parameters: map[string]any{"type": "object"}}
	}
	insert := func(run, call, bot, status string, truncated int, tools ...extensions.CachedMCPTool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"tools": tools})
		if _, e := s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,result,status,truncated,started_at,updated_at) VALUES(?,?,?,?,'search_mcp_tools',?,?,?,'2026-11-04T08:00:00Z','2026-11-04T08:00:01Z')`, b.DMConversationID, bot, run, call, string(body), status, truncated); e != nil {
			t.Fatal(e)
		}
	}
	insert("run_now", "s1", b.ID, "completed", 0, schema("mcp_update_page"), schema("mcp_search"))
	insert("run_now", "s2", b.ID, "completed", 0, schema("mcp_update_page"))
	insert("run_now", "s3", b.ID, "failed", 0, schema("mcp_failed"))
	insert("run_now", "s4", b.ID, "completed", 1, schema("mcp_truncated"))
	insert("run_now", "s5", other.ID, "completed", 0, schema("mcp_other_bot"))
	insert("run_old", "s6", b.ID, "completed", 0, schema("mcp_old_run"))
	got := s.runSearchedCapabilitySchemas("run_now", b.ID)
	var names []string
	for _, tool := range got {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "mcp_update_page,mcp_search" {
		t.Fatalf("schemas = %v", names)
	}
	merged := withRunSearchedSchemas(got, []extensions.CachedMCPTool{schema("mcp_search"), schema("mcp_recent")})
	names = nil
	for _, tool := range merged {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "mcp_update_page,mcp_search,mcp_recent" {
		t.Fatalf("merged = %v", names)
	}
	if s.runSearchedCapabilitySchemas("", b.ID) != nil || s.runSearchedCapabilitySchemas("run_now", "") != nil {
		t.Fatal("an empty run or Bot must return nothing")
	}
}

// The names a run was shown before a resume: every tool on its search pages
// (compacted ones too) and every tool a schema reveal showed. The "not searched"
// refusal shares the code schema_required but shows nothing, so it does not count.
func TestRunShownMCPToolNamesCountSearchesAndReveals(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("one", "", "model")
	row := func(run, call, name, arguments, result, status, outcome string) {
		t.Helper()
		if _, e := s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,outcome_json,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'2026-11-04T08:00:00Z','2026-11-04T08:00:01Z')`, b.DMConversationID, b.ID, run, call, name, arguments, result, status, outcome); e != nil {
			t.Fatal(e)
		}
	}
	row("run_now", "s1", "search_mcp_tools", `{}`, `{"tools":[{"name":"mcp_whole"},{"name":"mcp_compacted","schema_compacted":true}],"omitted_tools":["mcp_omitted"]}`, "completed", "")
	row("run_now", "c1", "call_mcp_tool", `{"name":"mcp_revealed","arguments":{}}`, "", "failed", `{"code":"schema_required","execution_certainty":"not_executed","next_action":"repair_arguments"}`)
	row("run_now", "c2", "call_mcp_tool", `{"name":"mcp_never_searched","arguments":{}}`, "", "failed", `{"code":"schema_required","execution_certainty":"not_executed","next_action":"refresh_schema"}`)
	row("run_now", "s2", "search_mcp_tools", `{}`, `{"tools":[{"name":"mcp_failed_search"}]}`, "failed", "")
	row("run_old", "s3", "search_mcp_tools", `{}`, `{"tools":[{"name":"mcp_other_run"}]}`, "completed", "")
	got := s.runShownMCPToolNames("run_now", b.ID)
	sort.Strings(got)
	if strings.Join(got, ",") != "mcp_compacted,mcp_revealed,mcp_whole" {
		t.Fatalf("names = %v", got)
	}
	tools := withRunShownNames(nil, got)
	if len(tools) != 3 || !tools[0].ShownInRun || tools[0].Parameters != nil {
		t.Fatalf("name-only entries = %+v", tools)
	}
}
