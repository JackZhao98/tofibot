package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// A scheduled run sees each schedule's real last outcome, so a report cannot
// repeat an old message's "it failed" after a later run succeeded.
func TestScheduledRunSeesScheduleStatesFromRecords(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	server := &Server{store: s, engine: scheduleTestEngine{}, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	server.execute(c, root)
	if got, _ := s.GetRun(root.ID); got.Status != "done" {
		t.Fatalf("fixture run status=%s", got.Status)
	}
	note := server.scheduleStateNote(c, Run{ID: "next", BotID: bot.ID, Kind: runKindSchedule}, time.UTC)
	if !strings.Contains(note, "[schedule states") || !strings.Contains(note, "latest run done at ") || !strings.Contains(note, "recent: ") {
		t.Fatalf("note lacks the recorded outcome:\n%s", note)
	}
	if !strings.Contains(note, trimRunes(strings.Join(strings.Fields(x.Content), " "), 60)) && (x.Title == "" || !strings.Contains(note, x.Title)) {
		t.Fatalf("note does not name the schedule:\n%s", note)
	}
	if other := server.scheduleStateNote(c, Run{ID: "chat", BotID: bot.ID}, time.UTC); other != "" {
		t.Fatalf("ordinary chat run got schedule states: %q", other)
	}
	// The block reaches the model as part of the volatile run note.
	msgs, _ := server.buildContextPartsWith(c, Run{ID: "next2", ConversationID: c.ID, BotID: bot.ID, Kind: runKindSchedule}, bot, nil)
	found := false
	for _, m := range msgs {
		found = found || strings.Contains(m.Content, "[schedule states")
	}
	if !found {
		t.Fatal("schedule states missing from the scheduled run's context")
	}
}
