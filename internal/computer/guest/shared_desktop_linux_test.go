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

func TestChromeMemoryArgsKeepIsolationAndUseOneFeatureSwitch(t *testing.T) {
	args := chromeStartArgs("/workspace/browser/shared", ":100", 40123)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--process-per-site", "--renderer-process-limit=4",
		"--disable-component-extensions-with-background-pages",
		"--disable-features=BackForwardCache,SpareRendererForSitePerProcess,",
		"--remote-debugging-port=40123", "--user-data-dir=/workspace/browser/shared",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Chrome args missing %q: %s", want, joined)
		}
	}
	// Chrome honors only the last --disable-features switch.
	if strings.Count(joined, "--disable-features=") != 1 || strings.Contains(joined, "--enable-features=") {
		t.Fatalf("Chrome feature switches must be a single list: %s", joined)
	}
	// Security and login persistence must not be traded for memory.
	for _, banned := range []string{
		"--disable-site-isolation-trials", "--site-per-process=false", "--single-process",
		"--disable-background-networking", "--disable-extensions", "--incognito", "--no-sandbox",
		"--disable-web-security", "--js-flags",
	} {
		if strings.Contains(joined, banned) {
			t.Fatalf("Chrome args contain unsafe %q: %s", banned, joined)
		}
	}
	// The trailing window arguments keep their established order.
	if args[len(args)-2] != "--new-window" || args[len(args)-1] != "about:blank" {
		t.Fatalf("Chrome args must end with a fresh window: %v", args)
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
