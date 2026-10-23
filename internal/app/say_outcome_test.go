package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// outcomeFixture is a claimed run with a registered say state, driven through
// the say tool directly.
func outcomeFixture(t *testing.T, kind string) (*Server, Conversation, Run, *sayRunState) {
	t.Helper()
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	c, _ := s.store.GetConversation(run.ConversationID)
	bot, _ := s.store.GetBot(run.BotID)
	_, live, _, err := s.store.AddUserRun(c.ID, bot.ID, "synthetic request", "outcome-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), live.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live.structured = true
	live.Kind = kind
	st := &sayRunState{checksPending: !saySendsSources(live)}
	s.sayRuns.Store(live.ID, st)
	return s, c, live, st
}

func sayOutcome(s *Server, c Conversation, r Run, callID, args string) error {
	_, err := s.sayTool(c, r).Execute(runtime.WithToolCallID(context.Background(), callID), json.RawMessage(args))
	return err
}

func outcomeErrCode(err error) string {
	if err == nil {
		return ""
	}
	if o, ok := tooloutcome.FromError(err); ok {
		return o.Code
	}
	return err.Error()
}

func TestPendingAnswerIsSentBackOnceThenAccepted(t *testing.T) {
	s, c, r, _ := outcomeFixture(t, "")
	args := `{"purpose":"answer","outcome":"pending","text":"Sounds good, ready to start."}`
	if code := outcomeErrCode(sayOutcome(s, c, r, "p1", args)); code != "answer_pending_work" {
		t.Fatalf("first pending answer code = %q", code)
	}
	if _, ok := s.store.sayAnswer(r.ID); ok {
		t.Fatal("a refused answer was recorded")
	}
	if err := sayOutcome(s, c, r, "p2", args); err != nil {
		t.Fatalf("identical re-send: %v", err)
	}
	if _, ok := s.store.sayAnswer(r.ID); !ok {
		t.Fatal("accepted answer not recorded")
	}
}

func TestDoneBlockedAndMissingOutcomeAreAccepted(t *testing.T) {
	for _, args := range []string{
		`{"purpose":"answer","outcome":"done","text":"All finished."}`,
		`{"purpose":"answer","outcome":"blocked","text":"The key is missing, so I stopped."}`,
		`{"purpose":"answer","text":"No outcome given."}`,
	} {
		s, c, r, st := outcomeFixture(t, "")
		if err := sayOutcome(s, c, r, "d1", args); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if st.pendingCheck.Load() {
			t.Fatalf("%s spent the pending check", args)
		}
	}
}

func TestDelegatedNeedsAWakeOrAHandoff(t *testing.T) {
	args := `{"purpose":"answer","outcome":"delegated","text":"Handing the rest over."}`
	s, c, r, _ := outcomeFixture(t, "")
	if code := outcomeErrCode(sayOutcome(s, c, r, "g1", args)); code != "answer_pending_work" {
		t.Fatalf("delegated without wake or handoff code = %q", code)
	}

	s, c, r, _ = outcomeFixture(t, "")
	c.Kind, c.UserVisible, c.BotIDs = "group", true, []string{r.BotID, "other-member"}
	wake := `{"purpose":"answer","outcome":"delegated","text":"Over to you.","wake":[{"bot_id":"other-member","request":"carry it out"}]}`
	if err := sayOutcome(s, c, r, "g2", wake); err != nil {
		t.Fatalf("delegated with wake: %v", err)
	}

	s, c, r, _ = outcomeFixture(t, "")
	if _, err := s.store.db.Exec(`UPDATE runs SET handoff_count=1 WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := sayOutcome(s, c, r, "g3", args); err != nil {
		t.Fatalf("delegated with handoff: %v", err)
	}
}

func TestPendingAnswerAfterSchedulingIsAccepted(t *testing.T) {
	for _, name := range []string{"create_schedule", "update_schedule"} {
		s, c, r, _ := outcomeFixture(t, "")
		if _, err := s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,'sch',?,'completed','2026-09-29T11:01:00Z','2026-09-29T11:01:00Z')`, c.ID, r.BotID, r.ID, name); err != nil {
			t.Fatal(err)
		}
		if err := sayOutcome(s, c, r, "s1", `{"purpose":"answer","outcome":"pending","text":"Scheduled for tomorrow."}`); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestScheduledRunIsNotGatedOnOutcome(t *testing.T) {
	s, c, r, st := outcomeFixture(t, runKindSchedule)
	if st.checksPending {
		t.Fatal("a scheduled run checks pending work")
	}
	if err := sayOutcome(s, c, r, "k1", `{"purpose":"answer","outcome":"pending","text":"Recorded.","sources":["synthetic source"]}`); err != nil {
		t.Fatalf("scheduled pending answer: %v", err)
	}
}

func TestInvalidOutcomeIsRefused(t *testing.T) {
	s, c, r, _ := outcomeFixture(t, "")
	err := sayOutcome(s, c, r, "x1", `{"purpose":"answer","outcome":"later","text":"Whatever."}`)
	if code := outcomeErrCode(err); code != "say_outcome_invalid" {
		t.Fatalf("code = %q (%v)", code, err)
	}
}

func TestPendingCheckDoesNotConsumeTheReview(t *testing.T) {
	s, c, r, st := outcomeFixture(t, "")
	reviews := 0
	st.review = func() bool { reviews++; return reviews == 1 }
	if code := outcomeErrCode(sayOutcome(s, c, r, "r1", `{"purpose":"answer","outcome":"pending","text":"Will do."}`)); code != "answer_pending_work" {
		t.Fatalf("code = %q", code)
	}
	if reviews != 0 {
		t.Fatal("the pending send-back spent the completion review")
	}
	if code := outcomeErrCode(sayOutcome(s, c, r, "r2", `{"purpose":"answer","outcome":"pending","text":"Will do."}`)); code != "answer_review" {
		t.Fatalf("code = %q", code)
	}
}

func TestSayBaseSchemaListsEveryPropertyRequiredInOrder(t *testing.T) {
	schema := sayToolSchema()
	b, _ := json.Marshal(schema)
	body := string(b)
	if !strings.Contains(body, `"required":["purpose","outcome","text"]`) || !strings.Contains(body, `"additionalProperties":false`) {
		t.Fatalf("schema = %s", body)
	}
	if n := len(schema["properties"].(map[string]any)); n != 3 {
		t.Fatalf("properties = %d", n)
	}
}

func feedSayWith(st *sayStreamer, frags ...string) {
	st.Delta(runtime.ToolCallDelta{Index: 0, ID: "c", Name: "say"})
	for _, f := range frags {
		st.Delta(runtime.ToolCallDelta{Index: 0, Arguments: f})
	}
}

func pendingStreamState(p *holdProbe) *sayRunState {
	st := p.state(func() bool { return false })
	st.checksPending = true
	return st
}

func TestPendingAnswerTextIsHeldAndDoneAnswerStreams(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frags []string
		want  string
	}{
		{"pending", []string{`{"purpose":"answer","outcome":"pen`, `ding","text":"Hel`, `lo there"}`}, ""},
		{"delegated", []string{`{"purpose":"answer","outcome":"delegated","text":"Hello there"}`}, ""},
		{"text before outcome", []string{`{"purpose":"answer","text":"Hello `, `there","outcome":"done"}`}, ""},
		{"done", []string{`{"purpose":"answer","outcome":"done","text":"Hel`, `lo there"}`}, "Hello there"},
		{"blocked", []string{`{"purpose":"answer","outcome":"blocked","text":"Hello there"}`}, "Hello there"},
	} {
		p := &holdProbe{}
		feedSayWith(newSayStreamer(pendingStreamState(p)), tc.frags...)
		if p.raw.String() != tc.want {
			t.Fatalf("%s: draft = %q, want %q", tc.name, p.raw.String(), tc.want)
		}
	}
	// Once the send-back is spent, a pending answer streams as before.
	p := &holdProbe{}
	st := pendingStreamState(p)
	st.pendingCheck.Store(true)
	feedSayWith(newSayStreamer(st), `{"purpose":"answer","outcome":"pending","text":"Hello there"}`)
	if p.raw.String() != "Hello there" {
		t.Fatalf("spent check: draft = %q", p.raw.String())
	}
}
