package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestReservedRepairRefusalCannotInventCompletionReceipt(t *testing.T) {
	for _, validFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-first", true: "valid-first"}[validFirst], func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, _ := s.CreateBot("synthetic", "", "synthetic")
			r, _ := s.AddRun(b.DMConversationID, b.ID, "")
			first := `{"content":5}`
			if validFirst {
				first = `{"content":"verified synthetic receipt"}`
			}
			batches := [][]reviewCall{nil, {{"complete_scheduled_task", first}, {"complete_scheduled_task", `{"content":"refused second receipt"}`}}, nil}
			executed := 0
			_, err = reviewBatchEngine(t, batches, time.Nanosecond).Run(context.Background(), runtime.Request{
				BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic receipt test"}},
				FinalResponseRepairTools: []string{"complete_scheduled_task"},
				BeforeFinalResponse:      func(string) (string, error) { return "Record the required receipt once.", nil },
				Tools:                    []runtime.Tool{{Name: "complete_scheduled_task", Parameters: objectSchema(map[string]any{"content": map[string]any{"type": "string"}}, []string{"content"}), Execute: func(context.Context, json.RawMessage) (string, error) { executed++; return "receipt recorded", nil }}},
				OnToolEvent:              func(e runtime.ToolEvent) error { return s.RecordToolEvent(b.DMConversationID, b.ID, r.ID, e) },
			})
			if err != nil {
				t.Fatal(err)
			}
			completed, err := s.HasCompletedTool(r.ID, "complete_scheduled_task")
			if err != nil || completed != validFirst || executed != map[bool]int{false: 0, true: 1}[validFirst] {
				t.Fatalf("completion=%v executions=%d err=%v", completed, executed, err)
			}
			rows, _, err := s.ToolActivitiesForRun(b.DMConversationID, r.ID, 0, 10)
			if err != nil || len(rows) != 2 {
				t.Fatalf("rows=%+v err=%v", rows, err)
			}
			if rows[1].Status != "failed" || rows[1].Outcome == nil || rows[1].Outcome.Code != "reserved_repair_refused" || rows[1].Outcome.Certainty != "not_executed" {
				t.Fatalf("refusal=%+v", rows[1])
			}
			if !validFirst && (rows[0].Status != "failed" || rows[0].Outcome == nil || rows[0].Outcome.Status != tooloutcome.Validation) {
				t.Fatalf("invalid first=%+v", rows[0])
			}
		})
	}
}

func TestLostNewFileResponseCannotReplayThroughCreatedHardlink(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	root := t.TempDir()
	g, err := guest.NewWithIdleTimeout(root, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(context.Background())
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-unused-hardlink.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
		var a computer.Action
		if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(a)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)).WithContext(req.Context()))
		if a.Name == "files.write" {
			writes++
			if writes == 1 {
				if w.Code != 200 {
					t.Fatalf("first write failed: %s", w.Body.String())
				}
				if err := os.Link(filepath.Join(root, "bots", b.ID, "a"), filepath.Join(root, "bots", b.ID, "hardlink")); err != nil {
					t.Fatal(err)
				}
				return nil, errors.New("synthetic response lost after creating file")
			}
		}
		return w.Result(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	calls := []reviewCall{{"computer_files", `{"action":"files.write","path":"a","content":"X","append":true}`}, {"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"hardlink","content":"X","append":true}}`}, {"computer_files", `{"action":"files.write","path":"b","content":"Y","append":true}`}}
	_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic physical target verification"}}, Tools: append(s.microVMTools(r), s.computerTools(r)...)})
	if err != nil || writes != 1 {
		t.Fatalf("writes=%d err=%v", writes, err)
	}
	for name, want := range map[string]string{"a": "X", "hardlink": "X"} {
		got, err := os.ReadFile(filepath.Join(root, "bots", b.ID, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s=%q want=%q err=%v", name, got, want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "bots", b.ID, "b")); !os.IsNotExist(err) {
		t.Fatal("uncertain create allowed a second target", err)
	}
}

func TestActionWrappersHaveSeparateRecoveryOperations(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Construct identities only; no secret value or computer action is used.
	s.secretVault = &secretVault{}
	s.microVM = &computer.Client{}
	r := Run{ID: "fixture", BotID: "synthetic-bot", TriggerMessageID: "trigger"}
	for _, tc := range []struct {
		name         string
		tool         Tool
		a, b         string
		riskA, riskB string
	}{
		{"extensions", s.extensionManagementTools(Conversation{ID: "fixture"}, r)[0], `{"action":"mcp_list"}`, `{"action":"mcp_update","name":"fixture"}`, tooloutcome.Observation, tooloutcome.TargetMutation},
		{"terminal", s.terminalTool(r), `{"action":"read","terminal_id":"a"}`, `{"action":"write","terminal_id":"a","data":"x"}`, tooloutcome.Observation, tooloutcome.OpaqueEffect},
		{"ssh-keys", s.sshKeyTool(r), `{"action":"list"}`, `{"action":"generate","name":"id_ed25519_fixture"}`, tooloutcome.Observation, tooloutcome.TargetMutation},
		{"secret", s.secretTools(r)[1], `{"action":"env","secret_ref":"fixture"}`, `{"action":"shell_exec","secret_ref":"fixture"}`, tooloutcome.OpaqueEffect, tooloutcome.OpaqueEffect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tool.Identity == nil {
				t.Fatal("missing wrapper identity")
			}
			a, b := tc.tool.Identity(json.RawMessage(tc.a)), tc.tool.Identity(json.RawMessage(tc.b))
			if a.Operation == b.Operation || a.Risk != tc.riskA || b.Risk != tc.riskB {
				t.Fatalf("identities collapse: %+v %+v", a, b)
			}
		})
	}
	react := s.reactionTool(Conversation{ID: "fixture"}, r)
	a := react.Identity(json.RawMessage(`{"emoji":"👍"}`))
	bIdentity := react.Identity(json.RawMessage(`{"action":"add","message_id":" trigger ","emoji":"👍"}`))
	other := react.Identity(json.RawMessage(`{"message_id":"other","emoji":"👍"}`))
	if a.Target != bIdentity.Target || a.Operation != bIdentity.Operation || a.Target == other.Target {
		t.Fatalf("reaction targets: %+v %+v %+v", a, bIdentity, other)
	}
}

func TestMissingSecretReferenceDoesNotFenceCorrectedReference(t *testing.T) {
	s, count := secretTestServer(t)
	r := Run{ID: "synthetic-run", BotID: "synthetic-bot", ConversationID: "synthetic-conversation"}
	ciphertext, err := s.secretVault.seal("synthetic-ref", "synthetic-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	s.secretVault.records["synthetic-ref"] = secretRecord{ID: "synthetic-ref", RunID: r.ID, BotID: r.BotID, ConversationID: r.ConversationID, Status: "ready", CreatedAt: now(), Ciphertext: ciphertext}
	var outcomes []tooloutcome.Outcome
	calls := []reviewCall{{"use_secret_input", `{"secret_ref":"missing","action":"shell_exec","target":"API_KEY","command":"synthetic-command"}`}, {"use_secret_input", `{"secret_ref":"synthetic-ref","action":"shell_exec","target":"API_KEY","command":"synthetic-command"}`}}
	_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: r.BotID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic private reference repair"}}, Tools: []Tool{s.secretTools(r)[1]}, OnToolEvent: func(e runtime.ToolEvent) error {
		if e.Outcome != nil {
			outcomes = append(outcomes, *e.Outcome)
		}
		return nil
	}})
	if err != nil || *count != 1 || len(outcomes) != 1 || outcomes[0].Status != tooloutcome.Denied || outcomes[0].Certainty != "not_executed" {
		t.Fatalf("count=%d outcomes=%+v err=%v", *count, outcomes, err)
	}
}

func TestExtensionPredispatchValidationAllowsListAndCorrectedUpdate(t *testing.T) {
	s, c, r, tool := extensionToolFixture(t)
	// All configuration is synthetic, local and never connected.
	extensionToolCall(t, tool, map[string]any{"action": "mcp_create", "name": "fixture", "url": "https://example.invalid/old"})
	cursor := s.store.workspaceEventCursor()
	var outcomes []tooloutcome.Outcome
	calls := []reviewCall{
		{"manage_extensions", `{"action":"mcp_update","url":"https://example.invalid/new"}`},
		{"manage_extensions", `{"action":"mcp_list"}`},
		{"manage_extensions", `{"action":"mcp_update","name":"fixture","url":"https://example.invalid/new"}`},
	}
	_, err := reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: r.BotID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic correction"}}, Tools: []Tool{tool}, OnToolEvent: func(e runtime.ToolEvent) error {
		if e.Outcome != nil {
			outcomes = append(outcomes, *e.Outcome)
		}
		return s.store.RecordToolEvent(c.ID, r.BotID, r.ID, e)
	}})
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != tooloutcome.Validation || outcomes[0].Certainty != "not_executed" {
		t.Fatalf("outcomes=%+v err=%v", outcomes, err)
	}
	servers, err := s.extensions.ListMCP()
	if err != nil || len(servers) != 1 || servers[0].URL != "https://example.invalid/new" || len(configEventsAfter(t, s, cursor)) != 1 {
		t.Fatalf("servers=%+v err=%v", servers, err)
	}
	rows, _, err := s.store.ToolActivitiesForRun(c.ID, r.ID, 0, 10)
	if err != nil || len(rows) != 3 || rows[0].Status != "failed" || rows[1].Status != "completed" || rows[2].Status != "completed" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestFileObservationFailuresDoNotBlockDifferentRequests(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "untyped-read-failure", true: "typed-uncertain-observation"}[uncertain], func(t *testing.T) {
			s, err := NewServer(Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, _ := s.store.CreateBot("synthetic", "", "synthetic")
			r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
			_, _ = s.store.SetRunStatus(r.ID, "running", "")
			reads := map[string]int{}
			reviewComputer(t, s, func(a computer.Action) error {
				if a.Name != "files.read" {
					return nil
				}
				var in struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(a.Args, &in)
				reads[in.Path]++
				if in.Path == "a" {
					if uncertain {
						return tooloutcome.New(tooloutcome.Uncertain, "synthetic_read_lost", "unknown", "Synthetic read response lost.", "verify_effect").Err()
					}
					return errors.New("synthetic read response lost")
				}
				return nil
			})
			var outcomes []tooloutcome.Outcome
			calls := []reviewCall{{"computer_files", `{"action":"files.read","path":"a"}`}, {"computer_action", `{"computer_id":"firecracker","action":"files.read","args":{"path":"a"}}`}, {"computer_files", `{"action":"files.read","path":"b"}`}}
			_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic observations"}}, Tools: append(s.microVMTools(r), s.computerTools(r)...), OnToolEvent: func(e runtime.ToolEvent) error {
				if e.Outcome != nil {
					outcomes = append(outcomes, *e.Outcome)
				}
				return nil
			}})
			if err != nil || reads["a"] != 1 || reads["b"] != 1 || len(outcomes) != 2 {
				t.Fatalf("reads=%v outcomes=%+v err=%v", reads, outcomes, err)
			}
			if !uncertain && (outcomes[0].Status != tooloutcome.Permanent || outcomes[0].Certainty != "no_side_effects") {
				t.Fatalf("observation misclassified: %+v", outcomes[0])
			}
		})
	}
}

func TestUncertainFileMutationAllowsVerifiedDistinctTarget(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	writes := map[string]int{}
	reviewComputer(t, s, func(a computer.Action) error {
		if a.Name != "files.write" {
			return nil
		}
		var in struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(a.Args, &in)
		writes[in.Path]++
		if in.Path == "a" {
			return errors.New("synthetic response lost after append")
		}
		return nil
	})
	calls := []reviewCall{{"computer_files", `{"action":"files.write","path":"a","content":"X","append":true}`}, {"computer_files", `{"action":"files.write","path":"b","content":"Y","append":true}`}, {"computer_files", `{"action":"files.write","path":"a","content":"changed","append":true,"offset":1}`}, {"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"/workspace/alias/a","content":"X","append":true}}`}}
	_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic separate file targets"}}, Tools: append(s.microVMTools(r), s.computerTools(r)...)})
	if err != nil || writes["a"] != 1 || writes["b"] != 1 || len(writes) != 2 {
		t.Fatalf("writes=%v err=%v", writes, err)
	}
}

func TestUnavailableFileIdentityCannotRelaxUncertainFence(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("synthetic", "", "synthetic")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	writes := 0
	reviewComputer(t, s, func(a computer.Action) error {
		if a.Name == "files.identity" {
			return errors.New("synthetic older backend unsupported lookup")
		}
		if a.Name == "files.write" {
			writes++
			return errors.New("synthetic lost response")
		}
		return nil
	})
	calls := []reviewCall{{"computer_files", `{"action":"files.write","path":"a","content":"X","append":true}`}, {"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"b","content":"X","append":true}}`}}
	_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic old backend"}}, Tools: append(s.microVMTools(r), s.computerTools(r)...)})
	if err != nil || writes != 1 {
		t.Fatalf("writes=%d err=%v", writes, err)
	}
}
