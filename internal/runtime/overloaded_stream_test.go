package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
)

// The provider answers HTTP 200 and reports overload inside the stream as a
// response.failed event. The first request fails that way, the second works:
// the run must complete, not fail on the first error.
func TestRunSurvivesOverloadReportedInsideTheStream(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if hits.Add(1) == 1 {
			fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"all done\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
	}))
	defer server.Close()
	e, err := New(Config{
		Resolve:  func(context.Context, string) (string, error) { return "synthetic-key", nil },
		Endpoint: func(string) string { return server.URL + "/v1" },
		Retries:  func() int { return 3 },
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(context.Background(), Request{RunID: "overloaded", BotID: "bot", Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "hi"}}, OnDelta: func(string) {}})
	if err != nil {
		t.Fatalf("an overloaded provider failed the run: %v", err)
	}
	if hits.Load() != 2 || res.Content != "all done" {
		t.Fatalf("hits=%d content=%q", hits.Load(), res.Content)
	}
}
