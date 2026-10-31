package guest

import (
	"encoding/json"
	"strings"
	"testing"
)

const ocuSettingsTree = "App=pcmanfm\n0 window Preferences\n 1 page tab list \n  2 page tab General\n   3 check box Show hidden files\n   4 check box Use trash can\n   5 radio button Small icons\n   6 toggle button Pin sidebar\n   7 push button Close"

func TestCheckStatesAreAppendedToCheckableLines(t *testing.T) {
	states := []ocuCheckState{
		{Role: "check box", Name: "Show hidden files", State: "checked"},
		{Role: "check box", Name: "Use trash can", State: "unchecked"},
		{Role: "radio button", Name: "Small icons", State: "mixed"},
		{Role: "toggle button", Name: "Pin sidebar", State: "checked"},
	}
	out, ok := annotateCheckStates(ocuSettingsTree, states)
	if !ok {
		t.Fatal("matching states were not applied")
	}
	for _, want := range []string{
		"3 check box Show hidden files [checked]",
		"4 check box Use trash can [unchecked]",
		"5 radio button Small icons [mixed]",
		"6 toggle button Pin sidebar [checked]",
		"7 push button Close\n",
	} {
		if !strings.Contains(out+"\n", want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Close [") || strings.Contains(out, "2 page tab General [") {
		t.Fatal("a line that is not checkable got a state")
	}
	if !hasCheckable(ocuSettingsTree) || hasCheckable("App=x\n0 window Files\n 1 push button Home") {
		t.Fatal("hasCheckable is wrong")
	}
}

func TestCheckStatesThatDoNotMatchTheTreeAreNotApplied(t *testing.T) {
	// One state too few: another window or a changed app. Nothing is guessed.
	few := []ocuCheckState{{Name: "Show hidden files", State: "checked"}}
	if out, ok := annotateCheckStates(ocuSettingsTree, few); ok || out != ocuSettingsTree {
		t.Fatal("a count mismatch must leave the tree as it is")
	}
	wrong := []ocuCheckState{
		{Name: "Something else", State: "checked"}, {Name: "Use trash can", State: "checked"},
		{Name: "Small icons", State: "checked"}, {Name: "Pin sidebar", State: "checked"},
	}
	if out, ok := annotateCheckStates(ocuSettingsTree, wrong); ok || out != ocuSettingsTree {
		t.Fatal("a name mismatch must leave the tree as it is")
	}
	bad := []ocuCheckState{{Name: "Show hidden files", State: "on"}, {Name: "Use trash can", State: "on"}, {Name: "Small icons", State: "on"}, {Name: "Pin sidebar", State: "on"}}
	if _, ok := annotateCheckStates(ocuSettingsTree, bad); ok {
		t.Fatal("an unknown state word was applied")
	}
}

func TestCheckStatesReachTheModelThroughTheResultShape(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":` + string(mustJSON(t, ocuSettingsTree)) + `}]}`)
	res, err := ocuResult("get_app_state", raw, func(text string) string {
		out, _ := annotateCheckStates(text, []ocuCheckState{
			{Name: "Show hidden files", State: "checked"}, {Name: "Use trash can", State: "unchecked"},
			{Name: "Small icons", State: "unchecked"}, {Name: "Pin sidebar", State: "unchecked"},
		})
		return out
	})
	if err != nil || !strings.Contains(res["text"].(string), "Show hidden files [checked]") {
		t.Fatalf("%v %v", res, err)
	}
	// list_apps is not a tree and is never shaped.
	res, _ = ocuResult("list_apps", json.RawMessage(`{"content":[{"type":"text","text":"1 check box x"}]}`), func(string) string { return "shaped" })
	if res["text"] != "1 check box x" {
		t.Fatalf("list_apps was shaped: %v", res["text"])
	}
}
