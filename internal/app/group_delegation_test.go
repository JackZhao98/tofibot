package app

import (
	"github.com/JackZhao98/tofibot/internal/runtime"
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
	for _, keep := range []string{"fully own stays with that member", "merely be nice to have is no reason", "mall talk is answered directly"} {
		if !strings.Contains(groupDelegationGuidance, keep) {
			t.Errorf("over-delegation guard missing %q", keep)
		}
	}
}

func contextTexts(msgs []runtime.Message, system string) string {
	var b strings.Builder
	b.WriteString(system)
	for _, m := range msgs {
		b.WriteString("\n" + m.Content)
	}
	return b.String()
}

func TestHandoffContributionsAreAssembledIntoOneResult(t *testing.T) {
	f := newGroupReturnFixture(t)
	defer f.store.Close()
	srv := &Server{store: f.store}
	msgs, system := srv.buildContext(f.group, f.child, f.b)
	child := contextTexts(msgs, system)
	if !strings.Contains(child, "Post your contribution once, complete") || strings.Contains(child, "Post your actual findings") {
		t.Fatalf("colleague run is not told to post its contribution once: %s", child)
	}
	finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
	finishGroupRun(t, f.store, f.child, f.b, "B result")
	follow := followupFor(t, f.store, f.child.ID)
	msgs, system = srv.buildContext(f.group, follow, f.a)
	resumed := contextTexts(msgs, system)
	if !strings.Contains(resumed, "publish the assembled outcome once") {
		t.Fatalf("coordinator is not told to publish one assembled outcome: %s", resumed)
	}
}
