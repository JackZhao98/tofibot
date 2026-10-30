package app

import (
	"strings"
	"testing"
)

func TestConversationPreviewIsPlainText(t *testing.T) {
	cases := map[string]string{
		"Open [Connections](/settings/connections) to **sign in**, then `retry`.": "Open Connections to sign in, then retry.",
		"## Done\n- _item_ one\n- two":                                           "Done - item one - two",
		"See ![chart](http://x/y.png) and [docs](http://x":                       "See chart and docs",
		"```go\nfmt.Println()\n```":                                              "go fmt.Println()",
		"snake_case stays":                                                       "snake_case stays",
	}
	for in, want := range cases {
		if got := plainPreview(in, 160); got != want {
			t.Errorf("plainPreview(%q) = %q, want %q", in, got, want)
		}
	}
	// A link cut by the old 160-character window still reads as its label.
	long := strings.Repeat("x", 140) + " Open [Connections](/settings/connections-and-more-and-more)"
	if got := plainPreview(long, 160); strings.Contains(got, "](") || strings.Contains(got, "[") {
		t.Fatalf("link survived: %q", got)
	}
}

func TestListConversationsPreviewStripsMarkdown(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b, err := store.CreateBot("first", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AddMessage(b.DMConversationID, "assistant", b.ID, "run", "Open [Connections](/settings/connections) now", "m1"); err != nil {
		t.Fatal(err)
	}
	conversations, err := store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.ID == b.DMConversationID && (c.LastMessage == nil || c.LastMessage.Content != "Open Connections now") {
			t.Fatalf("preview = %+v", c.LastMessage)
		}
	}
}
