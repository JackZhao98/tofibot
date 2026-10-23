package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// triageRoleEngine answers triage requests by matching the latest message
// against a keyword table and answers every other run with a short reply.
type triageRoleEngine struct {
	mu       sync.Mutex
	table    map[string]string // keyword in the latest message -> bot id
	mode     string            // "" = table, "garbage", "error"
	hold     chan struct{}     // when set, a triage request waits for it
	triage   []runtime.Request
	answered []string // bot ids of non-triage runs
}

func isTriageRequest(req runtime.Request) bool {
	return strings.HasPrefix(req.System, "You route one group message")
}

func (e *triageRoleEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if !isTriageRequest(req) {
		e.mu.Lock()
		e.answered = append(e.answered, req.BotID)
		e.mu.Unlock()
		return runtime.Result{Content: "ok"}, nil
	}
	e.mu.Lock()
	e.triage = append(e.triage, req)
	mode, hold := e.mode, e.hold
	e.mu.Unlock()
	if hold != nil {
		<-hold
	}
	switch mode {
	case "error":
		return runtime.Result{}, errors.New("background model unavailable")
	case "garbage":
		return runtime.Result{Content: "nobody in particular"}, nil
	}
	var latest string
	for _, m := range req.Messages {
		if m.Role == "user" {
			latest = m.Content
		}
	}
	for keyword, id := range e.table {
		if strings.Contains(strings.ToLower(latest), keyword) {
			return runtime.Result{Content: id}, nil
		}
	}
	return runtime.Result{Content: "{\"bot_id\":\"missing\"}"}, nil
}

func (e *triageRoleEngine) answeredBots() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.answered...)
}

type triageFixture struct {
	s      *Server
	e      *triageRoleEngine
	group  Conversation
	stocks Bot
	travel Bot
	chef   Bot
}

func newTriageFixture(t *testing.T) *triageFixture {
	t.Helper()
	e := &triageRoleEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := &triageFixture{s: s, e: e}
	f.stocks, _ = s.store.CreateBot("Quill", "Equity analyst. Covers listed companies, earnings and markets.", "model")
	f.travel, _ = s.store.CreateBot("Atlas", "Travel planner. Covers flights, hotels and itineraries.", "model")
	f.chef, _ = s.store.CreateBot("Basil", "Home cook. Covers recipes and kitchen technique.", "model")
	g, err := s.store.CreateGroup("desk", []string{f.stocks.ID, f.travel.ID, f.chef.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.group, _ = s.store.GetConversation(g.ID)
	e.table = map[string]string{"earnings": f.stocks.ID, "itinerary": f.travel.ID, "risotto": f.chef.ID}
	return f
}

func (f *triageFixture) post(t *testing.T, group, content, client string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "client_message_id": client})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+group+"/messages", bytes.NewReader(body))
	res := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

// settle waits until the group has n runs and none is queued or running.
func settleTriageRuns(t *testing.T, s *Server, group string, n int) []Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := s.store.Runs(group)
		if err == nil && len(runs) == n {
			idle := true
			for _, r := range runs {
				if r.Status == "queued" || r.Status == "running" {
					idle = false
				}
			}
			if idle {
				return runs
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	runs, _ := s.store.Runs(group)
	t.Fatalf("runs did not settle at %d: %+v", n, runs)
	return nil
}

func answeringRun(runs []Run) Run {
	for _, r := range runs {
		if r.Kind == runKindGroupChat {
			return r
		}
	}
	return Run{}
}

func TestTriagePicksTheMemberPerRoleOnASyntheticRoster(t *testing.T) {
	f := newTriageFixture(t)
	for i, tc := range []struct {
		message string
		want    Bot
	}{
		{"What do the earnings tell us this quarter?", f.stocks},
		{"Draft an itinerary for three days in Lisbon", f.travel},
		{"How long does risotto need?", f.chef},
	} {
		f.post(t, f.group.ID, tc.message, "tri-"+string(rune('a'+i)))
		runs := settleTriageRuns(t, f.s, f.group.ID, 2*(i+1))
		// Runs are ordered by queue position; the newest pair is last.
		triage, answer := runs[len(runs)-2], runs[len(runs)-1]
		if triage.Kind != runKindTriage || answer.Kind != runKindGroupChat || answer.ParentRunID != triage.ID {
			t.Fatalf("message %d: runs=%+v", i, runs[len(runs)-2:])
		}
		if answer.BotID != tc.want.ID {
			t.Fatalf("message %d went to %s, want %s", i, answer.BotID, tc.want.ID)
		}
	}
	// The worker saw every role description and the principle text, and no
	// tools.
	req := f.e.triage[0]
	for _, want := range []string{"Equity analyst", "Travel planner", "Home cook", f.chef.ID, "best fits the work"} {
		if !strings.Contains(req.System, want) {
			t.Fatalf("triage prompt lacks %q: %s", want, req.System)
		}
	}
	if len(req.Tools) != 0 {
		t.Fatalf("triage got %d tools", len(req.Tools))
	}
}

func TestTriageIngressDoesNotWaitForTheModel(t *testing.T) {
	f := newTriageFixture(t)
	f.e.hold = make(chan struct{})
	done := make(chan struct{})
	go func() {
		f.post(t, f.group.ID, "Earnings season is here", "tri-hold")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ingress waited for the triage model")
	}
	close(f.e.hold)
	runs := settleTriageRuns(t, f.s, f.group.ID, 2)
	if answeringRun(runs).BotID != f.stocks.ID {
		t.Fatalf("runs=%+v", runs)
	}
}

func TestTriageSkipsAGroupWithOneActiveMember(t *testing.T) {
	f := newTriageFixture(t)
	for _, b := range []Bot{f.travel, f.chef} {
		if _, err := f.s.store.SetBotArchived(b.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	f.post(t, f.group.ID, "Earnings again", "tri-one")
	runs := settleTriageRuns(t, f.s, f.group.ID, 1)
	if runs[0].Kind != runKindGroupChat || runs[0].BotID != f.stocks.ID {
		t.Fatalf("runs=%+v", runs)
	}
	if len(f.e.triage) != 0 {
		t.Fatal("a triage call ran for a group with one active member")
	}
}

func TestTriageFallsBackWhenTheAnswerIsInvalidOrTheModelFails(t *testing.T) {
	for _, mode := range []string{"garbage", "error"} {
		t.Run(mode, func(t *testing.T) {
			f := newTriageFixture(t)
			if err := f.s.store.SetPrimaryBot(f.chef.ID); err != nil {
				t.Fatal(err)
			}
			f.e.mode = mode
			f.post(t, f.group.ID, "Earnings season is here", "tri-fallback")
			runs := settleTriageRuns(t, f.s, f.group.ID, 2)
			if runs[0].Kind != runKindTriage || runs[0].Status != "done" {
				t.Fatalf("triage run=%+v", runs[0])
			}
			answer := answeringRun(runs)
			// The older rule: the Primary Assistant answers.
			if answer.BotID != f.chef.ID || answer.Status != "done" {
				t.Fatalf("fallback run=%+v, want the Primary Assistant %s", answer, f.chef.ID)
			}
		})
	}
}

func TestTriageSettingOffRestoresTheOlderRule(t *testing.T) {
	f := newTriageFixture(t)
	if err := f.s.store.SetPrimaryBot(f.travel.ID); err != nil {
		t.Fatal(err)
	}
	off := "false"
	if err := f.s.saveAutomationValue(autoGroupTriage, &off); err != nil {
		t.Fatal(err)
	}
	f.post(t, f.group.ID, "Earnings season is here", "tri-off")
	runs := settleTriageRuns(t, f.s, f.group.ID, 1)
	if runs[0].Kind != runKindGroupChat || runs[0].BotID != f.travel.ID || runs[0].ParentRunID != "" {
		t.Fatalf("runs=%+v, want the Primary Assistant directly", runs)
	}
	if len(f.e.triage) != 0 {
		t.Fatal("triage ran with the setting off")
	}
}

func TestTriageModelSettingChoosesTheModelAndDefaultsToTheBackgroundModel(t *testing.T) {
	f := newTriageFixture(t)
	if got, want := f.s.groupTriageModel(), f.s.backgroundModel(backgroundTriage); got != want {
		t.Fatalf("default triage model %q, want the background model %q", got, want)
	}
	chosen := "claude-haiku-5-5-custom"
	f.s.automation.mu.Lock()
	f.s.automation.loaded, f.s.automation.values = true, map[string]string{autoGroupTriageModel: chosen}
	f.s.automation.mu.Unlock()
	f.post(t, f.group.ID, "Earnings season is here", "tri-model")
	runs := settleTriageRuns(t, f.s, f.group.ID, 2)
	if runs[0].Kind != runKindTriage || runs[0].Model != chosen {
		t.Fatalf("triage run=%+v, want model %s", runs[0], chosen)
	}
}
