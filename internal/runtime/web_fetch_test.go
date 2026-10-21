package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestServerSideFetchIsRequestedReportedAsEvidenceAndCounted(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{Content: "done", WebFetchRequests: 2, WebFetches: []provider.WebFetch{
				{ID: "f1", URL: "https://example.com/a", Title: "Synthetic A", RetrievedAt: "2026-10-21T10:00:00Z"},
				{ID: "f2", URL: "https://example.com/b", Error: "url_not_accessible"},
			}}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	var events []ToolEvent
	var counted int64
	_, err := e.Run(context.Background(), Request{RunID: "wf-run", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}},
		WebFetchMaxUses:   3,
		OnWebFetchCounted: func(n int64) { counted += n },
		OnToolEvent:       func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.requests[0].WebFetch == nil || p.requests[0].WebFetch.MaxUses != 3 || p.requests[0].WebFetch.MaxContentTokens <= 0 {
		t.Fatalf("fetch not requested with a page cap: %+v", p.requests[0].WebFetch)
	}
	if p.requests[0].WebSearch != nil {
		t.Fatal("asking for fetch must not ask for search")
	}
	if counted != 2 {
		t.Fatalf("counted=%d", counted)
	}
	var ok, failed bool
	for _, ev := range events {
		if ev.Name != "web_fetch" {
			continue
		}
		if ev.Status == "completed" && strings.Contains(ev.Arguments, "https://example.com/a") && strings.Contains(ev.Result, "Synthetic A") {
			ok = true
		}
		if ev.Status == "failed" && strings.Contains(ev.Result, "url_not_accessible") {
			failed = true
		}
	}
	if !ok || !failed {
		t.Fatalf("the pages read are not in the evidence: %+v", events)
	}
}

func TestNoFetchIsRequestedUnlessAsked(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{Content: "done"}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	if _, err := e.Run(context.Background(), Request{RunID: "no-wf", BotID: "bot", WebSearchMaxUses: 3, Messages: []Message{{Role: "user", Content: "go"}}}); err != nil {
		t.Fatal(err)
	}
	if p.requests[0].WebFetch != nil {
		t.Fatal("fetch requested by default")
	}
}
