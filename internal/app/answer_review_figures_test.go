package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// figureReminder stores one tool result for the fixture run and returns the
// reminder built for the draft.
func figureReminder(t *testing.T, result, request, draft string) string {
	t.Helper()
	s, _, r, _ := outcomeFixture(t, "")
	if _, err := s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.ConversationID, r.BotID, r.ID, "c1", "computer_files", result, "completed", "2026-11-16T10:00:00Z", "2026-11-16T10:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if request != "" {
		if _, err := s.store.db.Exec(`UPDATE messages SET content=? WHERE id=?`, request, r.TriggerMessageID); err != nil {
			t.Fatal(err)
		}
	}
	var w toolWorkRecord
	w.note(runtime.ToolEvent{Name: "computer_files", Status: "completed"})
	w.haystack = func() (string, error) { return s.store.reviewHaystack(r.ID) }
	return w.reviewReminder("BASE", draft)
}

const figureBullet = "do not appear in the tool results yet"

func TestFiguresInResultsAreNotListed(t *testing.T) {
	got := figureReminder(t, "rows: 85 fields: 13", "", "The sheet has 85 records and 13 fields.")
	if strings.Contains(got, figureBullet) {
		t.Fatalf("reminder = %q", got)
	}
}

func TestFigureFromAScriptTheResultsDoNotShowIsListed(t *testing.T) {
	got := figureReminder(t, "wrote report.xlsx", "", "Saved it. The sheet has 85 records.")
	if !strings.Contains(got, figureBullet) || !strings.Contains(got, "85 records") || strings.Contains(got, "Saved it") {
		t.Fatalf("reminder = %q", got)
	}
}

func TestFigureFromTheRequestIsNotListed(t *testing.T) {
	got := figureReminder(t, "ok", "Please list the top 25 rows", "Here are the top 25 rows.")
	if strings.Contains(got, figureBullet) {
		t.Fatalf("reminder = %q", got)
	}
}

func TestFigureSeparatorsAndFullWidthDigitsMatch(t *testing.T) {
	for _, c := range []struct{ result, draft string }{
		{"total 1234567", "Total is 1,234,567."},
		{"total 1,234,567", "Total is 1234567."},
		{"total 1_000", "Total is 1\u2009000 items, 1,000 rows."},
		{"share 45", "The share is 45%."},
		{"count 120", "Count is １２０."},
		{"price 3.14", "Price is 3.14."},
	} {
		if got := figureReminder(t, c.result, "", c.draft); strings.Contains(got, figureBullet) {
			t.Fatalf("%q vs %q listed: %q", c.result, c.draft, got)
		}
	}
	if got := figureReminder(t, "total 1234", "", "Total is 1,235."); !strings.Contains(got, figureBullet) {
		t.Fatalf("a different figure matched: %q", got)
	}
}

func TestSmallFiguresAreIgnored(t *testing.T) {
	got := figureReminder(t, "nothing numeric", "", "I found 3 files and 7 notes.")
	if strings.Contains(got, figureBullet) {
		t.Fatalf("reminder = %q", got)
	}
}

func TestReviewTextHoldsTheKeepVerifiedPrinciple(t *testing.T) {
	got := figureReminder(t, "x", "", "It has 85 records.")
	for _, want := range []string{"open or print it", "keep it or correct it", "Verified figures and facts stay", "keep_draft true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if !strings.Contains(replyMatchesRecordPrinciple, "checked in this turn and kept") {
		t.Fatal("principle lacks the keep rule")
	}
}

func TestRestoredRunWithoutResultsKeepsTheOldList(t *testing.T) {
	var w toolWorkRecord
	w.note(runtime.ToolEvent{Name: "computer_files", Status: "completed"})
	w.markRestored()
	got := w.reviewReminder("BASE", "The sheet has 85 records.")
	if !strings.Contains(got, "85 records") {
		t.Fatalf("reminder = %q", got)
	}
}
