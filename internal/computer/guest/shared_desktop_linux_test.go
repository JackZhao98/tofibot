//go:build linux

package guest

import (
	"strings"
	"sync"
	"testing"
)

func TestSharedChromeStartContractKeepsFreshTabAndSharedProfile(t *testing.T) {
	args := chromeStartArgs("/workspace/browser/shared", ":100", 40123)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--user-data-dir=/workspace/browser/shared", "--disable-restore-session-state", "--new-window", "about:blank"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("shared Chrome args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--restore-last-session") {
		t.Fatalf("shared Chrome args restore an old page: %s", joined)
	}
}

func TestSharedDesktopAliasBindingRejectsStaleStopReplacement(t *testing.T) {
	s := newTestService(t)
	d := &desktop{botID: sharedDesktopKey}
	s.mu.Lock()
	s.sharedDesktop = d
	s.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			_ = s.bindSharedDesktopAliasLocked(testBotB, d)
			s.mu.Unlock()
		}()
	}
	wg.Wait()
	s.mu.Lock()
	if s.desktops[testBotB] != d {
		s.mu.Unlock()
		t.Fatal("shared alias was not bound")
	}
	d.stopping = true
	if s.bindSharedDesktopAliasLocked(testBot, d) {
		s.mu.Unlock()
		t.Fatal("stopping desktop accepted a new alias")
	}
	s.mu.Unlock()
}
