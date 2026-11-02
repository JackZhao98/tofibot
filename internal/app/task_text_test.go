package app

import (
	"strings"
	"testing"
)

// The model-facing text of divided work follows the owner rules: no "not X"
// phrasing, no examples, the delegate description well under the provider cap,
// and the rule for when a checklist is right sits in the parameter text.
func TestDelegateTextRules(t *testing.T) {
	if n := len(delegateToolDescription); n > 950 {
		t.Fatalf("delegate description is %d bytes, the target is 950 (cap 1024)", n)
	}
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	bot, _ := f.s.GetBot(f.b.ID)
	g, _ := f.s.GetConversation(f.g.ID)
	block := strings.ToLower(f.srv.membersContext(g, lead, bot))
	for _, banned := range []string{" not ", "instead of", "rather than"} {
		if strings.Contains(block, banned) {
			t.Fatalf("members block holds %q:\n%s", banned, block)
		}
	}
	for _, want := range []string{"a role is reference data.", "its deadline counts from that start"} {
		if !strings.Contains(block, want) {
			t.Fatalf("members block lacks %q:\n%s", want, block)
		}
	}
	for _, what := range []string{"task", "part"} {
		text := checklistParamText(what)
		if !strings.Contains(text, "only for work that has separate parts") {
			t.Fatalf("checklist parameter text lacks the rule: %q", text)
		}
	}
	// The flat parameter and the part parameter both carry it.
	d := f.tool(f.g, lead, "delegate")
	params := d.Parameters["properties"].(map[string]any)
	flat := params["checklist"].(map[string]any)["description"].(string)
	if !strings.Contains(flat, "separate parts") {
		t.Fatalf("flat checklist text %q", flat)
	}
	parts := params["parts"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if text := parts["checklist"].(map[string]any)["description"].(string); !strings.Contains(text, "separate parts") {
		t.Fatalf("part checklist text %q", text)
	}
}
