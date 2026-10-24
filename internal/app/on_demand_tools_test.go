package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func declaredBytes(tools []Tool) (n int) {
	for _, tl := range tools {
		if tl.Deferred {
			continue
		}
		e, _ := json.Marshal(map[string]any{"name": tl.Name, "description": tl.Description, "parameters": tl.Parameters})
		n += len(e)
	}
	return n
}

func TestOnDemandToolsLeaveTheDeclaredList(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "m", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("A", "", "m")
	c, _ := s.store.GetConversation(b.DMConversationID)
	run := Run{ID: "r", ConversationID: c.ID, BotID: b.ID, Status: "running"}
	tools := s.tools(c, run)
	before := declaredBytes(tools)
	tools = append(tools, s.sayTool(c, run))
	tools = deferOnDemandTools(tools)
	capability := capabilityTool(tools)
	after := declaredBytes(tools)
	t.Logf("declared schema bytes: %d -> %d (%.0f%% less)", before, after, 100*float64(before-after)/float64(before))
	if after > before*80/100 {
		t.Fatalf("on-demand tools should cut the declared schemas by at least a fifth: %d -> %d", before, after)
	}
	deferred := map[string]bool{}
	for _, tl := range tools {
		if tl.Deferred {
			deferred[tl.Name] = true
		}
		// Tools that a prompt or guide names, and the everyday core, stay declared.
		switch tl.Name {
		case "say", "save_memory", "search_history", "create_schedule", "update_schedule", "prepare_email", "ask_user_question", "list_bots", "list_work_items":
			if tl.Deferred {
				t.Fatalf("%s must stay declared", tl.Name)
			}
		}
	}
	// Decision 0059: message is an alias of delegate, kept one version. It runs
	// when called but does not take a place in the declared list.
	for _, tl := range tools {
		if tl.Name == "message" && !tl.Deferred {
			t.Error("message is an alias of delegate and must stay off the declared list")
		}
	}
	for name := range onDemandTools {
		if !deferred[name] {
			t.Errorf("%s is listed on demand but not present as a deferred tool", name)
		}
	}
	// The inventory still lists them, marked, so the model can find them.
	out, err := capability.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name":"workspace_delete_bot"`) || !strings.Contains(out, `"on_demand":true`) {
		t.Fatalf("inventory = %s", out)
	}
}

func declaredInBody(t *testing.T, body string) map[string]bool {
	t.Helper()
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range req.Tools {
		out[tl.Function.Name] = true
	}
	return out
}

// Naming an on-demand tool to workspace_capabilities makes it callable on the
// model's next call, and a run that never asks never carries it.
func TestWorkspaceCapabilitiesLoadsAnOnDemandToolForTheRun(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "workspace_capabilities", `{"names":["workspace_list"]}`}}},
		fakeStep{calls: []fakeCall{{"c2", "workspace_list", `{}`}}},
		fakeStep{calls: []fakeCall{{"c3", "say", `{"purpose":"answer","outcome":"done","text":"Listed."}`}}})
	s.engine = model.engine(t)
	s.enqueue(c, old)
	if got := waitRunTerminal(t, s, old.ID); got.Status != "done" {
		t.Fatalf("run = %+v", got)
	}
	if len(model.bodies) < 3 {
		t.Fatalf("model calls = %d", len(model.bodies))
	}
	first, second := declaredInBody(t, model.bodies[0]), declaredInBody(t, model.bodies[1])
	if first["workspace_list"] || !first["workspace_capabilities"] {
		t.Fatalf("first call declared on-demand tool: %v", first)
	}
	if !second["workspace_list"] {
		t.Fatalf("second call did not declare the loaded tool: %v", second)
	}
	var activity string
	_ = s.store.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND name='workspace_list'`, old.ID).Scan(&activity)
	if activity != "completed" {
		t.Fatalf("workspace_list activity = %q", activity)
	}
}
