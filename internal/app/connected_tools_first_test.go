package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The mail guide names the order: a connected mail tool first, the browser only as the fallback.
func TestMailGuideNamesConnectionFirstOrder(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"topic": "mail"})
	text, err := workflowGuideTool().Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(text, "Email in the browser") {
		t.Fatal("mail guide still opens with the browser path")
	}
	connected := strings.Index(text, "Connected tools come first")
	search := strings.Index(text, "search_mcp_tools")
	notAuthorized := strings.Index(text, "not connected, not authorized or expired")
	fallback := strings.Index(text, "is the fallback")
	browser := strings.Index(text, "browser.read")
	for name, at := range map[string]int{"connected": connected, "search": search, "not authorized": notAuthorized, "fallback": fallback, "browser": browser} {
		if at < 0 {
			t.Fatalf("mail guide lacks the %s step:\n%s", name, text)
		}
	}
	if !(connected < search && search < notAuthorized && notAuthorized < fallback && fallback < browser) {
		t.Fatalf("mail guide steps are out of order: %d %d %d %d %d", connected, search, notAuthorized, fallback, browser)
	}
	if !strings.Contains(text, "Settings > Connections") || !strings.Contains(text, "offer to continue") {
		t.Fatal("mail guide does not tell the Bot to name the reconnect page and offer to continue")
	}
	web, _ := workflowGuideTool().Execute(context.Background(), json.RawMessage(`{"topic":"web_tasks"}`))
	if !strings.HasPrefix(strings.SplitN(web, "\n", 2)[1], "When a connected tool covers the task") {
		t.Fatalf("web task guide does not defer to connected tools: %s", web)
	}
}

// The computer tools state that the computer is the fallback.
func TestComputerIsTheFallbackAfterConnectedTools(t *testing.T) {
	for _, want := range []string{"no connected tool fits", "not connected or authorized", "asks for the computer"} {
		if !strings.Contains(computerFallbackNote, want) {
			t.Fatalf("fallback note lacks %q: %s", want, computerFallbackNote)
		}
	}
}
