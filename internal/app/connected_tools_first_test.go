package app

import (
	"strings"
	"testing"
)

// The email Skill names the order: a connected mail tool first, the browser only as the fallback.
func TestEmailSkillNamesConnectionFirstOrder(t *testing.T) {
	text := builtinSkillBody(t, "email")
	connected := strings.Index(text, "Connected tools come first")
	search := strings.Index(text, "search_mcp_tools")
	notAuthorized := strings.Index(text, "not connected, not authorized or expired")
	fallback := strings.Index(text, "is the fallback")
	reference := strings.Index(text, "references/gmail-browser.md")
	for name, at := range map[string]int{"connected": connected, "search": search, "not authorized": notAuthorized, "fallback": fallback, "reference": reference} {
		if at < 0 {
			t.Fatalf("email skill lacks the %s part:\n%s", name, text)
		}
	}
	if !(connected < search && search < notAuthorized && notAuthorized < fallback && fallback < reference) {
		t.Fatalf("email skill parts are out of order: %d %d %d %d %d", connected, search, notAuthorized, fallback, reference)
	}
	if !strings.Contains(text, "when the run has search_mcp_tools") && !strings.Contains(text, "When the run has search_mcp_tools") {
		t.Fatal("email skill names search_mcp_tools without the run condition")
	}
	if !strings.Contains(text, "Settings > Connections") || !strings.Contains(text, "offer to continue") {
		t.Fatal("email skill does not tell the Bot to name the reconnect page and offer to continue")
	}
	web := builtinSkillBody(t, "web-tasks")
	if !strings.HasPrefix(strings.SplitN(web, "\n", 3)[2], "## Route\n\n- When a connected tool covers the task") {
		t.Fatalf("web-tasks skill does not defer to connected tools: %s", web)
	}
}

// The computer tools state that the computer is the fallback.
func TestComputerIsTheFallbackAfterConnectedTools(t *testing.T) {
	for _, want := range []string{"no connected tool fits", "not connected or authorized", "asks for it"} {
		if !strings.Contains(computerFallbackNote, want) {
			t.Fatalf("fallback note lacks %q: %s", want, computerFallbackNote)
		}
	}
}
