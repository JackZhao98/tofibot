package app

// One sweeper ends the waits whose deadline has passed and wakes the Bots with
// inbox rows that ask for it. It runs at start and then every 15 seconds. All
// state is in the tables, so a restart loses nothing: an overdue wait is ended
// by the first sweep after the start. The same loop runs the daily cleanup of
// finished tasks.

import (
	"context"
	"time"
)

const (
	waitSweepTick      = 15 * time.Second
	taskRetentionEvery = 24 * time.Hour
)

// sweepWaits is one pass: end due waits, settle the ones that mirror a
// question, and wake whoever the results are for.
func (s *Server) sweepWaits(at time.Time) {
	if s.store == nil {
		return
	}
	if _, err := s.store.expireDueWaits(at); err != nil {
		logTaskError("expire waits", err)
	}
	if err := s.store.reconcileHumanAnswerWaits(); err != nil {
		logTaskError("human answer waits", err)
	}
	if err := s.store.reconcileTaskRuns(); err != nil {
		logTaskError("task runs", err)
	}
	s.flushInbox()
}

func (s *Server) startWaitSweeper() {
	if s.store == nil || s.waitSweepCancel != nil {
		return
	}
	interval := s.waitSweepEvery
	if interval <= 0 {
		interval = waitSweepTick
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.waitSweepCancel, s.waitSweepDone = cancel, make(chan struct{})
	s.sweepWaits(time.Now()) // the start sweep: overdue waits end now
	go func() {
		defer close(s.waitSweepDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var lastRetention time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				s.sweepWaits(at)
				if lastRetention.IsZero() || at.Sub(lastRetention) >= taskRetentionEvery {
					lastRetention = at
					s.runTaskRetention(at)
				}
			}
		}
	}()
}

func (s *Server) stopWaitSweeper() {
	if s.waitSweepCancel == nil {
		return
	}
	s.waitSweepCancel()
	<-s.waitSweepDone
}
