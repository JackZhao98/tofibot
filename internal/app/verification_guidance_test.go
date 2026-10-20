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
			"check a named work, product, person or event that could postdate your knowledge before you answer about it",
			"if you cannot search, offer your best reading marked as from memory instead of asking",
			"must rest on a lookup made in this run",
			"otherwise look it up or say it is from memory",
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
	if !strings.Contains(groupLatestMessageLine, "searched before you answer or ask") {
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
	if !strings.Contains(core, "Search a name or term you do not recognize, or a named work, product, person or event that could postdate your knowledge, before answering or asking.") {
		t.Fatal("unstructured group prompt lost the lookup rule")
	}
}
