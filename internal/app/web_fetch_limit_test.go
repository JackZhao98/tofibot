package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchLimitIsASettingWithAHigherDefault(t *testing.T) {
	item, ok := automationItemFor(autoWebFetchLimit)
	if !ok || item.Default != "20" || item.Min < 1 {
		t.Fatalf("item = %+v ok=%v", item, ok)
	}
	if webFetchMaxUsesPerRequest <= 3 {
		t.Fatalf("per-request cap = %d", webFetchMaxUsesPerRequest)
	}
	s := webServer(t)
	if got := s.webFetchRunLimit(); got != 20 {
		t.Fatalf("default limit = %d", got)
	}
}

// The run's page budget is the setting; the page result names what is left only when little is.
func TestServerRunWebFetchFollowsTheSettingAndWarnsOnlyWhenClose(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Synthetic</title></head><body><article><h1>Synthetic page</h1><p>" + strings.Repeat("This sentence is synthetic filler for the reader. ", 20) + "</p></article></body></html>"))
	}))
	defer site.Close()
	webBuiltinTransport = http.DefaultTransport
	t.Cleanup(func() { webBuiltinTransport = nil })
	s := webServer(t)
	limit := "5"
	if err := s.store.putAutomationValue(autoWebFetchLimit, &limit); err != nil {
		t.Fatal(err)
	}
	var fetch Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_fetch" {
			fetch = tool
		}
	}
	local := strings.Replace(site.URL, "127.0.0.1", "localhost", 1)
	args := json.RawMessage(`{"url":"` + local + `"}`)
	for i := 1; i <= 5; i++ {
		out, err := fetch.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		warned := strings.Contains(out, "pages_left")
		if want := i >= 2; warned != want {
			t.Fatalf("page %d: pages_left present=%v, want %v: %s", i, warned, want, out)
		}
	}
	if _, err := fetch.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "page limit") {
		t.Fatalf("page 6 err = %v", err)
	}
}
