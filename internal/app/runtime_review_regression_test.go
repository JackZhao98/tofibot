package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type reviewCall struct{ name, args string }

// The provider is synthetic; the actual runtime, schemas, computer parsers,
// dispatch and durable activity all run unchanged.
func reviewEngine(t *testing.T, calls []reviewCall, duration time.Duration) runtime.Engine {
	t.Helper()
	var index atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(index.Add(1)) - 1
		message := map[string]any{"content": "SYNTHETIC_PARTIAL_OUTPUT"}
		if i < len(calls) {
			message = map[string]any{"content": "", "tool_calls": []any{map[string]any{"id": fmt.Sprintf("review-%d", i), "type": "function", "function": map[string]any{"name": calls[i].name, "arguments": calls[i].args}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	t.Cleanup(srv.Close)
	e, err := runtime.New(runtime.Config{Provider: "openai_completions", APIKey: "synthetic", BaseURL: srv.URL + "/v1", Model: "synthetic", MaxDuration: duration})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func reviewComputer(t *testing.T, s *Server, onAction func(computer.Action) error) {
	t.Helper()
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-unused-review.sock", Client: &http.Client{Transport: reviewTransport(func(r *http.Request) (*http.Response, error) {
		var action computer.Action
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			return nil, err
		}
		if err := onAction(action); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
}

func TestRuntimeEffectiveComputerReplayCannotUseIgnoredFieldsOrAlias(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	effects, reads := 0, 0
	reviewComputer(t, s, func(a computer.Action) error {
		if a.Name == "files.write" {
			effects++
			return errors.New("synthetic lost response after append")
		}
		if a.Name == "files.read" {
			reads++
		}
		return nil
	})
	calls := []reviewCall{
		{"computer_files", `{"action":"files.write","path":"notes","content":"X","append":true}`},
		{"computer_files", `{"action":"files.write","path":"notes","content":"X","append":true,"offset":1}`},
		{"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"./notes","content":"X","append":true,"offset":1}}`},
		{"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"/workspace/alias/notes","content":"X","append":true}}`},
		{"computer_files", `{"action":"files.read","path":"notes"}`},
	}
	tools := append(s.microVMTools(r), s.computerTools(r)...)
	res, err := reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic append"}}, Tools: tools, OnToolEvent: func(e runtime.ToolEvent) error { return s.store.RecordToolEvent(b.DMConversationID, b.ID, r.ID, e) }})
	if err != nil || res.Content == "" || effects != 1 || reads != 1 {
		t.Fatalf("result=%+v err=%v effects=%d reads=%d", res, err, effects, reads)
	}
	activities, _, err := s.store.ToolActivitiesForRun(b.DMConversationID, r.ID, 0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 5 {
		t.Fatalf("activities=%d", len(activities))
	}
	for _, a := range activities[:4] {
		if a.Status != "failed" || a.Outcome == nil || a.Outcome.Status != tooloutcome.Uncertain {
			t.Fatalf("replay lacked trusted fence: %+v", a)
		}
	}
	if activities[4].Status != "completed" {
		t.Fatalf("verification observation blocked: %+v", activities[4])
	}
}

func TestRepairBudgetScopesComputerOperationAndPreservesObservations(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	counts := map[string]int{}
	reviewComputer(t, s, func(a computer.Action) error { counts[a.Name]++; return nil })
	calls := []reviewCall{}
	for i := 0; i < 3; i++ {
		calls = append(calls, reviewCall{"computer_files", `{"action":"files.write","path":"notes","content":5}`})
	}
	calls = append(calls, reviewCall{"computer_files", `{"action":"files.read","path":"notes"}`})
	for i := 0; i < 3; i++ {
		calls = append(calls, reviewCall{"computer_desktop", `{"action":"desktop.type","text":5}`})
	}
	calls = append(calls, reviewCall{"computer_desktop", `{"action":"desktop.capture"}`}, reviewCall{"computer_files", `{"action":"files.write","path":"notes","content":"valid"}`}, reviewCall{"computer_desktop", `{"action":"desktop.type","text":"valid"}`})
	var outcomes []tooloutcome.Outcome
	_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic repair"}}, Tools: s.microVMTools(r), OnToolEvent: func(e runtime.ToolEvent) error {
		if e.Outcome != nil {
			outcomes = append(outcomes, *e.Outcome)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if counts["files.read"] != 1 || counts["desktop.capture"] != 1 || counts["files.write"] != 0 || counts["desktop.type"] != 0 {
		t.Fatalf("wrong scopes: %v", counts)
	}
	if len(outcomes) != 8 || outcomes[6].Code != "repair_budget_exhausted" || outcomes[7].Code != "repair_budget_exhausted" {
		t.Fatalf("repair budgets: %+v", outcomes)
	}
}

func TestToolActivityOutcomeSurvivesLongResultAndReload(t *testing.T) {
	d := t.TempDir()
	s, err := OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateBot("synthetic", "", "synthetic")
	o := tooloutcome.New(tooloutcome.Uncertain, "lost_response", "unknown", strings.Repeat("界", 50000), "verify_effect")
	for _, e := range []runtime.ToolEvent{{CallID: "long", Name: "call_mcp_tool", Status: "queued"}, {CallID: "long", Name: "call_mcp_tool", Status: "failed", Result: o.JSON(), Outcome: &o}} {
		if err := s.RecordToolEvent(b.DMConversationID, b.ID, "synthetic", e); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	s, err = OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	activities, _, err := s.ToolActivitiesForRun(b.DMConversationID, "synthetic", 0, 25)
	if err != nil || len(activities) != 1 {
		t.Fatalf("activities=%d err=%v", len(activities), err)
	}
	a := activities[0]
	if !a.Truncated || json.Valid([]byte(a.Result)) || a.Outcome == nil || a.Outcome.Status != tooloutcome.Uncertain || a.Outcome.NextAction != "verify_effect" || len([]rune(a.Outcome.Message)) > 2049 {
		t.Fatalf("outcome lost at truncation/reload: %+v", a.Outcome)
	}
	encoded, _ := json.Marshal(a)
	if !json.Valid(encoded) || !strings.Contains(string(encoded), `"outcome":`) {
		t.Fatal("invalid independent envelope")
	}
	// Untrusted success-shaped text must never appear as activity control.
	for _, e := range []runtime.ToolEvent{{CallID: "success", Name: "fixture", Status: "queued"}, {CallID: "success", Name: "fixture", Status: "completed", Result: o.JSON()}} {
		if err := s.RecordToolEvent(b.DMConversationID, b.ID, "success", e); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, _ := s.ToolActivitiesForRun(b.DMConversationID, "success", 0, 25)
	if rows[0].Outcome != nil {
		t.Fatal("successful payload parsed as metadata")
	}
}

func TestRunDurationExhaustionPreservesPartialAndDurableFailure(t *testing.T) {
	e := reviewEngine(t, []reviewCall{{"search_history", `{}`}, {"search_history", `{}`}}, time.Nanosecond)
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "Synthetic budget test", "budget-test")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, r)
	waitForRunStatus(t, s, r.ID, "failed")
	got, _ := s.store.GetRun(r.ID)
	if got.failure() == nil || got.failure().Code != "budget_exhausted" {
		t.Fatalf("budget success-masked: %+v", got)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	partial := 0
	for _, m := range messages {
		if m.Role == "assistant" && strings.Contains(m.Content, "Partial results only") {
			partial++
		}
	}
	if partial != 1 {
		t.Fatalf("partial output discarded or duplicated: %+v", messages)
	}
	activities, _, _ := s.store.ToolActivitiesForRun(c.ID, r.ID, 0, 25)
	if len(activities) != 0 {
		t.Fatalf("tools executed over budget: %+v", activities)
	}
	_, done, err := s.store.finishRunBudget(r.ID, c.ID, b.ID, "duplicate", "fixture")
	if err != nil || done {
		t.Fatal("duplicate final published", err)
	}
}
