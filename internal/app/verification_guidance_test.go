package app

import (
	"strings"
	"testing"
)

// A claim presented as checked, current or verified must rest on a lookup made
// in this run. The principle lives in the shared guidance so DMs and groups both
// carry it, and it stays joined to the existing lookup rule.
func TestVerifiedClaimsNeedALookupInDMsAndGroups(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Synthetic", "Role", "model")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateBot("Peer", "Peer role", "model")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("Synthetic group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, conv := range []Conversation{dm, group} {
		run := Run{ID: "verify", BotID: bot.ID, ConversationID: conv.ID}
		if conv.Kind == "group" {
			run.Kind = runKindGroupChat
		}
		_, core := (&Server{store: store}).buildContextParts(conv, run, bot)
		for _, want := range []string{
			"Search unfamiliar names or terms before asking about them, in groups as in DMs",
			"has its identity and current status confirmed by a search before you answer, even when the name seems familiar",
			"any figure or attribution presented as sourced, rests on a page you opened in this run",
			"else say it is from memory; if you cannot search, offer your best reading marked as from memory instead of asking",
		} {
			if !strings.Contains(core, want) {
				t.Fatalf("%s prompt missing %q", conv.Kind, want)
			}
		}
	}
}

// The group framing sits next to the person's latest message, so the lookup
// rule is repeated there and in the unstructured group prompt: the framing must
// not turn an unrecognised name into a question back to the person.
func TestGroupFramingKeepsLookupBeforeAsking(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Synthetic", "Role", "model")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateBot("Peer", "Peer role", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("Synthetic group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err = store.GetConversation(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddMessage(group.ID, "user", "", "", "Zorvath Quillon", "lookup-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(groupLatestMessageLine, "even if the name seems familiar, is searched before you answer or ask") {
		t.Fatal("structured group latest-message line lost the lookup rule")
	}
	pm, _ := (&Server{store: store}).buildContextParts(group, Run{ID: "g1", BotID: bot.ID, ConversationID: group.ID, Kind: runKindGroupChat, structured: true}, bot)
	found := false
	for _, m := range pm {
		found = found || strings.Contains(m.Content, groupLatestMessageLine)
	}
	if !found {
		t.Fatal("structured group turn lacks the latest-message line")
	}
	_, core := (&Server{store: store}).buildContextParts(group, Run{ID: "g2", BotID: bot.ID, ConversationID: group.ID, Kind: runKindGroupChat}, bot)
	if !strings.Contains(core, "Search a name or term you do not recognize, or a named work, product, person or event even if the name seems familiar, before answering or asking.") {
		t.Fatal("unstructured group prompt lost the lookup rule")
	}
}

// The lookup rule sits beside the request in every structured turn that
// answers a person (a DM, an addressed group turn, an unaddressed one), once;
// a scheduled run has no person's message to look a name up from.
func TestLookupRuleSitsBesideTheRequestInEveryStructuredTurn(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Synthetic", "Role", "model")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateBot("Peer", "Peer role", "model")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("Synthetic group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err = store.GetConversation(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conv := range []Conversation{dm, group} {
		if _, _, err := store.AddMessage(conv.ID, "user", "", "", "Zorvath Quillon", "note-"+conv.ID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(conv Conversation, run Run) int {
		pm, _ := (&Server{store: store}).buildContextParts(conv, run, bot)
		n := 0
		for _, m := range pm {
			n += strings.Count(m.Content, lookupNoteLine)
		}
		return n
	}
	for name, tc := range map[string]struct {
		conv Conversation
		run  Run
		want int
	}{
		"dm":              {dm, Run{ID: "n1", BotID: bot.ID, ConversationID: dm.ID, structured: true}, 1},
		"group mention":   {group, Run{ID: "n2", BotID: bot.ID, ConversationID: group.ID, Kind: runKindGroupMention, structured: true}, 1},
		"group unaddress": {group, Run{ID: "n3", BotID: bot.ID, ConversationID: group.ID, Kind: runKindGroupChat, structured: true}, 1},
		"scheduled":       {dm, Run{ID: "n4", BotID: bot.ID, ConversationID: dm.ID, Kind: runKindSchedule, structured: true}, 0},
		"not structured":  {dm, Run{ID: "n5", BotID: bot.ID, ConversationID: dm.ID}, 0},
	} {
		if got := count(tc.conv, tc.run); got != tc.want {
			t.Errorf("%s: the lookup rule appears %d times beside the request, want %d", name, got, tc.want)
		}
	}
}
