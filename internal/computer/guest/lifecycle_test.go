package guest

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDesktopHoldDoesNotStartDesktopAndIsIdempotent(t *testing.T) {
	s := newTestService(t)
	result, err := s.holdDesktop(testBot, "run-1", []byte(`{"ttl_sec":30}`))
	if err != nil || result["bot_id"] != testBot {
		t.Fatalf("hold result=%#v err=%v", result, err)
	}
	result, err = s.holdDesktop(testBot, "run-1", []byte(`{"ttl_sec":30}`))
	if err != nil || result["run_id"] != "run-1" {
		t.Fatalf("repeat hold result=%#v err=%v", result, err)
	}
	s.mu.Lock()
	if !s.hasActiveHoldLocked(testBot, time.Now()) || s.desktops[testBot] != nil {
		s.mu.Unlock()
		t.Fatal("hold unexpectedly started or failed to protect a desktop")
	}
	s.mu.Unlock()
	if _, err := s.releaseDesktop(testBot, "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStopSharesCleanup(t *testing.T) {
	s := newTestService(t)
	d := &desktop{botID: testBot, ready: make(chan struct{}), readyClosed: true, stopDone: make(chan struct{})}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.stopDesktop(context.Background(), testBot)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && runtime.GOOS == "linux" {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.desktops) != 0 {
		t.Fatal("stopped desktop remained in map")
	}
}

func TestShellWaitsForAnnouncedDesktopTeardown(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("desktop teardown race requires Linux guest process support")
	}
	s := newTestService(t)
	d := &desktop{botID: testBot, ready: make(chan struct{}), readyClosed: true,
		stopDone: make(chan struct{}), inFlight: 1, stopping: true}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()

	done := make(chan struct{})
	var result any
	var err error
	go func() {
		result, err = s.action(context.Background(), ActionRequest{
			BotID: testBot, RunID: "shell-teardown", Action: "shell.exec",
			Args: []byte(`{"command":"printf shell-finished"}`),
		})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("shell ran while desktop teardown was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	// Release the operation that caused the announced teardown. The single
	// teardown worker can now finish and the Shell retries against the empty
	// desktop map without starting a new desktop.
	s.mu.Lock()
	d.inFlight = 0
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shell did not proceed after teardown completed")
	}
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.(map[string]any)
	if !ok || got["stdout"] != "shell-finished" {
		t.Fatalf("shell result=%#v", result)
	}
	s.mu.Lock()
	_, stillRunning := s.desktops[testBot]
	s.mu.Unlock()
	if stillRunning {
		t.Fatal("ordinary shell unexpectedly restarted the desktop")
	}
}

func TestShellTeardownWaitHonorsCancellation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("desktop teardown cancellation requires Linux guest process support")
	}
	s := newTestService(t)
	d := &desktop{botID: testBot, ready: make(chan struct{}), readyClosed: true,
		stopDone: make(chan struct{}), inFlight: 1, stopping: true}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := s.action(ctx, ActionRequest{
			BotID: testBot, RunID: "shell-cancel", Action: "shell.exec",
			Args: []byte(`{"command":"printf should-not-run"}`),
		})
		done <- callErr
	}()
	deadline := time.Now().Add(time.Second)
	started := false
	for time.Now().Before(deadline) {
		d.stopRequestMu.Lock()
		running := d.stopRequestRunning
		d.stopRequestMu.Unlock()
		if running {
			started = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !started {
		t.Fatal("shell did not join the announced teardown")
	}
	cancel()
	select {
	case callErr := <-done:
		if callErr == nil || callErr.Error() != "context canceled" {
			t.Fatalf("cancelled shell error=%v", callErr)
		}
	case <-time.After(time.Second):
		t.Fatal("shell teardown wait ignored cancellation")
	}

	// Let the background teardown worker finish so the test does not leave a
	// stopping desktop behind for Service.Close.
	s.mu.Lock()
	d.inFlight = 0
	s.mu.Unlock()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, exists := s.desktops[testBot]
		s.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cancelled shell left teardown worker or desktop behind")
}

func TestCancelledStopContinuesAfterOperationFinishes(t *testing.T) {
	s := newTestService(t)
	d := &desktop{botID: testBot, ready: make(chan struct{}), readyClosed: true, stopDone: make(chan struct{}), inFlight: 1}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.stopDesktop(ctx, testBot); err == nil {
		t.Fatal("cancelled stop unexpectedly succeeded")
	}
	s.mu.Lock()
	if !d.stopping {
		s.mu.Unlock()
		t.Fatal("cancelled stop released stopping state before operation finished")
	}
	d.inFlight = 0
	s.mu.Unlock()
	select {
	case <-d.stopDone:
	case <-time.After(time.Second):
		t.Fatal("background stop did not finish after operation released")
	}
}

func TestViewerReadDoesNotAutoStartDesktop(t *testing.T) {
	s := newTestService(t)
	_, err := s.action(context.Background(), ActionRequest{
		BotID: testBot, RunID: "viewer-1", Action: "desktop.capture", Source: ActionSourceViewer,
		Args: []byte(`{}`),
	})
	if err == nil || err.Error() != "desktop is not running; call desktop.start first" {
		t.Fatalf("viewer error=%v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.desktops) != 0 {
		t.Fatal("viewer read allocated a desktop")
	}
}

func TestIdleReaperHonorsOperationAndHold(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 2, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	d := &desktop{
		botID: testBot, ready: make(chan struct{}), readyClosed: true,
		stopDone: make(chan struct{}), lastActivity: time.Now().Add(-time.Hour),
	}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()
	release, err := s.beginDesktopOperation(context.Background(), testBot, true)
	if err != nil {
		t.Fatal(err)
	}
	// An in-flight model action keeps the desktop in the map even though its
	// previous activity is old.
	s.reapIdleDesktops()
	s.mu.Lock()
	if s.desktops[testBot] != d {
		s.mu.Unlock()
		t.Fatal("reaper stopped an in-flight desktop")
	}
	s.mu.Unlock()
	release()

	if _, err := s.holdDesktop(testBot, "run-1", nil); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	d.lastActivity = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.reapIdleDesktops()
	s.mu.Lock()
	if s.desktops[testBot] != d {
		s.mu.Unlock()
		t.Fatal("reaper stopped a held desktop")
	}
	s.mu.Unlock()
	if _, err := s.releaseDesktop(testBot, "run-1"); err != nil {
		t.Fatal(err)
	}
	s.reapIdleDesktops()
	select {
	case <-d.stopDone:
	case <-time.After(time.Second):
		t.Fatal("idle desktop was not stopped")
	}
}
