//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// nopageChrome is a minimal DevTools endpoint: an HTTP target list plus a
// runCDPCommand stub that reads and mutates the same page table.
type nopageChrome struct {
	mu     sync.Mutex
	pages  []*fakePage
	opened int
	port   int
	server *httptest.Server
}

type fakePage struct {
	id      string
	url     string
	focused bool
}

func newNopageChrome(t *testing.T) *nopageChrome {
	t.Helper()
	f := &nopageChrome{}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	u, _ := url.Parse(f.server.URL)
	f.port, _ = strconv.Atoi(u.Port())
	old := runCDPCommand
	t.Cleanup(func() { runCDPCommand = old })
	runCDPCommand = f.cdp
	return f
}

func (f *nopageChrome) ws(id string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", f.port, id)
}

func (f *nopageChrome) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/json/list":
		out := []browserTarget{}
		for _, p := range f.pages {
			out = append(out, browserTarget{ID: p.id, Type: "page", URL: p.url, WebSocketDebuggerURL: f.ws(p.id)})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.URL.Path == "/json/new":
		f.opened++
		p := &fakePage{id: fmt.Sprintf("p%d", f.opened), url: r.URL.RawQuery}
		if decoded, err := url.QueryUnescape(r.URL.RawQuery); err == nil {
			p.url = decoded
		}
		f.pages = append(f.pages, p)
		_ = json.NewEncoder(w).Encode(browserTarget{ID: p.id, Type: "page", URL: p.url, WebSocketDebuggerURL: f.ws(p.id)})
	case strings.HasPrefix(r.URL.Path, "/json/activate/"):
		_, _ = w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func (f *nopageChrome) cdp(_ context.Context, wsURL string, _ int, method string, params map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := wsURL[strings.LastIndex(wsURL, "/")+1:]
	var page *fakePage
	for _, p := range f.pages {
		if p.id == id {
			page = p
		}
	}
	if page == nil {
		return nil, errors.New("no such target")
	}
	switch method {
	case "Runtime.evaluate":
		state := browserPageState{Title: "t", URL: page.url, VisibilityState: "visible", HasFocus: page.focused, ReadyState: "complete"}
		value, _ := json.Marshal(state)
		return json.RawMessage(`{"result":{"value":` + string(value) + `}}`), nil
	case "Page.bringToFront":
		for _, p := range f.pages {
			p.focused = p == page
		}
		return json.RawMessage(`{}`), nil
	case "Page.navigate":
		page.url, _ = params["url"].(string)
		return json.RawMessage(`{"frameId":"f","loaderId":"l"}`), nil
	case "Page.getFrameTree":
		return json.RawMessage(`{"frameTree":{"frame":{"loaderId":"l"}}}`), nil
	}
	return nil, fmt.Errorf("unexpected CDP method %s", method)
}

const nopageBot = "00000000-0000-4000-8000-000000000001"

func TestResolveBrowserTargetZeroPagesSuggestsNavigate(t *testing.T) {
	_, err := resolveBrowserTarget(nil, "")
	var refusal *rejectedError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "browser.navigate") || !strings.Contains(err.Error(), "current_source=none") {
		t.Fatalf("zero-page resolve error = %v, want typed refusal that suggests navigate", err)
	}
	if _, err := resolveBrowserTarget(nil, "gone"); !errors.As(err, &refusal) {
		t.Fatalf("unknown target error = %v, want typed refusal", err)
	}
}

func TestNavigateWithNoPageOpensNewPage(t *testing.T) {
	f := newNopageChrome(t)
	s := &Service{}
	d := &desktop{remotePort: f.port}
	got, err := s.browserNavigatePage(context.Background(), nopageBot, d, "https://example.com/a", "")
	if err != nil {
		t.Fatalf("navigate with no page: %v", err)
	}
	if got["action"] != "navigate" || got["opened_new_page"] != true || got["url"] != "https://example.com/a" {
		t.Fatalf("result = %#v", got)
	}
	if len(f.pages) != 1 || !f.pages[0].focused {
		t.Fatalf("pages = %#v, want one focused page", f.pages)
	}
	// With a current page now present, navigate reuses it.
	got, err = s.browserNavigatePage(context.Background(), nopageBot, d, "https://example.com/b", "")
	if err != nil || len(f.pages) != 1 || f.pages[0].url != "https://example.com/b" || got["opened_new_page"] != nil {
		t.Fatalf("second navigate: err=%v pages=%#v result=%#v", err, f.pages, got)
	}
}

func TestConcurrentNavigateWithNoPageOpensOnePage(t *testing.T) {
	f := newNopageChrome(t)
	s := &Service{}
	d := &desktop{remotePort: f.port}
	urls := []string{"https://example.com/1", "https://example.com/2", "https://example.com/3", "https://example.com/4"}
	var wg sync.WaitGroup
	errs := make([]error, len(urls))
	for i, u := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.browserNavigatePage(context.Background(), nopageBot, d, u, "")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("navigate %d: %v", i, err)
		}
	}
	if len(f.pages) != 1 {
		t.Fatalf("%d pages after concurrent navigates, want exactly 1 (serialized)", len(f.pages))
	}
}

func TestNavigateInvalidURLIsAStructuredRefusal(t *testing.T) {
	f := newNopageChrome(t)
	s := &Service{}
	_, err := s.browserNavigatePage(context.Background(), nopageBot, &desktop{remotePort: f.port}, "file:///etc/passwd", "")
	var refusal *rejectedError
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want typed refusal", err)
	}
}

func TestRejectedErrorSerializesAsNotExecutedOutcome(t *testing.T) {
	w := httptest.NewRecorder()
	writeRejection(w, rejectedf("no focused visible browser page"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	var resp ActionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Outcome == nil || resp.Outcome.Certainty != "not_executed" || resp.Outcome.Version != 1 {
		t.Fatalf("body = %s", w.Body.String())
	}
}
