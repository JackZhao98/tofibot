package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/webtools"
)

func TestWebFetchReadsALongPageInPartsWithoutFetchingAgain(t *testing.T) {
	var hits atomic.Int32
	para := "<p>" + strings.Repeat("Synthetic filler sentence for the reader. ", 40) + "</p>"
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Long</title></head><body><article><h1>Long page</h1>" + strings.Repeat(para, 40) + "</article></body></html>"))
	}))
	defer site.Close()
	webBuiltinTransport = http.DefaultTransport
	t.Cleanup(func() { webBuiltinTransport = nil })
	s := webServer(t)
	limit := "2"
	if err := s.store.putAutomationValue(autoWebFetchLimit, &limit); err != nil {
		t.Fatal(err)
	}
	var fetch Tool
	for _, tool := range s.webTools(Run{Model: "gpt-5.5"}) {
		if tool.Name == "web_fetch" {
			fetch = tool
		}
	}
	if _, ok := fetch.Parameters["properties"].(map[string]any)["offset"]; !ok {
		t.Fatal("schema lacks offset")
	}
	local := strings.Replace(site.URL, "127.0.0.1", "localhost", 1)
	read := func(offset int) webtools.Page {
		t.Helper()
		out, err := fetch.Execute(context.Background(), json.RawMessage(`{"url":"`+local+`","max_chars":10000,"offset":`+offsetJSON(offset)+`}`))
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		var p webtools.Page
		if json.Unmarshal([]byte(out), &p) != nil {
			t.Fatal(out)
		}
		return p
	}
	var joined strings.Builder
	offset, parts := 0, 0
	for {
		p := read(offset)
		parts++
		joined.WriteString(p.Content)
		if p.TotalChars < 20000 || p.Offset != offset {
			t.Fatalf("%+v", p)
		}
		if p.NextOffset == 0 {
			break
		}
		offset = p.NextOffset
		if parts > 20 {
			t.Fatal("runaway")
		}
	}
	if parts < 3 || len([]rune(joined.String())) != read(0).TotalChars {
		t.Fatalf("parts=%d joined=%d", parts, len([]rune(joined.String())))
	}
	// Later parts came from the held text: one fetch for the first part, then one more for the read(0) just above.
	if hits.Load() != 2 {
		t.Fatalf("site fetched %d times", hits.Load())
	}
	// A negative offset is an argument problem.
	if _, err := fetch.Execute(context.Background(), json.RawMessage(`{"url":"`+local+`","offset":-1}`)); outcomeCode(err) != "invalid_arguments" {
		t.Fatalf("%v", err)
	}
}

func offsetJSON(n int) string { b, _ := json.Marshal(n); return string(b) }
