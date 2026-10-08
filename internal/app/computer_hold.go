package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// Expiring guest holds prevent idle collection between model tool calls. They
// never start a desktop and expire after control-plane failure.
func (s *Server) renewComputerHold(ctx context.Context, r Run) error {
	if s.microVM == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.microVM.Action(callCtx, computer.Action{BotID: r.BotID, RunID: r.ID, Name: "desktop.hold", Source: "model", Args: json.RawMessage(`{"ttl_sec":90}`)})
	return err
}

func (s *Server) ownsComputer(r Run) bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	return s.computerOwners[r.BotID] == r.ID
}

func (s *Server) maintainComputerHold(parent context.Context, r Run) func() {
	if s.microVM == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				lease := s.computerLease(r.BotID)
				if lockComputerLease(ctx, lease) == nil {
					if s.ownsComputer(r) {
						_ = s.renewComputerHold(ctx, r)
					}
					lease.Unlock()
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done // A renewal must not race the final release.
		if !s.ownsComputer(r) {
			return
		}
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer releaseCancel()
		// The run is over: the guest closes tabs this run opened, keeping the
		// most recently active one. Mid-run yields never send this flag.
		_, _ = s.microVM.Action(releaseCtx, computer.Action{BotID: r.BotID, RunID: r.ID, Name: "desktop.release", Source: "model", Args: json.RawMessage(`{"close_run_tabs":true}`)})
	}
}
