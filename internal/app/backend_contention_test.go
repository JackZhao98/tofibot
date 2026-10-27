package app

import (
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

// With one database connection a slow read blocks every write behind it. This
// measures how long a stream delta waits while the conversation list is polled
// in a loop on a large account, which is what the sidebar does every few
// seconds per open tab.
func TestStreamDeltaLatencyWhileListIsPolled(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds a large account")
	}
	s := seedLargeAccount(t, 600)
	if err := s.EnsureStreamSchema(); err != nil {
		t.Fatal(err)
	}
	run, err := s.AddRun("conv_001", "bot_001", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start run: %v %v", ok, err)
	}
	if _, err = s.BeginStream(run); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := s.ListConversations(true); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	var waits []time.Duration
	for i := 0; i < 150; i++ {
		start := time.Now()
		if _, _, err := s.AppendStreamDelta(t.Context(), run.ID, "tick "); err != nil {
			t.Fatal(err)
		}
		waits = append(waits, time.Since(start))
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	p50, p99 := waits[len(waits)/2], waits[len(waits)*99/100]
	t.Logf("delta append while the list is polled: p50=%v p99=%v max=%v", p50, p99, waits[len(waits)-1])
	// TOFI_SKIP_TIMING=1 is the repo-wide switch for CI (see docs/process.md 3.3): a
	// shared runner stalls for hundreds of milliseconds at random, which says
	// nothing about the connection starving the append. The run above still
	// checks that every append succeeds while the list is polled; the bound is
	// enforced on a quiet machine.
	if os.Getenv("TOFI_SKIP_TIMING") == "1" {
		t.Skip("p99 bound skipped (timing check, CI mode)")
	}
	if p99 > 250*time.Millisecond {
		t.Fatalf("stream delta p99 %v while the list is polled", p99)
	}
}
