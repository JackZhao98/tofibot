package app

import (
	"context"
	"log"
	"sync"
	"time"
)

// The background copier of legacy `vm:` attachments (design §5.4). It runs once
// at start and then every ten minutes while `vm:` rows remain. The row is the
// only state, so a restart, a crash or a second copier changes nothing.
const (
	fileCopyInterval = 10 * time.Minute
	fileCopyBatch    = 50
	fileCopyWakeGap  = time.Hour
)

type fileStoreRuntime struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	lastWake time.Time
	// interval is overridden by tests.
	interval time.Duration
}

func (s *Server) startFileStoreCopier() {
	if s.microVM == nil || s.store == nil {
		return
	}
	rt := &s.fileStore
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.cancel != nil {
		return
	}
	interval := rt.interval
	if interval <= 0 {
		interval = fileCopyInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.cancel, rt.done = cancel, make(chan struct{})
	go func() {
		defer close(rt.done)
		s.copyLegacyFiles(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.copyLegacyFiles(ctx)
			}
		}
	}()
}

func (s *Server) stopFileStoreCopier() {
	rt := &s.fileStore
	rt.mu.Lock()
	cancel, done := rt.cancel, rt.done
	rt.cancel = nil
	rt.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// copyLegacyFiles is one pass. It never wakes a sleeping computer more than
// once an hour, so the migration does not keep a computer awake.
func (s *Server) copyLegacyFiles(ctx context.Context) {
	pending, err := s.store.pendingGuestCopies()
	if err != nil || pending == 0 {
		return
	}
	if s.microVM != nil {
		info, ierr := s.microVM.Info(ctx)
		if ierr != nil {
			return // the computer does not answer; rows stay `vm:`
		}
		if info.State != "ready" {
			rt := &s.fileStore
			rt.mu.Lock()
			wake := rt.lastWake.IsZero() || time.Since(rt.lastWake) >= fileCopyWakeGap
			if wake {
				rt.lastWake = time.Now()
			}
			rt.mu.Unlock()
			if !wake {
				return
			}
		}
	}
	copied, failed, err := s.store.copyGuestRows(ctx, fileCopyBatch)
	if err != nil {
		log.Printf("[files] copy pass failed: %v", err)
		return
	}
	if copied > 0 || failed > 0 {
		left, _ := s.store.pendingGuestCopies()
		log.Printf("[files] server file store: %d copied, %d failed, %d still on the computer", copied, failed, left)
	}
	s.store.cleanupDeletedAttachments()
}
