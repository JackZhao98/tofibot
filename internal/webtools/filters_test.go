package webtools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchOptionsClean(t *testing.T) {
	o, err := SearchOptions{Recency: " Week ", Domains: []string{"https://www.Example.com/a/b?x=1", "example.com", "*.news.example.org:8080"}}.Clean()
	if err != nil || o.Recency != "week" || len(o.Domains) != 2 || o.Domains[0] != "example.com" || o.Domains[1] != "news.example.org" {
		t.Fatalf("%+v %v", o, err)
	}
	for _, bad := range []SearchOptions{{Recency: "decade"}, {Domains: []string{"nodot"}}, {ExcludeDomains: []string{"a b.com"}}} {
		if _, err := bad.Clean(); CodeOf(err) != BadRequest {
			t.Fatalf("%+v accepted", bad)
		}
	}
	if _, err := SearchWith(context.Background(), nil, "q", 5, SearchOptions{Recency: "decade"}); codeOfChain(t, err) != BadRequest {
		t.Fatal("bad recency must end the chain")
	}
}

func TestTavilySendsTheFilters(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"results":[{"title":"t","url":"https://a.example.com/x","content":"c"}]}`))
	}))
	defer srv.Close()
	p := Tavily{Key: "k", Endpoint: srv.URL}
	resp, err := SearchWith(context.Background(), []SearchProvider{p}, "q", 3, SearchOptions{Recency: "week", Domains: []string{"example.com"}, ExcludeDomains: []string{"spam.test"}})
	if err != nil || len(resp.Results) != 1 || resp.Note != "" {
		t.Fatalf("%+v %v", resp, err)
	}
	inc, _ := got["include_domains"].([]any)
	exc, _ := got["exclude_domains"].([]any)
	if got["time_range"] != "week" || len(inc) != 1 || inc[0] != "example.com" || len(exc) != 1 || exc[0] != "spam.test" {
		t.Fatalf("%v", got)
	}
	// Without filters the body carries none.
	got = nil
	if _, err := SearchWith(context.Background(), []SearchProvider{p}, "q", 3, SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, has := got["time_range"]; has {
		t.Fatalf("%v", got)
	}
}

func TestBraveMapsRecencyAndDomainsAndSaysBestEffort(t *testing.T) {
	var q map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"web":{"results":[{"title":"t","url":"https://example.com/x","description":"d"}]}}`))
	}))
	defer srv.Close()
	resp, err := SearchWith(context.Background(), []SearchProvider{Brave{Key: "k", Endpoint: srv.URL}}, "cats", 3,
		SearchOptions{Recency: "month", Domains: []string{"example.com", "other.org"}, ExcludeDomains: []string{"spam.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if q["freshness"][0] != "pm" || q["q"][0] != "cats (site:example.com OR site:other.org) -site:spam.test" {
		t.Fatalf("%v", q)
	}
	if !strings.Contains(resp.Note, "best-effort") {
		t.Fatalf("note %q", resp.Note)
	}
}

func TestSearXNGMapsRecency(t *testing.T) {
	var q map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"results":[{"title":"t","url":"https://example.com/x","content":"c"}]}`))
	}))
	defer srv.Close()
	if _, err := SearchWith(context.Background(), []SearchProvider{SearXNG{BaseURL: srv.URL}}, "cats", 3, SearchOptions{Recency: "day", Domains: []string{"example.com"}}); err != nil {
		t.Fatal(err)
	}
	if q["time_range"][0] != "day" || q["q"][0] != "cats site:example.com" {
		t.Fatalf("%v", q)
	}
}

func TestDomainFilterIsAppliedToResultsAndFallsThrough(t *testing.T) {
	mixed := []SearchResult{
		{URL: "https://www.spam.test/a"}, {URL: "https://sub.example.com/b"}, {URL: "https://notexample.com/c"},
	}
	resp, err := SearchWith(context.Background(), []SearchProvider{stubSearch{id: "s", res: mixed}}, "q", 5,
		SearchOptions{Domains: []string{"example.com"}, ExcludeDomains: []string{"spam.test"}})
	if err != nil || len(resp.Results) != 1 || resp.Results[0].URL != "https://sub.example.com/b" {
		t.Fatalf("%+v %v", resp, err)
	}
	// A plain provider is asked without filters and the response says so.
	if !strings.Contains(resp.Note, "ignores") {
		t.Fatalf("note %q", resp.Note)
	}
	// Everything filtered out counts as empty, so the next source is tried.
	good := []SearchResult{{URL: "https://example.com/ok"}}
	resp, err = SearchWith(context.Background(), []SearchProvider{stubSearch{id: "a", res: mixed[:1]}, stubSearch{id: "b", res: good}}, "q", 5, SearchOptions{Domains: []string{"example.com"}})
	if err != nil || resp.Source != "b" {
		t.Fatalf("%+v %v", resp, err)
	}
}
