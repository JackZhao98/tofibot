package guest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestShellAndFilesDoNotWaitForSharedGraphicsGate(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	gate := s.inputGate(testBot)
	gate.Lock()
	defer gate.Unlock()

	result, err := s.action(context.Background(), ActionRequest{
		BotID: testBot, RunID: "shell-parallel", Action: "shell.exec", Source: ActionSourceModel,
		Args: json.RawMessage(`{"command":"printf parallel"}`),
	})
	if err != nil {
		t.Fatalf("shell was blocked by graphics gate: %v", err)
	}
	if got := result.(map[string]any)["stdout"]; got != "parallel" {
		t.Fatalf("shell result=%#v", result)
	}
	if _, err := s.action(context.Background(), ActionRequest{
		BotID: testBot, RunID: "files-parallel", Action: "files.write", Source: ActionSourceModel,
		Args: json.RawMessage(`{"path":"shared.txt","content":"parallel"}`),
	}); err != nil {
		t.Fatalf("files.write was blocked by graphics gate: %v", err)
	}
	if _, err := s.readFile(context.Background(), testBot, fileArgs{Path: "shared.txt"}); err != nil {
		t.Fatal(err)
	}
}

func TestSharedDesktopAliasesResolveToOneSession(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	d := &desktop{botID: sharedDesktopKey, shared: true, readyClosed: true}
	s.mu.Lock()
	s.sharedDesktop = d
	s.desktops[testBot] = d
	s.desktops[testBotB] = d
	s.mu.Unlock()

	for _, bot := range []string{testBot, testBotB} {
		got, err := s.currentDesktop(context.Background(), bot)
		if err != nil {
			t.Fatalf("current desktop %s: %v", bot, err)
		}
		if got != d {
			t.Fatalf("bot %s resolved a different desktop", bot)
		}
	}
	if got := s.inputGate(testBot); got != s.inputGate(testBotB) {
		t.Fatal("shared desktop input gate must be workspace-wide")
	}
}

func TestSharedDesktopStreamAllowsBoundedViewers(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	d := &desktop{botID: sharedDesktopKey, shared: true, readyClosed: true}
	s.mu.Lock()
	s.sharedDesktop = d
	s.desktops[testBot] = d
	s.desktops[testBotB] = d
	s.mu.Unlock()

	for i := 0; i < maxSharedDesktopViewers; i++ {
		got, status := s.reserveDesktopStream(testBot)
		if status != 200 || got != d {
			t.Fatalf("viewer %d reserve: status=%d desktop=%p", i, status, got)
		}
	}
	if _, status := s.reserveDesktopStream(testBotB); status != 409 {
		t.Fatalf("viewer limit status=%d, want 409", status)
	}
	if d.streamViewers != maxSharedDesktopViewers {
		t.Fatalf("viewer count=%d", d.streamViewers)
	}
	// Releasing a viewer must make exactly one slot available.
	s.mu.Lock()
	d.streamViewers--
	s.mu.Unlock()
	if _, status := s.reserveDesktopStream(testBotB); status != 200 {
		t.Fatalf("reserve after release status=%d", status)
	}
}

func TestSharedDesktopInputOwnerBlocksOtherBot(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	d := &desktop{botID: sharedDesktopKey, shared: true, readyClosed: true}
	s.mu.Lock()
	s.sharedDesktop = d
	s.desktops[testBot] = d
	s.desktops[testBotB] = d
	s.inputs[testBot] = &inputSession{id: "human-control:a", ownerBot: testBot, desktop: d, expires: time.Now().Add(time.Minute)}
	s.activeInput = s.inputs[testBot]
	s.mu.Unlock()

	gateA := s.inputGate(testBot)
	gateB := s.inputGate(testBotB)
	gateA.Lock()
	var wg sync.WaitGroup
	locked := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		gateB.Lock()
		close(locked)
		gateB.Unlock()
	}()
	select {
	case <-locked:
		t.Fatal("different Bot acquired the shared input gate concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	gateA.Unlock()
	wg.Wait()
}
