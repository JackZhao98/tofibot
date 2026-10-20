package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
)

// Config.Retries is read for every model request: the count of retries after a
// transient provider error follows the live setting, and zero means none.
func TestRoutedRetriesFollowTheLiveSetting(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	retries := 0
	e, err := New(Config{
		Resolve:  func(context.Context, string) (string, error) { return "synthetic-key", nil },
		Endpoint: func(string) string { return server.URL + "/v1" },
		Retries:  func() int { return retries },
	})
	if err != nil {
		t.Fatal(err)
	}
	run := func() int32 {
		hits.Store(0)
		_, runErr := e.Run(context.Background(), Request{RunID: "retries", BotID: "bot", Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "hi"}}})
		if runErr == nil {
			t.Fatal("a failing provider must fail the run")
		}
		return hits.Load()
	}
	if got := run(); got != 1 {
		t.Fatalf("retries=0 made %d requests, want 1", got)
	}
	retries = 1
	if got := run(); got != 2 {
		t.Fatalf("retries=1 made %d requests, want 2", got)
	}
}
