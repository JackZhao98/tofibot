package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func wireReview(st *sayRunState, w *toolWorkRecord) *int {
	spent := new(int)
	st.review = func() bool { *spent++; return *spent == 1 }
	st.reviewSkip = w.reviewSkippable
	st.reviewNote = w.reviewReminder
	return spent
}

func TestReviewSkippedWhenEveryCallSucceededAndDraftClaimsNoExtent(t *testing.T) {
	s, c, r, st := outcomeFixture(t, "")
	var w toolWorkRecord
	w.note(runtime.ToolEvent{Name: "files_write", Status: "completed"})
	spent := wireReview(st, &w)
	if err := sayOutcome(s, c, r, "k1", `{"purpose":"answer","outcome":"done","text":"Saved the note."}`); err != nil {
		t.Fatalf("answer = %v", err)
	}
	if *spent != 0 {
		t.Fatal("the review was spent on a draft with nothing to check")
	}
}

func TestReviewRunsWhenACallFailedAndNamesIt(t *testing.T) {
	s, c, r, st := outcomeFixture(t, "")
	var w toolWorkRecord
	w.note(runtime.ToolEvent{Name: "files_write", Status: "failed"})
	wireReview(st, &w)
	err := sayOutcome(s, c, r, "k1", `{"purpose":"answer","outcome":"done","text":"Saved the note."}`)
	if outcomeErrCode(err) != "answer_review" {
		t.Fatalf("code = %q", outcomeErrCode(err))
	}
	o, _ := tooloutcome.FromError(err)
	if !strings.Contains(o.Message, "files_write") || !strings.Contains(o.Message, "runs once") {
		t.Fatalf("message = %q", o.Message)
	}
}

func TestReviewRunsForFiguresAndQuotesTheSentence(t *testing.T) {
	s, c, r, st := outcomeFixture(t, "")
	var w toolWorkRecord
	w.note(runtime.ToolEvent{Name: "files_write", Status: "completed"})
	wireReview(st, &w)
	err := sayOutcome(s, c, r, "k1", `{"purpose":"answer","outcome":"done","text":"Saved the note. I checked 42 files."}`)
	o, _ := tooloutcome.FromError(err)
	if o.Code != "answer_review" || !strings.Contains(o.Message, "I checked 42 files") || strings.Contains(o.Message, "Saved the note") {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestRestoredToolWorkKeepsTheReview(t *testing.T) {
	var w toolWorkRecord
	w.markRestored()
	if w.reviewSkippable("Done.") {
		t.Fatal("restored work with unknown results must be reviewed")
	}
}
