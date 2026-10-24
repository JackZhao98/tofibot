package app

// Per-Bot lanes (decision 0062). A Bot does one run at a time, in every
// conversation it belongs to, and different Bots run in parallel. The unit of
// serial work is the Bot, not the conversation.
//
// The conversation worker is still the thing that looks at queued runs: it
// picks the next run whose Bot has a free lane, starts it in its own
// goroutine and goes on picking, so two Bots of one group run side by side.
// The lane is an in-memory map (one run id per Bot) backed by a recheck of the
// database (`status='running'` for the same Bot), so a run started by another
// path, a recovered run or a test that executes directly still counts as busy.
//
// When one Bot has queued work in several conversations, the head of each
// conversation (its first queued run in the conversation's own order) competes
// by laneRank: the next run of the task tree the Bot just left, then a
// person's message, then a delegation, then a schedule. Inside one
// conversation the older order stays (a delegated child before the round run
// it supersedes), because the round rules depend on it.

import (
	"sort"
	"sync"
)

const (
	laneExpiry = iota
	laneTree
	lanePerson
	laneDelegation
	laneSchedule
)

type laneState struct {
	mu       sync.Mutex
	holder   map[string]string // bot id -> run id that holds its lane
	lastTree map[string]string // bot id -> lineage key of the run that held it last
	inflight map[string]bool   // run ids a worker has started and not yet finished
}

func (l *laneState) init() {
	if l.holder == nil {
		l.holder = map[string]string{}
		l.lastTree = map[string]string{}
		l.inflight = map[string]bool{}
	}
}

// laneClass orders runs of one Bot that wait in different conversations.
func laneClass(r Run, expiry, sameTree bool) int {
	switch {
	case expiry:
		return laneExpiry
	case sameTree:
		return laneTree
	case r.Kind == runKindSchedule:
		return laneSchedule
	case r.Kind == "" || r.Kind == runKindTriage || (r.Kind == runKindGroupChat && r.ParentRunID == ""):
		return lanePerson
	}
	return laneDelegation
}

type laneRank struct {
	class     int
	createdAt string
	queueSeq  int64
	id        string
}

func (a laneRank) before(b laneRank) bool {
	if a.class != b.class {
		return a.class < b.class
	}
	if a.createdAt != b.createdAt {
		return a.createdAt < b.createdAt
	}
	if a.queueSeq != b.queueSeq {
		return a.queueSeq < b.queueSeq
	}
	return a.id < b.id
}

// queuedEligibleSQL is the part of the old queue query that decides whether a
// run can start: queued, or a claimed approval-expiry settlement that is due,
// and not held back by the schedule order gate. Its two arguments are the
// expiry retry cutoff and the schedule order cutoff.
const queuedEligibleSQL = `(status='queued' OR (status='running' AND EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=runs.id AND e.state='claimed' AND e.retry_after<=?))) AND NOT (` + scheduleOrderBlockedSQL + `)`

const queuedOrderSQL = ` ORDER BY CASE WHEN EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=runs.id AND e.state IN ('ready','claimed')) THEN -1 WHEN kind IN ('group_task','group_followup','group_mention') THEN 0 WHEN kind='schedule' THEN 2 ELSE 1 END,queue_seq,created_at,id`

const laneRunColumns = `id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at`

// queuedRunsOrdered lists the runs that could start now in the conversation's
// own order, for one Bot or (bot == "") for all of them.
func (s *Store) queuedRunsOrdered(conv, bot string) ([]Run, error) {
	query := `SELECT ` + laneRunColumns + ` FROM runs WHERE conversation_id=? AND ` + queuedEligibleSQL
	args := []any{conv, now(), scheduleTime(scheduleOrderClock().Add(-scheduleOrderMaxWait))}
	if bot != "" {
		query += ` AND bot_id=?`
		args = append(args, bot)
	}
	return s.scanRunList(query+queuedOrderSQL, args...)
}

func (s *Store) scanRunList(query string, args ...any) ([]Run, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queuedRunsForBot lists the Bot's runs that could start, in every active
// conversation, each conversation's runs in that conversation's order.
func (s *Store) queuedRunsForBot(bot string) ([]Run, error) {
	query := `SELECT ` + laneRunColumns + ` FROM runs WHERE bot_id=? AND kind<>'triage' AND ` + queuedEligibleSQL +
		` AND EXISTS(SELECT 1 FROM conversations c WHERE c.id=runs.conversation_id AND c.archived=0)` + queuedOrderSQL
	return s.scanRunList(query, bot, now(), scheduleTime(scheduleOrderClock().Add(-scheduleOrderMaxWait)))
}

// laneBusyInDB is true when the Bot has another run in flight: running and not
// a parked approval settlement waiting to be picked up.
func (s *Store) laneBusyInDB(bot, except string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND status='running' AND id<>? AND kind<>'triage'
		AND NOT EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=runs.id AND e.state='claimed')`, bot, except).Scan(&n)
	return n > 0
}

func (s *Server) laneRankOf(r Run) laneRank {
	expiry := s.store.hasApprovalExpiry(r.ID)
	s.lanes.mu.Lock()
	last := s.lanes.lastTree[r.BotID]
	s.lanes.mu.Unlock()
	same := last != "" && lineageKey(s.store.db, r.ID) == last
	return laneRank{class: laneClass(r, expiry, same), createdAt: r.CreatedAt, queueSeq: r.QueueSeq, id: r.ID}
}

// laneWinner reports whether r is the run the Bot should take next among the
// heads of its conversations. A head is a conversation's first run that can
// start now; one that waits on an order (its parent still runs) or is already
// starting does not compete, and neither does a conversation nobody works for:
// a head that cannot start must not hold the others back.
func (s *Server) laneWinner(r Run) bool {
	all, err := s.store.queuedRunsForBot(r.BotID)
	if err != nil {
		return true
	}
	mine := s.laneRankOf(r)
	seen := map[string]bool{r.ConversationID: true}
	for _, h := range all {
		if seen[h.ConversationID] {
			continue
		}
		s.mu.Lock()
		worker := s.queues[h.ConversationID] != nil
		s.mu.Unlock()
		if !worker || s.isInflight(h.ID) || s.store.runOrderBlocked(h) {
			continue
		}
		seen[h.ConversationID] = true
		if s.laneRankOf(h).before(mine) {
			return false
		}
	}
	return true
}

// acquireLane takes the Bot's lane for r. It fails when the Bot already has a
// run in flight, here or in the database.
func (s *Server) acquireLane(r Run) bool {
	if s.store.laneBusyInDB(r.BotID, r.ID) {
		return false
	}
	s.lanes.mu.Lock()
	defer s.lanes.mu.Unlock()
	s.lanes.init()
	if holder := s.lanes.holder[r.BotID]; holder != "" && holder != r.ID {
		return false
	}
	s.lanes.holder[r.BotID] = r.ID
	return true
}

func (s *Server) laneBusyInMemory(bot, except string) bool {
	s.lanes.mu.Lock()
	defer s.lanes.mu.Unlock()
	s.lanes.init()
	holder := s.lanes.holder[bot]
	return holder != "" && holder != except
}

// releaseLane frees the lane and remembers which task tree held it last, then
// wakes every worker: the freed Bot may have work in any conversation.
func (s *Server) releaseLane(r Run) {
	tree := lineageKey(s.store.db, r.ID)
	s.lanes.mu.Lock()
	s.lanes.init()
	if s.lanes.holder[r.BotID] == r.ID {
		delete(s.lanes.holder, r.BotID)
	}
	s.lanes.lastTree[r.BotID] = tree
	s.lanes.mu.Unlock()
}

func (s *Server) markInflight(id string, on bool) bool {
	s.lanes.mu.Lock()
	defer s.lanes.mu.Unlock()
	s.lanes.init()
	if on {
		if s.lanes.inflight[id] {
			return false
		}
		s.lanes.inflight[id] = true
		return true
	}
	delete(s.lanes.inflight, id)
	return true
}

func (s *Server) isInflight(id string) bool {
	s.lanes.mu.Lock()
	defer s.lanes.mu.Unlock()
	s.lanes.init()
	return s.lanes.inflight[id]
}

// pickRunnable returns the next run of the conversation that can start now: a
// triage run (it has no tools and takes no lane), else the first run in the
// conversation's order whose Bot has a free lane and who wins the Bot's
// conversations. The run is not claimed. The third result says some run waits on
// an order or a lane held elsewhere: its worker rechecks on a short tick as well
// as on the wake the end of a run sends, so no missed wake can strand it.
func (s *Server) pickRunnable(conv string) (Run, bool, bool, error) {
	if err := s.store.refreshInputWaits(conv); err != nil {
		return Run{}, false, false, err
	}
	if err := s.store.ensureSchedules(); err != nil {
		return Run{}, false, false, err
	}
	cands, err := s.store.queuedRunsOrdered(conv, "")
	if err != nil {
		return Run{}, false, false, err
	}
	waiting := false
	configured := s.modelConfigured()
	seenBot := map[string]bool{}
	for _, r := range cands {
		s.mu.Lock()
		active := s.runs[r.ID] != nil
		s.mu.Unlock()
		if active || s.isInflight(r.ID) {
			continue
		}
		if !configured && !s.store.hasApprovalExpiry(r.ID) {
			continue
		}
		if r.Kind == runKindTriage {
			return r, true, waiting, nil
		}
		if s.store.runOrderBlocked(r) {
			waiting = true
			continue
		}
		// One Bot, one head: a later run of a Bot never overtakes its earlier one.
		if seenBot[r.BotID] {
			continue
		}
		seenBot[r.BotID] = true
		if s.laneBusyInMemory(r.BotID, r.ID) || s.store.laneBusyInDB(r.BotID, r.ID) {
			waiting = true
			continue
		}
		if !s.laneWinner(r) {
			waiting = true
			continue
		}
		return r, true, waiting, nil
	}
	return Run{}, false, waiting, nil
}

// launchRun starts r in its own goroutine under the Bot's lane. It reports
// false when the lane was taken in the meantime.
func (s *Server) launchRun(q *conversationQueue, r Run) bool {
	if !s.markInflight(r.ID, true) {
		return false
	}
	if r.Kind != runKindTriage && !s.acquireLane(r) {
		s.markInflight(r.ID, false)
		return false
	}
	q.running.Add(1)
	go func() {
		defer q.running.Done()
		defer func() {
			if r.Kind != runKindTriage {
				s.releaseLane(r)
			}
			s.markInflight(r.ID, false)
			s.wakeAfterRun(r)
		}()
		s.executeForQueue(r)
	}()
	return true
}

// sortRunsByLaneRank is used by tests and diagnostics to show the order the
// lanes would take heads in.
func (s *Server) sortRunsByLaneRank(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool { return s.laneRankOf(runs[i]).before(s.laneRankOf(runs[j])) })
}

// runOrderBlocked keeps the two orders the serial worker used to give for free.
// A run does not start while the run that made it is still running or queued
// (a delegated child, an invited member, a wake start after the turn that
// created them), and a Bot addressed together with others by one message does
// not start before the members addressed ahead of it have finished (a member's
// note is input for the next one). Everything else runs side by side.
func (s *Store) runOrderBlocked(r Run) bool {
	if r.ParentRunID != "" {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND status IN ('running','queued')`, r.ParentRunID).Scan(&n)
		if n > 0 {
			return true
		}
	}
	if r.ParentRunID == "" && r.TriggerMessageID != "" && (r.Kind == "" || r.Kind == runKindGroupChat) {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs o WHERE o.conversation_id=? AND o.trigger_message_id=? AND o.id<>? AND o.queue_seq<? AND COALESCE(o.parent_run_id,'')='' AND o.kind IN ('',?) AND o.status IN ('queued','running')`,
			r.ConversationID, r.TriggerMessageID, r.ID, r.QueueSeq, runKindGroupChat).Scan(&n)
		if n > 0 {
			return true
		}
	}
	return false
}

// wakeTargets are the conversations whose workers can have something to start
// now that r ended: its own (a sibling may no longer wait for it), the ones
// where r's Bot has queued work (its lane is free) and the ones that hold its
// children.
func (s *Store) wakeTargets(r Run) []string {
	rows, err := s.db.Query(`SELECT DISTINCT conversation_id FROM runs WHERE status='queued' AND (bot_id=? OR parent_run_id=?)`, r.BotID, r.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{r.ConversationID}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// wakeAfterRun signals those workers. A signal missed anyway is caught by the
// worker's own short recheck while it has something waiting.
func (s *Server) wakeAfterRun(r Run) {
	targets := s.store.wakeTargets(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range targets {
		if q := s.queues[id]; q != nil {
			q.signal()
		}
	}
}
