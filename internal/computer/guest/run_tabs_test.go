package guest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunTabsToCloseKeepsBaselineAndMostRecentTab(t *testing.T) {
	baseline := map[string]bool{"old": true}
	for _, tc := range []struct {
		current []string
		want    []string
	}{
		{[]string{"new2", "new1", "old"}, []string{"new1"}},
		{[]string{"old", "new1", "new2"}, []string{"new1", "new2"}},
		{[]string{"new2", "new1"}, []string{"new1"}},
		{[]string{"old"}, nil},
		{nil, nil},
	} {
		if got := runTabsToClose(baseline, tc.current); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("runTabsToClose(%v) = %v, want %v", tc.current, got, tc.want)
		}
	}
}

type fakeChromeTabs struct {
	mu     sync.Mutex
	pages  []string
	closed []string
}

func (f *fakeChromeTabs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/json/list":
		targets := []map[string]string{{"id": "worker", "type": "service_worker"}}
		for _, id := range f.pages {
			targets = append(targets, map[string]string{"id": id, "type": "page"})
		}
		_ = json.NewEncoder(w).Encode(targets)
	case strings.HasPrefix(r.URL.Path, "/json/close/"):
		id := strings.TrimPrefix(r.URL.Path, "/json/close/")
		f.closed = append(f.closed, id)
		kept := f.pages[:0]
		for _, page := range f.pages {
			if page != id {
				kept = append(kept, page)
			}
		}
		f.pages = kept
		_, _ = w.Write([]byte("Target is closing"))
	default:
		http.NotFound(w, r)
	}
}

func fakeChromeDesktop(t *testing.T, s *Service, chrome *fakeChromeTabs) *desktop {
	t.Helper()
	server := httptest.NewServer(chrome)
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	d := &desktop{botID: sharedDesktopKey, shared: true, readyClosed: true}
	if d.remotePort, err = strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sharedDesktop = d
	s.desktops[testBot] = d
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.sharedDesktop = nil
		delete(s.desktops, testBot)
		s.mu.Unlock()
	})
	return d
}

func TestEndOfRunReleaseClosesOnlyTabsTheRunOpened(t *testing.T) {
	s := newTestService(t)
	chrome := &fakeChromeTabs{pages: []string{"user-tab"}}
	fakeChromeDesktop(t, s, chrome)
	ctx := context.Background()
	req := ActionRequest{BotID: testBot, RunID: "run-1", Source: ActionSourceModel, Action: "browser.navigate"}
	s.recordRunTabs(ctx, req)
	chrome.mu.Lock()
	chrome.pages = []string{"bot-current", "bot-background", "user-tab"}
	chrome.mu.Unlock()
	// A later action must not move the baseline.
	s.recordRunTabs(ctx, req)

	// Another run still holds the shared desktop: nothing is closed.
	if _, err := s.holdDesktop(testBotB, "run-2", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.closeRunTabs(ctx, testBot, "run-1"); got != 0 {
		t.Fatalf("closed %d tabs while another run held the desktop", got)
	}
	if _, err := s.releaseDesktop(testBotB, "run-2"); err != nil {
		t.Fatal(err)
	}
	// The skipped attempt consumed the baseline; record a fresh one.
	chrome.mu.Lock()
	chrome.pages = []string{"user-tab"}
	chrome.mu.Unlock()
	s.recordRunTabs(ctx, req)
	chrome.mu.Lock()
	chrome.pages = []string{"bot-current", "bot-background", "user-tab"}
	chrome.mu.Unlock()

	if _, err := s.holdDesktop(testBot, "run-1", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(releaseArgs{CloseRunTabs: true})
	result, err := s.action(ctx, ActionRequest{BotID: testBot, RunID: "run-1", Source: ActionSourceModel, Action: "desktop.release", Args: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any)["closed_tabs"]; got != 1 {
		t.Fatalf("closed_tabs = %v, want 1", got)
	}
	chrome.mu.Lock()
	defer chrome.mu.Unlock()
	if !reflect.DeepEqual(chrome.closed, []string{"bot-background"}) || !reflect.DeepEqual(chrome.pages, []string{"bot-current", "user-tab"}) {
		t.Fatalf("closed %v, remaining %v", chrome.closed, chrome.pages)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runTabs) != 0 {
		t.Fatalf("baseline retained after end-of-run release: %v", s.runTabs)
	}
}

func TestPlainReleaseAndNonModelRunsKeepTabs(t *testing.T) {
	s := newTestService(t)
	chrome := &fakeChromeTabs{pages: []string{"user-tab"}}
	fakeChromeDesktop(t, s, chrome)
	ctx := context.Background()
	s.recordRunTabs(ctx, ActionRequest{BotID: testBot, RunID: "viewer-1", Source: ActionSourceViewer})
	s.recordRunTabs(ctx, ActionRequest{BotID: testBot, RunID: "", Source: ActionSourceModel})
	s.recordRunTabs(ctx, ActionRequest{BotID: testBot, RunID: "run-1", Source: ActionSourceModel})
	chrome.mu.Lock()
	chrome.pages = []string{"bot-a", "bot-b", "user-tab"}
	chrome.mu.Unlock()
	// A mid-run yield (handoff) releases without the flag.
	result, err := s.action(ctx, ActionRequest{BotID: testBot, RunID: "run-1", Source: ActionSourceModel, Action: "desktop.release", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(map[string]any)["closed_tabs"]; ok {
		t.Fatal("plain release must not close tabs")
	}
	chrome.mu.Lock()
	closed := len(chrome.closed)
	chrome.mu.Unlock()
	s.mu.Lock()
	baselines := len(s.runTabs)
	s.mu.Unlock()
	if closed != 0 || baselines != 1 {
		t.Fatalf("closed=%d baselines=%d", closed, baselines)
	}
	// Stale baselines are pruned when another run records one.
	s.mu.Lock()
	s.runTabs["run-1"].created = time.Now().Add(-runTabsMaxAge - time.Minute)
	s.mu.Unlock()
	s.recordRunTabs(ctx, ActionRequest{BotID: testBot, RunID: "run-2", Source: ActionSourceModel})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runTabs["run-1"] != nil || s.runTabs["run-2"] == nil {
		t.Fatalf("baselines = %v", s.runTabs)
	}
}
