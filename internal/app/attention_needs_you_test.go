package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func saveFixtureWait(t *testing.T, f *autoReviewFixture, questionID string) {
	t.Helper()
	if err := f.s.store.SaveInputContinuation(context.Background(), f.r.ID, questionID, json.RawMessage(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
}

// runGateWithReviewer runs the MCP gate under auto mode with a reviewer that
// answers with the given decision.
func runGateWithReviewer(t *testing.T, f *autoReviewFixture, decision string) chan error {
	t.Helper()
	setSyntheticMCPEffect(t, f)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	f.p.reply = func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return reviewReply(req, decision), nil
	}
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	return done
}

// An approval the automatic reviewer approves never needs a person.
func TestAutoApprovedApprovalRecordsNoNeedsYou(t *testing.T) {
	f := newAutoReviewFixture(t)
	done := runGateWithReviewer(t, f, "allow")
	q := waitReviewQuestion(t, f, "approved")
	if err := waitReviewDone(t, done); err != nil {
		t.Fatal(err)
	}
	saveFixtureWait(t, f, q.ID)
	wantKinds(t, f.s.store)
}

// A reviewer that sends the card to a person makes exactly one row.
func TestEscalatedApprovalRecordsOneNeedsYou(t *testing.T) {
	f := newAutoReviewFixture(t)
	done := runGateWithReviewer(t, f, "needs_human")
	q := waitReviewQuestion(t, f, "human_required")
	wantKinds(t, f.s.store) // the run has not stopped yet
	saveFixtureWait(t, f, q.ID)
	wantKinds(t, f.s.store, attentionNeedsYou)
	row := attentionRows(t, f.s.store)[0]
	if row.Conv != f.c.ID || row.Bot != f.r.BotID || row.Seq != nil {
		t.Fatalf("row=%+v", row)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
	wantKinds(t, f.s.store, attentionNeedsYou)
}

// While an automatic review is still deciding the wait records nothing; the
// verdict that leaves the card pending records it, once.
func TestNeedsYouWaitsForTheReviewVerdict(t *testing.T) {
	f := newAutoReviewFixture(t)
	done := runGateWithReviewer(t, f, "needs_human")
	q := waitReviewQuestion(t, f, "human_required")
	if _, err := f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET status='reviewing' WHERE question_id=?`, q.ID); err != nil {
		t.Fatal(err)
	}
	saveFixtureWait(t, f, q.ID)
	wantKinds(t, f.s.store)
	if _, err := f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET status='human_required' WHERE question_id=?`, q.ID); err != nil {
		t.Fatal(err)
	}
	record := func() {
		t.Helper()
		tx, err := f.s.store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = recordNeedsYouTx(tx, f.r.ID, q.ID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	record()
	wantKinds(t, f.s.store, attentionNeedsYou)
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	record() // an answered card needs nobody
	_ = waitReviewDone(t, done)
	wantKinds(t, f.s.store, attentionNeedsYou)
}

// Shadow review only advises: a person decides every card, so the wait itself is the row.
func TestShadowModeApprovalRecordsNeedsYouAtOnce(t *testing.T) {
	f := newAutoReviewFixture(t)
	setSyntheticMCPEffect(t, f)
	if err := f.s.store.putAutoReviewMode("shadow"); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	f.p.reply = func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		<-gate
		return reviewReply(req, "allow"), nil
	}
	defer close(gate)
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := waitReviewQuestion(t, f, "shadow_reviewing")
	saveFixtureWait(t, f, q.ID)
	wantKinds(t, f.s.store, attentionNeedsYou)
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
}

func TestQuestionCardRecordsNeedsYou(t *testing.T) {
	s, c, r := questionFixtureDir(t, t.TempDir())
	t.Cleanup(func() { s.Close() })
	q, err := s.CreateQuestion(c.ID, r, askQuestionInput{Type: questionText, Question: "Which name?"})
	if err != nil {
		t.Fatal(err)
	}
	wantKinds(t, s)
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	wantKinds(t, s, attentionNeedsYou)
}
