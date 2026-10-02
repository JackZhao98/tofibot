package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	maxToolEventArguments = 16 * 1024
	maxToolEventResult    = 32 * 1024
)

var validToolEventStatuses = map[string]bool{
	"queued":    true,
	"running":   true,
	"completed": true,
	"failed":    true,
}

type trackedTool struct {
	name          string
	arguments     string
	status        string
	resumeWaiting bool
}

// restoreContinuation rebuilds only the parked portion of the original batch.
// It deliberately emits no queued/running events: those happened before the
// checkpoint, and replaying them would duplicate visible activity after a
// process restart.
func (t *toolEventTracker) restoreContinuation(c *agent.Continuation) error {
	if err := agent.ValidateContinuation(c); err != nil {
		return err
	}
	var waiting provider.ToolCall
	found := false
	for _, msg := range c.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.ID == c.WaitingToolCallID {
				waiting = call
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return errors.New("continuation waiting tool is absent from transcript")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.tools[waiting.ID]; exists {
		return fmt.Errorf("duplicate restored tool call id %q", waiting.ID)
	}
	t.tools[waiting.ID] = trackedTool{name: waiting.Name, arguments: waiting.Arguments, status: "running", resumeWaiting: true}
	for _, call := range c.SkippedToolCalls {
		if _, exists := t.tools[call.ID]; exists {
			return fmt.Errorf("duplicate restored tool call id %q", call.ID)
		}
		t.tools[call.ID] = trackedTool{name: call.Name, arguments: call.Arguments, status: "queued"}
		t.pending[call.Name] = append(t.pending[call.Name], call.ID)
	}
	return nil
}

type toolEventTracker struct {
	callback func(ToolEvent) error

	mu      sync.Mutex
	tools   map[string]trackedTool
	pending map[string][]string
	err     error
}

func newToolEventTracker(callback func(ToolEvent) error) *toolEventTracker {
	return &toolEventTracker{
		callback: callback,
		tools:    make(map[string]trackedTool),
		pending:  make(map[string][]string),
	}
}

func (t *toolEventTracker) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *toolEventTracker) setErr(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.err == nil {
		t.err = err
	}
	t.mu.Unlock()
}

func (t *toolEventTracker) emit(event ToolEvent) error {
	if !validToolEventStatuses[event.Status] {
		return fmt.Errorf("invalid tool event status %q", event.Status)
	}
	event, _ = boundedToolEvent(event)
	if t.callback == nil {
		return nil
	}
	if err := t.callback(event); err != nil {
		t.setErr(err)
		return err
	}
	return nil
}

func (t *toolEventTracker) queue(calls []provider.ToolCall) {
	for _, call := range calls {
		if t.Err() != nil {
			return
		}
		if strings.TrimSpace(call.ID) == "" {
			t.setErr(errors.New("provider tool call has empty call id"))
			return
		}
		t.mu.Lock()
		if _, exists := t.tools[call.ID]; exists {
			t.mu.Unlock()
			t.setErr(fmt.Errorf("duplicate tool call id %q", call.ID))
			return
		}
		t.tools[call.ID] = trackedTool{name: call.Name, arguments: call.Arguments, status: "queued"}
		t.pending[call.Name] = append(t.pending[call.Name], call.ID)
		t.mu.Unlock()
		if err := t.emit(ToolEvent{CallID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: "queued"}); err != nil {
			return
		}
	}
}

// start claims the next provider call for a declared tool. ToolsOnly executes
// calls serially, so a per-name queue preserves the provider's exact IDs.
func (t *toolEventTracker) start(name string) (string, error) {
	if err := t.Err(); err != nil {
		return "", err
	}
	t.mu.Lock()
	ids := t.pending[name]
	if len(ids) == 0 {
		t.mu.Unlock()
		err := fmt.Errorf("tool %q ran without a queued provider call", name)
		t.setErr(err)
		return "", err
	}
	callID := ids[0]
	t.pending[name] = ids[1:]
	call := t.tools[callID]
	call.status = "running"
	t.tools[callID] = call
	t.mu.Unlock()
	if err := t.emit(ToolEvent{CallID: callID, Name: name, Arguments: call.arguments, Status: "running"}); err != nil {
		return "", err
	}
	return callID, nil
}

func (t *toolEventTracker) finish(msg provider.Message) {
	if msg.ToolCallID == "" {
		return
	}
	t.mu.Lock()
	call, ok := t.tools[msg.ToolCallID]
	if !ok {
		t.mu.Unlock()
		t.setErr(fmt.Errorf("tool result has unknown call id %q", msg.ToolCallID))
		return
	}
	if call.status != "queued" && call.status != "running" {
		t.mu.Unlock()
		t.setErr(fmt.Errorf("tool call %q completed twice", msg.ToolCallID))
		return
	}
	status := "completed"
	if msg.ToolFailed {
		status = "failed"
	}
	if call.status == "queued" {
		ids := t.pending[call.name]
		for i, id := range ids {
			if id == msg.ToolCallID {
				t.pending[call.name] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
	}
	call.status = status
	t.tools[msg.ToolCallID] = call
	t.mu.Unlock()
	if t.Err() != nil {
		return
	}
	name := msg.ToolName
	if name == "" {
		name = call.name
	}
	_ = t.emit(ToolEvent{CallID: msg.ToolCallID, Name: name, Arguments: call.arguments, Result: msg.Content, Status: status, Outcome: msg.ToolOutcome})
}

func boundedToolEvent(event ToolEvent) (ToolEvent, bool) {
	truncated := false
	event.Outcome = tooloutcome.Bounded(event.Outcome)
	event.Arguments, truncated = limitToolEventText(event.Arguments, maxToolEventArguments)
	result, resultTruncated := limitToolEventText(event.Result, maxToolEventResult)
	event.Result = result
	truncated = truncated || resultTruncated
	event.Truncated = event.Truncated || truncated
	return event, truncated
}

func limitToolEventText(value string, max int) (string, bool) {
	runes := []rune(value)
	if len(runes) <= max {
		return value, false
	}
	return string(runes[:max]) + "…", true
}
