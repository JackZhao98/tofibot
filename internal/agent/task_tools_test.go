package agent

import "testing"

// The only human-input mechanism is the app's durable question rendezvous.
// The agent package must not carry a second, callback-based ask tool.
func TestTaskToolsDoNotIncludeAnAskUserTool(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range buildTaskTools(NewBackgroundTaskManager()) {
		names[tool.Schema().Name] = true
	}
	for _, want := range []string{"tofi_task_status", "tofi_task_list", "tofi_task_stop"} {
		if !names[want] {
			t.Errorf("missing task tool %s: %v", want, names)
		}
	}
	for name := range names {
		if name == "tofi_ask_user" {
			t.Fatalf("callback-based ask tool is registered: %v", names)
		}
	}
}
