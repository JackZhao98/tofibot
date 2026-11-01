package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestBrowserNavigateAndActionLeaveTheDesktopObserved(t *testing.T) {
	s, _ := secretTestServer(t)
	a := Run{BotID: "a", ID: "a"}
	if _, err := s.microVMAction(context.Background(), a, "desktop.capture", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ name, args string }{
		{"browser.navigate", `{"url":"https://example.test"}`},
		{"browser.action", `{"action":"switch","target_id":"t1"}`},
		{"browser.action", `{"action":"close","target_id":"t1"}`},
	} {
		if _, err := s.microVMAction(context.Background(), a, step.name, json.RawMessage(step.args)); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
	if !s.desktopObservedFor(a) {
		t.Fatal("navigate and action must leave the observation in place")
	}
	if _, err := s.microVMAction(context.Background(), a, "desktop.stop", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	_, err := s.microVMAction(context.Background(), a, "desktop.click", json.RawMessage(`{"x":1,"y":1}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != tooloutcome.NeedsObservation || o.Certainty != "not_executed" {
		t.Fatalf("stop must clear the observation with a typed refusal, got %v", err)
	}
}
