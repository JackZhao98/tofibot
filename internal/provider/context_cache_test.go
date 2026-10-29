package provider

import (
	"strings"
	"testing"
)

func TestContextWindowPrecedenceCatalogTableFallback(t *testing.T) {
	t.Cleanup(func() { SetCatalogContextWindows(nil) })
	SetCatalogContextWindows(nil)

	// No catalog row, no table row: the heuristic, and it is not "known".
	if got, known := GetContextWindow("codex-gpt-5.6-terra"), ContextWindowKnown("codex-gpt-5.6-terra"); got != 500000 || known {
		t.Fatalf("fallback = %d known=%v", got, known)
	}
	// A table row is known.
	if got, known := GetContextWindow("codex-gpt-5.5"), ContextWindowKnown("codex-gpt-5.5"); got != 272000 || !known {
		t.Fatalf("table = %d known=%v", got, known)
	}
	// The catalog outranks the table and fills the gap for new models.
	SetCatalogContextWindows(map[string]int{"codex-gpt-5.6-terra": 400000, "Codex-GPT-5.5": 258400, "codex-bad": 0, "codex-neg": -5})
	if got, known := GetContextWindow("codex-gpt-5.6-terra"), ContextWindowKnown("codex-gpt-5.6-terra"); got != 400000 || !known {
		t.Fatalf("catalog = %d known=%v", got, known)
	}
	if got := GetContextWindow("codex-gpt-5.5"); got != 258400 {
		t.Fatalf("catalog over table = %d", got)
	}
	if ContextWindowKnown("codex-bad") || ContextWindowKnown("codex-neg") || GetContextWindow("codex-bad") != 500000 {
		t.Fatal("non-positive catalog windows must be ignored")
	}
	// A model that left the catalog stops using the stale value.
	SetCatalogContextWindows(map[string]int{"codex-other": 300000})
	if got := GetContextWindow("codex-gpt-5.6-terra"); got != 500000 {
		t.Fatalf("stale catalog window kept: %d", got)
	}
}

func TestAnthropicCachePricesFollowPublishedRule(t *testing.T) {
	// Cache read = 0.1x input, 5-minute cache write = 1.25x input.
	n := 0
	for name, info := range Registry {
		if info.Provider != "anthropic" {
			continue
		}
		n++
		if info.CacheReadCostPer1M != 0 || info.CacheWriteCostPer1M != 0 {
			t.Errorf("%s overrides cache prices (%v / %v); use the 0.1x / 1.25x defaults", name, info.CacheReadCostPer1M, info.CacheWriteCostPer1M)
		}
		read := CalculateCost(name, Usage{InputTokens: 1_000_000, CacheReadTokens: 1_000_000})
		write := CalculateCost(name, Usage{InputTokens: 1_000_000, CacheWriteTokens: 1_000_000})
		if d := read - info.InputCostPer1M*0.1; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s cache read cost = %v, want %v", name, read, info.InputCostPer1M*0.1)
		}
		if d := write - info.InputCostPer1M*1.25; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s cache write cost = %v, want %v", name, write, info.InputCostPer1M*1.25)
		}
	}
	if n == 0 {
		t.Fatal("no anthropic entries")
	}
}

func TestCostUsesExplicitCacheReadPrice(t *testing.T) {
	// gpt-4.1 lists cached input at $0.50, not 0.1x.
	got := CalculateCost("gpt-4.1", Usage{InputTokens: 1_000_000, CacheReadTokens: 400_000})
	want := 0.6*2.00 + 0.4*0.50
	if d := got - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestResponsesUsageParsesCachedTokens(t *testing.T) {
	o := &openaiResponses{}
	body := `{"output":[],"usage":{"input_tokens":1000,"output_tokens":50,"input_tokens_details":{"cached_tokens":800}}}`
	resp, err := o.parseResponse(body)
	if err != nil || resp.Usage != (Usage{InputTokens: 1000, OutputTokens: 50, CacheReadTokens: 800, CacheReported: true}) {
		t.Fatalf("non-stream usage = %+v err=%v", resp.Usage, err)
	}
	// No details object: nothing reported, not a miss.
	resp, err = o.parseResponse(`{"output":[],"usage":{"input_tokens":10,"output_tokens":1}}`)
	if err != nil || resp.Usage.CacheReported || resp.Usage.CacheReadTokens != 0 {
		t.Fatalf("absent details = %+v err=%v", resp.Usage, err)
	}
	// An explicit zero is a reported miss.
	resp, _ = o.parseResponse(`{"output":[],"usage":{"input_tokens":10,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`)
	if !resp.Usage.CacheReported || resp.Usage.CacheReadTokens != 0 {
		t.Fatalf("zero details = %+v", resp.Usage)
	}
	// Cached tokens are a subset of input and never exceed it.
	resp, _ = o.parseResponse(`{"output":[],"usage":{"input_tokens":10,"output_tokens":1,"input_tokens_details":{"cached_tokens":99}}}`)
	if resp.Usage.CacheReadTokens != 10 {
		t.Fatalf("clamp = %+v", resp.Usage)
	}

	for _, event := range []string{"response.completed", "response.incomplete"} {
		stream := "event: " + event + "\ndata: {\"type\":\"" + event + "\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":2000,\"output_tokens\":70,\"input_tokens_details\":{\"cached_tokens\":1536}}}}\n\n"
		resp, err = o.parseStream(strings.NewReader(stream), nil)
		if err != nil || resp.Usage != (Usage{InputTokens: 2000, OutputTokens: 70, CacheReadTokens: 1536, CacheReported: true}) {
			t.Fatalf("%s stream usage = %+v err=%v", event, resp.Usage, err)
		}
	}
}

func TestCompletionsUsageParsesCachedTokens(t *testing.T) {
	o := &openaiChatCompletions{}
	resp, err := o.parseResponse(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":500,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":256}}}`)
	if err != nil || resp.Usage != (Usage{InputTokens: 500, OutputTokens: 5, CacheReadTokens: 256, CacheReported: true}) {
		t.Fatalf("non-stream usage = %+v err=%v", resp.Usage, err)
	}
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":300,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":128}}}\n\ndata: [DONE]\n\n"
	resp, err = o.parseStream(strings.NewReader(stream), nil)
	if err != nil || resp.Usage != (Usage{InputTokens: 300, OutputTokens: 3, CacheReadTokens: 128, CacheReported: true}) {
		t.Fatalf("stream usage = %+v err=%v", resp.Usage, err)
	}
}

func TestUsageAddKeepsCacheReported(t *testing.T) {
	var u Usage
	u.Add(Usage{InputTokens: 10})
	u.Add(Usage{InputTokens: 10, CacheReadTokens: 4, CacheReported: true})
	if u != (Usage{InputTokens: 20, CacheReadTokens: 4, CacheReported: true}) {
		t.Fatalf("sum = %+v", u)
	}
}
