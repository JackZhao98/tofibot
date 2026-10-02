package provider

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type mockProvider struct {
	responses []mockResponse
	callCount atomic.Int32
}
type mockResponse struct {
	resp *ChatResponse
	err  error
}

func (m *mockProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx >= len(m.responses) {
		return &ChatResponse{Content: "final"}, nil
	}
	return m.responses[idx].resp, m.responses[idx].err
}
func (m *mockProvider) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	return m.Chat(ctx, req)
}

func TestRetryProvider_SuccessOnFirstTry(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{resp: &ChatResponse{Content: "hello"}}}}
	rp := NewRetryProvider(mock, RetryConfig{})
	resp, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("expected 'hello', got %q", resp.Content)
	}
	if mock.callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", mock.callCount.Load())
	}
}
func TestRetryProvider_RetryOn500(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: NewAPIError("test", 500, "internal error")}, {err: NewAPIError("test", 502, "bad gateway")}, {resp: &ChatResponse{Content: "recovered"}}}}
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond})
	resp, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("expected 'recovered', got %q", resp.Content)
	}
	if mock.callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", mock.callCount.Load())
	}
}
func TestRetryProvider_NoRetryOn400(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: NewAPIError("test", 400, "bad request")}}}
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond})
	_, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if mock.callCount.Load() != 1 {
		t.Errorf("should not retry on 400, got %d calls", mock.callCount.Load())
	}
}
func TestRetryProvider_NoRetryOnContextOverflow(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: fmt.Errorf("context length exceeded: too many tokens")}}}
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond})
	_, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for context overflow")
	}
	if mock.callCount.Load() != 1 {
		t.Errorf("should not retry on context overflow, got %d calls", mock.callCount.Load())
	}
}
func TestRetryProvider_FallbackOnRateLimit(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: NewAPIError("test", 429, "rate limited")}, {err: NewAPIError("test", 429, "rate limited")}, {err: NewAPIError("test", 429, "rate limited")}, {resp: &ChatResponse{Content: "fallback success"}}}}
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond, MaxRateLimitRetries: 3, FallbackModel: "gpt-5-mini"})
	resp, err := rp.Chat(context.Background(), &ChatRequest{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "fallback success" {
		t.Errorf("expected 'fallback success', got %q", resp.Content)
	}
}
func TestRetryProvider_MaxRetriesExceeded(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: NewAPIError("test", 500, "error")}, {err: NewAPIError("test", 500, "error")}, {err: NewAPIError("test", 500, "error")}, {err: NewAPIError("test", 500, "error")}}}
	rp := NewRetryProvider(mock, RetryConfig{MaxRetries: 2, BaseDelay: time.Millisecond})
	_, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if mock.callCount.Load() != 3 {
		t.Errorf("expected 3 calls (1 + 2 retries), got %d", mock.callCount.Load())
	}
}
func TestRetryProvider_CancelledContext(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: NewAPIError("test", 500, "error")}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond})
	_, err := rp.Chat(ctx, &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
func TestRetryProvider_ConnectionError(t *testing.T) {
	mock := &mockProvider{responses: []mockResponse{{err: fmt.Errorf("dial tcp: connection refused")}, {resp: &ChatResponse{Content: "recovered"}}}}
	rp := NewRetryProvider(mock, RetryConfig{BaseDelay: time.Millisecond})
	resp, err := rp.Chat(context.Background(), &ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("expected 'recovered', got %q", resp.Content)
	}
}
