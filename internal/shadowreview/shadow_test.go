package shadowreview

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/provider"
	"testing"
)

type syntheticReviewer struct {
	t     *testing.T
	tools bool
}

func (p syntheticReviewer) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if req.Model != ModelID || len(req.Tools) != 0 {
		p.t.Fatal("unsafe shadow request", req)
	}
	result := &provider.ChatResponse{Content: `{"recommendation":"allow","rationale":"Synthetic recommendation only","execution_approved":true}`}
	if p.tools {
		result.ToolCalls = []provider.ToolCall{{ID: "forbidden", Name: "execute"}}
	}
	return result, nil
}
func (p syntheticReviewer) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}
func TestShadowRecommendationsNeverAuthorizeExecution(t *testing.T) {
	for fixture := range fixtures {
		r, err := EvaluateSynthetic(context.Background(), syntheticReviewer{t: t}, fixture)
		if err != nil || r.ExecutionApproved {
			t.Fatalf("report=%+v err=%v", r, err)
		}
	}
	if _, err := EvaluateSynthetic(context.Background(), syntheticReviewer{t: t, tools: true}, "public_search"); err == nil {
		t.Fatal("accepted executable reviewer output")
	}
	if _, err := EvaluateSynthetic(context.Background(), syntheticReviewer{t: t}, "real-user-record"); err == nil {
		t.Fatal("accepted real input")
	}
}
