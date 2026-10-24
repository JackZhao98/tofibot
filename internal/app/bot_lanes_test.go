package app

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// laneProbe is a model engine that records which runs overlap. A run waits on
// its gate (when it has one) so a test decides when it finishes.
type laneProbe struct {
	mu        sync.Mutex
	active    map[string]int
	maxActive map[string]int
	started   []string
	kinds     map[string]string
	gates     map[string]chan struct{}
	startedCh chan string
}

func newLaneProbe() *laneProbe {
	return &laneProbe{active: map[string]int{}, maxActive: map[string]int{}, kinds: map[string]string{}, gates: map[string]chan struct{}{}, startedCh: make(chan string, 64)}
}

func (p *laneProbe) gate(runID string) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	g := make(chan struct{})
	p.gates[runID] = g
	return g
}

func (p *laneProbe) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	p.mu.Lock()
	p.active[req.BotID]++
	if p.active[req.BotID] > p.maxActive[req.BotID] {
		p.maxActive[req.BotID] = p.active[req.BotID]
	}
	p.started = append(p.started, req.RunID)
	g := p.gates[req.RunID]
	p.mu.Unlock()
	p.startedCh <- req.RunID
	if g != nil {
		select {
		case <-g:
		case <-ctx.Done():
		}
	} else {
		time.Sleep(15 * time.Millisecond)
	}
	p.mu.Lock()
	p.active[req.BotID]--
	p.mu.Unlock()
	return runtime.Result{Content: "ok"}, nil
}

func (p *laneProbe) max(bot string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive[bot]
}

func (p *laneProbe) order() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.started...)
}

func waitStarted(t *testing.T, p *laneProbe, id string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case got := <-p.startedCh:
			if got == id {
				return
			}
		case <-deadline:
			t.Fatalf("run %s did not start within %s", id, d)
		}
	}
}

// waitStartedAll waits until every named run has started, in any order.
func waitStartedAll(t *testing.T, p *laneProbe, d time.Duration, ids ...string) {
	t.Helper()
	left := map[string]bool{}
	for _, id := range ids {
		left[id] = true
	}
	deadline := time.After(d)
	for len(left) > 0 {
		select {
		case got := <-p.startedCh:
			delete(left, got)
		case <-deadline:
			t.Fatalf("runs did not all start within %s: still waiting for %v", d, left)
		}
	}
}

func waitRunStatus(t *testing.T, s *Store, id string, want ...string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if r.Status == w {
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := s.GetRun(id)
	t.Fatalf("run %s is %q (%s), want %v", id, r.Status, r.Error, want)
	return r
}

func laneServer(t *testing.T, e runtime.Engine) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// queueRun makes a queued run of a kind directly, the way the paths that create
// delegations, schedules and wakes do.
func queueRun(t *testing.T, s *Store, conv, bot, kind, parent string) Run {
	t.Helper()
	var seq int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, conv).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var model string
	_ = s.db.QueryRow(`SELECT model FROM bots WHERE id=?`, bot).Scan(&model)
	r := Run{ID: newID(), ConversationID: conv, BotID: bot, Status: "queued", ParentRunID: parent, Model: model, Kind: kind, OriginConversationID: conv, QueueSeq: seq, CreatedAt: now(), UpdatedAt: now()}
	if _, err := s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.ConversationID, r.BotID, r.Status, nullString(r.ParentRunID), 0, r.Model, r.Kind, r.OriginConversationID, "", r.QueueSeq, r.CreatedAt, r.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOneBotNeverRunsTwoRunsAtOnceAcrossConversations(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	group, _ := s.store.CreateGroup("lane group", []string{a.ID, b.ID})
	first := queueRun(t, s.store, dm.ID, a.ID, "", "")
	second := queueRun(t, s.store, group.ID, a.ID, "", "")
	g1, g2 := p.gate(first.ID), p.gate(second.ID)
	s.startConversationWorker(dm.ID)
	s.startConversationWorker(group.ID)
	select {
	case id := <-p.startedCh:
		if id != first.ID && id != second.ID {
			t.Fatalf("unexpected run %s", id)
		}
		// Whichever one took the lane, the other must still be waiting.
		other := second.ID
		if id == second.ID {
			other = first.ID
		}
		time.Sleep(150 * time.Millisecond)
		if r, _ := s.store.GetRun(other); r.Status != "queued" {
			t.Fatalf("the Bot ran two runs at once: %s is %s", other, r.Status)
		}
		if id == first.ID {
			close(g1)
		} else {
			close(g2)
		}
		waitStarted(t, p, other, 5*time.Second)
		if other == first.ID {
			close(g1)
		} else {
			close(g2)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no run started")
	}
	waitRunStatus(t, s.store, first.ID, "done")
	waitRunStatus(t, s.store, second.ID, "done")
	if n := p.max(a.ID); n != 1 {
		t.Fatalf("Bot ran %d runs at once", n)
	}
}

func TestTwoBotsOfOneGroupRunInParallel(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	group, _ := s.store.CreateGroup("parallel group", []string{a.ID, b.ID})
	ra := queueRun(t, s.store, group.ID, a.ID, "", "")
	rb := queueRun(t, s.store, group.ID, b.ID, "", "")
	ga, gb := p.gate(ra.ID), p.gate(rb.ID)
	s.startConversationWorker(group.ID)
	// Both start while neither has finished: serial execution would never get
	// the second one going.
	waitStartedAll(t, p, 5*time.Second, ra.ID, rb.ID)
	close(ga)
	close(gb)
	waitRunStatus(t, s.store, ra.ID, "done")
	waitRunStatus(t, s.store, rb.ID, "done")
}

func TestFreedBotTakesTreeThenPersonThenDelegationThenSchedule(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	busy, _ := s.store.CreateGroup("busy group", []string{a.ID, b.ID})
	g1, _ := s.store.CreateGroup("g1", []string{a.ID, b.ID})
	g2, _ := s.store.CreateGroup("g2", []string{a.ID, b.ID})
	g3, _ := s.store.CreateGroup("g3", []string{a.ID, b.ID})
	// The Bot is busy with a run of the task tree T.
	root := queueRun(t, s.store, busy.ID, a.ID, "", "")
	gate := p.gate(root.ID)
	s.startConversationWorker(busy.ID)
	waitStarted(t, p, root.ID, 5*time.Second)
	// Everything below waits for the lane; they sit in four conversations.
	schedule := queueRun(t, s.store, g1.ID, a.ID, runKindSchedule, "")
	delegation := queueRun(t, s.store, g2.ID, a.ID, runKindGroupTask, "")
	person := queueRun(t, s.store, dm.ID, a.ID, "", "")
	continuation := queueRun(t, s.store, g3.ID, a.ID, runKindFollowup, root.ID)
	for _, c := range []string{g1.ID, g2.ID, dm.ID, g3.ID} {
		s.startConversationWorker(c)
	}
	time.Sleep(200 * time.Millisecond)
	for _, r := range []Run{schedule, delegation, person, continuation} {
		if got, _ := s.store.GetRun(r.ID); got.Status != "queued" {
			t.Fatalf("%s started while the Bot was busy: %s", r.Kind, got.Status)
		}
	}
	close(gate)
	for _, r := range []Run{continuation, person, delegation, schedule} {
		// The fake schedule run ends without a receipt, so it fails; it still ran.
		waitRunStatus(t, s.store, r.ID, "done", "failed")
	}
	want := []string{root.ID, continuation.ID, person.ID, delegation.ID, schedule.ID}
	got := p.order()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order %v, want %v (root, same-tree continuation, person, delegation, schedule)", got, want)
	}
	if n := p.max(a.ID); n != 1 {
		t.Fatalf("Bot ran %d runs at once", n)
	}
}

func TestRestartedRunsOfOneBotKeepToTheLane(t *testing.T) {
	dir := t.TempDir()
	p := newLaneProbe()
	s, err := NewServer(Config{DataDir: dir, Engine: p})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	group, _ := s.store.CreateGroup("restart group", []string{a.ID, b.ID})
	// Two runs of the Bot were running in two conversations when the process died.
	r1 := queueRun(t, s.store, dm.ID, a.ID, "", "")
	r2 := queueRun(t, s.store, group.ID, a.ID, "", "")
	for _, r := range []Run{r1, r2} {
		if _, err := s.store.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = NewServer(Config{DataDir: dir, Engine: p})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitRunStatus(t, s.store, r1.ID, "done")
	waitRunStatus(t, s.store, r2.ID, "done")
	if n := p.max(a.ID); n != 1 {
		t.Fatalf("after restart the Bot ran %d runs at once", n)
	}
}

// Six Bots of one group stream and run tools at the same time on the single
// SQLite connection, while people keep sending messages to them and a probe
// takes the connection as a reader would. Nothing may deadlock and the wait for
// the connection must stay bounded. The numbers are logged (run with -v).
func TestSixBotsStreamingWithToolsOnOneConnectionDoNotStarveIt(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	const bots, steps = 6, 300
	var mu sync.Mutex
	var opLat []time.Duration
	record := func(d time.Duration) { mu.Lock(); opLat = append(opLat, d); mu.Unlock() }
	timed := func(f func() error) error {
		start := time.Now()
		err := f()
		record(time.Since(start))
		return err
	}
	var finished sync.WaitGroup
	var original sync.Map // run id -> true, for the six runs the test counts
	engine := steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, ok := original.Load(req.RunID); !ok {
			// A message that reached a Bot after its run ended is answered by a
			// continuation run; it only has to finish.
			return runtime.Result{Content: "done"}, nil
		}
		defer finished.Done()
		for i := 0; i < steps; i++ {
			req.OnDelta(fmt.Sprintf("token-%d ", i))
			call := fmt.Sprintf("%s-call-%d", req.RunID[:6], i)
			for _, status := range []string{"queued", "running", "completed"} {
				ev := runtime.ToolEvent{CallID: call, Name: "synthetic_lookup", Arguments: `{"q":"x"}`, Status: status}
				if status == "completed" {
					ev.Result = "synthetic result"
				}
				if err := timed(func() error { return req.OnToolEvent(ev) }); err != nil {
					return runtime.Result{}, err
				}
			}
			if err := timed(func() error { return req.OnCheckpoint("tool", []byte(`{"synthetic":true}`)) }); err != nil {
				return runtime.Result{}, err
			}
			if _, err := drain(req); err != nil {
				return runtime.Result{}, err
			}
		}
		return runtime.Result{Content: "done"}, nil
	})
	s := laneServer(t, engine)
	if err := s.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	var members []Bot
	var ids []string
	for i := 0; i < bots; i++ {
		b, err := s.store.CreateBot(fmt.Sprintf("bot%d", i), "", "model")
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, b)
		ids = append(ids, b.ID)
	}
	group, err := s.store.CreateGroup("stress", ids)
	if err != nil {
		t.Fatal(err)
	}
	var runs []Run
	for _, b := range members {
		r := queueRun(t, s.store, group.ID, b.ID, "", "")
		original.Store(r.ID, true)
		runs = append(runs, r)
		finished.Add(1)
	}
	stop := make(chan struct{})
	var side sync.WaitGroup
	var probe []time.Duration
	side.Add(2)
	go func() { // a reader asking for the connection, like a page polling
		defer side.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			var n int
			if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
				t.Error(err)
				return
			}
			probe = append(probe, time.Since(start))
			time.Sleep(2 * time.Millisecond)
		}
	}()
	go func() { // people keep writing to the Bots
		defer side.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			b := members[i%bots]
			if _, _, _, _, err := s.store.addUserRunsSteering(group.ID, fmt.Sprintf("@%s update %d", b.Name, i), fmt.Sprintf("stress-%d", i), []runSpec{{BotID: b.ID, Model: b.Model}}, nil); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()
	s.startConversationWorker(group.ID)
	done := make(chan struct{})
	go func() { finished.Wait(); close(done) }()
	started := time.Now()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("the runs did not finish: the connection or a lock is stuck")
	}
	elapsed := time.Since(started)
	close(stop)
	side.Wait()
	for _, r := range runs {
		// A Bot that is written to more than the reading cap allows closes into a
		// continuation run (steered); the others finish.
		waitRunStatus(t, s.store, r.ID, "done", "failed", "cancelled")
	}
	p := func(d []time.Duration, q float64) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[int(float64(len(d)-1)*q)]
	}
	t.Logf("6 Bots x %d steps in %v; engine DB ops n=%d p50=%v p99=%v max=%v; connection probe n=%d p50=%v p99=%v max=%v",
		steps, elapsed.Round(time.Millisecond), len(opLat), p(opLat, 0.5), p(opLat, 0.99), p(opLat, 1),
		len(probe), p(probe, 0.5), p(probe, 0.99), p(probe, 1))
	if p(probe, 0.99) > 500*time.Millisecond || p(opLat, 0.99) > 500*time.Millisecond {
		t.Fatalf("the connection wait is unbounded: probe p99=%v op p99=%v", p(probe, 0.99), p(opLat, 0.99))
	}
}

// A head that waits on its parent must not hold the Bot back from work that can
// start: the Bot's other conversations are served while it waits.
func TestABlockedHeadDoesNotStarveTheBotsOtherConversations(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	x, _ := s.store.CreateGroup("x", []string{a.ID, b.ID})
	// Bravo works in x; a run for Alpha is queued in x as its child, older than
	// anything Alpha has in its DM.
	parent := queueRun(t, s.store, x.ID, b.ID, "", "")
	gate := p.gate(parent.ID)
	s.startConversationWorker(x.ID)
	waitStarted(t, p, parent.ID, 5*time.Second)
	child := queueRun(t, s.store, x.ID, a.ID, "", parent.ID)
	direct := queueRun(t, s.store, dm.ID, a.ID, "", "")
	s.startConversationWorker(dm.ID)
	s.startConversationWorker(x.ID)
	waitRunStatus(t, s.store, direct.ID, "done")
	if got, _ := s.store.GetRun(child.ID); got.Status != "queued" {
		t.Fatalf("the child ran before its parent ended: %s", got.Status)
	}
	close(gate)
	waitRunStatus(t, s.store, child.ID, "done")
}
