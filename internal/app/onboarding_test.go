package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func profileValue(value string) *string { return &value }

func TestOnboardingCreationIsIdempotentAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, DefaultModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	b, duplicate, err := s.store.CreateOnboardingBot(key, "test-model")
	if err != nil || duplicate {
		t.Fatalf("first create bot=%+v duplicate=%v err=%v", b, duplicate, err)
	}
	messages, _, err := s.store.Messages(b.DMConversationID, 0, 20)
	if err != nil || len(messages) != 1 {
		t.Fatalf("initial messages=%d err=%v", len(messages), err)
	}
	if messages[0].Role != "assistant" || messages[0].Kind != "" || messages[0].SenderBotID != b.ID || !strings.Contains(messages[0].Content, "What would you like me to help with") {
		t.Fatalf("unexpected welcome=%+v", messages[0])
	}
	var pending int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_onboarding WHERE bot_id=? AND status='pending'`, b.ID).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	events, err := s.store.Events(b.DMConversationID, 0)
	if err != nil || len(events) != 2 {
		t.Fatalf("creation events=%d err=%v", len(events), err)
	}
	again, duplicate, err := s.store.CreateOnboardingBot(key, "different-model")
	if err != nil || !duplicate || again.ID != b.ID || again.DMConversationID != b.DMConversationID {
		t.Fatalf("retry bot=%+v duplicate=%v err=%v", again, duplicate, err)
	}
	variant, duplicate, err := s.store.CreateOnboardingBot(strings.ToUpper(key), "different-model")
	if err != nil || !duplicate || variant.ID != b.ID {
		t.Fatalf("canonical UUID retry bot=%+v duplicate=%v err=%v", variant, duplicate, err)
	}
	var count int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM bots`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bot count=%d err=%v", count, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewServer(Config{DataDir: dir, DefaultModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	afterRestart, duplicate, err := s.store.CreateOnboardingBot(key, "test-model")
	if err != nil || !duplicate || afterRestart.ID != b.ID {
		t.Fatalf("restart retry bot=%+v duplicate=%v err=%v", afterRestart, duplicate, err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, b.DMConversationID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("welcome count=%d err=%v", count, err)
	}
}

type onboardingProfileEngine struct {
	system string
	tools  []string
}

func (e *onboardingProfileEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.system = req.System
	for _, tool := range req.Tools {
		e.tools = append(e.tools, tool.Name)
		if tool.Name == "set_bot_profile" {
			args, _ := json.Marshal(map[string]string{
				"name":         "Project Guide",
				"instructions": "Guide project planning, track decisions, and keep replies concise.",
			})
			if _, err := tool.Execute(ctx, args); err != nil {
				return runtime.Result{}, err
			}
		}
	}
	return runtime.Result{Content: "I will help with project planning."}, nil
}

func TestOnboardingContextAndProfileToolPersistDurableProfile(t *testing.T) {
	engine := &onboardingProfileEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine, DefaultModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, err := s.store.CreateOnboardingBot(uuid.NewString(), "test-model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Help me plan projects and track decisions.", "onboard-run")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	if !strings.Contains(engine.system, "newly created self-hosted Bot") || !strings.Contains(engine.system, "persistent direct message") || !strings.Contains(engine.system, "actually connected") || !strings.Contains(engine.system, "user's language") || !strings.Contains(engine.system, "one focused clarification") {
		t.Fatalf("setup context omitted required guidance: %q", engine.system)
	}
	found := false
	for _, name := range engine.tools {
		found = found || name == "set_bot_profile"
	}
	if !found {
		t.Fatalf("tools=%v", engine.tools)
	}
	got, err := s.store.GetBot(b.ID)
	if err != nil || got.Name != "Project Guide" || got.Instructions == "" {
		t.Fatalf("profile=%+v err=%v", got, err)
	}
	c, _ = s.store.GetConversation(c.ID)
	if c.Name != got.Name {
		t.Fatalf("DM name=%q bot name=%q", c.Name, got.Name)
	}
	var status string
	if err = s.store.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("onboarding status=%q err=%v", status, err)
	}
	events, _ := s.store.Events(c.ID, 0)
	var profileEvent map[string]any
	for _, event := range events {
		if event["type"] == "bot" {
			profileEvent, _ = event["data"].(map[string]any)
		}
	}
	if profileEvent == nil || profileEvent["conversation_id"] != c.ID || profileEvent["bot"] == nil {
		t.Fatalf("profile bot event=%v", profileEvent)
	}
}

func TestOnboardingProfileToolScopeAndCancellation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, _ := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	other, _ := s.store.CreateBot("Established", "old", "model")
	otherC, _ := s.store.GetConversation(other.DMConversationID)
	if _, ok := onboardingProfileTool(s, otherC, Run{BotID: other.ID}); ok {
		t.Fatal("profile tool leaked to established bot")
	}
	if _, ok := onboardingProfileTool(s, Conversation{ID: "group", Kind: "group", BotID: b.ID}, Run{BotID: b.ID}); ok {
		t.Fatal("profile tool leaked to group")
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "set me up", "cancel-profile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, run)
	if !ok {
		t.Fatal("profile tool missing for pending own DM")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.Execute(ctx, json.RawMessage(`{"name":"Should Not Persist","instructions":"should not persist"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != b.Name || got.Instructions != b.Instructions {
		t.Fatalf("cancel changed profile=%+v", got)
	}
	var status string
	if err = s.store.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("cancel status=%q err=%v", status, err)
	}
}

type retryOnboardingEngine struct{ calls int }

func (e *retryOnboardingEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.calls++
	if e.calls == 1 {
		return runtime.Result{}, errors.New("temporary provider failure")
	}
	for _, tool := range req.Tools {
		if tool.Name == "set_bot_profile" {
			_, err := tool.Execute(ctx, json.RawMessage(`{"name":"Retry Guide","instructions":"Handle the user's retryable work."}`))
			if err != nil {
				return runtime.Result{}, err
			}
		}
	}
	return runtime.Result{Content: "configured after retry"}, nil
}

func TestOnboardingRetryKeepsProfileToolEligibility(t *testing.T) {
	engine := &retryOnboardingEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine, DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, _ := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Please configure yourself as a retryable project guide.", "retry-onboard")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	failed, _ := s.store.GetRun(run.ID)
	if failed.Status != "failed" {
		t.Fatalf("first status=%q", failed.Status)
	}
	retry, err := s.store.RetryRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, retry)
	got, _ := s.store.GetBot(b.ID)
	if got.Name != "Retry Guide" || engine.calls != 2 {
		t.Fatalf("retry profile=%+v calls=%d", got, engine.calls)
	}
}

func TestOnboardingProfileToolExcludesScheduledRuns(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, _ := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "scheduled assignment", "scheduled-onboard")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE runs SET kind='schedule' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run.Kind = "schedule"
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := onboardingProfileTool(s, c, run); ok {
		t.Fatal("profile tool leaked to scheduled run")
	}
	ctx := context.Background()
	if _, err = s.store.setBotProfile(ctx, run, c, profileValue("No Schedule"), profileValue("must not persist")); err == nil {
		t.Fatal("scheduled profile write unexpectedly succeeded")
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != b.Name || got.Instructions != b.Instructions {
		t.Fatalf("scheduled write changed profile=%+v", got)
	}
}

func TestOnboardingCreationEventFailureRollsBackEverything(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_onboarding_bot_event BEFORE INSERT ON events WHEN NEW.type='bot' BEGIN SELECT RAISE(ABORT, 'bot event failure'); END`); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, _, err = s.store.CreateOnboardingBot(key, "model"); err == nil {
		t.Fatal("creation unexpectedly succeeded despite bot event failure")
	}
	for table, query := range map[string]string{
		"bots":          `SELECT COUNT(*) FROM bots`,
		"conversations": `SELECT COUNT(*) FROM conversations`,
		"members":       `SELECT COUNT(*) FROM members`,
		"markers":       `SELECT COUNT(*) FROM bot_onboarding`,
		"messages":      `SELECT COUNT(*) FROM messages`,
		"events":        `SELECT COUNT(*) FROM events`,
	} {
		var count int
		if err = s.store.db.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s left %d rows after rollback", table, count)
		}
	}
	if _, _, err = s.store.CreateOnboardingBot(key, "model"); err == nil {
		t.Fatal("failed creation became reusable while trigger remained installed")
	}
}

func TestOnboardingProfileEventFailureRollsBackProfileAndMarker(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, err := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_onboarding_bot_event BEFORE INSERT ON events WHEN NEW.type='bot' BEGIN SELECT RAISE(ABORT, 'bot event failure'); END`); err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "configure me", "profile-event-failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, run)
	if !ok {
		t.Fatal("profile tool missing")
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"Should Roll Back","instructions":"should roll back"}`))
	if err == nil {
		t.Fatal("profile unexpectedly succeeded despite bot event failure")
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != b.Name || got.Instructions != b.Instructions {
		t.Fatalf("profile changed after event failure=%+v", got)
	}
	gotConv, _ := s.store.GetConversation(c.ID)
	if gotConv.Name != c.Name {
		t.Fatalf("DM name changed after event failure=%q want %q", gotConv.Name, c.Name)
	}
	var status string
	if err = s.store.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("marker status=%q err=%v", status, err)
	}
	var botEvents int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='bot'`, c.ID).Scan(&botEvents); err != nil || botEvents != 1 {
		t.Fatalf("bot events=%d err=%v", botEvents, err)
	}
}

func TestOnboardingProfileRejectsLateCancellationAndForgedBot(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, _ := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	other, _ := s.store.CreateBot("Other", "old", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "late setup", "late-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, run)
	if !ok {
		t.Fatal("profile tool missing")
	}
	if _, err = s.store.SetRunStatus(run.ID, "cancelled", "user cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"Late","instructions":"late"}`)); err == nil {
		t.Fatal("late cancelled profile unexpectedly succeeded")
	}
	if _, err = s.store.setBotProfile(context.Background(), Run{ID: run.ID, BotID: other.ID}, c, profileValue("Forged"), profileValue("must not persist")); err == nil {
		t.Fatal("forged other-bot profile unexpectedly succeeded")
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != b.Name || got.Instructions != b.Instructions {
		t.Fatalf("rejected writes changed target profile=%+v", got)
	}
}

func TestOnboardingProfileRepeatDoesNotEmitDuplicateBotEvent(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, _ := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "repeat setup", "repeat-profile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, run)
	if !ok {
		t.Fatal("profile tool missing")
	}
	args := json.RawMessage(`{"name":"Repeat Guide","instructions":"Handle repeated setup safely."}`)
	if _, err = tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err = tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='bot'`, c.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("bot event count=%d want creation plus one profile update", events)
	}
	var pending string
	if err = s.store.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&pending); err != nil || pending != "completed" {
		t.Fatalf("marker status=%q err=%v", pending, err)
	}
}

func TestBotProfileCanBeUpdatedAgainByUserAndPreservesOmittedFields(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("Existing Guide", "Keep this durable role.", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, first, _, err := s.store.AddUserRun(c.ID, b.ID, "Rename yourself to Planning Guide.", "profile-repeat-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(first.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, first)
	if !ok {
		t.Fatal("profile tool missing for established Bot")
	}
	if required, _ := tool.Parameters["required"].([]string); len(required) != 0 {
		t.Fatalf("profile fields unexpectedly required: %v", required)
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"Planning Guide"}`)); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.store.GetBot(b.ID)
	if updated.Name != "Planning Guide" || updated.Instructions != b.Instructions || updated.Model != b.Model {
		t.Fatalf("rename changed profile unexpectedly: %+v", updated)
	}
	updatedConversation, _ := s.store.GetConversation(c.ID)
	if updatedConversation.Name != updated.Name {
		t.Fatalf("DM name=%q want %q", updatedConversation.Name, updated.Name)
	}
	if _, err = s.store.SetRunStatus(first.ID, "done", ""); err != nil {
		t.Fatal(err)
	}

	_, second, _, err := s.store.AddUserRun(c.ID, b.ID, "Change your durable duties.", "profile-repeat-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(second.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok = onboardingProfileTool(s, c, second)
	if !ok {
		t.Fatal("profile tool missing on a later user run")
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"instructions":"Own planning reviews and keep decisions concise."}`)); err != nil {
		t.Fatal(err)
	}
	updated, _ = s.store.GetBot(b.ID)
	if updated.Name != "Planning Guide" || updated.Instructions != "Own planning reviews and keep decisions concise." {
		t.Fatalf("later update=%+v", updated)
	}
	var events int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='bot'`, c.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("bot event count=%d want two profile updates", events)
	}
	var markerCount int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&markerCount); err != nil {
		t.Fatal(err)
	}
	if markerCount != 0 {
		t.Fatalf("established Bot unexpectedly got onboarding marker: %d", markerCount)
	}
}

func TestCompletedOnboardingBotCanUpdateOnALaterUserRun(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, err := s.store.CreateOnboardingBot(uuid.NewString(), "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, first, _, err := s.store.AddUserRun(c.ID, b.ID, "Set yourself up as a concise planning guide.", "completed-profile-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(first.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	name := "Planning Guide"
	instructions := "Guide planning decisions and preserve important constraints."
	if _, err = s.store.setBotProfile(context.Background(), first, c, &name, &instructions); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(first.ID, "done", ""); err != nil {
		t.Fatal(err)
	}

	_, second, _, err := s.store.AddUserRun(c.ID, b.ID, "Change only your name to Decision Guide.", "completed-profile-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(second.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := onboardingProfileTool(s, c, second)
	if !ok {
		t.Fatal("profile tool missing after onboarding completed")
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"Decision Guide"}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != "Decision Guide" || got.Instructions != instructions {
		t.Fatalf("later rename=%+v", got)
	}
	_, system := s.buildContext(c, second, got)
	if !strings.Contains(system, "only when the user explicitly asks") {
		t.Fatalf("later profile guidance missing: %q", system)
	}
}
