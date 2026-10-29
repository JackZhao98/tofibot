package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityQueryFindsScheduleTools(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "m", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("A", "", "m")
	c, _ := s.store.GetConversation(b.DMConversationID)
	run := Run{ID: "r", ConversationID: c.ID, BotID: b.ID, Status: "running"}
	tools := deferOnDemandTools(append(s.tools(c, run), s.sayTool(c, run)))
	out, err := capabilityTool(tools).Execute(context.Background(), []byte(`{"query":"schedule list cancel pause automation"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Schemas []struct {
			Name string `json:"name"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sc := range got.Schemas {
		names = append(names, sc.Name)
	}
	for _, want := range []string{"list_schedules", "pause_schedule", "delete_schedule"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s missing from %v", want, names)
		}
	}
	for i, n := range names {
		if i < 3 && !strings.Contains(n, "schedule") {
			t.Errorf("schedule tools should rank first: %v", names)
		}
	}
}
