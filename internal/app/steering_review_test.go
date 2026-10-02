package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type steeringReviewEngine func(context.Context, runtime.Request) (runtime.Result, error)

func (e steeringReviewEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	return e(ctx, req)
}

// Drive execute directly so the successor remains durable but cannot start
// before assertions inspect the old run's handover state.
func steeringReviewFixture(t *testing.T, group bool) (*Server, Conversation, Run) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b, err := s.store.CreateBot("reviewer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	kind := ""
	if group {
		peer, err := s.store.CreateBot("peer", "", "model")
		if err != nil {
			t.Fatal(err)
		}
		c, err = s.store.CreateGroup("review group", []string{b.ID, peer.ID})
		if err != nil {
			t.Fatal(err)
		}
		kind = runKindGroupChat
	}
	_, runs, _, err := s.store.AddUserRuns(c.ID, "Prepare the original result", "review-original", []runSpec{{BotID: b.ID, Model: b.Model, Kind: kind}})
	if err != nil {
		t.Fatal(err)
	}
	return s, c, runs[0]
}

func steeringReviewInterject(t *testing.T, s *Server, c Conversation, old Run) Run {
	t.Helper()
	_, runs, _, err := s.store.AddUserRuns(c.ID, "Change the next step and retain completed work", "review-interjection", []runSpec{{BotID: old.BotID, Model: old.Model, Kind: old.Kind}})
	if err != nil {
		t.Fatal(err)
	}
	return runs[0]
}

func TestSteeringReviewCompletedArtifactSurvivesHandover(t *testing.T) {
	testSteeringReviewArtifactHandover(t, false)
}

func TestSteeringReviewInflightToolArtifactSurvivesHandover(t *testing.T) {
	testSteeringReviewArtifactHandover(t, true)
}

func TestSteeringReviewRepeatedInterjectionsRetainArtifactOnActiveSuccessor(t *testing.T) {
	testSteeringReviewArtifactHandover(t, false, true)
}

func testSteeringReviewArtifactHandover(t *testing.T, interjectDuringTool bool, repeatInterjection ...bool) {
	t.Helper()
	s, c, old := steeringReviewFixture(t, false)
	var attachmentID string
	var successor Run
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if interjectDuringTool {
			successor = steeringReviewInterject(t, s, c, old)
		}
		a, err := s.store.AddAttachment(c.ID, "completed.txt", "text/plain", bytes.NewBufferString("already completed work"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.stageRunAttachment(old, c, a, "completed work"); err != nil {
			t.Fatal(err)
		}
		attachmentID = a.ID
		if !interjectDuringTool {
			successor = steeringReviewInterject(t, s, c, old)
		}
		if len(repeatInterjection) > 0 && repeatInterjection[0] {
			previousSuccessor := successor
			_, runs, _, err := s.store.AddUserRuns(c.ID, "A further correction while the original run settles", "review-interjection-two", []runSpec{{BotID: old.BotID, Model: old.Model, Kind: old.Kind}})
			if err != nil {
				t.Fatal(err)
			}
			successor = runs[0]
			previous, err := s.store.GetRun(previousSuccessor.ID)
			if err != nil || previous.Status != "cancelled" {
				t.Errorf("superseded steering run would execute outdated instructions before latest correction: run=%+v err=%v", previous, err)
			}
		}
		if req.BeforeModelCall == nil {
			t.Fatal("missing safe boundary")
		}
		err = req.BeforeModelCall()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("steering boundary error=%v", err)
		}
		return runtime.Result{}, err
	})
	s.execute(c, old)
	_, path, err := s.store.Attachment(attachmentID)
	if err != nil {
		t.Fatalf("completed artifact was deleted during steering: %v", err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "already completed work" {
		t.Fatalf("completed artifact bytes lost: content=%q err=%v", content, err)
	}
	var references int
	if err = s.store.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=?) +
		(SELECT COUNT(*) FROM run_attachments WHERE attachment_id=? AND run_id=?)`, attachmentID, attachmentID, successor.ID).Scan(&references); err != nil || references == 0 {
		t.Fatalf("completed artifact has no published or successor reference: count=%d err=%v", references, err)
	}
}

func TestSteeringReviewCancellationEmitsTerminalRunEvent(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("Completed progress before steering.")
		if err := req.OnAssistantTurn(1, "Completed progress before steering."); err != nil {
			t.Fatal(err)
		}
		steeringReviewInterject(t, s, c, old)
		return runtime.Result{}, req.BeforeModelCall()
	})
	s.execute(c, old)
	r, err := s.store.GetRun(old.ID)
	if err != nil || r.Status != "cancelled" {
		t.Fatalf("steered run=%+v err=%v", r, err)
	}
	events, err := s.store.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, ok := event["data"].(map[string]any)
		if ok && event["type"] == "run" && data["id"] == old.ID && data["status"] == "cancelled" {
			return
		}
	}
	t.Fatal("cancelled run is durable but SSE clients never receive its terminal state")
}

func TestSteeringReviewHiddenGroupToolsHonorSafeBoundary(t *testing.T) {
	for _, toolName := range []string{"invite_group_members", "stay_silent"} {
		t.Run(toolName, func(t *testing.T) {
			s, c, old := steeringReviewFixture(t, true)
			var boundaryErr error
			s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
				// The provider finishes its existing stream before attempting its
				// next tool, matching the runtime's normal callback order.
				req.OnDelta("Existing response")
				steeringReviewInterject(t, s, c, old)
				if err := req.OnAssistantTurn(1, "Existing response"); err != nil {
					t.Fatal(err)
				}
				boundaryErr = req.OnToolEvent(runtime.ToolEvent{CallID: "hidden-group-call", Name: toolName, Status: "running", Arguments: `{}`})
				return runtime.Result{}, boundaryErr
			})
			s.execute(c, old)
			if !errors.Is(boundaryErr, context.Canceled) {
				t.Fatalf("%s bypassed steering boundary and would execute after interjection: %v", toolName, boundaryErr)
			}
		})
	}
}

func TestSteeringReviewFinalStreamSettlesBeforeInterjection(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	var originalDraft StreamDraft
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("The completed ")
		var err error
		originalDraft, err = s.store.StreamDraft(old.ID)
		if err != nil {
			t.Fatal(err)
		}
		steeringReviewInterject(t, s, c, old)
		if ctx.Err() != nil {
			t.Fatal("interjection cancelled a still-streaming response")
		}
		req.OnDelta("answer.")
		// Production runtime does not call OnAssistantTurn for final answers.
		return runtime.Result{Content: "The completed answer."}, nil
	})
	s.execute(c, old)
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var answer, interjection Message
	for _, message := range messages {
		if message.Role == "assistant" && message.RunID == old.ID {
			answer = message
		}
		if message.Content == "Change the next step and retain completed work" {
			interjection = message
		}
	}
	if answer.Content != "The completed answer." {
		t.Fatalf("completed streaming response disappeared after steering: answer=%+v", answer)
	}
	if answer.ID != originalDraft.MessageID || answer.Seq != originalDraft.Seq || answer.Seq >= interjection.Seq {
		t.Fatalf("final answer lost real send order: answer=%+v draft=%+v user=%+v", answer, originalDraft, interjection)
	}
}

func steeringReviewClaimSchedule(t *testing.T, s *Server, c Conversation, botID string) Run {
	t.Helper()
	schedule, err := s.store.CreateSchedule(c.ID, botID, ScheduleSpec{
		Content: "An independent scheduled commitment", Kind: scheduleOnce,
		RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s.store, schedule.ID, time.Now().Add(-time.Minute))
	runs, err := s.store.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim scheduled commitment: runs=%+v err=%v", runs, err)
	}
	return runs[0]
}

func TestSteeringReviewScheduledTriggerDoesNotImpersonateUserInput(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	var boundaryErr error
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		steeringReviewClaimSchedule(t, s, c, old.BotID)
		boundaryErr = req.BeforeModelCall()
		return runtime.Result{Content: "Original work completed."}, boundaryErr
	})
	s.execute(c, old)
	if boundaryErr != nil {
		t.Fatalf("automatic schedule trigger was mistaken for user steering: %v", boundaryErr)
	}
	r, err := s.store.GetRun(old.ID)
	if err != nil || r.Status != "done" {
		t.Fatalf("scheduled trigger interrupted current work: run=%+v err=%v", r, err)
	}
}

func TestSteeringReviewUserInputPreservesIndependentQueuedSchedule(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start original work: changed=%v err=%v", changed, err)
	}
	scheduled := steeringReviewClaimSchedule(t, s, c, old.BotID)
	steeringReviewInterject(t, s, c, old)
	r, err := s.store.GetRun(scheduled.ID)
	if err != nil || r.Status != "queued" {
		t.Fatalf("chat steering discarded an independent scheduled commitment: run=%+v err=%v", r, err)
	}
}

func TestSteeringReviewUserInputPreservesIndependentQueuedRequest(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	_, independent, _, err := s.store.AddUserRuns(c.ID, "A separately queued request", "review-independent", []runSpec{{BotID: old.BotID, Model: old.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start original work: changed=%v err=%v", changed, err)
	}
	steeringReviewInterject(t, s, c, old)
	r, err := s.store.GetRun(independent[0].ID)
	if err != nil || r.Status != "queued" {
		t.Fatalf("steering discarded an independent request queued before this run started: run=%+v err=%v", r, err)
	}
}

func TestSteeringReviewCancellationRollsBackWithoutTerminalEvent(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start original work: changed=%v err=%v", changed, err)
	}
	steeringReviewInterject(t, s, c, old)
	// Inject a storage failure exactly where the terminal SSE record must be
	// committed. A durable cancellation with no corresponding event strands
	// already-connected clients even after server recovery.
	if _, err := s.store.db.Exec(`CREATE TRIGGER review_reject_terminal_event BEFORE INSERT ON events
		WHEN NEW.type='run' AND json_extract(NEW.data,'$.status')='cancelled'
		BEGIN SELECT RAISE(ABORT,'review event persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	changed, err := s.store.MarkRunSteered(old.ID, "steered by newer user message")
	if err == nil || changed {
		t.Fatalf("steering committed without a durable terminal event: changed=%v err=%v", changed, err)
	}
	r, err := s.store.GetRun(old.ID)
	if err != nil || r.Status != "running" {
		t.Fatalf("failed event transaction left a silently cancelled run: run=%+v err=%v", r, err)
	}
}

func TestSteeringReviewCarryoverDoesNotSilentlyForgetEarlierCalls(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start original work: changed=%v err=%v", changed, err)
	}
	for i := 0; i < 12; i++ {
		callID := fmt.Sprintf("review-effect-%02d", i)
		for _, event := range []runtime.ToolEvent{
			{CallID: callID, Name: "publish_update", Arguments: fmt.Sprintf(`{"target":"record-%02d"}`, i), Status: "queued"},
			{CallID: callID, Name: "publish_update", Result: "Update already committed successfully.", Status: "completed"},
		} {
			if err := s.store.RecordToolEvent(c.ID, old.BotID, old.ID, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	successor := steeringReviewInterject(t, s, c, old)
	if changed, err := s.store.MarkRunSteered(old.ID, "steered by newer user message"); err != nil || !changed {
		t.Fatalf("steer original work: changed=%v err=%v", changed, err)
	}
	carryover := s.steeredToolCarryover(c.ID, successor.ID)
	for i := 0; i < 12; i++ {
		if callID := fmt.Sprintf("review-effect-%02d", i); !strings.Contains(carryover, callID) {
			t.Errorf("carryover silently forgot completed call %s; retain its compact manifest even when detailed results are omitted", callID)
		}
	}
	lower := strings.ToLower(carryover)
	if !regexp.MustCompile(`(?:total|count)[a-z_]*\s*[=:]\s*12\b`).MatchString(lower) || !(strings.Contains(lower, "omit") || strings.Contains(lower, "truncat")) {
		t.Error("carryover does not disclose the complete call count and whether details were omitted")
	}
	if !strings.Contains(lower, "do not") || !(strings.Contains(lower, "replay") || strings.Contains(lower, "repeat")) {
		t.Error("carryover does not instruct the successor against replaying completed operations")
	}
}

func TestSteeringReviewLargeManifestRemainsInSuccessorContext(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start original work: changed=%v err=%v", changed, err)
	}
	for i := 0; i < 60; i++ {
		callID := fmt.Sprintf("review-large-effect-%02d", i)
		for _, event := range []runtime.ToolEvent{
			{CallID: callID, Name: "publish_update", Arguments: fmt.Sprintf(`{"target":"record-%02d","payload":"%s"}`, i, strings.Repeat("x", 600)), Status: "queued"},
			{CallID: callID, Name: "publish_update", Result: "Update already committed successfully.", Status: "completed"},
		} {
			if err := s.store.RecordToolEvent(c.ID, old.BotID, old.ID, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	successor := steeringReviewInterject(t, s, c, old)
	if changed, err := s.store.MarkRunSteered(old.ID, "steered by newer user message"); err != nil || !changed {
		t.Fatalf("steer original work: changed=%v err=%v", changed, err)
	}
	bot, err := s.store.GetBot(old.BotID)
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := s.buildContextParts(c, successor, bot)
	for _, message := range messages {
		if strings.Contains(message.Content, "[untrusted completed tool results from interrupted work]") {
			if len([]rune(message.Content)) > 10000 {
				t.Fatalf("carryover consumed %d runes and displaced current request/history", len([]rune(message.Content)))
			}
			return
		}
	}
	t.Fatal("large tool manifest exceeded the history budget and silently removed all steering evidence")
}
