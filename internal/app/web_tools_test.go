package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/webtools"
)

const (
	testTavilyKey = "tvly-synthetic-key-0001"
	testBraveKey  = "brave-synthetic-key-0002"
	testFCKey     = "fc-synthetic-key-0003"
)

func webServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func toolNames(tools []Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		out[t.Name] = true
	}
	return out
}

func putWebKey(t *testing.T, s *Server, id, key string) {
	t.Helper()
	rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/"+id, `{"key":"`+key+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %s: %d %s", id, rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Fatalf("the response echoes the key: %s", rec.Body.String())
	}
}

func webEntries(t *testing.T, s *Server, chain string) []webEntry {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/web-tools", "")
	var view struct {
		Search []webEntry `json:"search"`
		Fetch  []webEntry `json:"fetch"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if chain == "search" {
		return view.Search
	}
	return view.Fetch
}

func entryByID(list []webEntry, id string) (webEntry, bool) {
	for _, e := range list {
		if e.ID == id {
			return e, true
		}
	}
	return webEntry{}, false
}

func TestWebSearchIsRegisteredOnlyWithAUsableProvider(t *testing.T) {
	s := webServer(t)
	run := Run{Model: "gpt-5.5"}
	names := toolNames(s.webTools(run))
	if names["web_search"] {
		t.Fatal("web_search registered with no provider: the Bot would pretend to search")
	}
	if !names["web_fetch"] {
		t.Fatal("the built-in fetch needs no setup")
	}
	putWebKey(t, s, "tavily", testTavilyKey)
	if !toolNames(s.webTools(run))["web_search"] {
		t.Fatal("web_search missing with a Tavily key")
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/search/tavily", `{"enabled":false}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if toolNames(s.webTools(run))["web_search"] {
		t.Fatal("a switched-off provider still registers the tool")
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/searxng", `{"base_url":"http://10.1.2.3:8080/"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if !toolNames(s.webTools(run))["web_search"] {
		t.Fatal("a SearXNG address should register the tool")
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/searxng", `{"base_url":"ftp://x"}`); rec.Code != 400 {
		t.Fatalf("bad address accepted: %d", rec.Code)
	}
}

func TestNativeSearchWinsAndMasterSwitchOff(t *testing.T) {
	s := webServer(t)
	putWebKey(t, s, "tavily", testTavilyKey)
	if names := toolNames(s.webTools(Run{Model: "claude-sonnet-5-5"})); names["web_search"] || names["web_fetch"] {
		t.Fatalf("the model's own tools must win: %v", names)
	}
	off := "false"
	if err := s.saveAutomationValue(autoWebSearch, &off); err != nil {
		t.Fatal(err)
	}
	if len(s.webTools(Run{Model: "gpt-5.5"})) != 0 {
		t.Fatal("the master switch is off, no web tool may register")
	}
}

func TestSearchChainFallsBackByCodeAndRemembersARejectedKey(t *testing.T) {
	var tavilyCalls, braveCalls atomic.Int32
	tav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tavilyCalls.Add(1)
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":{"error":"quota exceeded"}}`)) // text must not steer anything
	}))
	defer tav.Close()
	brv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		braveCalls.Add(1)
		if r.Header.Get("X-Subscription-Token") != testBraveKey {
			t.Errorf("wrong key sent to Brave")
		}
		w.Write([]byte(`{"web":{"results":[{"title":"R","url":"https://r.example","description":"d"}]}}`))
	}))
	defer brv.Close()
	oldT, oldB := webtools.TavilyEndpoint, webtools.BraveEndpoint
	webtools.TavilyEndpoint, webtools.BraveEndpoint = tav.URL, brv.URL
	t.Cleanup(func() { webtools.TavilyEndpoint, webtools.BraveEndpoint = oldT, oldB })

	s := webServer(t)
	putWebKey(t, s, "tavily", testTavilyKey)
	putWebKey(t, s, "brave", testBraveKey)
	var search Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_search" {
			search = tool
		}
	}
	out, err := search.Execute(context.Background(), json.RawMessage(`{"query":"cats"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res webtools.SearchResponse
	if json.Unmarshal([]byte(out), &res) != nil || res.Source != "brave" || len(res.Results) != 1 {
		t.Fatalf("%s", out)
	}
	if !strings.HasPrefix(out, `{"source":"brave"`) {
		t.Fatalf("the source must lead the result so the activity preview shows it: %s", out)
	}
	e, _ := entryByID(webEntries(t, s, "search"), "tavily")
	if e.Status != "error" || e.ErrorCode != "auth_failed" {
		t.Fatalf("tavily entry after a rejected key: %+v", e)
	}
	// The rejected key is skipped until it is replaced.
	if _, err = search.Execute(context.Background(), json.RawMessage(`{"query":"dogs"}`)); err != nil {
		t.Fatal(err)
	}
	if tavilyCalls.Load() != 1 || braveCalls.Load() != 2 {
		t.Fatalf("tavily=%d brave=%d", tavilyCalls.Load(), braveCalls.Load())
	}
	// Reorder: Brave first.
	rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/search/order", `{"order":["brave","tavily"]}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	list := webEntries(t, s, "search")
	if list[0].ID != "native" || list[1].ID != "brave" || list[2].ID != "tavily" {
		t.Fatalf("order: %+v", list)
	}
	if rec = providerRequest(t, s, http.MethodPut, "/api/web-tools/search/order", `{"order":["nope"]}`); rec.Code != 400 {
		t.Fatalf("unknown id accepted: %d", rec.Code)
	}
}

func TestTestButtonRecordsOnlyTheCode(t *testing.T) {
	status := 401
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			w.Write([]byte("secret vendor message " + testTavilyKey))
			return
		}
		w.Write([]byte(`{"results":[{"title":"t","url":"https://t.example","content":"c"}]}`))
	}))
	defer srv.Close()
	old := webtools.TavilyEndpoint
	webtools.TavilyEndpoint = srv.URL
	t.Cleanup(func() { webtools.TavilyEndpoint = old })
	s := webServer(t)
	putWebKey(t, s, "tavily", testTavilyKey)
	test := func() (map[string]any, string) {
		rec := providerRequest(t, s, http.MethodPost, "/api/web-tools/providers/tavily/test", "")
		if strings.Contains(rec.Body.String(), testTavilyKey) || strings.Contains(rec.Body.String(), "vendor message") {
			t.Fatalf("test response leaks: %s", rec.Body.String())
		}
		var v struct{ Test map[string]any }
		json.Unmarshal(rec.Body.Bytes(), &v)
		e, _ := entryByID(webEntries(t, s, "search"), "tavily")
		return v.Test, e.Status
	}
	if tr, st := test(); tr["ok"] != false || tr["code"] != "auth_failed" || st != "error" {
		t.Fatalf("%v %s", tr, st)
	}
	status = 429
	if tr, _ := test(); tr["code"] != "quota_exceeded" {
		t.Fatalf("%v", tr)
	}
	status = 200
	if tr, st := test(); tr["ok"] != true || st != "ready" {
		t.Fatalf("%v %s", tr, st)
	}
	if rec := providerRequest(t, s, http.MethodPost, "/api/web-tools/providers/firecrawl/test", ""); !strings.Contains(rec.Body.String(), `"not_configured"`) {
		t.Fatalf("no key: %s", rec.Body.String())
	}
	if e, _ := entryByID(webEntries(t, s, "fetch"), "firecrawl"); e.Status != "needs_key" || !e.Paid {
		t.Fatalf("%+v", e)
	}
}

func TestKeysStayOutOfTheSecretList(t *testing.T) {
	s := webServer(t)
	putWebKey(t, s, "firecrawl", testFCKey)
	rec := providerRequest(t, s, http.MethodGet, "/api/secrets", "")
	if strings.Contains(rec.Body.String(), "webtool") || strings.Contains(rec.Body.String(), testFCKey) {
		t.Fatalf("web provider key listed: %s", rec.Body.String())
	}
	if rec = providerRequest(t, s, http.MethodDelete, "/api/secrets/webtool:firecrawl", ""); rec.Code != 404 {
		t.Fatalf("the secrets route removed a provider key: %d", rec.Code)
	}
	if e, _ := entryByID(webEntries(t, s, "fetch"), "firecrawl"); e.Status != "ready" || e.KeyHint != "…0003" {
		t.Fatalf("%+v", e)
	}
	// Bright Data needs its zone with the key.
	if rec = providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/brightdata", `{"key":"bd-key-12345678"}`); rec.Code != 400 {
		t.Fatalf("zone-less Bright Data accepted: %d", rec.Code)
	}
	if rec = providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/brightdata", `{"key":"bd-key-12345678","zone":"web_unlocker1"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec = providerRequest(t, s, http.MethodDelete, "/api/web-tools/providers/firecrawl", ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if e, _ := entryByID(webEntries(t, s, "fetch"), "firecrawl"); e.Status != "needs_key" {
		t.Fatalf("%+v", e)
	}
}

func TestComputerEntryOnlyWithAComputer(t *testing.T) {
	s := webServer(t)
	if _, ok := entryByID(webEntries(t, s, "search"), "computer"); ok {
		t.Fatal("computer listed with no computer")
	}
	if _, ok := entryByID(webEntries(t, s, "fetch"), "computer"); ok {
		t.Fatal("computer listed with no computer")
	}
	if e, ok := entryByID(webEntries(t, s, "fetch"), "builtin"); !ok || !e.Fixed || e.Status != "always" {
		t.Fatalf("%+v", e)
	}
	if e, ok := entryByID(webEntries(t, s, "search"), "native"); !ok || !e.Fixed {
		t.Fatalf("%+v", e)
	}
}

func TestFetchToolRefusesPrivateTargetsAndFallsBack(t *testing.T) {
	var fcCalls atomic.Int32
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fcCalls.Add(1)
		w.Write([]byte(`{"success":true,"data":{"markdown":"served by firecrawl","metadata":{"statusCode":200}}}`))
	}))
	defer fc.Close()
	old := webtools.FirecrawlEndpoint
	webtools.FirecrawlEndpoint = fc.URL
	t.Cleanup(func() { webtools.FirecrawlEndpoint = old })
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer site.Close()

	webBuiltinTransport = extensions.GuardedTransport()
	t.Cleanup(func() { webBuiltinTransport = nil })
	s := webServer(t)
	putWebKey(t, s, "firecrawl", testFCKey)
	var fetch Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_fetch" {
			fetch = tool
		}
	}
	// A private target ends the chain: Firecrawl is never asked.
	_, err := fetch.Execute(context.Background(), json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`))
	if err == nil || !strings.Contains(err.Error(), "target_refused") || fcCalls.Load() != 0 {
		t.Fatalf("err=%v firecrawl calls=%d", err, fcCalls.Load())
	}
	// A name that resolves to loopback dies at dial time through the guard.
	local := strings.Replace(site.URL, "127.0.0.1", "localhost", 1)
	_, err = fetch.Execute(context.Background(), json.RawMessage(`{"url":"`+local+`"}`))
	if err == nil || !strings.Contains(err.Error(), "target_refused") || fcCalls.Load() != 0 {
		t.Fatalf("err=%v firecrawl calls=%d", err, fcCalls.Load())
	}
	// With the guard replaced (test only), the site's 403 falls through to Firecrawl.
	webBuiltinTransport = http.DefaultTransport
	t.Cleanup(func() { webBuiltinTransport = nil })
	out, err := fetch.Execute(context.Background(), json.RawMessage(`{"url":"`+local+`"}`))
	if err != nil || !strings.HasPrefix(out, `{"source":"firecrawl"`) || fcCalls.Load() != 1 {
		t.Fatalf("out=%s err=%v", out, err)
	}
	// Switched off, Firecrawl is not tried and the failure names the codes.
	providerRequest(t, s, http.MethodPut, "/api/web-tools/fetch/firecrawl", `{"enabled":false}`)
	_, err = fetch.Execute(context.Background(), json.RawMessage(`{"url":"`+local+`"}`))
	if err == nil || !strings.Contains(err.Error(), "builtin:blocked") || strings.Contains(err.Error(), "firecrawl") {
		t.Fatalf("err=%v", err)
	}
}

func TestDailyLimitSkipsPaidSourcesAndShowsInSettings(t *testing.T) {
	var tavily, searx atomic.Int32
	tav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tavily.Add(1)
		w.Write([]byte(`{"results":[{"title":"T","url":"https://t.example","content":"c"}]}`))
	}))
	defer tav.Close()
	sx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searx.Add(1)
		w.Write([]byte(`{"results":[{"title":"S","url":"https://s.example","content":"c"}]}`))
	}))
	defer sx.Close()
	old := webtools.TavilyEndpoint
	webtools.TavilyEndpoint = tav.URL
	t.Cleanup(func() { webtools.TavilyEndpoint = old })
	s := webServer(t)
	putWebKey(t, s, "tavily", testTavilyKey)
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/searxng", `{"base_url":"`+sx.URL+`/ignored/path?x=1"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if got := s.webConfig().SearXNGURL; got != sx.URL {
		t.Fatalf("path and query must be stripped on save: %q", got)
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/limits", `{"search":2}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var search Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_search" {
			search = tool
		}
	}
	served := func() string {
		out, err := search.Execute(context.Background(), json.RawMessage(`{"query":"q"}`))
		if err != nil {
			t.Fatal(err)
		}
		var res webtools.SearchResponse
		json.Unmarshal([]byte(out), &res)
		return res.Source
	}
	if served() != "tavily" || served() != "tavily" {
		t.Fatal("the first two calls go to the paid source")
	}
	e, _ := entryByID(webEntries(t, s, "search"), "tavily")
	if e.Status != "limit" || e.UsedToday != 2 {
		t.Fatalf("settings must show the limit: %+v", e)
	}
	if served() != "searxng" || tavily.Load() != 2 || searx.Load() != 1 {
		t.Fatalf("after the cap the chain falls to the free source: tavily=%d searx=%d", tavily.Load(), searx.Load())
	}
	providerRequest(t, s, http.MethodPut, "/api/web-tools/limits", `{"search":-1}`)
	if served() != "tavily" {
		t.Fatal("no cap, paid source serves again")
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/limits", `{"search":-5}`); rec.Code != 400 {
		t.Fatalf("bad limit accepted: %d", rec.Code)
	}
}

func TestSiteRefusalFromBrightDataDoesNotDisableTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	old := webtools.BrightDataEndpoint
	webtools.BrightDataEndpoint = srv.URL
	t.Cleanup(func() { webtools.BrightDataEndpoint = old })
	webBuiltinTransport = http.DefaultTransport
	t.Cleanup(func() { webBuiltinTransport = nil })
	s := webServer(t)
	if rec := providerRequest(t, s, http.MethodPut, "/api/web-tools/providers/brightdata", `{"key":"bd-key-12345678","zone":"z1"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	_, err := (trackedFetch{webtools.BrightData{Key: "bd-key-12345678", Zone: "z1"}, s}).Fetch(context.Background(), "https://example.com/", 0)
	if webtools.CodeOf(err) != webtools.Blocked {
		t.Fatalf("a site's 403 is blocked, got %v", err)
	}
	if e, _ := entryByID(webEntries(t, s, "fetch"), "brightdata"); e.Status != "ready" {
		t.Fatalf("the key must stay usable: %+v", e)
	}
}
