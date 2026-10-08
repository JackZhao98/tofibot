package guest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTabBrowser is a Chrome target list in /json/list order (most recently
// activated first).
type fakeTabBrowser struct {
	mu      sync.Mutex
	pages   []tabCapPage
	closed  []string
	failIDs map[string]bool
	listErr error
}

func (f *fakeTabBrowser) listPages(context.Context) ([]tabCapPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]tabCapPage(nil), f.pages...), nil
}

func (f *fakeTabBrowser) closePage(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failIDs[id] {
		return errors.New("close failed")
	}
	f.closed = append(f.closed, id)
	kept := f.pages[:0]
	for _, page := range f.pages {
		if page.ID != id {
			kept = append(kept, page)
		}
	}
	f.pages = kept
	return nil
}

// open puts a new tab in front, like Chrome activating a freshly opened tab.
func (f *fakeTabBrowser) open(id, title, rawURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages = append([]tabCapPage{{ID: id, Title: title, URL: rawURL}}, f.pages...)
}

// activate moves an existing tab to the front.
func (f *fakeTabBrowser) activate(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, page := range f.pages {
		if page.ID == id {
			f.pages = append([]tabCapPage{page}, append(f.pages[:i:i], f.pages[i+1:]...)...)
			return
		}
	}
}

func (f *fakeTabBrowser) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, page := range f.pages {
		ids = append(ids, page.ID)
	}
	return ids
}

func TestTabCapClosesLeastRecentlyUsedTabsBeyondLimit(t *testing.T) {
	browser := &fakeTabBrowser{}
	tracker := newTabCapTracker()
	now := time.Unix(1_000_000, 0)
	// Tabs opened over time: a first, then b, c, d; each new tab is active.
	for i, id := range []string{"a", "b", "c"} {
		browser.open(id, "Site "+id, "https://"+id+".example/")
		if closed := tracker.enforce(context.Background(), browser, 3, 0, now.Add(time.Duration(i)*10*time.Second), false); closed != 0 {
			t.Fatalf("closed %d tabs within the limit", closed)
		}
	}
	// The person returns to a, so b is now least recently used.
	browser.activate("a")
	tracker.enforce(context.Background(), browser, 3, 0, now.Add(40*time.Second), false)
	browser.open("d", "Site d", "https://d.example/")
	// d is new and active; the cap needs one victim and the oldest is b.
	closed := tracker.enforce(context.Background(), browser, 3, 0, now.Add(50*time.Second), false)
	if closed != 1 || strings.Join(browser.closed, ",") != "b" {
		t.Fatalf("closed=%d %v, want the least recently used tab b", closed, browser.closed)
	}
	if got := strings.Join(browser.ids(), ","); got != "d,a,c" {
		t.Fatalf("remaining tabs = %s", got)
	}
	note := tracker.drainNotes()
	if note != `Closed least-recently-used tab "Site b (b.example)" to stay within the 3-tab limit.` {
		t.Fatalf("note = %q", note)
	}
	if again := tracker.drainNotes(); again != "" {
		t.Fatalf("notes were not consumed: %q", again)
	}
	// Before the Bot opens a tab at the cap, one slot is freed first; the
	// note still names the configured limit.
	closed = tracker.enforce(context.Background(), browser, 3, 1, now.Add(70*time.Second), false)
	if closed != 1 || strings.Join(browser.ids(), ",") != "d,a" {
		t.Fatalf("closed=%d remaining=%v, want c closed to make room", closed, browser.ids())
	}
	if note := tracker.drainNotes(); !strings.Contains(note, `"Site c (c.example)"`) || !strings.Contains(note, "3-tab limit") {
		t.Fatalf("note = %q", note)
	}
	// A limit of one never closes the active tab to make room.
	if closed := tracker.enforce(context.Background(), browser, 1, 1, now.Add(80*time.Second), false); closed != 1 || strings.Join(browser.ids(), ",") != "d" {
		t.Fatalf("closed=%d remaining=%v", closed, browser.ids())
	}
}

func TestTabCapVictimOrderUsesNavigationAndListOrder(t *testing.T) {
	tracker := newTabCapTracker()
	start := time.Unix(2_000_000, 0)
	pages := []tabCapPage{{ID: "w", URL: "https://w/"}, {ID: "x", URL: "https://x/"}, {ID: "y", URL: "https://y/"}, {ID: "z", URL: "https://z/"}}
	tracker.mu.Lock()
	tracker.observeLocked(pages, start)
	// y navigated later (a background tab the Bot used), so x and z are older;
	// between equally old tabs the one later in Chrome's list goes first.
	pages[2].URL = "https://y/next"
	live := tracker.observeLocked(pages, start.Add(20*time.Second))
	victims := tracker.victimsLocked(live, 2, start.Add(30*time.Second), false)
	tracker.mu.Unlock()
	var got []string
	for _, victim := range victims {
		got = append(got, victim.ID)
	}
	if strings.Join(got, ",") != "z,x" {
		t.Fatalf("victims = %v, want z,x", got)
	}
}

func TestTabCapProtectsActiveNewAndHumanTabs(t *testing.T) {
	start := time.Unix(3_000_000, 0)
	t.Run("active and just-opened tabs stay over the cap", func(t *testing.T) {
		tracker := newTabCapTracker()
		browser := &fakeTabBrowser{}
		browser.open("old", "Old", "https://old/")
		tracker.enforce(context.Background(), browser, 1, 0, start, false)
		browser.open("new", "New", "https://new/")
		// "new" is active and brand new; "old" is the only candidate but is
		// itself within grace of its own first sighting only at start.
		if closed := tracker.enforce(context.Background(), browser, 1, 0, start.Add(time.Second), false); closed != 0 {
			t.Fatalf("closed %d tabs while the only candidate was protected", closed)
		}
		if closed := tracker.enforce(context.Background(), browser, 1, 0, start.Add(10*time.Second), false); closed != 1 || browser.closed[0] != "old" {
			t.Fatalf("closed=%d %v, want old", closed, browser.closed)
		}
		// One tab over a limit of one: the active tab is never closed.
		if closed := tracker.enforce(context.Background(), browser, 1, 0, start.Add(time.Minute), false); closed != 0 {
			t.Fatalf("closed the active tab")
		}
	})
	t.Run("a person driving keeps recently used tabs", func(t *testing.T) {
		tracker := newTabCapTracker()
		browser := &fakeTabBrowser{}
		for _, id := range []string{"a", "b", "c"} {
			browser.open(id, id, "https://"+id+"/")
		}
		tracker.enforce(context.Background(), browser, 2, 0, start, true)
		browser.activate("a") // the person switched to a, then back to c
		tracker.enforce(context.Background(), browser, 2, 0, start.Add(10*time.Second), true)
		browser.activate("c")
		if closed := tracker.enforce(context.Background(), browser, 2, 0, start.Add(12*time.Second), true); closed != 0 {
			t.Fatalf("closed %v while the person used every tab recently", browser.closed)
		}
		// Without a person at the desktop the same state closes b.
		if closed := tracker.enforce(context.Background(), browser, 2, 0, start.Add(12*time.Second), false); closed != 1 || browser.closed[0] != "b" {
			t.Fatalf("closed=%d %v, want b", closed, browser.closed)
		}
	})
}

func TestTabCapIgnoresNonPagesFailuresAndLingeringTargets(t *testing.T) {
	for _, tc := range []struct {
		kind, url string
		want      bool
	}{
		{"page", "https://mail.google.com/", true},
		{"page", "chrome://newtab/", true},
		{"page", "devtools://devtools/bundled/inspector.html", false},
		{"page", "chrome-extension://abc/popup.html", false},
		{"service_worker", "https://x/sw.js", false},
		{"iframe", "https://x/", false},
		{"other", "", false},
	} {
		if got := realBrowserPage(tc.kind, tc.url); got != tc.want {
			t.Errorf("realBrowserPage(%s, %s) = %v", tc.kind, tc.url, got)
		}
	}

	start := time.Unix(4_000_000, 0)
	tracker := newTabCapTracker()
	browser := &fakeTabBrowser{failIDs: map[string]bool{"a": true}}
	for _, id := range []string{"a", "b", "c"} {
		browser.open(id, id, "https://"+id+"/")
	}
	tracker.enforce(context.Background(), browser, 1, 0, start, false)
	// a fails to close; b still closes and only b gets a note.
	if closed := tracker.enforce(context.Background(), browser, 1, 0, start.Add(time.Minute), false); closed != 1 {
		t.Fatalf("closed=%d", closed)
	}
	if note := tracker.drainNotes(); note != `Closed least-recently-used tab "b" to stay within the 1-tab limit.` {
		t.Fatalf("note = %q", note)
	}
	// A closed target that lingers in /json/list is neither counted nor closed twice.
	browser.failIDs = nil
	browser.pages = append(browser.pages, tabCapPage{ID: "b", Title: "b", URL: "https://b/"})
	tracker.enforce(context.Background(), browser, 1, 0, start.Add(time.Minute+time.Second), false)
	if count := strings.Count(strings.Join(browser.closed, ","), "b"); count != 1 {
		t.Fatalf("lingering target closed %d times: %v", count, browser.closed)
	}
	browser.listErr = errors.New("chrome hung")
	if closed := tracker.enforce(context.Background(), browser, 1, 0, start.Add(2*time.Minute), false); closed != 0 {
		t.Fatalf("closed tabs without a target list")
	}
	if closed := tracker.enforce(context.Background(), &fakeTabBrowser{}, 0, 0, start, false); closed != 0 {
		t.Fatal("limit 0 must disable the cap")
	}
}

func TestTabCapConfiguration(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if s.maxBrowserTabs != DefaultMaxBrowserTabs || DefaultMaxBrowserTabs != 3 {
		t.Fatalf("default cap = %d", s.maxBrowserTabs)
	}
	s.SetMaxBrowserTabs(5)
	if s.maxBrowserTabs != 5 {
		t.Fatalf("cap = %d", s.maxBrowserTabs)
	}
	s.SetMaxBrowserTabs(-2)
	if s.maxBrowserTabs != 0 {
		t.Fatalf("negative cap = %d, want disabled", s.maxBrowserTabs)
	}
}

func TestTabCapActionSelection(t *testing.T) {
	shell := func(command string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"command": command, "timeout_sec": 30})
		return raw
	}
	for _, tc := range []struct {
		req  ActionRequest
		want bool
	}{
		{ActionRequest{Action: "browser.navigate"}, true},
		{ActionRequest{Action: "browser.snapshot"}, true},
		{ActionRequest{Action: "browser.action"}, true},
		{ActionRequest{Action: "shell.exec", Args: shell("python3 - x <<'TOFI_BROWSER_READ'\nscript\nTOFI_BROWSER_READ")}, true},
		{ActionRequest{Action: "shell.exec", Args: shell("python3 - x <<'TOFI_BROWSER_LOCATE'\nscript\nTOFI_BROWSER_LOCATE")}, false},
		{ActionRequest{Action: "shell.exec", Args: shell("ls")}, false},
		{ActionRequest{Action: "desktop.click"}, false},
	} {
		if got := tabCapAction(tc.req); got != tc.want {
			t.Errorf("tabCapAction(%s %s) = %v", tc.req.Action, tc.req.Args, got)
		}
	}
}

// fakeChrome serves the DevTools HTTP endpoints the cap uses.
type fakeChrome struct {
	mu      sync.Mutex
	targets []browserTargetSummary
	closed  []string
}

func (f *fakeChrome) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/json/list":
		_ = json.NewEncoder(w).Encode(f.targets)
	case strings.HasPrefix(r.URL.Path, "/json/close/"):
		id := strings.TrimPrefix(r.URL.Path, "/json/close/")
		f.closed = append(f.closed, id)
		kept := f.targets[:0]
		for _, target := range f.targets {
			if target.ID != id {
				kept = append(kept, target)
			}
		}
		f.targets = kept
		_, _ = w.Write([]byte("Target is closing"))
	default:
		http.NotFound(w, r)
	}
}

func TestApplyTabCapClosesOverDevToolsAndNotesTheModelResult(t *testing.T) {
	chrome := &fakeChrome{targets: []browserTargetSummary{
		{ID: "new", Type: "page", Title: "Inbox", URL: "https://mail.example/"},
		{ID: "sw", Type: "service_worker", URL: "https://mail.example/sw.js"},
		{ID: "two", Type: "page", Title: "News", URL: "https://news.example/"},
		{ID: "dev", Type: "page", URL: "devtools://devtools/inspector.html"},
		{ID: "three", Type: "page", Title: "Shop", URL: "https://shop.example/"},
		{ID: "four", Type: "page", Title: "Docs", URL: "https://docs.example/"},
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(chrome)
	server.Listener = listener
	server.Start()
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	botID := "11111111-2222-3333-4444-555555555555"
	d := &desktop{remotePort: port, readyClosed: true, tabCap: newTabCapTracker()}
	s := &Service{maxBrowserTabs: 3, desktops: map[string]*desktop{botID: d}, sharedDesktop: d, inputs: map[string]*inputSession{}}
	// Seen a while ago, so none of the tabs is within the new-tab grace.
	d.tabCap.mu.Lock()
	tracked := []tabCapPage{{ID: "new", URL: "https://mail.example/"}, {ID: "two", URL: "https://news.example/"}, {ID: "three", URL: "https://shop.example/"}, {ID: "four", URL: "https://docs.example/"}}
	d.tabCap.observeLocked(tracked, time.Now().Add(-time.Minute))
	d.tabCap.mu.Unlock()

	req := ActionRequest{BotID: botID, Action: "browser.navigate", Source: ActionSourceModel}
	result := s.applyTabCap(context.Background(), req, map[string]any{"url": "https://mail.example/"})
	values := result.(map[string]any)
	if got := strings.Join(chrome.closed, ","); got != "four" {
		t.Fatalf("closed %q, want only the least recently used page", got)
	}
	if values["tab_limit_note"] != `Closed least-recently-used tab "Docs (docs.example)" to stay within the 3-tab limit.` {
		t.Fatalf("result = %#v", values)
	}
	// Viewer and human requests never consume the Bot's notes or trigger closes.
	d.tabCap.mu.Lock()
	d.tabCap.notes = []string{"pending"}
	d.tabCap.mu.Unlock()
	viewer := s.applyTabCap(context.Background(), ActionRequest{BotID: botID, Action: "browser.snapshot", Source: ActionSourceViewer}, map[string]any{})
	if _, ok := viewer.(map[string]any)["tab_limit_note"]; ok {
		t.Fatal("viewer result carried the Bot note")
	}
	s.SetMaxBrowserTabs(0)
	off := s.applyTabCap(context.Background(), req, map[string]any{})
	if off.(map[string]any)["tab_limit_note"] != "pending" {
		t.Fatalf("disabled cap must still deliver pending notes: %#v", off)
	}
}
