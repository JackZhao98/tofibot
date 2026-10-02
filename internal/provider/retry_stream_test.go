package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type streamAttempt struct {
	delta    string
	err      error
	response *ChatResponse
}
type streamRetryFixture struct {
	attempts []streamAttempt
	calls    atomic.Int32
}

func (p *streamRetryFixture) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, errors.New("unused")
}
func (p *streamRetryFixture) ChatStream(_ context.Context, _ *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	i := int(p.calls.Add(1)) - 1
	a := p.attempts[i]
	if a.delta != "" {
		onDelta(StreamDelta{Content: a.delta})
	}
	return a.response, a.err
}

func TestRetryStreamForwardsDeltaBeforeProviderReturns(t *testing.T) {
	called := false
	p := &streamRetryFixture{attempts: []streamAttempt{{delta: "hello", response: &ChatResponse{Content: "hello"}}}}
	rp := NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond})
	var resp *ChatResponse
	var err error
	resp, err = rp.ChatStream(context.Background(), &ChatRequest{Model: "m"}, func(d StreamDelta) {
		called = true
		if resp != nil {
			t.Fatal("callback ran after ChatStream returned")
		}
		if d.Content != "hello" {
			t.Errorf("delta=%q", d.Content)
		}
	})
	if err != nil || resp == nil || !called {
		t.Fatalf("resp=%v err=%v called=%v", resp, err, called)
	}
}

func TestRetryStreamDoesNotRetryAfterDeltaError(t *testing.T) {
	p := &streamRetryFixture{attempts: []streamAttempt{{delta: "prefix", err: NewAPIError("test", 500, "connection lost")}, {response: &ChatResponse{Content: "retry"}}}}
	var got string
	rp := NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond})
	_, err := rp.ChatStream(context.Background(), &ChatRequest{Model: "m"}, func(d StreamDelta) { got += d.Content })
	if err == nil || got != "prefix" || p.calls.Load() != 1 {
		t.Fatalf("err=%v got=%q calls=%d", err, got, p.calls.Load())
	}
}

func TestRetryStreamRetriesWhenErrorPrecedesDelta(t *testing.T) {
	p := &streamRetryFixture{attempts: []streamAttempt{{err: NewAPIError("test", 500, "connection lost")}, {delta: "recovered", response: &ChatResponse{Content: "recovered"}}}}
	var got string
	rp := NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond})
	resp, err := rp.ChatStream(context.Background(), &ChatRequest{Model: "m"}, func(d StreamDelta) { got += d.Content })
	if err != nil || resp == nil || got != "recovered" || p.calls.Load() != 2 {
		t.Fatalf("resp=%v err=%v got=%q calls=%d", resp, err, got, p.calls.Load())
	}
}
