package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/webtools"
)

func TestWebSearchToolPassesRecencyAndDomainsToTheProvider(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = nil
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"results":[{"title":"T","url":"https://news.example.com/a","content":"c"},{"title":"X","url":"https://spam.test/b","content":"c"}]}`))
	}))
	defer srv.Close()
	old := webtools.TavilyEndpoint
	webtools.TavilyEndpoint = srv.URL
	t.Cleanup(func() { webtools.TavilyEndpoint = old })
	s := webServer(t)
	putWebKey(t, s, "tavily", testTavilyKey)
	var search Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_search" {
			search = tool
		}
	}
	props, _ := search.Parameters["properties"].(map[string]any)
	for _, name := range []string{"recency", "domains", "exclude_domains"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("schema lacks %s", name)
		}
	}
	out, err := search.Execute(context.Background(), json.RawMessage(`{"query":"news","recency":"week","domains":["example.com"],"exclude_domains":["spam.test"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got["time_range"] != "week" {
		t.Fatalf("%v", got)
	}
	var res webtools.SearchResponse
	if json.Unmarshal([]byte(out), &res) != nil || len(res.Results) != 1 || res.Results[0].URL != "https://news.example.com/a" {
		t.Fatalf("%s", out)
	}
	// An unknown recency is refused as an argument problem.
	_, err = search.Execute(context.Background(), json.RawMessage(`{"query":"news","recency":"decade"}`))
	if outcomeCode(err) != "web_bad_request" {
		t.Fatalf("%v", err)
	}
}

func TestDuckDuckGoURLCarriesRecencyAndSites(t *testing.T) {
	u := duckDuckGoURL("cats", webtools.SearchOptions{Recency: "day", Domains: []string{"example.com"}, ExcludeDomains: []string{"spam.test"}})
	parsed, err := url.Parse(u)
	if err != nil || parsed.Query().Get("df") != "d" || parsed.Query().Get("q") != "cats site:example.com -site:spam.test" {
		t.Fatalf("%s", u)
	}
	if strings.Contains(duckDuckGoURL("cats", webtools.SearchOptions{}), "df=") {
		t.Fatal("df without a recency")
	}
}
