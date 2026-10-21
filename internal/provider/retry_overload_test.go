package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInStreamOverloadIsRetryableByCode(t *testing.T) {
	for code, want := range map[string]bool{
		"server_is_overloaded": true, "overloaded": true, "service_unavailable": true,
		"usage_limit_reached": false, "insufficient_quota": false, "invalid_prompt": false, "": false,
	} {
		if got := IsRetryable(&ResponseFailedError{Code: code, Message: "any wording"}); got != want {
			t.Fatalf("%q: got %v want %v", code, got, want)
		}
	}
}

func TestResponsesStreamErrorEventsCarryTheirCode(t *testing.T) {
	for name, stream := range map[string]string{
		"response.failed": "event: response.failed\ndata: {\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"busy\"}}}\n\n",
		"error flat":      "event: error\ndata: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\"message\":\"busy\"}\n\n",
		"error nested":    "data: {\"type\":\"error\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"busy\"}}\n\n",
	} {
		_, err := (&openaiResponses{}).parseStream(strings.NewReader(stream), nil)
		var failed *ResponseFailedError
		if !errors.As(err, &failed) || failed.Code != "server_is_overloaded" || !IsRetryable(err) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

type flakyStream struct {
	errs  []error
	calls int
}

func (p *flakyStream) Chat(context.Context, *ChatRequest) (*ChatResponse, error) { return nil, nil }
func (p *flakyStream) ChatStream(context.Context, *ChatRequest, func(StreamDelta)) (*ChatResponse, error) {
	i := p.calls
	p.calls++
	if i < len(p.errs) {
		return nil, p.errs[i]
	}
	return &ChatResponse{Content: "ok"}, nil
}

func TestRetryStreamRecoversFromOverloadOnce(t *testing.T) {
	p := &flakyStream{errs: []error{&ResponseFailedError{Code: "server_is_overloaded", Message: "busy"}}}
	resp, err := NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond}).ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil)
	if err != nil || resp.Content != "ok" || p.calls != 2 {
		t.Fatalf("resp=%v err=%v calls=%d", resp, err, p.calls)
	}
}

func TestRetryHonorsRetryAfterAndStopsAtTheBudget(t *testing.T) {
	asked := &APIError{StatusCode: 429, Body: "slow down", RetryAfter: 80 * time.Millisecond}
	p := &flakyStream{errs: []error{asked}}
	var waited time.Duration
	rp := NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond, OnRetry: func(_ int, _ error, d time.Duration) { waited = d }})
	if _, err := rp.ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil); err != nil || waited < 80*time.Millisecond {
		t.Fatalf("err=%v waited=%v", err, waited)
	}

	p = &flakyStream{errs: []error{&APIError{StatusCode: 503}, &APIError{StatusCode: 503}, &APIError{StatusCode: 503}}}
	rp = NewRetryProvider(p, RetryConfig{BaseDelay: 40 * time.Millisecond, TotalBudget: 60 * time.Millisecond})
	_, err := rp.ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil)
	var api *APIError
	if err == nil || !errors.As(err, &api) || p.calls > 2 {
		t.Fatalf("budget not enforced: err=%v calls=%d", err, p.calls)
	}

	p = &flakyStream{errs: []error{&APIError{StatusCode: 429, RetryAfter: 10 * time.Minute}}}
	if _, err = NewRetryProvider(p, RetryConfig{BaseDelay: time.Millisecond}).ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil); err == nil || p.calls != 1 {
		t.Fatalf("an unreasonable Retry-After must end the retries: err=%v calls=%d", err, p.calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	h := func(k, v string) http.Header { return http.Header{k: []string{v}} }
	for name, c := range map[string]struct {
		h    http.Header
		want time.Duration
	}{
		"seconds":  {h("Retry-After", "7"), 7 * time.Second},
		"fraction": {h("Retry-After", "1.5"), 1500 * time.Millisecond},
		"date":     {h("Retry-After", now.Add(9*time.Second).Format(http.TimeFormat)), 9 * time.Second},
		"ms":       {h("Retry-After-Ms", "250"), 250 * time.Millisecond},
		"negative": {h("Retry-After", "-3"), 0},
		"junk":     {h("Retry-After", "soon"), 0},
		"none":     {http.Header{}, 0},
	} {
		if got := parseRetryAfter(c.h, now); got != c.want {
			t.Fatalf("%s: got %v want %v", name, got, c.want)
		}
	}
}
