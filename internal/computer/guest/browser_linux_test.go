//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestChooseCurrentPageRequiresFocusedVisiblePage(t *testing.T) {
	pages := []browserPage{
		{TargetID: "amazon", URL: "https://amazon.example", Visible: true, Focused: false},
		{TargetID: "google", URL: "https://google.example", Visible: true, Focused: true},
		{TargetID: "worker", URL: "chrome://worker", Visible: true, Focused: false},
	}
	current, source := chooseCurrentPage(pages)
	if current == nil || current.TargetID != "google" || source != "focused" {
		t.Fatalf("current = %#v source=%q, want focused google", current, source)
	}

	pages[1].Focused = false
	current, source = chooseCurrentPage(pages)
	if current != nil || source != "none" {
		t.Fatalf("ambiguous visible pages = %#v source=%q, want no current", current, source)
	}

	pages = pages[:1]
	current, source = chooseCurrentPage(pages)
	if current == nil || current.TargetID != "amazon" || source != "visible-single" {
		t.Fatalf("single visible page = %#v source=%q", current, source)
	}
}

func TestResolveBrowserTargetDoesNotGuessJSONOrder(t *testing.T) {
	_, err := resolveBrowserTarget([]browserPage{
		{TargetID: "a", Visible: true},
		{TargetID: "b", Visible: true},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "specify target_id") {
		t.Fatalf("resolve error = %v, want explicit target guidance", err)
	}
}

func TestCDPEvaluateBrowserStateUnwrapsRuntimeValue(t *testing.T) {
	old := runCDPCommand
	t.Cleanup(func() { runCDPCommand = old })
	runCDPCommand = func(context.Context, string, int, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"result":{"type":"object","value":{"title":"Google","url":"https://google.example/search?q=tofi","visibility_state":"visible","has_focus":true,"ready_state":"complete"}}}`), nil
	}
	state, err := cdpEvaluateBrowserState(context.Background(), "ws://127.0.0.1:9222/devtools/page/1", 9222)
	if err != nil {
		t.Fatal(err)
	}
	if state.Title != "Google" || state.URL == "" || !state.HasFocus || state.ReadyState != "complete" {
		t.Fatalf("state = %#v", state)
	}
}

func TestCDPEvaluateBrowserStateRejectsException(t *testing.T) {
	old := runCDPCommand
	t.Cleanup(func() { runCDPCommand = old })
	runCDPCommand = func(context.Context, string, int, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"result":{"type":"undefined"},"exceptionDetails":{"text":"boom"}}`), nil
	}
	_, err := cdpEvaluateBrowserState(context.Background(), "ws://127.0.0.1:9222/devtools/page/1", 9222)
	if err == nil || !strings.Contains(err.Error(), "exception") {
		t.Fatalf("error = %v, want Runtime.evaluate exception", err)
	}
}

func TestNormalizeScrollContract(t *testing.T) {
	direction, button, units, err := normalizeScroll(" DOWN ", 0)
	if err != nil || direction != "down" || button != "5" || units != 3 {
		t.Fatalf("default scroll = %q %q %d %v", direction, button, units, err)
	}
	if _, _, _, err := normalizeScroll("diagonal", 1); err == nil {
		t.Fatal("invalid direction unexpectedly accepted")
	}
	if _, _, _, err := normalizeScroll("up", 21); err == nil {
		t.Fatal("oversized scroll unexpectedly accepted")
	}
}

func TestNavigationVerificationRejectsOldCompleteDocument(t *testing.T) {
	old := runCDPCommand
	t.Cleanup(func() { runCDPCommand = old })
	runCDPCommand = func(context.Context, string, int, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"frameTree":{"frame":{"loaderId":"previous-document"}}}`), nil
	}
	if browserLoaderMatches(context.Background(), "ws://127.0.0.1:9222/devtools/page/a", 9222, "new-document") {
		t.Fatal("old complete document verified a new navigation")
	}
	if !browserLoaderMatches(context.Background(), "ws://127.0.0.1:9222/devtools/page/a", 9222, "previous-document") {
		t.Fatal("matching loader not recognized")
	}
}
