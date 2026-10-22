package app

import (
	"strings"
	"testing"
)

func groupDelegationSystem(t *testing.T, kind string, structured, dm bool) (string, string) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := store.CreateBot("Writer", "You write clear announcement copy for the team.", "model")
	if err != nil {
		t.Fatal(err)
	}
	designer, err := store.CreateBot("Designer", "You make the visual that goes with a piece of copy.\n\nYou hand over finished image files.", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("Team", []string{writer.ID, designer.ID})
	if err != nil {
		t.Fatal(err)
	}
	conv := group
	if dm {
		conv, err = store.GetConversation(writer.DMConversationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	run := Run{ID: "r", BotID: writer.ID, ConversationID: conv.ID, Kind: kind, structured: structured}
	_, core := (&Server{store: store}).buildContextParts(conv, run, writer)
	return core, designer.ID
}

func TestGroupRosterCarriesEachColleaguesRole(t *testing.T) {
	core, designerID := groupDelegationSystem(t, runKindGroupChat, true, false)
	for _, want := range []string{
		"Designer (" + designerID + "): You make the visual that goes with a piece of copy. You hand over finished image files.",
		"Writer (", "[you]: You write clear announcement copy",
	} {
		if !strings.Contains(core, want) {
			t.Fatalf("roster missing %q in %s", want, core)
		}
	}
	if long := memberRoleLine(strings.Repeat("x", 500)); len([]rune(long)) > 140 {
		t.Fatalf("role line not bounded: %d", len([]rune(long)))
	}
}

func TestDelegationPrincipleInGroupTurnsNotDMs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		structured bool
	}{
		{"unaddressed-structured", runKindGroupChat, true},
		{"unaddressed-plain", runKindGroupChat, false},
		{"addressed-structured", "", true},
		{"addressed-plain", "", false},
	} {
		core, _ := groupDelegationSystem(t, tc.kind, tc.structured, false)
		if !strings.Contains(core, groupDelegationGuidance) {
			t.Errorf("%s: delegation principle missing", tc.name)
		}
	}
	dm, _ := groupDelegationSystem(t, "", true, true)
	if strings.Contains(dm, groupDelegationGuidance) || strings.Contains(dm, "Group members:") {
		t.Error("DM carries the group delegation principle")
	}
	for _, banned := range []string{"for example", "e.g.", " not "} {
		if strings.Contains(strings.ToLower(groupDelegationGuidance), banned) {
			t.Errorf("principle holds an example or contrast: %q", banned)
		}
	}
	for _, keep := range []string{"fully own stays with that member", "small talk is answered directly"} {
		if !strings.Contains(groupDelegationGuidance, keep) {
			t.Errorf("over-delegation guard missing %q", keep)
		}
	}
}
