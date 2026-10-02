package guest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTimezoneSetGetAndCleanEnv(t *testing.T) {
	root := t.TempDir()
	s, err := NewWithIdleTimeout(root, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	bot := "11111111-1111-1111-1111-111111111111"
	result, err := s.action(context.Background(), ActionRequest{BotID: bot, RunID: "timezone-test", Action: "timezone.get", Args: []byte(`{}`)})
	if err != nil || result.(map[string]any)["configured"] != false {
		t.Fatalf("initial timezone=%#v err=%v", result, err)
	}
	result, err = s.action(context.Background(), ActionRequest{BotID: bot, RunID: "timezone-test", Action: "timezone.set", Args: []byte(`{"timezone":"America/Los_Angeles"}`)})
	if err != nil || result.(map[string]any)["timezone"] != "America/Los_Angeles" {
		t.Fatalf("set timezone=%#v err=%v", result, err)
	}
	env := cleanEnv(root, filepath.Join(root, "bots", bot))
	found := false
	for _, item := range env {
		if item == "TZ=America/Los_Angeles" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clean env missing TZ: %v", env)
	}
	if _, err := os.Stat(filepath.Join(root, guestTimezoneFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.action(context.Background(), ActionRequest{BotID: bot, RunID: "timezone-test", Action: "timezone.set", Args: []byte(`{"timezone":"Not/IANA"}`)}); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}
