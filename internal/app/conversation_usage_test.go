package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestComputeConversationUsage(t *testing.T) {
	run := func(id string, in, out int64) convUsageRun {
		return convUsageRun{RunID: id, Model: "codex-gpt-6-sol", TotalIn: in, TotalOut: out, WindowTokens: 272000, WindowKnown: true, LastIn: in / 2}
	}
	chat := func(r string, in, out int64, at string) convUsageCall {
		return convUsageCall{RunID: r, Kind: usageKindChat, Model: "codex-gpt-6-sol", In: in, Out: out, USD: float64(in*2+out*10) / 1e6, PriceKnown: true, At: at}
	}
	tests := []struct {
		name      string
		runs      []convUsageRun
		calls     []convUsageCall
		starts    map[string][]string
		wantSplit ConversationToolSplit
		wantReq   int64
		wantUSD   float64
	}{
		{name: "empty", wantSplit: ConversationToolSplit{Code: usageSplitNoUsage}},
		{
			name:      "tool call then final answer",
			runs:      []convUsageRun{run("r1", 300, 30)},
			calls:     []convUsageCall{chat("r1", 100, 10, "t1"), chat("r1", 200, 20, "t3")},
			starts:    map[string][]string{"r1": {"t2"}},
			wantSplit: ConversationToolSplit{Available: true, ToolTokens: 110, ConversationTokens: 220, ToolModelCalls: 1, ConversationCalls: 1, ToolCalls: 1},
			wantReq:   2, wantUSD: float64(300*2+30*10) / 1e6,
		},
		{
			name:      "plain conversation, no tools",
			runs:      []convUsageRun{run("r1", 100, 10)},
			calls:     []convUsageCall{chat("r1", 100, 10, "t1")},
			wantSplit: ConversationToolSplit{Available: true, ConversationTokens: 110, ConversationCalls: 1},
			wantReq:   1, wantUSD: float64(100*2+10*10) / 1e6,
		},
		{
			name:      "reviewer calls are counted apart",
			runs:      []convUsageRun{run("r1", 150, 15)},
			calls:     []convUsageCall{chat("r1", 100, 10, "t1"), {RunID: "r1", Kind: usageKindReview, Model: "codex-gpt-6-luna", In: 50, Out: 5, PriceKnown: true, At: "t2"}},
			starts:    map[string][]string{"r1": {"t2"}},
			wantSplit: ConversationToolSplit{Available: true, ToolTokens: 110, ReviewTokens: 55, ToolModelCalls: 1, ToolCalls: 1},
			wantReq:   2, wantUSD: float64(300) / 1e6,
		},
		{
			name:      "expired call detail makes the split unavailable",
			runs:      []convUsageRun{run("old", 500, 50), run("r1", 100, 10)},
			calls:     []convUsageCall{chat("r1", 100, 10, "t1")},
			starts:    map[string][]string{"old": {"a", "b"}},
			wantSplit: ConversationToolSplit{Code: usageSplitCallDetail, ToolCalls: 2},
			wantReq:   1, wantUSD: float64(100*2+10*10) / 1e6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeConversationUsage("c", tc.runs, tc.calls, tc.starts, nil)
			want := tc.wantSplit
			if want.Available {
				want.Basis = "model_calls_followed_by_tool_start"
			}
			if got.ToolSplit != want {
				t.Fatalf("split=%+v want %+v", got.ToolSplit, want)
			}
			if got.Totals.Requests != tc.wantReq || got.Totals.EquivalentUSD != tc.wantUSD {
				t.Fatalf("totals=%+v", got.Totals)
			}
			var in int64
			for _, r := range tc.runs {
				in += r.TotalIn
			}
			if got.Totals.InputTokens != in {
				t.Fatalf("input=%d want %d", got.Totals.InputTokens, in)
			}
			if (len(tc.runs) == 0) != (got.LatestContext == nil) {
				t.Fatalf("latest=%+v", got.LatestContext)
			}
		})
	}
}

func TestLatestContextGroupsSnapshotParts(t *testing.T) {
	snap := &ContextSnapshot{Parts: []ContextPart{
		{Kind: ctxKindSystemGuidance, Tokens: 100},
		{Kind: ctxKindBotInstruction, Tokens: 20},
		{Kind: ctxKindTools, Tokens: 300},
		{Kind: ctxKindToolsOnDemand, Tokens: 999, OnDemand: true},
		{Kind: ctxKindMemory, Tokens: 40},
		{Kind: ctxKindHistory, Tokens: 1700, Message: &ContextMessage{Images: 1}},
		{Kind: ctxKindHistory, Tokens: 60},
		{Kind: "tool_carryover", Tokens: 15},
		{Kind: "brand_new_kind", Tokens: 5},
	}}
	tests := []struct {
		name     string
		run      convUsageRun
		wantUsed int
		measured bool
	}{
		{"measured", convUsageRun{RunID: "r", Model: "codex-gpt-6-sol", WindowTokens: 1000, WindowKnown: true, LastIn: 700, EstimatedIn: 650}, 700, true},
		{"estimate only", convUsageRun{RunID: "r", Model: "codex-gpt-6-sol", WindowTokens: 1000, WindowKnown: true, EstimatedIn: 650}, 650, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lc := latestContext(tc.run, snap)
			if lc.UsedTokens != tc.wantUsed || lc.UsedMeasured != tc.measured || lc.WindowTokens != 1000 {
				t.Fatalf("lc=%+v", lc)
			}
			want := map[string]int{usageGroupInstructions: 120, usageGroupHistory: 260, usageGroupToolDefs: 300, usageGroupToolResults: 15, usageGroupMemory: 40, usageGroupAttachments: 1500, usageGroupOther: 5}
			got := map[string]int{}
			for _, p := range lc.Parts {
				if !p.Estimated {
					t.Fatalf("part %s not marked estimated", p.Kind)
				}
				got[p.Kind] = p.Tokens
			}
			if len(got) != len(want) {
				t.Fatalf("got=%v", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s=%d want %d (%v)", k, got[k], v, got)
				}
			}
			if lc.PartsTokens != 2240 {
				t.Fatalf("parts_tokens=%d", lc.PartsTokens)
			}
		})
	}
	if lc := latestContext(convUsageRun{RunID: "r", Model: "codex-gpt-6-sol"}, nil); lc.PartsCode != contextSnapshotUnavailable || len(lc.Parts) != 0 || lc.WindowTokens <= 0 {
		t.Fatalf("no snapshot: %+v", lc)
	}
}

func TestRouteConversationUsage(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Usage Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: s}
	get := func(method, id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		path := "conversations/" + id + "/usage"
		if !srv.routeConversationUsage(w, httptest.NewRequest(method, "/api/"+path, nil), path) {
			t.Fatal("route did not match")
		}
		return w
	}
	if w := get(http.MethodGet, "missing"); w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d", w.Code)
	}
	if w := get(http.MethodPost, bot.DMConversationID); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post=%d", w.Code)
	}
	if srv.routeConversationUsage(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/conversations/x/tools", nil), "conversations/x/tools") {
		t.Fatal("matched another route")
	}
	var empty ConversationUsage
	if w := get(http.MethodGet, bot.DMConversationID); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &empty) != nil || empty.LatestContext != nil || empty.ToolSplit.Code != usageSplitNoUsage {
		t.Fatalf("empty=%d %s", w.Code, w.Body)
	}

	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "look it up", "usage-route", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	id := runs[0].ID
	if err = s.recordContextEstimate(id, bot.Model, 900); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(id, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		bot.DMConversationID, bot.ID, id, "c1", "web_search", "completed", now(), now()); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(id, 1500, 50); err != nil {
		t.Fatal(err)
	}
	if err = s.saveRunContextSnapshot(ContextSnapshot{Version: contextSnapshotVersion, RunID: id, ConversationID: bot.DMConversationID, BotID: bot.ID, Model: bot.Model, CapturedAt: now(),
		Parts: []ContextPart{{Kind: ctxKindSystemGuidance, Tokens: 400}, {Kind: ctxKindHistory, Tokens: 100}}}); err != nil {
		t.Fatal(err)
	}
	var got ConversationUsage
	w := get(http.MethodGet, bot.DMConversationID)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("code=%d %s", w.Code, w.Body)
	}
	if got.Totals.Requests != 2 || got.Totals.InputTokens != 2500 || got.Totals.OutputTokens != 150 || got.Totals.UnpricedRequests != 0 || got.Totals.EquivalentUSD <= 0 {
		t.Fatalf("totals=%+v", got.Totals)
	}
	if len(got.PerModel) != 1 || got.PerModel[0].Model != bot.Model || got.PerModel[0].Requests != 2 {
		t.Fatalf("per_model=%+v", got.PerModel)
	}
	lc := got.LatestContext
	if lc == nil || lc.RunID != id || lc.UsedTokens != 1500 || !lc.UsedMeasured || lc.WindowTokens <= 0 || len(lc.Parts) != 2 || lc.PartsTokens != 500 {
		t.Fatalf("latest=%+v", lc)
	}
	if sp := got.ToolSplit; !sp.Available || sp.ToolModelCalls != 1 || sp.ConversationCalls != 1 || sp.ToolTokens != 1100 || sp.ConversationTokens != 1550 || sp.ToolCalls != 1 {
		t.Fatalf("split=%+v", sp)
	}
}
