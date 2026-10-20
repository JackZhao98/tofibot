package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestServerSideSearchIsRequestedReportedAsEvidenceAndBilled(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{Content: "done", WebSearchRequests: 2, WebSearches: []provider.WebSearch{
				{ID: "s1", Query: "synthetic title", Sources: []provider.WebSearchSource{{Title: "Synthetic", URL: "https://example.com/a"}}},
				{ID: "s2", Query: "second", Error: "unavailable"},
			}}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	var events []ToolEvent
	var billed int64
	_, err := e.Run(context.Background(), Request{RunID: "ws-run", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}},
		WebSearchMaxUses:  3,
		OnWebSearchBilled: func(n int64) { billed += n },
		OnToolEvent:       func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.requests[0].WebSearch == nil || p.requests[0].WebSearch.MaxUses != 3 {
		t.Fatalf("search not requested: %+v", p.requests[0].WebSearch)
	}
	if billed != 2 {
		t.Fatalf("billed=%d", billed)
	}
	var ok, failed bool
	for _, ev := range events {
		if ev.Name != "web_search" {
			continue
		}
		if ev.Status == "completed" && strings.Contains(ev.Arguments, "synthetic title") && strings.Contains(ev.Result, "https://example.com/a") {
			ok = true
		}
		if ev.Status == "failed" && strings.Contains(ev.Result, "unavailable") {
			failed = true
		}
	}
	if !ok || !failed {
		t.Fatalf("sources are not in the evidence: %+v", events)
	}
}

func TestNoSearchIsRequestedUnlessAsked(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{Content: "done"}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	if _, err := e.Run(context.Background(), Request{RunID: "no-ws", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}}}); err != nil {
		t.Fatal(err)
	}
	if p.requests[0].WebSearch != nil {
		t.Fatal("search requested by default")
	}
}
