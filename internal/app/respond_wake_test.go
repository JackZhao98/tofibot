package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestWakeWithANonReplyDispositionNamesTheDispositionToUse(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.post(t, "@tofi go", f.tofi)
	run := f.roots[0]
	run.structured = false
	tools := f.server.groupRespondTools(f.group, run)
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	schema, _ := json.Marshal(tools[0].Parameters)
	if !strings.Contains(string(schema), "Only with disposition reply") {
		t.Fatalf("wake is undescribed in the schema: %s", schema)
	}
	args, _ := json.Marshal(map[string]any{"disposition": "silent", "wake": wakeOf(f.meta.ID, "please look")})
	_, err := tools[0].Execute(context.Background(), args)
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "wake_requires_reply" || o.NextAction != "repair_arguments" {
		t.Fatalf("refusal = %v", err)
	}
	for _, want := range []string{"disposition reply", "same wake list", "disposition silent and no wake"} {
		if !strings.Contains(o.Message, want) {
			t.Fatalf("message lacks %q: %q", want, o.Message)
		}
	}
}
