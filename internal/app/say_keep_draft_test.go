package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func keptAnswerTexts(t *testing.T, s *Server, runID string) []string {
	t.Helper()
	rows, err := s.store.db.Query(`SELECT text FROM run_say_answers WHERE run_id=?`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	return out
}

func TestKeepDraftSendsTheReviewedDraftWithItsAttachments(t *testing.T) {
	s, c, run, state := outcomeFixture(t, "")
	spent := false
	state.review = func() bool { r := !spent; spent = true; return r }
	up, err := s.store.AddAttachment(c.ID, "notes.txt", "text/plain", strings.NewReader("synthetic notes"))
	if err != nil {
		t.Fatal(err)
	}
	draft := `{"purpose":"answer","outcome":"done","text":"Synthetic draft answer.","attachments":[{"attachment_id":"` + up.ID + `"}]}`
	if _, err := sayCall(s, c, run, "k1", draft); outcomeErrCode(err) != "answer_review" {
		t.Fatalf("first send: %v", err)
	}
	out, err := sayCall(s, c, run, "k2", `{"purpose":"answer","outcome":"done","text":"","keep_draft":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "notes.txt") {
		t.Fatalf("result %q", out)
	}
	if got := keptAnswerTexts(t, s, run.ID); len(got) != 1 || got[0] != "Synthetic draft answer." {
		t.Fatalf("published %v", got)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 1 || names[0] != "notes.txt" {
		t.Fatalf("attachments %v", names)
	}
	// Used once: a second keep_draft has nothing to send, and nothing more is published.
	_, err = sayCall(s, c, run, "k3", `{"purpose":"answer","outcome":"done","text":"","keep_draft":true}`)
	if code := outcomeErrCode(err); code != "keep_draft_unavailable" {
		t.Fatalf("second keep_draft code %q", code)
	}
	if got := keptAnswerTexts(t, s, run.ID); len(got) != 1 {
		t.Fatalf("published %v", got)
	}
}

func TestKeepDraftWithoutAKeptDraftIsRefused(t *testing.T) {
	s, c, run, _ := outcomeFixture(t, "")
	_, err := sayCall(s, c, run, "k1", `{"purpose":"answer","outcome":"done","text":"","keep_draft":true}`)
	if code := outcomeErrCode(err); code != "keep_draft_unavailable" {
		t.Fatalf("code %q err %v", code, err)
	}
	o, _ := tooloutcome.FromError(err)
	if o.Message != "No reviewed draft is waiting; send the answer with its text." {
		t.Fatalf("message %q", o.Message)
	}
	if got := keptAnswerTexts(t, s, run.ID); len(got) != 0 {
		t.Fatalf("published %v", got)
	}
}

func TestCorrectedAnswerAfterReviewStillWorks(t *testing.T) {
	s, c, run, state := outcomeFixture(t, "")
	spent := false
	state.review = func() bool { r := !spent; spent = true; return r }
	if _, err := sayCall(s, c, run, "k1", `{"purpose":"answer","outcome":"done","text":"First draft."}`); outcomeErrCode(err) != "answer_review" {
		t.Fatalf("first send: %v", err)
	}
	if _, err := sayCall(s, c, run, "k2", `{"purpose":"answer","outcome":"done","text":"Corrected answer."}`); err != nil {
		t.Fatal(err)
	}
	if got := keptAnswerTexts(t, s, run.ID); len(got) != 1 || got[0] != "Corrected answer." {
		t.Fatalf("published %v", got)
	}
}

func TestReviewReminderHoldsTheKeepDraftRoute(t *testing.T) {
	var w toolWorkRecord
	got := w.reviewReminder("BASE", "Short.")
	for _, want := range []string{"keep_draft true", "empty text", "corrected answer with its text", "Verified figures and facts stay"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reminder lacks %q: %s", want, got)
		}
	}
}
