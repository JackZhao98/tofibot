package guest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Bot runs open tabs (new pages, target=_blank links) that each keep a
// renderer alive in the small guest. The App's end-of-run release asks the
// guest to close the tabs that appeared during that run. Tabs that existed
// before the run's first desktop action are never touched, and the most
// recently active tab always stays open, so a page the Bot left for the user
// (for example a sign-in form) survives.

const (
	runTabsProbeTimeout = 2 * time.Second
	runTabsMaxAge       = 12 * time.Hour
)

type runTabBaseline struct {
	desktop *desktop
	pages   map[string]bool
	created time.Time
}

type releaseArgs struct {
	CloseRunTabs bool `json:"close_run_tabs"`
}

// listBrowserPageIDs returns page target IDs in Chrome's /json/list order,
// which is most recently active first.
func listBrowserPageIDs(ctx context.Context, d *desktop) ([]string, error) {
	body, err := chromeHTTP(ctx, d, http.MethodGet, "/json/list")
	if err != nil {
		return nil, err
	}
	var targets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Type == "page" && target.ID != "" {
			ids = append(ids, target.ID)
		}
	}
	return ids, nil
}

// recordRunTabs captures the open tabs before a model run's first desktop
// action. It is best effort: without a baseline the run's tabs are kept.
func (s *Service) recordRunTabs(ctx context.Context, req ActionRequest) {
	if req.Source != ActionSourceModel || req.RunID == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	d := s.desktopLocked(req.BotID)
	existing := s.runTabs[req.RunID]
	if d == nil || d.stopping || !d.readyClosed || d.remotePort <= 0 || (existing != nil && existing.desktop == d) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, runTabsProbeTimeout)
	defer cancel()
	ids, err := listBrowserPageIDs(probe, d)
	if err != nil {
		return
	}
	pages := make(map[string]bool, len(ids))
	for _, id := range ids {
		pages[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runTabs == nil {
		s.runTabs = make(map[string]*runTabBaseline)
	}
	for runID, baseline := range s.runTabs {
		if now.Sub(baseline.created) > runTabsMaxAge {
			delete(s.runTabs, runID)
		}
	}
	if current := s.runTabs[req.RunID]; current == nil || current.desktop != d {
		s.runTabs[req.RunID] = &runTabBaseline{desktop: d, pages: pages, created: now}
	}
}

// runTabsToClose keeps every baseline tab and the most recently active tab.
func runTabsToClose(baseline map[string]bool, current []string) []string {
	var closing []string
	for i, id := range current {
		if i == 0 || baseline[id] {
			continue
		}
		closing = append(closing, id)
	}
	return closing
}

// closeRunTabs runs after the run's hold was released. It does nothing while
// another run holds the shared desktop, a person controls it, an operation is
// in flight, or the Chrome process that the baseline described was replaced.
func (s *Service) closeRunTabs(ctx context.Context, botID, runID string) int {
	s.mu.Lock()
	baseline := s.runTabs[runID]
	delete(s.runTabs, runID)
	d := s.desktopLocked(botID)
	if baseline == nil || d == nil || baseline.desktop != d || d.stopping || !d.readyClosed ||
		d.inFlight != 0 || s.activeInput != nil || s.hasAnyActiveHoldLocked(time.Now()) {
		s.mu.Unlock()
		return 0
	}
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, runTabsProbeTimeout)
	defer cancel()
	ids, err := listBrowserPageIDs(probe, d)
	if err != nil {
		return 0
	}
	closed := 0
	for _, id := range runTabsToClose(baseline.pages, ids) {
		if _, err := chromeHTTP(probe, d, http.MethodGet, "/json/close/"+url.PathEscape(id)); err == nil {
			closed++
		}
	}
	return closed
}
