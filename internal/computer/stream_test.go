package computer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDesktopStreamCancellationAndTypedStatus(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/desktop/stream" || r.URL.Query().Get("bot_id") != "00000000-0000-0000-0000-000000000001" {
			t.Error("unexpected path")
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("stream"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	client, _ := New(Config{Client: &http.Client{Transport: redirectStreamTransport{url: server.URL}, Timeout: time.Nanosecond}})
	ctx, cancel := context.WithCancel(context.Background())
	response, err := client.DesktopStream(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err = io.ReadFull(response.Body, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("stream cancellation not propagated")
	}
	if _, err = client.DesktopStream(context.Background(), "../other"); err == nil {
		t.Fatal("invalid bot accepted")
	}
}

func TestDesktopStreamWithCursorValidatesAndForwardsPolicy(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.RawQuery
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("stream"))
	}))
	defer server.Close()
	client, _ := New(Config{Client: &http.Client{Transport: redirectStreamTransport{url: server.URL}}})
	response, err := client.DesktopStreamWithCursor(context.Background(), "00000000-0000-0000-0000-000000000001", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := <-seen; got != "bot_id=00000000-0000-0000-0000-000000000001&cursor=hidden" {
		t.Fatalf("query=%q", got)
	}
	if _, err := client.DesktopStreamWithCursor(context.Background(), "00000000-0000-0000-0000-000000000001", "bad"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

type redirectStreamTransport struct{ url string }

func (t redirectStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	dest, _ := http.NewRequest(req.Method, t.url+req.URL.RequestURI(), nil)
	clone.URL = dest.URL
	return http.DefaultTransport.RoundTrip(clone)
}
