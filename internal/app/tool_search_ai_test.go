package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

type toolSearchAIStubProvider struct {
	response *provider.ChatResponse
	err      error
	request  *provider.ChatRequest
	delay    time.Duration
}

func (p *toolSearchAIStubProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.request = req
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return p.response, p.err
}

func (p *toolSearchAIStubProvider) ChatStream(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return nil, nil
}

func TestExpandToolSearchQueryParsesBoundedTerms(t *testing.T) {
	stub := &toolSearchAIStubProvider{response: &provider.ChatResponse{Content: `["web search","browser", "memory save", "WEB SEARCH"]`}}
	got, err := expandToolSearchQueryWithProvider(context.Background(), "中文找网页搜索工具", stub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "web search,browser,memory save" {
		t.Fatalf("terms = %#v", got)
	}
	if stub.request.Model != toolSearchAIModel || stub.request.ReasoningEffort != "low" {
		t.Fatalf("request model/effort = %q/%q", stub.request.Model, stub.request.ReasoningEffort)
	}
	if len(stub.request.Tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(stub.request.Tools))
	}
	if !strings.Contains(stub.request.Messages[0].Content, "untrusted") {
		t.Fatalf("query was not framed as untrusted data: %q", stub.request.Messages[0].Content)
	}
}

func TestExpandToolSearchQueryRejectsInvalidInputAndOutput(t *testing.T) {
	for _, tc := range []struct {
		name, query, output string
	}{
		{name: "empty query", query: " ", output: `[]`},
		{name: "query too long", query: strings.Repeat("x", toolSearchAIQueryLimit+1), output: `[]`},
		{name: "invalid JSON", query: "browser", output: "browser"},
		{name: "not an array", query: "browser", output: `null`},
		{name: "too many terms", query: "browser", output: `["a","b","c","d","e"]`},
		{name: "non English", query: "browser", output: `["网页搜索"]`},
		{name: "too long term", query: "browser", output: `["` + strings.Repeat("a", toolSearchAITermBytes+1) + `"]`},
		{name: "oversized response", query: "browser", output: strings.Repeat(" ", toolSearchAIResponseCap+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &toolSearchAIStubProvider{response: &provider.ChatResponse{Content: tc.output}}
			if _, err := expandToolSearchQueryWithProvider(context.Background(), tc.query, stub); err == nil {
				t.Fatal("expected error")
			}
			if tc.name == "empty query" || tc.name == "query too long" {
				if stub.request != nil {
					t.Fatal("invalid query reached provider")
				}
			}
		})
	}
}

func TestExpandToolSearchQueryRejectsToolCallsAndHidesProviderError(t *testing.T) {
	stub := &toolSearchAIStubProvider{response: &provider.ChatResponse{Content: `[]`, ToolCalls: []provider.ToolCall{{Name: "unexpected"}}}}
	if _, err := expandToolSearchQueryWithProvider(context.Background(), "secret query", stub); err == nil || strings.Contains(err.Error(), "secret query") {
		t.Fatalf("expected generic error without query text, got %v", err)
	}
	stub = &toolSearchAIStubProvider{err: context.DeadlineExceeded}
	if _, err := expandToolSearchQueryWithProvider(context.Background(), "secret query", stub); err == nil || strings.Contains(err.Error(), "secret query") {
		t.Fatalf("expected generic error without query text, got %v", err)
	}
}

func TestExpandToolSearchQueryHasShortTimeout(t *testing.T) {
	stub := &toolSearchAIStubProvider{delay: 4 * time.Second}
	started := time.Now()
	_, err := expandToolSearchQueryWithProvider(context.Background(), "browser", stub)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(started); elapsed > 3500*time.Millisecond {
		t.Fatalf("timeout took %s", elapsed)
	}
}
