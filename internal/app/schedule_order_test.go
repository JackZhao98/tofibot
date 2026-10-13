package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type orderEvent struct {
	name       string
	runID      string
	start, end time.Time
	sawNote    bool
}

// orderRig scripts a group of named bots. Roots whose bot is in delegates hand
// a task to the helper bot's DM (a separate conversation worker) and park in
// "waiting"; the helper run blocks until hold is closed (or sleeps helperTime).
type orderRig struct {
	t          *testing.T
	server     *Server
	store      *Store
	group      Conversation
	bots       map[string]Bot
	names      map[string]string
	mu         sync.Mutex
	events     []orderEvent
	delegates  map[string]bool
	helperTime time.Duration
	rootTime   time.Duration
	hold       chan struct{}
}

func (g *orderRig) engine() scheduleResultEngine {
	return func(ctx context.Context, req Request) (Result, error) {
		run, err := g.store.GetRun(req.RunID)
		if err != nil {
			return Result{}, err
		}
		name := g.names[run.BotID]
		ev := orderEvent{name: name, runID: run.ID, start: time.Now()}
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[earlier scheduled runs]") {
				ev.sawNote = true
			}
		}
		defer func() {
			ev.end = time.Now()
			g.mu.Lock()
			g.events = append(g.events, ev)
			g.mu.Unlock()
		}()
		if name == "helper" {
			if g.hold != nil {
				select {
				case <-g.hold:
				case <-ctx.Done():
				}
			}
			time.Sleep(g.helperTime)
			return (scheduleTestEngine{}).Run(ctx, req)
		}
		time.Sleep(g.rootTime)
		if run.Kind == runKindSchedule && run.ParentRunID == "" && g.delegates[name] {
			_, child, err := g.store.AddBotMessage(run.ConversationID, run.BotID, g.bots["helper"].ID, run.ID, "bounded fixture task")
			if err != nil {
				return Result{}, err
			}
			trace, err := g.store.GetConversation(child.ConversationID)
			if err != nil {
				return Result{}, err
			}
			g.server.enqueue(trace, child)
			if reminder, err := req.BeforeFinalResponse("assignment only"); err != nil || reminder != "" {
				return Result{}, err
			}
			return Result{Content: "assignment only"}, nil
		}
		return (scheduleTestEngine{}).Run(ctx, req)
	}
}

func newOrderRig(t *testing.T, dir string, names ...string) *orderRig {
	t.Helper()
	store, first, dm := scheduleTestStoreAt(t, dir)
	g := &orderRig{t: t, store: store, bots: map[string]Bot{names[0]: first}, names: map[string]string{first.ID: names[0]}, delegates: map[string]bool{}, helperTime: 30 * time.Millisecond, rootTime: 40 * time.Millisecond}
	_ = dm
	ids := []string{first.ID}
	for _, n := range append(names[1:], "helper") {
		b, err := store.CreateBot(n, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		g.bots[n] = b
		g.names[b.ID] = n
		ids = append(ids, b.ID)
	}
	grp, err := store.CreateGroup("order group", ids)
	if err != nil {
		t.Fatal(err)
	}
	g.group = grp
	return g
}

// schedule creates one daily-style occurrence due `ago` before now.
func (g *orderRig) schedule(conv Conversation, name string, ago time.Duration) Schedule {
	g.t.Helper()
	x, err := g.store.CreateSchedule(conv.ID, g.bots[name].ID, ScheduleSpec{Title: name + " job", Content: "task for " + name, Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		g.t.Fatal(err)
	}
	makeDue(g.t, g.store, x.ID, time.Now().Add(-ago))
	return x
}

func (g *orderRig) open(dir string) {
	g.t.Helper()
	srv, err := NewServer(Config{DataDir: dir, Engine: g.engine()})
	if err != nil {
		g.t.Fatal(err)
	}
	g.server, g.store = srv, srv.store
	g.t.Cleanup(func() { srv.Close() })
}

func (g *orderRig) first(name string) (orderEvent, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.events {
		if e.name == name {
			return e, true
		}
	}
	return orderEvent{}, false
}

func (g *orderRig) waitStarted(name string, within time.Duration) orderEvent {
	g.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if e, ok := g.first(name); ok {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.t.Fatalf("%s never ran", name)
	return orderEvent{}
}

func (g *orderRig) waitScheduleDone(x Schedule, within time.Duration) {
	g.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if got, err := g.store.GetSchedule(x.ID); err == nil && got.Status == scheduleComplete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := g.store.GetSchedule(x.ID)
	r, _ := g.store.GetRun(got.LastRunID)
	g.t.Fatalf("schedule %s not complete: exec=%s run=%s err=%q", x.Title, got.ExecutionStatus, r.Status, r.Error)
}

// window returns the first start and last end of every run rooted in root.
func (g *orderRig) window(rootID string) (start, end time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.events {
		id := e.runID
		for hops := 0; id != "" && hops < 16; hops++ {
			if id == rootID {
				if start.IsZero() || e.start.Before(start) {
					start = e.start
				}
				if e.end.After(end) {
					end = e.end
				}
				break
			}
			r, err := g.store.GetRun(id)
			if err != nil {
				break
			}
			id = r.ParentRunID
		}
	}
	return
}

func occurrenceRoot(t *testing.T, s *Store, x Schedule) Run {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT run_id FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGroupSchedulesRunInDueOrderWithoutOverlap(t *testing.T) {
	dir := t.TempDir()
	g := newOrderRig(t, dir, "metaphor", "news", "ledger", "lead")
	g.delegates["news"] = true
	g.helperTime = 400 * time.Millisecond
	xs := []Schedule{
		g.schedule(g.group, "metaphor", 5*time.Minute),
		g.schedule(g.group, "news", 4*time.Minute),
		g.schedule(g.group, "ledger", 3*time.Minute),
		g.schedule(g.group, "lead", 2*time.Minute),
	}
	g.store.Close()
	g.open(dir)
	for _, x := range xs {
		g.waitScheduleDone(x, 15*time.Second)
	}
	var prevEnd time.Time
	for i, x := range xs {
		root := occurrenceRoot(t, g.store, x)
		start, end := g.window(root.ID)
		if start.IsZero() {
			t.Fatalf("schedule %d never ran", i)
		}
		if start.Before(prevEnd) {
			t.Fatalf("schedule %d (%s) started %v before the previous lineage ended %v", i, x.Title, start, prevEnd)
		}
		prevEnd = end
		var planned, started string
		if err := g.store.db.QueryRow(`SELECT scheduled_for_utc,started_at FROM schedule_occurrences WHERE run_id=?`, root.ID).Scan(&planned, &started); err != nil {
			t.Fatal(err)
		}
		if started == "" || planned == started {
			t.Fatalf("planned=%q started=%q: actual start must be recorded apart from the plan", planned, started)
		}
	}
	lead, _ := g.first("lead")
	if !lead.sawNote {
		t.Fatal("lead did not see the earlier-runs note")
	}
	if first, _ := g.first("metaphor"); first.sawNote {
		t.Fatal("the earliest run has nothing ahead of it and must not get the note")
	}
}

func TestGroupUserMessageNotBlockedByScheduleChain(t *testing.T) {
	dir := t.TempDir()
	g := newOrderRig(t, dir, "metaphor", "news", "lead")
	g.delegates["news"] = true
	g.hold = make(chan struct{})
	xs := []Schedule{g.schedule(g.group, "news", 3*time.Minute), g.schedule(g.group, "lead", 2*time.Minute)}
	g.store.Close()
	g.open(dir)
	defer func() {
		select {
		case <-g.hold:
		default:
			close(g.hold)
		}
	}()
	g.waitStarted("news", 5*time.Second)
	root := occurrenceRoot(t, g.store, xs[0])
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, _ := g.store.GetRun(root.ID)
		if r.Status == runWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("root status=%s", r.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, runs, _, err := g.store.AddUserRuns(g.group.ID, "hello group", "", []runSpec{{BotID: g.bots["metaphor"].ID, Kind: runKindGroupChat}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("user run %v %v", runs, err)
	}
	g.server.enqueue(g.group, runs[0])
	waitForRunStatus(t, g.server, runs[0].ID, "done")
	if _, ok := g.first("lead"); ok {
		t.Fatal("blocked lead ran before the earlier lineage finished")
	}
	close(g.hold)
	g.waitScheduleDone(xs[1], 15*time.Second)
}

func TestGroupScheduleBoundedWaitReleases(t *testing.T) {
	old := scheduleOrderMaxWait
	scheduleOrderMaxWait = 600 * time.Millisecond
	defer func() { scheduleOrderMaxWait = old }()
	dir := t.TempDir()
	g := newOrderRig(t, dir, "news", "lead")
	g.delegates["news"] = true
	g.hold = make(chan struct{})
	xs := []Schedule{g.schedule(g.group, "news", 3*time.Minute), g.schedule(g.group, "lead", 2*time.Minute)}
	g.store.Close()
	g.open(dir)
	defer func() {
		select {
		case <-g.hold:
		default:
			close(g.hold)
		}
	}()
	news := g.waitStarted("news", 5*time.Second)
	lead := g.waitStarted("lead", 8*time.Second)
	if gap := lead.start.Sub(news.start); gap < 500*time.Millisecond {
		t.Fatalf("lead started %v after news; the bounded wait did not apply", gap)
	}
	if r, _ := g.store.GetRun(occurrenceRoot(t, g.store, xs[0]).ID); r.Status != runWaiting && r.Status != "done" {
		t.Fatalf("news lineage should still be open, root=%s", r.Status)
	}
	close(g.hold)
	g.waitScheduleDone(xs[1], 15*time.Second)
}

func TestDirectConversationSchedulesNotGated(t *testing.T) {
	dir := t.TempDir()
	g := newOrderRig(t, dir, "news", "other")
	g.delegates["news"] = true
	g.hold = make(chan struct{})
	dm, err := g.store.GetConversation(g.bots["news"].DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	first := g.schedule(dm, "news", 3*time.Minute)
	second := g.schedule(dm, "news", 2*time.Minute)
	g.store.Close()
	g.open(dir)
	defer func() {
		select {
		case <-g.hold:
		default:
			close(g.hold)
		}
	}()
	g.waitStarted("news", 5*time.Second)
	deadline := time.Now().Add(8 * time.Second)
	for {
		g.mu.Lock()
		n := 0
		for _, e := range g.events {
			if e.name == "news" {
				n++
			}
		}
		g.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second DM schedule was held behind the first")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = first
	_ = second
	close(g.hold)
}

func TestGroupScheduleOrderSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	g := newOrderRig(t, dir, "news", "ledger", "lead")
	xs := []Schedule{g.schedule(g.group, "news", 4*time.Minute), g.schedule(g.group, "ledger", 3*time.Minute), g.schedule(g.group, "lead", 2*time.Minute)}
	runs, err := g.store.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 3 {
		t.Fatalf("claim=%v %v", runs, err)
	}
	// News has started and delegated before the restart; ledger and lead are queued.
	if ok, err := g.store.SetRunStatus(runs[0].ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, child, err := g.store.AddBotMessage(g.group.ID, g.bots["news"].ID, g.bots["helper"].ID, runs[0].ID, "bounded fixture task")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := g.store.yieldScheduledRun(runs[0].ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	g.store.Close()
	g.open(dir)
	for _, x := range xs {
		g.waitScheduleDone(x, 15*time.Second)
	}
	_ = child
	var ledgerStart, newsEnd time.Time
	if e, ok := g.first("ledger"); ok {
		ledgerStart = e.start
	}
	_, newsEnd = g.window(runs[0].ID)
	leadStart, _ := g.first("lead")
	if ledgerStart.IsZero() || ledgerStart.Before(newsEnd) || leadStart.start.Before(ledgerStart) {
		t.Fatalf("restart broke order: news ended %v ledger %v lead %v", newsEnd, ledgerStart, leadStart.start)
	}
}
