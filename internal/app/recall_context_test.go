package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func contextHas(messages []runtime.Message, text string) bool {
	for _, m := range messages {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

// The model must know the conversation reaches back further than the history it
// was given: a trimmed window carries the summary and the note that earlier
// turns can be searched.
func TestTrimmedHistoryKeepsSummaryAndPointsAtSearch(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	var covered int64
	filler := strings.Repeat("synthetic filler sentence. ", 300)
	for i := 0; i < 12; i++ {
		m, _, err := s.store.AddMessage(conv.ID, "user", "", "", fmt.Sprintf("turn %02d %s", i, filler), "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			covered = m.Seq
		}
	}
	if _, err := s.store.SaveSummary(conv.ID, covered, "synthetic summary of the earliest turns"); err != nil {
		t.Fatal(err)
	}
	run := contextSurfaceRun(t, s, conv, bot, "continue")
	messages, _ := s.buildContextParts(conv, run, bot)
	if !contextHas(messages, "synthetic summary of the earliest turns") {
		t.Fatal("summary missing from a trimmed window")
	}
	if !contextHas(messages, earlierTurnsNote) {
		t.Fatal("trimmed window does not say earlier turns exist and can be searched")
	}
	used := 0
	for _, m := range messages {
		used += len([]rune(m.Content))
	}
	if used > maxHistoryRunes {
		t.Fatalf("context %d exceeds the history budget %d", used, maxHistoryRunes)
	}
}

// Without a summary, history that did not fit still leaves the note; a short
// conversation that fits whole does not.
func TestEarlierTurnsNoteOnlyWhenHistoryIsCutOff(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	short, _ := s.buildContextParts(conv, run, bot)
	if contextHas(short, earlierTurnsNote) {
		t.Fatal("note shown although the whole conversation is visible")
	}
	s, bot, conv = contextSurfaceServer(t)
	filler := strings.Repeat("synthetic filler sentence. ", 300)
	for i := 0; i < 12; i++ {
		if _, _, err := s.store.AddMessage(conv.ID, "user", "", "", fmt.Sprintf("turn %02d %s", i, filler), ""); err != nil {
			t.Fatal(err)
		}
	}
	run = contextSurfaceRun(t, s, conv, bot, "continue")
	long, _ := s.buildContextParts(conv, run, bot)
	if !contextHas(long, earlierTurnsNote) {
		t.Fatal("cut-off history without a summary does not say earlier turns exist")
	}
}

func TestMemoryAndRecallGuidanceIsInTheSystemPrompt(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	_, system := s.buildContext(conv, run, bot)
	if !strings.Contains(system, memoryAndRecallGuidance) {
		t.Fatal("memory and recall guidance missing from the system prompt")
	}
}

func TestMemoryAndHistoryToolsSayWhenToUseThem(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	tools := s.tools(conv, run)
	save, _ := toolNamed(tools, "save_memory")
	if !strings.Contains(save.Description, "durable") || !strings.Contains(save.Description, "at the moment you learn it") {
		t.Fatalf("save_memory does not say when to save: %q", save.Description)
	}
	search, _ := toolNamed(tools, "search_history")
	if !strings.Contains(search.Description, "refers to something said earlier") || !strings.Contains(search.Description, "literal text") {
		t.Fatalf("search_history does not say when to use it: %q", search.Description)
	}
}
