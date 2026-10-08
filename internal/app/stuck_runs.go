package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const (
	// stuckRunCode prefixes the durable error of a run concluded by the sweeper.
	stuckRunCode = "tool_stuck"
	// stuckToolMargin is added to the runtime's hard tool deadline. A call
	// still "running" after both has lost its executor or its response.
	stuckToolMargin   = 5 * time.Minute
	stuckToolAfter    = runtime.MaxToolTimeout + stuckToolMargin
	stuckRunSweepTick = time.Minute
)

func (s *Server) startStuckRunSweeper() {
	if s.store == nil || s.stuckRunCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stuckRunCancel, s.stuckRunDone = cancel, make(chan struct{})
	go func() {
		defer close(s.stuckRunDone)
		ticker := time.NewTicker(stuckRunSweepTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileStuckRuns(ctx, time.Now())
			}
		}
	}()
}

func (s *Server) stopStuckRunSweeper() {
	if s.stuckRunCancel == nil {
		return
	}
	s.stuckRunCancel()
	<-s.stuckRunDone
}

// noteDesktopQueueLeft records when a run stopped waiting for the shared
// desktop; that wait does not count toward a tool call's deadline.
func (s *Server) noteDesktopQueueLeftLocked(runID string, at time.Time) {
	if s.desktopQueueLeft == nil {
		s.desktopQueueLeft = map[string]time.Time{}
	}
	s.desktopQueueLeft[runID] = at
}

type stuckToolCall struct {
	runID, conversationID, name string
	since                       time.Time
}

// reconcileStuckRuns concludes runs whose tool call has stayed "running" past
// the runtime's hard deadline plus a margin: the executor or its response was
// lost (for example the host froze mid-call). Waits for a person or for the
// shared desktop are excluded, as they are from the deadline itself.
func (s *Server) reconcileStuckRuns(ctx context.Context, at time.Time) []string {
	rows, err := s.store.db.QueryContext(ctx, `SELECT a.run_id,r.conversation_id,a.name,a.updated_at,
  COALESCE((SELECT MAX(q.updated_at) FROM questions q WHERE q.run_id=a.run_id),'')
FROM tool_activities a JOIN runs r ON r.id=a.run_id
WHERE a.status='running' AND r.status='running'
  AND NOT EXISTS (SELECT 1 FROM questions q WHERE q.run_id=a.run_id AND q.status=?)`, questionPending)
	if err != nil {
		return nil
	}
	var candidates []stuckToolCall
	for rows.Next() {
		var c stuckToolCall
		var updated, answered string
		if rows.Scan(&c.runID, &c.conversationID, &c.name, &updated, &answered) != nil {
			continue
		}
		since, parseErr := time.Parse(time.RFC3339Nano, updated)
		if parseErr != nil {
			continue
		}
		if t, e := time.Parse(time.RFC3339Nano, answered); e == nil && t.After(since) {
			since = t
		}
		c.since = since
		candidates = append(candidates, c)
	}
	rows.Close()
	var concluded []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c.runID] {
			continue
		}
		s.computerOwnerMu.Lock()
		queued := false
		for _, w := range s.desktopWaiters {
			if w.RunID == c.runID {
				queued = true
			}
		}
		if left, ok := s.desktopQueueLeft[c.runID]; ok && left.After(c.since) {
			c.since = left
		}
		s.computerOwnerMu.Unlock()
		if queued || at.Sub(c.since) < stuckToolAfter {
			continue
		}
		seen[c.runID] = true
		reason := fmt.Sprintf("%s: %s did not finish within %s; the run was stopped", stuckRunCode, c.name, stuckToolAfter)
		ok, err := s.store.SetRunStatus(c.runID, "failed", reason)
		if err != nil || !ok {
			continue
		}
		log.Printf("[runs] concluded stuck run %s: %s running since %s", c.runID, c.name, c.since.UTC().Format(time.RFC3339))
		concluded = append(concluded, c.runID)
		if failed, e := s.store.GetRun(c.runID); e == nil {
			_, _ = s.store.Event(c.conversationID, "run", failed)
		}
		// Unwind a live executor; its own terminal write cannot reopen the run.
		s.mu.Lock()
		cancel := s.runs[c.runID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return concluded
}
