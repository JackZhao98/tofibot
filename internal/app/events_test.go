package app

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readSSEEvent(t *testing.T, scanner *bufio.Scanner) int64 {
	t.Helper()
frames:
	for {
		var id int64
		hasID := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if hasID {
					return id
				}
				continue frames // comment or another metadata-only SSE frame
			}
			if strings.HasPrefix(line, "id: ") {
				var err error
				id, err = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
				if err != nil {
					t.Fatalf("invalid event id %q: %v", line, err)
				}
				hasID = true
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("reading SSE frame: %v", err)
		}
		t.Fatal("SSE stream ended before a complete frame")
		return 0
	}
}

func TestEventsKeepsConnectionForLaterEventsAndReplaysFromCursor(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	b, err := server.store.CreateBot("events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := server.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}

	firstID, err := server.store.Event(c.ID, "test", map[string]string{"value": "first"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/conversations/%s/events?after=0", httpServer.URL, c.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering=%q", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	if got := readSSEEvent(t, scanner); got != firstID {
		t.Fatalf("first event id=%d, want %d", got, firstID)
	}

	secondID, err := server.store.Event(c.ID, "test", map[string]string{"value": "second"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readSSEEvent(t, scanner); got != secondID {
		t.Fatalf("second event id=%d, want %d", got, secondID)
	}
	cancel()

	readFrom := func(target string, headers map[string]string) int64 {
		readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer readCancel()
		req, err := http.NewRequestWithContext(readCtx, http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return readSSEEvent(t, bufio.NewScanner(resp.Body))
	}

	if got := readFrom(fmt.Sprintf("%s/api/conversations/%s/events?after=%d", httpServer.URL, c.ID, firstID), nil); got != secondID {
		t.Fatalf("after replay id=%d, want %d", got, secondID)
	}
	if got := readFrom(fmt.Sprintf("%s/api/conversations/%s/events?after=0", httpServer.URL, c.ID), map[string]string{"Last-Event-ID": strconv.FormatInt(firstID, 10)}); got != secondID {
		t.Fatalf("Last-Event-ID replay id=%d, want %d", got, secondID)
	}
}

func TestEventsFlushesInitialCommentBeforeAnyDurableEvent(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	b, err := server.store.CreateBot("events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := server.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/conversations/%s/events?after=0", httpServer.URL, c.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		t.Fatalf("reading initial SSE comment: %v", scanner.Err())
	}
	if got := scanner.Text(); got != ": connected" {
		t.Fatalf("initial SSE frame=%q, want comment", got)
	}
	if !scanner.Scan() {
		t.Fatalf("reading initial SSE frame boundary: %v", scanner.Err())
	}
	if got := scanner.Text(); got != "" {
		t.Fatalf("initial SSE frame boundary=%q, want blank line", got)
	}
}

func TestEventsStopsWhenRequestContextIsCancelled(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	b, err := server.store.CreateBot("events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := server.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.events(recorder, req, c.ID)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("events handler did not stop after context cancellation")
	}
}
