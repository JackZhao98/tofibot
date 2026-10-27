package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func codeOfChain(t *testing.T, err error) Code {
	t.Helper()
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("not a chain error: %v", err)
	}
	return ce.Last
}

type stubSearch struct {
	id  string
	res []SearchResult
	err error
	n   *int
}

func (s stubSearch) ID() string { return s.id }
func (s stubSearch) Search(context.Context, string, int) ([]SearchResult, error) {
	if s.n != nil {
		*s.n++
	}
	return s.res, s.err
}

func TestSearchFallsBackByCodeOnly(t *testing.T) {
	ok := []SearchResult{{Title: "a", URL: "https://a.example"}}
	for _, code := range []Code{AuthFailed, QuotaExceeded, Blocked, EmptyContent, NeedsJavaScript, Network, NotConfigured} {
		// The error text is hostile: only the code may decide.
		chain := []SearchProvider{stubSearch{id: "one", err: &Error{Code: code}}, stubSearch{id: "two", res: ok}}
		got, err := Search(context.Background(), chain, "q", 5)
		if err != nil || got.Source != "two" {
			t.Fatalf("%s: got=%+v err=%v", code, got, err)
		}
	}
	// A plain error without a code is a network failure, which falls through.
	got, err := Search(context.Background(), []SearchProvider{stubSearch{id: "one", err: errors.New("quota exceeded auth_failed")}, stubSearch{id: "two", res: ok}}, "q", 5)
	if err != nil || got.Source != "two" {
		t.Fatalf("uncoded error: %+v %v", got, err)
	}
	// Empty results are empty_content and fall through.
	got, err = Search(context.Background(), []SearchProvider{stubSearch{id: "one"}, stubSearch{id: "two", res: ok}}, "q", 5)
	if err != nil || got.Source != "two" {
		t.Fatalf("empty: %+v %v", got, err)
	}
}

func TestChainStopsOnTerminalCodes(t *testing.T) {
	n := 0
	chain := []FetchProvider{FetchFunc{Name: "a", Fn: func(context.Context, string, int) (Page, error) { return Page{}, fail(NotFound) }},
		FetchFunc{Name: "b", Fn: func(context.Context, string, int) (Page, error) { n++; return Page{Content: "x"}, nil }}}
	_, err := Fetch(context.Background(), chain, "https://example.com/x", 0)
	if codeOfChain(t, err) != NotFound || n != 0 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestChainReportsEveryAttempt(t *testing.T) {
	chain := []SearchProvider{stubSearch{id: "a", err: fail(AuthFailed)}, stubSearch{id: "b", err: fail(QuotaExceeded)}}
	_, err := Search(context.Background(), chain, "q", 5)
	var ce *ChainError
	if !errors.As(err, &ce) || len(ce.Attempts) != 2 || ce.Attempts[0].Code != AuthFailed || ce.Last != QuotaExceeded {
		t.Fatalf("%+v", ce)
	}
	if _, err := Search(context.Background(), nil, "q", 5); codeOfChain(t, err) != NotConfigured {
		t.Fatalf("empty chain: %v", err)
	}
	if _, err := Search(context.Background(), chain, "  ", 5); CodeOf(err) != BadRequest {
		t.Fatalf("blank query: %v", err)
	}
}

func TestVendorStatusCodes(t *testing.T) {
	for status, want := range map[int]Code{401: AuthFailed, 403: AuthFailed, 402: QuotaExceeded, 429: QuotaExceeded, 432: QuotaExceeded, 433: QuotaExceeded, 500: Network, 422: Network} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := Tavily{Key: "k", Endpoint: srv.URL}.Search(context.Background(), "q", 3)
		_, err2 := Brave{Key: "k", Endpoint: srv.URL}.Search(context.Background(), "q", 3)
		_, err3 := Firecrawl{Key: "k", Endpoint: srv.URL}.Fetch(context.Background(), "https://example.com", 0)
		srv.Close()
		for _, e := range []error{err, err2, err3} {
			if CodeOf(e) != want {
				t.Fatalf("status %d: got %v want %s", status, e, want)
			}
		}
	}
}

func TestTavilyShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer tvly-1" || in["query"] != "cats" || in["max_results"] != float64(3) {
			t.Errorf("bad request: %s %v %v", r.Method, r.Header, in)
		}
		w.Write([]byte(`{"results":[{"title":"T","url":"https://t.example","content":"C","score":0.9},{"title":"no url"}]}`))
	}))
	defer srv.Close()
	res, err := Tavily{Key: "tvly-1", Endpoint: srv.URL}.Search(context.Background(), "cats", 3)
	if err != nil || len(res) != 1 || res[0].URL != "https://t.example" || res[0].Snippet != "C" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := (Tavily{}).Search(context.Background(), "q", 3); CodeOf(err) != NotConfigured {
		t.Fatal("missing key")
	}
}

func TestBraveShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "bk" || r.URL.Query().Get("q") != "go lang" || r.URL.Query().Get("count") != "2" {
			t.Errorf("bad request: %v %s", r.Header, r.URL)
		}
		w.Write([]byte(`{"web":{"results":[{"title":"<strong>Go</strong>","url":"https://go.dev","description":"The <b>Go</b> language &amp; tools"}]}}`))
	}))
	defer srv.Close()
	res, err := Brave{Key: "bk", Endpoint: srv.URL}.Search(context.Background(), "go lang", 2)
	if err != nil || len(res) != 1 || res[0].Title != "Go" || res[0].Snippet != "The Go language & tools" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSearXNGShapeAndFormatDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			t.Errorf("bad request %s", r.URL)
		}
		w.Write([]byte(`{"results":[{"title":"A","url":"https://a.example","content":"x"},{"title":"B","url":"https://b.example","content":"y"}]}`))
	}))
	defer srv.Close()
	res, err := SearXNG{BaseURL: srv.URL + "/"}.Search(context.Background(), "q", 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	off := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer off.Close()
	if _, err := (SearXNG{BaseURL: off.URL}).Search(context.Background(), "q", 1); CodeOf(err) != NotConfigured {
		t.Fatalf("403 must read as format disabled: %v", err)
	}
	if _, err := (SearXNG{BaseURL: "ftp://x"}).Search(context.Background(), "q", 1); CodeOf(err) != NotConfigured {
		t.Fatal("bad base url")
	}
}

func TestFirecrawlShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("Authorization") != "Bearer fc" || in["url"] != "https://example.com/p" || in["onlyMainContent"] != true {
			t.Errorf("bad request %v", in)
		}
		w.Write([]byte(`{"success":true,"data":{"markdown":"# Hi\n\nBody","metadata":{"title":"Hi","statusCode":200,"sourceURL":"https://example.com/p"}}}`))
	}))
	defer srv.Close()
	p, err := Firecrawl{Key: "fc", Endpoint: srv.URL}.Fetch(context.Background(), "https://example.com/p", 0)
	if err != nil || p.Title != "Hi" || !strings.Contains(p.Content, "Body") {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestBrightDataShape(t *testing.T) {
	html := "<html><head><title>BD</title></head><body><main><p>" + strings.Repeat("unlocked page text ", 30) + "</p></main></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("Authorization") != "Bearer bd" || in["zone"] != "z1" || in["format"] != "raw" || in["url"] != "https://example.com" {
			t.Errorf("bad request %v", in)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer srv.Close()
	p, err := BrightData{Key: "bd", Zone: "z1", Endpoint: srv.URL}.Fetch(context.Background(), "https://example.com", 0)
	if err != nil || p.Title != "BD" || !strings.Contains(p.Content, "unlocked page text") {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := (BrightData{Key: "bd"}).Fetch(context.Background(), "https://example.com", 0); CodeOf(err) != NotConfigured {
		t.Fatal("zone missing")
	}
}

func TestFetchChainOrderBuiltinThenFirecrawl(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer site.Close()
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"markdown":"from firecrawl","metadata":{"statusCode":200}}}`))
	}))
	defer fc.Close()
	chain := []FetchProvider{Builtin{Transport: http.DefaultTransport}, Firecrawl{Key: "k", Endpoint: fc.URL}}
	// A literal loopback address is refused before any provider is tried.
	if _, err := Fetch(context.Background(), chain, site.URL, 0); codeOfChain(t, err) != TargetRefused {
		t.Fatalf("loopback must end the chain: %v", err)
	}
	// With a public-looking name, the built-in is blocked and Firecrawl serves.
	p, err := Fetch(context.Background(), []FetchProvider{FetchFunc{Name: "builtin", Fn: func(context.Context, string, int) (Page, error) { return Page{}, fail(Blocked) }}, chain[1]}, "https://example.com/x", 0)
	if err != nil || p.Source != "firecrawl" || p.Content != "from firecrawl" {
		t.Fatalf("%+v %v", p, err)
	}
}
