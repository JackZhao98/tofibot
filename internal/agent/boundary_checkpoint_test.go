package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func boundaryBase(messages []provider.Message, skipped []provider.ToolCall) *Continuation {
	return &Continuation{
		Version: continuationVersion, Kind: ContinuationBoundary, Attempt: 1,
		Messages: messages, SkippedToolCalls: skipped,
		TotalUsage: provider.Usage{InputTokens: 10, OutputTokens: 2},
		ModelUsage: map[string]ModelUsage{"test-model": {InputTokens: 10, OutputTokens: 2, APICallCount: 1}},
		LLMCalls:   1,
	}
}

func twoCallTurn() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "synthetic task"},
		{Role: "assistant", ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "lookup", Arguments: `{"q":"1"}`},
			{ID: "b", Name: "write_thing", Arguments: `{"id":"x"}`},
			{ID: "c", Name: "lookup", Arguments: `{"q":"3"}`},
		}},
	}
}

func TestValidateContinuationVersionsAndKinds(t *testing.T) {
	wait := continuationWithUsage(provider.Usage{InputTokens: 5, OutputTokens: 1}, ModelUsage{InputTokens: 5, OutputTokens: 1, APICallCount: 1}, 1)

	v1 := *wait
	v1.Version = 1
	if err := ValidateContinuation(&v1); err != nil {
		t.Fatalf("v1 input wait rejected: %v", err)
	}
	if v1.EffectiveKind() != ContinuationInputWait {
		t.Fatalf("v1 kind = %q", v1.EffectiveKind())
	}

	v2 := *wait
	v2.Kind = ContinuationInputWait
	if err := ValidateContinuation(&v2); err != nil {
		t.Fatalf("v2 input_wait rejected: %v", err)
	}
	v2NoKind := *wait // a missing kind means input_wait
	if err := ValidateContinuation(&v2NoKind); err != nil || v2NoKind.EffectiveKind() != ContinuationInputWait {
		t.Fatalf("v2 continuation without a kind: %v", err)
	}

	v1Boundary := boundaryBase(twoCallTurn()[:1], nil)
	v1Boundary.Version = 1
	if err := ValidateContinuation(v1Boundary); err == nil {
		t.Fatal("v1 carrying a boundary kind accepted")
	}

	future := *wait
	future.Version, future.Kind = 3, ContinuationInputWait
	err := ValidateContinuation(&future)
	if err == nil || !strings.Contains(err.Error(), "unsupported agent continuation version 3") {
		t.Fatalf("future version error = %v", err)
	}
}

func TestValidateContinuationBoundary(t *testing.T) {
	turn := twoCallTurn()
	calls := turn[1].ToolCalls

	// No unresolved call: all results present.
	resolved := append(append([]provider.Message(nil), turn...),
		provider.Message{Role: "tool", ToolCallID: "a", ToolName: "lookup", Content: "ok"},
		provider.Message{Role: "tool", ToolCallID: "b", ToolName: "write_thing", Content: "ok"},
		provider.Message{Role: "tool", ToolCallID: "c", ToolName: "lookup", Content: "ok"})
	if err := ValidateContinuation(boundaryBase(resolved, nil)); err != nil {
		t.Fatalf("fully resolved boundary rejected: %v", err)
	}
	// No tool calls at all in the transcript is a legal boundary too.
	if err := ValidateContinuation(boundaryBase(turn[:1], nil)); err != nil {
		t.Fatalf("boundary without calls rejected: %v", err)
	}
	// Whole turn unresolved, then a suffix after the first result.
	if err := ValidateContinuation(boundaryBase(turn, calls)); err != nil {
		t.Fatalf("turn boundary rejected: %v", err)
	}
	partial := append(append([]provider.Message(nil), turn...), provider.Message{Role: "tool", ToolCallID: "a", ToolName: "lookup", Content: "ok"})
	if err := ValidateContinuation(boundaryBase(partial, calls[1:])); err != nil {
		t.Fatalf("partial boundary rejected: %v", err)
	}
	// Mismatches and forbidden waiting state.
	if err := ValidateContinuation(boundaryBase(partial, calls)); err == nil {
		t.Fatal("boundary listing an already resolved call accepted")
	}
	if err := ValidateContinuation(boundaryBase(partial, nil)); err == nil {
		t.Fatal("boundary omitting an unresolved call accepted")
	}
	waiting := boundaryBase(turn, calls)
	waiting.WaitingToolCallID = "a"
	if err := ValidateContinuation(waiting); err == nil {
		t.Fatal("boundary with waiting tool state accepted")
	}
	altered := append([]provider.ToolCall(nil), calls...)
	altered[1].Arguments = `{"id":"other"}`
	if err := ValidateContinuation(boundaryBase(turn, altered)); err == nil {
		t.Fatal("boundary with altered call arguments accepted")
	}
}

func TestResumeBoundaryFourCases(t *testing.T) {
	turn := twoCallTurn()
	calls := turn[1].ToolCalls

	t.Run("no unresolved call adds nothing", func(t *testing.T) {
		resolved := append(append([]provider.Message(nil), turn...),
			provider.Message{Role: "tool", ToolCallID: "a", Content: "ok"},
			provider.Message{Role: "tool", ToolCallID: "b", Content: "ok"},
			provider.Message{Role: "tool", ToolCallID: "c", Content: "ok"})
		c := boundaryBase(resolved, nil)
		got, err := resumeBoundary(c, map[string]ResumeToolResult{"a": {Content: "must not be used"}})
		if err != nil || len(got) != len(resolved) {
			t.Fatalf("messages=%d want %d err=%v", len(got), len(resolved), err)
		}
	})

	t.Run("in-flight observation tool is retryable", func(t *testing.T) {
		partial := turn
		o := RestartInterruptedOutcome(true)
		got, err := resumeBoundary(boundaryBase(partial, calls), map[string]ResumeToolResult{"a": {Content: o.JSON(), Outcome: &o, Failed: true}})
		if err != nil {
			t.Fatal(err)
		}
		m := got[len(turn)]
		if m.ToolCallID != "a" || m.ToolOutcome == nil || m.ToolOutcome.Code != "restart_interrupted" || m.ToolOutcome.Certainty != "no_side_effects" || m.ToolOutcome.NextAction != "retry" || m.ToolOutcome.Status != tooloutcome.Transient {
			t.Fatalf("observation message = %+v", m)
		}
	})

	t.Run("in-flight effectful tool needs verification", func(t *testing.T) {
		resolvedA := append(append([]provider.Message(nil), turn...), provider.Message{Role: "tool", ToolCallID: "a", Content: "ok"})
		o := RestartInterruptedOutcome(false)
		got, err := resumeBoundary(boundaryBase(resolvedA, calls[1:]), map[string]ResumeToolResult{"b": {Content: o.JSON(), Outcome: &o, Failed: true}})
		if err != nil {
			t.Fatal(err)
		}
		m := got[len(resolvedA)]
		if m.ToolCallID != "b" || m.ToolOutcome == nil || m.ToolOutcome.Code != "restart_interrupted_effect" || m.ToolOutcome.Status != tooloutcome.Uncertain || m.ToolOutcome.Certainty != "unknown" || m.ToolOutcome.NextAction != "verify_effect" {
			t.Fatalf("effect message = %+v", m)
		}
	})

	t.Run("remaining batch calls are skipped not replayed", func(t *testing.T) {
		resolvedA := append(append([]provider.Message(nil), turn...), provider.Message{Role: "tool", ToolCallID: "a", Content: "ok"})
		o := RestartInterruptedOutcome(false)
		got, err := resumeBoundary(boundaryBase(resolvedA, calls[1:]), map[string]ResumeToolResult{"b": {Content: o.JSON(), Outcome: &o, Failed: true}})
		if err != nil {
			t.Fatal(err)
		}
		added := got[len(resolvedA):]
		if len(added) != 2 || added[0].ToolCallID != "b" || added[1].ToolCallID != "c" {
			t.Fatalf("added = %+v", added)
		}
		skip := added[1]
		if !skip.ToolFailed || skip.ToolOutcome == nil || skip.ToolOutcome.Code != "batch_skipped" || skip.ToolOutcome.Certainty != "not_executed" || skip.ToolOutcome.Status != tooloutcome.Permanent {
			t.Fatalf("skipped message = %+v", skip)
		}
	})
}

func lookupAndWriteTools(lookups, writes *atomic.Int32) []ExtraBuiltinTool {
	return []ExtraBuiltinTool{
		lookupTool(func(context.Context, map[string]interface{}) (string, error) { lookups.Add(1); return "found", nil }),
		{Schema: provider.Tool{Name: "write_thing", Parameters: map[string]any{"type": "object"}}, HandlerCtx: func(context.Context, map[string]interface{}) (string, error) {
			writes.Add(1)
			return "written", nil
		}},
	}
}

func TestCheckpointsAtTurnAndEveryToolResult(t *testing.T) {
	var lookups, writes atomic.Int32
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "lookup", Arguments: `{"q":"1"}`},
			{ID: "b", Name: "write_thing", Arguments: `{"id":"x"}`},
		}, Usage: provider.Usage{InputTokens: 10, OutputTokens: 2}}),
		reply(provider.ChatResponse{Content: "done", Usage: provider.Usage{InputTokens: 12, OutputTokens: 3}}),
	}}
	type seen struct {
		kind      string
		unresolve []string
		c         *Continuation
	}
	var got []seen
	var writesAtCheckpoint []int32
	_, err := runLoop(t, AgentConfig{Provider: p, Attempt: 7, ExtraTools: lookupAndWriteTools(&lookups, &writes), OnCheckpoint: func(kind string, c *Continuation) error {
		if err := ValidateContinuation(c); err != nil {
			t.Fatalf("%s checkpoint invalid: %v", kind, err)
		}
		var ids []string
		for _, call := range c.SkippedToolCalls {
			ids = append(ids, call.ID)
		}
		got = append(got, seen{kind, ids, c})
		writesAtCheckpoint = append(writesAtCheckpoint, writes.Load())
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].kind != "turn" || got[1].kind != "tool" || got[2].kind != "tool" {
		t.Fatalf("checkpoints = %+v", got)
	}
	if strings.Join(got[0].unresolve, ",") != "a,b" || strings.Join(got[1].unresolve, ",") != "b" || len(got[2].unresolve) != 0 {
		t.Fatalf("unresolved sequences = %v %v %v", got[0].unresolve, got[1].unresolve, got[2].unresolve)
	}
	if writesAtCheckpoint[0] != 0 || writesAtCheckpoint[1] != 0 || writesAtCheckpoint[2] != 1 {
		t.Fatalf("checkpoint timing relative to the write tool: %v", writesAtCheckpoint)
	}
	for _, s := range got {
		if s.c.Kind != ContinuationBoundary || s.c.Attempt != 7 || s.c.Version != continuationVersion {
			t.Fatalf("checkpoint header = kind %q attempt %d version %d", s.c.Kind, s.c.Attempt, s.c.Version)
		}
	}
}

func TestCheckpointFailureStopsBeforeNextDispatch(t *testing.T) {
	var lookups, writes atomic.Int32
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "lookup", Arguments: `{}`},
			{ID: "b", Name: "write_thing", Arguments: `{}`},
		}, Usage: provider.Usage{InputTokens: 10, OutputTokens: 2}}),
	}}
	boom := errors.New("disk full")
	_, err := runLoop(t, AgentConfig{Provider: p, ExtraTools: lookupAndWriteTools(&lookups, &writes), OnCheckpoint: func(kind string, _ *Continuation) error {
		if kind == "tool" {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if lookups.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("lookups=%d writes=%d: the run kept dispatching after a failed checkpoint", lookups.Load(), writes.Load())
	}
}

func TestCheckpointFailureOnTurnPreventsAnyDispatch(t *testing.T) {
	var lookups, writes atomic.Int32
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		toolCallReply("a", "write_thing", `{}`),
	}}
	_, err := runLoop(t, AgentConfig{Provider: p, ExtraTools: lookupAndWriteTools(&lookups, &writes), OnCheckpoint: func(string, *Continuation) error { return errors.New("no space") }})
	if err == nil || writes.Load() != 0 {
		t.Fatalf("err=%v writes=%d", err, writes.Load())
	}
}

// A resumed boundary run hands the model the settled outcomes, executes none of
// the old batch, and the recovery fence still blocks an identical effect.
func TestBoundaryResumeFeedsOutcomesAndKeepsRerunGuard(t *testing.T) {
	var lookups, writes atomic.Int32
	turn := twoCallTurn()
	calls := turn[1].ToolCalls
	resolvedA := append(append([]provider.Message(nil), turn...), provider.Message{Role: "tool", ToolCallID: "a", ToolName: "lookup", Content: "found"})
	c := boundaryBase(resolvedA, calls[1:])
	inflight := RestartInterruptedOutcome(false)

	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		// The model tries the identical effectful operation again.
		toolCallReply("retry-b", "write_thing", `{"id":"x"}`),
		reply(provider.ChatResponse{Content: "verified, stopping"}),
	}}
	var emitted []provider.Message
	_, err := runLoop(t, AgentConfig{
		Provider: p, Continuation: c, ExtraTools: lookupAndWriteTools(&lookups, &writes),
		ResumeToolResults: map[string]ResumeToolResult{"b": {Content: inflight.JSON(), Outcome: &inflight, Failed: true}},
		OnMessage:         func(m provider.Message) { emitted = append(emitted, m) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("resume re-executed old calls or the fenced effect: lookups=%d writes=%d", lookups.Load(), writes.Load())
	}
	first := p.requests[0].Messages
	var sawB, sawC bool
	for _, m := range first {
		if m.Role == "tool" && m.ToolCallID == "b" && m.ToolOutcome != nil && m.ToolOutcome.Code == "restart_interrupted_effect" {
			sawB = true
		}
		if m.Role == "tool" && m.ToolCallID == "c" && m.ToolOutcome != nil && m.ToolOutcome.Code == "batch_skipped" {
			sawC = true
		}
	}
	if !sawB || !sawC {
		t.Fatalf("model did not receive the settled outcomes (b=%v c=%v)", sawB, sawC)
	}
	blocked := false
	for _, m := range emitted {
		if m.ToolCallID == "retry-b" && m.ToolOutcome != nil && m.ToolFailed {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("identical effectful retry was not blocked by the recovery guard: %+v", emitted)
	}
}

func TestBoundaryResumeWithoutUnresolvedCallAsksModelAgain(t *testing.T) {
	var lookups, writes atomic.Int32
	turn := twoCallTurn()[:1]
	c := boundaryBase(turn, nil)
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{Content: "regenerated"}),
	}}
	res, err := runLoop(t, AgentConfig{Provider: p, Continuation: c, ExtraTools: lookupAndWriteTools(&lookups, &writes)})
	if err != nil || res == nil || res.Content != "regenerated" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(p.requests[0].Messages) != 1 {
		t.Fatalf("resume added messages: %+v", p.requests[0].Messages)
	}
}
