package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type sseEvent struct {
	name string
	data map[string]any
}

func sseStream(events []sseEvent, withEventLines bool) string {
	var b strings.Builder
	for _, ev := range events {
		data := map[string]any{"type": ev.name}
		for k, v := range ev.data {
			data[k] = v
		}
		raw, _ := json.Marshal(data)
		if withEventLines {
			fmt.Fprintf(&b, "event: %s\n", ev.name)
		}
		fmt.Fprintf(&b, "data: %s\n\n", raw)
	}
	return b.String()
}

type parallelCall struct {
	index        int
	itemID, call string
	args         string
}

func parallelCalls() []parallelCall {
	return []parallelCall{
		{1, "fc_a", "call_a", `{"name":"mcp_gh__list_issues","arguments":{"repo":"tofi","state":"open"}}`},
		{2, "fc_b", "call_b", `{"name":"mcp_gh__list_prs","arguments":{"repo":"tofi"}}`},
		{3, "fc_c", "call_c", `{"name":"mcp_gh__get_repo","arguments":{"repo":"tofi"}}`},
		{4, "fc_d", "call_d", `{"name":"mcp_gh__list_commits","arguments":{"repo":"tofi","limit":5}}`},
	}
}

func reasoningPrefix() []sseEvent {
	reasoning := map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "enc", "summary": []any{}}
	return []sseEvent{
		{"response.created", map[string]any{"response": map[string]any{"id": "resp_1"}}},
		{"response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}}}},
		{"response.output_item.done", map[string]any{"output_index": 0, "item": reasoning}},
	}
}

func fcItem(c parallelCall, args string) map[string]any {
	return map[string]any{"type": "function_call", "id": c.itemID, "call_id": c.call, "name": "call_mcp_tool", "arguments": args, "status": "completed"}
}

var completedSSE = sseEvent{"response.completed", map[string]any{"response": map[string]any{"usage": map[string]any{"input_tokens": 10, "output_tokens": 20}}}}

func chunks(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func assertParallelArgs(t *testing.T, resp *ChatResponse, calls []parallelCall) {
	t.Helper()
	if len(resp.ToolCalls) != len(calls) {
		t.Fatalf("tool calls = %d, want %d: %+v", len(resp.ToolCalls), len(calls), resp.ToolCalls)
	}
	for i, c := range calls {
		got := resp.ToolCalls[i]
		if got.ID != c.call || got.Name != "call_mcp_tool" || got.Arguments != c.args {
			t.Fatalf("call %d = %+v, want id=%s args=%s", i, got, c.call, c.args)
		}
		if !json.Valid([]byte(got.Arguments)) {
			t.Fatalf("call %d arguments are not JSON: %q", i, got.Arguments)
		}
	}
}

// Interleaved deltas across four parallel calls after a reasoning item.
func TestResponsesStreamParallelCallsInterleavedDeltas(t *testing.T) {
	calls := parallelCalls()
	events := reasoningPrefix()
	for _, c := range calls {
		events = append(events, sseEvent{"response.output_item.added", map[string]any{"output_index": c.index, "item": fcItem(c, "")}})
	}
	parts := make([][]string, len(calls))
	for i, c := range calls {
		parts[i] = chunks(c.args, 7)
	}
	for step := 0; ; step++ {
		emitted := false
		for i, c := range calls {
			if step < len(parts[i]) {
				emitted = true
				events = append(events, sseEvent{"response.function_call_arguments.delta", map[string]any{"output_index": c.index, "item_id": c.itemID, "delta": parts[i][step]}})
			}
		}
		if !emitted {
			break
		}
	}
	for _, c := range calls {
		events = append(events,
			sseEvent{"response.function_call_arguments.done", map[string]any{"output_index": c.index, "item_id": c.itemID, "arguments": c.args}},
			sseEvent{"response.output_item.done", map[string]any{"output_index": c.index, "item": fcItem(c, c.args)}})
	}
	events = append(events, completedSSE)
	for _, withEvent := range []bool{true, false} {
		var streamed = map[int]string{}
		resp, err := (&openaiResponses{}).parseStream(strings.NewReader(sseStream(events, withEvent)), func(d StreamDelta) {
			for _, tc := range d.ToolCalls {
				streamed[tc.Index] += tc.Arguments
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		assertParallelArgs(t, resp, calls)
		for _, c := range calls {
			if streamed[c.index] != c.args {
				t.Fatalf("event lines=%v: streamed deltas for %d = %q, want %q", withEvent, c.index, streamed[c.index], c.args)
			}
		}
		if len(resp.ReasoningItems) != 1 {
			t.Fatalf("reasoning items = %+v", resp.ReasoningItems)
		}
	}
}

// The incident shape: the backend sends no argument deltas for some parallel
// calls; only function_call_arguments.done and/or output_item.done carry them.
func TestResponsesStreamParallelCallsArgumentsOnlyInDoneEvents(t *testing.T) {
	calls := parallelCalls()
	events := reasoningPrefix()
	for i, c := range calls {
		events = append(events, sseEvent{"response.output_item.added", map[string]any{"output_index": c.index, "item": fcItem(c, "")}})
		switch i {
		case 0: // normal streaming
			for _, p := range chunks(c.args, 5) {
				events = append(events, sseEvent{"response.function_call_arguments.delta", map[string]any{"output_index": c.index, "item_id": c.itemID, "delta": p}})
			}
			events = append(events, sseEvent{"response.function_call_arguments.done", map[string]any{"output_index": c.index, "item_id": c.itemID, "arguments": c.args}})
		case 1: // only arguments.done
			events = append(events, sseEvent{"response.function_call_arguments.done", map[string]any{"output_index": c.index, "item_id": c.itemID, "arguments": c.args}})
		}
		// cases 2, 3: only output_item.done carries arguments
		events = append(events, sseEvent{"response.output_item.done", map[string]any{"output_index": c.index, "item": fcItem(c, c.args)}})
	}
	events = append(events, completedSSE)
	streamed := map[int]int{}
	resp, err := (&openaiResponses{}).parseStream(strings.NewReader(sseStream(events, true)), func(d StreamDelta) {
		for _, tc := range d.ToolCalls {
			streamed[tc.Index] += len(tc.Arguments)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	assertParallelArgs(t, resp, calls)
	// The agent's argument-size cap must still see every call's full size.
	for _, c := range calls {
		if streamed[c.index] != len(c.args) {
			t.Fatalf("cap observer saw %d chars for index %d, want %d", streamed[c.index], c.index, len(c.args))
		}
	}
}

// Deltas keyed only by item_id, arriving before (or without) output_item.added,
// and a partial delta stream corrected by the authoritative done event.
func TestResponsesStreamCallArgumentsRobustToOrderingAndKeys(t *testing.T) {
	calls := parallelCalls()[:2]
	a, b := calls[0], calls[1]
	events := append(reasoningPrefix(),
		// b's deltas reference only item_id and arrive before its added event.
		sseEvent{"response.function_call_arguments.delta", map[string]any{"item_id": b.itemID, "delta": b.args[:10]}},
		sseEvent{"response.output_item.added", map[string]any{"output_index": a.index, "item": fcItem(a, "")}},
		sseEvent{"response.output_item.added", map[string]any{"output_index": b.index, "item": fcItem(b, "")}},
		sseEvent{"response.function_call_arguments.delta", map[string]any{"item_id": b.itemID, "output_index": b.index, "delta": b.args[10:]}},
		// a streams a truncated prefix only; output_item.done is authoritative.
		sseEvent{"response.function_call_arguments.delta", map[string]any{"item_id": a.itemID, "output_index": a.index, "delta": a.args[:12]}},
		sseEvent{"response.output_item.done", map[string]any{"output_index": b.index, "item": fcItem(b, b.args)}},
		sseEvent{"response.output_item.done", map[string]any{"output_index": a.index, "item": fcItem(a, a.args)}},
		completedSSE,
	)
	resp, err := (&openaiResponses{}).parseStream(strings.NewReader(sseStream(events, true)), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertParallelArgs(t, resp, calls)
}
