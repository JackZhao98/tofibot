package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// The shared guest runs Chrome in 1 GiB. Around six heavy sites fill that
// memory and Chrome stops answering (new/close tab time out, Gmail stays on
// "Loading"), so the guest keeps at most a few page tabs open. When a new tab
// pushes the count over the cap, whatever opened it (a Bot, window.open, a
// target=_blank link, the person at the desktop), the least recently used
// tabs are closed. The focused tab, a just-opened tab, and tabs a person has
// used recently while controlling the desktop are never closed.
//
// Recency comes from the same DevTools HTTP endpoint the rest of the guest
// uses: /json/list lists page targets most recently activated first, a target
// seen for the first time was just created, and a changed URL means the page
// navigated. A light poll observes tabs opened outside guest actions; guest
// browser actions also enforce the cap before returning, so the Bot learns
// about a closed tab in the same result.

// DefaultMaxBrowserTabs is the default cap on simultaneously open page tabs.
// tofi-guest --max-browser-tabs (boot argument tofi_browser_tabs, Worker
// config browser_max_tabs) overrides it; 0 disables the cap.
const DefaultMaxBrowserTabs = 3

const (
	tabCapPollInterval = 2 * time.Second
	tabCapProbeTimeout = 1500 * time.Millisecond
	// tabCapNewGrace protects a tab that just opened, so a page that is still
	// committing its first navigation (or a sign-in popup) is never the victim.
	tabCapNewGrace = 5 * time.Second
	// tabCapHumanGrace protects tabs a person activated or navigated recently
	// while controlling the desktop.
	tabCapHumanGrace = 30 * time.Second
	// tabCapClosingGrace stops a closed target that still lingers in
	// /json/list from being counted or closed twice.
	tabCapClosingGrace = 5 * time.Second
	tabCapMaxNotes     = 6
)

// tabCapPage is one real page target in Chrome's /json/list order.
type tabCapPage struct {
	ID    string
	Title string
	URL   string
}

// tabCapBrowser is the DevTools surface the cap needs; tests use a fake.
type tabCapBrowser interface {
	listPages(ctx context.Context) ([]tabCapPage, error)
	closePage(ctx context.Context, id string) error
}

type tabCapTracker struct {
	mu        sync.Mutex
	firstSeen map[string]time.Time
	lastUsed  map[string]time.Time
	lastURL   map[string]string
	closing   map[string]time.Time
	notes     []string
}

func newTabCapTracker() *tabCapTracker {
	return &tabCapTracker{
		firstSeen: make(map[string]time.Time),
		lastUsed:  make(map[string]time.Time),
		lastURL:   make(map[string]string),
		closing:   make(map[string]time.Time),
	}
}

// realBrowserPage keeps only tabs a person or Bot browses: DevTools windows
// and extension pages are page targets too, but they are not web pages.
func realBrowserPage(targetType, rawURL string) bool {
	if targetType != "page" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	return !strings.HasPrefix(lower, "devtools://") && !strings.HasPrefix(lower, "chrome-extension://")
}

// observeLocked updates recency from one /json/list snapshot and returns the pages
// that still count toward the cap. Caller holds t.mu.
func (t *tabCapTracker) observeLocked(pages []tabCapPage, now time.Time) []tabCapPage {
	present := make(map[string]bool, len(pages))
	live := make([]tabCapPage, 0, len(pages))
	for i, page := range pages {
		present[page.ID] = true
		if closedAt, ok := t.closing[page.ID]; ok && now.Sub(closedAt) < tabCapClosingGrace {
			continue
		}
		if _, seen := t.firstSeen[page.ID]; !seen {
			t.firstSeen[page.ID] = now
			t.lastUsed[page.ID] = now
		} else if i == 0 || t.lastURL[page.ID] != page.URL {
			// The first target is the active tab; a changed URL is a navigation.
			t.lastUsed[page.ID] = now
		}
		t.lastURL[page.ID] = page.URL
		live = append(live, page)
	}
	for id := range t.firstSeen {
		if !present[id] {
			delete(t.firstSeen, id)
			delete(t.lastUsed, id)
			delete(t.lastURL, id)
		}
	}
	for id, closedAt := range t.closing {
		if !present[id] || now.Sub(closedAt) >= tabCapClosingGrace {
			delete(t.closing, id)
		}
	}
	return live
}

// victimsLocked picks the least recently used unprotected pages to close so
// that at most limit pages remain. live is in /json/list order (most recently
// activated first). If protected tabs alone exceed the cap, fewer are closed.
func (t *tabCapTracker) victimsLocked(live []tabCapPage, limit int, now time.Time, humanDriving bool) []tabCapPage {
	if limit <= 0 || len(live) <= limit {
		return nil
	}
	type candidate struct {
		page  tabCapPage
		index int
		used  time.Time
	}
	var candidates []candidate
	for i, page := range live {
		if i == 0 {
			continue // the active (focused) tab
		}
		if now.Sub(t.firstSeen[page.ID]) < tabCapNewGrace {
			continue
		}
		if humanDriving && now.Sub(t.lastUsed[page.ID]) < tabCapHumanGrace {
			continue
		}
		candidates = append(candidates, candidate{page: page, index: i, used: t.lastUsed[page.ID]})
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if !candidates[a].used.Equal(candidates[b].used) {
			return candidates[a].used.Before(candidates[b].used)
		}
		return candidates[a].index > candidates[b].index
	})
	excess := len(live) - limit
	if excess > len(candidates) {
		excess = len(candidates)
	}
	victims := make([]tabCapPage, 0, excess)
	for _, c := range candidates[:excess] {
		victims = append(victims, c.page)
	}
	return victims
}

// enforce closes least recently used tabs beyond limit and records a note
// for the Bot about each one. It returns the number of tabs closed.
func (t *tabCapTracker) enforce(ctx context.Context, browser tabCapBrowser, limit int, now time.Time, humanDriving bool) int {
	if t == nil || browser == nil || limit <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, tabCapProbeTimeout)
	pages, err := browser.listPages(probe)
	cancel()
	if err != nil {
		return 0
	}
	live := t.observeLocked(pages, now)
	closed := 0
	for _, victim := range t.victimsLocked(live, limit, now, humanDriving) {
		probe, cancel := context.WithTimeout(ctx, tabCapProbeTimeout)
		err := browser.closePage(probe, victim.ID)
		cancel()
		if err != nil {
			continue
		}
		t.closing[victim.ID] = now
		closed++
		t.addNoteLocked(tabCapNote(victim, limit))
	}
	return closed
}

func (t *tabCapTracker) addNoteLocked(note string) {
	if len(t.notes) >= tabCapMaxNotes {
		t.notes = t.notes[1:]
	}
	t.notes = append(t.notes, note)
}

// drainNotes returns and clears the pending notes for the next Bot result.
func (t *tabCapTracker) drainNotes() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	note := strings.Join(t.notes, " ")
	t.notes = nil
	return note
}

func tabCapNote(page tabCapPage, limit int) string {
	label := strings.TrimSpace(page.Title)
	if label == "" {
		label = page.URL
	}
	if u, err := url.Parse(page.URL); err == nil && u.Host != "" && label != page.URL && !strings.Contains(label, u.Host) {
		label += " (" + u.Host + ")"
	}
	if runes := []rune(label); len(runes) > 120 {
		label = string(runes[:120]) + "..."
	}
	return fmt.Sprintf("Closed least-recently-used tab %q to stay within the %d-tab limit.", label, limit)
}

// desktopTabBrowser talks to one desktop's Chrome over the DevTools HTTP API.
type desktopTabBrowser struct{ d *desktop }

func (b desktopTabBrowser) listPages(ctx context.Context) ([]tabCapPage, error) {
	body, err := chromeHTTP(ctx, b.d, http.MethodGet, "/json/list")
	if err != nil {
		return nil, err
	}
	var targets []browserTargetSummary
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, err
	}
	pages := make([]tabCapPage, 0, len(targets))
	for _, target := range targets {
		if target.ID != "" && realBrowserPage(target.Type, target.URL) {
			pages = append(pages, tabCapPage{ID: target.ID, Title: target.Title, URL: target.URL})
		}
	}
	return pages, nil
}

func (b desktopTabBrowser) closePage(ctx context.Context, id string) error {
	_, err := chromeHTTP(ctx, b.d, http.MethodGet, "/json/close/"+url.PathEscape(id))
	return err
}

type browserTargetSummary struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// SetMaxBrowserTabs sets the open page tab cap (0 disables it). It is
// configured once before the service handles requests.
func (s *Service) SetMaxBrowserTabs(limit int) {
	if limit < 0 {
		limit = 0
	}
	s.mu.Lock()
	s.maxBrowserTabs = limit
	s.mu.Unlock()
}

// tabCapTarget returns the settings for one enforcement pass, and false when
// the cap is off or the desktop is not usable.
func (s *Service) tabCapTarget(d *desktop) (limit int, humanDriving bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxBrowserTabs <= 0 || d == nil || d.tabCap == nil || d.stopping || !d.readyClosed || d.startErr != nil || d.remotePort <= 0 {
		return 0, false, false
	}
	return s.maxBrowserTabs, s.activeInput != nil || len(s.inputs) > 0, true
}

func (s *Service) enforceTabCap(ctx context.Context, d *desktop) int {
	limit, human, ok := s.tabCapTarget(d)
	if !ok {
		return 0
	}
	return d.tabCap.enforce(ctx, desktopTabBrowser{d: d}, limit, time.Now(), human)
}

// startTabCapWatcher observes tabs opened outside guest actions (page
// scripts, links, the person at the desktop) until the desktop stops. It
// stays out of the way while an action is in flight; actions enforce the cap
// themselves before returning.
func (s *Service) startTabCapWatcher(d *desktop) {
	s.mu.Lock()
	if s.maxBrowserTabs <= 0 || d == nil || d.tabCap == nil {
		s.mu.Unlock()
		return
	}
	stop := s.reaperStop
	s.mu.Unlock()
	go func() {
		ticker := time.NewTicker(tabCapPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-d.stopDone:
				return
			case <-stop:
				return
			}
			s.mu.Lock()
			gone := d.stopping || s.closed
			busy := d.inFlight != 0
			s.mu.Unlock()
			if gone {
				return
			}
			if !busy {
				s.enforceTabCap(context.Background(), d)
			}
		}
	}()
}

// tabCapAction reports whether a model action is a browser call whose result
// carries tab cap notes.
func tabCapAction(req ActionRequest) bool {
	switch req.Action {
	case "browser.navigate", "browser.snapshot", "browser.action", "browser.type_private":
		return true
	case "shell.exec":
		// browser.read and browser.click run as a bounded shell helper.
		var args shellArgs
		return decodeArgs(req.Args, &args) == nil && strings.Contains(args.Command, "TOFI_BROWSER_READ")
	}
	return false
}

// applyTabCap enforces the cap after a model browser action and attaches any
// pending note to the result as tab_limit_note.
func (s *Service) applyTabCap(ctx context.Context, req ActionRequest, result any) any {
	if req.Source != ActionSourceModel || !tabCapAction(req) {
		return result
	}
	values, ok := result.(map[string]any)
	if !ok {
		return result
	}
	s.mu.Lock()
	d := s.desktopLocked(req.BotID)
	s.mu.Unlock()
	if d == nil || d.tabCap == nil {
		return result
	}
	s.enforceTabCap(ctx, d)
	if note := d.tabCap.drainNotes(); note != "" {
		values["tab_limit_note"] = note
	}
	return values
}
