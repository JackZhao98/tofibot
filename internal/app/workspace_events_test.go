package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type workspaceSSEFrame struct {
	ID    int64
	Event string
	Data  map[string]any
}

func readWorkspaceSSEFrame(t *testing.T, scanner *bufio.Scanner) workspaceSSEFrame {
	t.Helper()
	frame := workspaceSSEFrame{}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			var err error
			frame.ID, err = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("invalid workspace event id %q: %v", line, err)
			}
		case strings.HasPrefix(line, "event: "):
			frame.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame.Data); err != nil {
				t.Fatalf("invalid workspace event data: %v", err)
			}
		case line == "":
			if frame.ID == 0 {
				continue
			}
			return frame
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("reading workspace SSE frame: %v", err)
	}
	t.Fatal("workspace SSE stream ended before a complete frame")
	return workspaceSSEFrame{}
}

func openWorkspaceEvents(t *testing.T, base string, after int64) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/workspace/events?after=%d", base, after), nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		resp.Body.Close()
		cancel()
		t.Fatalf("workspace Content-Type=%q", got)
	}
	return resp, cancel
}

func TestWorkspaceEventsStreamMutationReconnectAndCredentialFreePayload(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	initial := server.store.workspaceEventCursor()
	resp, cancel := openWorkspaceEvents(t, httpServer.URL, initial)
	scanner := bufio.NewScanner(resp.Body)

	bot, err := server.store.CreateBot("workspace events", "private instructions", "model")
	if err != nil {
		t.Fatal(err)
	}
	created := readWorkspaceSSEFrame(t, scanner)
	if created.Event != "workspace" || created.Data["scope"] != workspaceScopeBots {
		t.Fatalf("bot event=%+v", created)
	}
	if created.Data["revision"] != float64(created.ID) {
		t.Fatalf("bot event revision=%v id=%d", created.Data["revision"], created.ID)
	}
	if raw, _ := json.Marshal(created.Data); strings.Contains(string(raw), "private instructions") || strings.Contains(string(raw), "workspace events") {
		t.Fatalf("workspace payload leaked state: %s", raw)
	}

	updatedName := "renamed workspace bot"
	if _, err = server.store.UpdateBot(bot.ID, &updatedName, nil, nil); err != nil {
		t.Fatal(err)
	}
	profile := readWorkspaceSSEFrame(t, scanner)
	if profile.Data["scope"] != workspaceScopeBots || profile.ID <= created.ID {
		t.Fatalf("profile event=%+v", profile)
	}

	other, err := server.store.CreateBot("other", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	_ = readWorkspaceSSEFrame(t, scanner) // bot creation for the second Bot
	if _, err = server.store.CreateGroup("workspace group", []string{bot.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	group := readWorkspaceSSEFrame(t, scanner)
	if group.Data["scope"] != workspaceScopeGroups {
		t.Fatalf("group event=%+v", group)
	}

	// The auth endpoint represents a credential file mutation while keeping
	// access tokens out of the durable event and SSE payload.
	if err = server.codex.SaveAccessOnlyCredential("test-access-token", "test-account", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodDelete, httpServer.URL+"/api/auth/codex", nil)
	if authResp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else {
		authResp.Body.Close()
		if authResp.StatusCode != http.StatusOK {
			t.Fatalf("disconnect status=%d", authResp.StatusCode)
		}
	}
	config := readWorkspaceSSEFrame(t, scanner)
	if config.Data["scope"] != workspaceScopeConfig {
		t.Fatalf("config event=%+v", config)
	}

	cancel()
	resp.Body.Close()

	// Reconnect with the last delivered cursor, then prove the next event is
	// replayable from that cursor.
	resp, reconnectCancel := openWorkspaceEvents(t, httpServer.URL, config.ID)
	defer reconnectCancel()
	defer resp.Body.Close()
	if _, err = server.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
		t.Fatal(err)
	}
	replayed := readWorkspaceSSEFrame(t, bufio.NewScanner(resp.Body))
	if replayed.ID <= config.ID || replayed.Data["scope"] != workspaceScopeConfig {
		t.Fatalf("reconnected event=%+v", replayed)
	}

}

func TestWorkspaceEventsHighCursorReplaysAfterDatabaseRestore(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err = server.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	resp, cancel := openWorkspaceEvents(t, httpServer.URL, 100000)
	defer cancel()
	defer resp.Body.Close()
	frame := readWorkspaceSSEFrame(t, bufio.NewScanner(resp.Body))
	if frame.ID != 1 || frame.Data["scope"] != workspaceScopeConfig {
		t.Fatalf("high cursor replay=%+v", frame)
	}
}

func TestWorkspaceEventMutationRollsBackWithState(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err = server.store.db.Exec(`CREATE TRIGGER fail_workspace_bot_event BEFORE INSERT ON workspace_events WHEN NEW.scope='bots' BEGIN SELECT RAISE(ABORT,'workspace event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = server.store.CreateBot("should roll back", "private", "model"); err == nil {
		t.Fatal("CreateBot unexpectedly succeeded")
	}
	var count int
	if err = server.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE name=?`, "should roll back").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("bot state survived failed workspace event: %d", count)
	}
}

func TestWorkspaceEventDoesNotRepeatForUnchangedBotProfile(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("stable", "instructions", "model")
	if err != nil {
		t.Fatal(err)
	}
	cursor := server.store.workspaceEventCursor()
	if _, err = server.store.UpdateBot(bot.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := server.store.workspaceEventCursor(); got != cursor {
		t.Fatalf("unchanged profile advanced workspace cursor from %d to %d", cursor, got)
	}
}
