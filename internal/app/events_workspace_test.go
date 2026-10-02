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

type conversationWorkspaceFrame struct {
	ID    *int64
	Event string
	Data  map[string]any
}

func readConversationWorkspaceFrame(t *testing.T, scanner *bufio.Scanner) conversationWorkspaceFrame {
	t.Helper()
	frame := conversationWorkspaceFrame{}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			value, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("invalid conversation event id %q: %v", line, err)
			}
			frame.ID = &value
		case strings.HasPrefix(line, "event: "):
			frame.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame.Data); err != nil {
				t.Fatalf("invalid conversation event data: %v", err)
			}
		case line == "":
			if frame.Event != "" {
				return frame
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("reading conversation event: %v", err)
	}
	t.Fatal("conversation event stream ended before a complete frame")
	return conversationWorkspaceFrame{}
}

func openConversationWorkspaceEvents(t *testing.T, base, conversationID, query string, headers map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/conversations/%s/events?%s", base, conversationID, query), nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		resp.Body.Close()
		cancel()
		t.Fatalf("conversation Content-Type=%q", got)
	}
	return resp, cancel
}

func TestConversationEventsMultiplexWorkspaceWithIndependentCursors(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("combined events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceBefore, err := server.store.WorkspaceEvent(workspaceScopeBots)
	if err != nil {
		t.Fatal(err)
	}
	conversationFirst, err := server.store.Event(conversation.ID, "message", map[string]string{"value": "first"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceFirst, err := server.store.WorkspaceEvent(workspaceScopeConfig)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	resp, cancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after=0&workspace_after="+strconv.FormatInt(workspaceBefore, 10), nil)
	scanner := bufio.NewScanner(resp.Body)
	first := readConversationWorkspaceFrame(t, scanner)
	if first.ID == nil || *first.ID != conversationFirst || first.Event != "message" {
		t.Fatalf("first conversation frame=%+v", first)
	}
	workspace := readConversationWorkspaceFrame(t, scanner)
	if workspace.ID != nil || workspace.Event != "workspace" || workspace.Data["scope"] != workspaceScopeConfig || workspace.Data["revision"] != float64(workspaceFirst) {
		t.Fatalf("first workspace frame=%+v", workspace)
	}

	conversationSecond, err := server.store.Event(conversation.ID, "message", map[string]string{"value": "second"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceSecond, err := server.store.WorkspaceEvent(workspaceScopeGroups)
	if err != nil {
		t.Fatal(err)
	}
	second := readConversationWorkspaceFrame(t, scanner)
	if second.ID == nil || *second.ID != conversationSecond || second.Event != "message" {
		t.Fatalf("second conversation frame=%+v", second)
	}
	secondWorkspace := readConversationWorkspaceFrame(t, scanner)
	if secondWorkspace.ID != nil || secondWorkspace.Event != "workspace" || secondWorkspace.Data["revision"] != float64(workspaceSecond) {
		t.Fatalf("second workspace frame=%+v", secondWorkspace)
	}
	cancel()
	resp.Body.Close()

	// The conversation cursor can come from Last-Event-ID while the workspace
	// cursor remains an independent query parameter. Workspace frames have no
	// id line, so they cannot overwrite the conversation cursor on reconnect.
	resp, reconnectCancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after=0&workspace_after="+strconv.FormatInt(workspaceFirst, 10),
		map[string]string{"Last-Event-ID": strconv.FormatInt(conversationFirst, 10)})
	defer reconnectCancel()
	defer resp.Body.Close()
	reconnectedConversation := readConversationWorkspaceFrame(t, bufio.NewScanner(resp.Body))
	if reconnectedConversation.ID == nil || *reconnectedConversation.ID != conversationSecond {
		t.Fatalf("reconnected conversation frame=%+v", reconnectedConversation)
	}
	resp.Body.Close()
	reconnectCancel()
	resp, reconnectCancel = openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after="+strconv.FormatInt(conversationSecond, 10)+"&workspace_after="+strconv.FormatInt(workspaceFirst, 10), nil)
	defer reconnectCancel()
	defer resp.Body.Close()
	reconnectedWorkspace := readConversationWorkspaceFrame(t, bufio.NewScanner(resp.Body))
	if reconnectedWorkspace.ID != nil || reconnectedWorkspace.Event != "workspace" || reconnectedWorkspace.Data["revision"] != float64(workspaceSecond) {
		t.Fatalf("reconnected workspace frame=%+v", reconnectedWorkspace)
	}

	// A cursor above the restored workspace log must use the same safe reset
	// rule as the standalone workspace stream and replay from revision zero.
	allWorkspace, err := server.store.workspaceEvents(0)
	if err != nil || len(allWorkspace) == 0 {
		t.Fatalf("workspace replay fixture=%+v err=%v", allWorkspace, err)
	}
	resp, highCancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after="+strconv.FormatInt(conversationSecond, 10)+"&workspace_after="+strconv.FormatInt(server.store.workspaceEventCursor()+100, 10), nil)
	defer highCancel()
	defer resp.Body.Close()
	highScanner := bufio.NewScanner(resp.Body)
	highReset := readConversationWorkspaceFrame(t, highScanner)
	if highReset.ID != nil || highReset.Event != "workspace_reset" || highReset.Data["revision"] != float64(0) {
		t.Fatalf("high cursor reset=%+v", highReset)
	}
	highReplay := readConversationWorkspaceFrame(t, highScanner)
	if highReplay.ID != nil || highReplay.Event != "workspace" || highReplay.Data["revision"] != float64(allWorkspace[0].ID) {
		t.Fatalf("high cursor replay=%+v want first revision %d", highReplay, allWorkspace[0].ID)
	}
}

func TestConversationEventsWithoutWorkspaceCursorRemainUnchanged(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("legacy events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	conversationEvent, err := server.store.Event(conversation.ID, "message", map[string]string{"value": "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.store.WorkspaceEvent(workspaceScopeConfig); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	resp, cancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID, "after=0", nil)
	defer cancel()
	defer resp.Body.Close()
	frame := readConversationWorkspaceFrame(t, bufio.NewScanner(resp.Body))
	if frame.ID == nil || *frame.ID != conversationEvent || frame.Event != "message" {
		t.Fatalf("legacy frame=%+v", frame)
	}
}

func TestConversationEventsRejectInvalidWorkspaceCursorAndStopOnCancel(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("cursor events", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	for _, value := range []string{"", "-1", "nope", "1&workspace_after=2"} {
		path := "after=0&workspace_after=" + value
		resp, requestErr := http.Get(fmt.Sprintf("%s/api/conversations/%s/events?%s", httpServer.URL, bot.DMConversationID, path))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("workspace_after=%q status=%d", value, resp.StatusCode)
		}
		resp.Body.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events?workspace_after=0", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.events(recorder, req, bot.DMConversationID)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("multiplexed events handler did not stop after context cancellation")
	}
}

func TestWorkspaceEventsHighCursorResetThenReplaysNewFirstEvent(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	resp, cancel := openWorkspaceEvents(t, httpServer.URL, 1)
	scanner := bufio.NewScanner(resp.Body)
	reset := readConversationWorkspaceFrame(t, scanner)
	if reset.ID != nil || reset.Event != "workspace_reset" || reset.Data["revision"] != float64(0) {
		t.Fatalf("workspace reset=%+v", reset)
	}
	first, err := server.store.WorkspaceEvent(workspaceScopeConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace := readConversationWorkspaceFrame(t, scanner)
	if workspace.ID == nil || *workspace.ID != first || workspace.Event != "workspace" || workspace.Data["revision"] != float64(first) {
		t.Fatalf("workspace after reset=%+v want revision %d", workspace, first)
	}
	cancel()
	resp.Body.Close()
}

func TestConversationWorkspaceResetDoesNotChangeConversationLastEventID(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("reset cursor", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	conversationEvent, err := server.store.Event(conversation.ID, "message", map[string]string{"value": "kept"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a restored database whose workspace log was reset while the
	// conversation event cursor remains durable.
	if _, err = server.store.db.Exec("DELETE FROM workspace_events"); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	resp, cancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after=0&workspace_after=1", nil)
	scanner := bufio.NewScanner(resp.Body)
	reset := readConversationWorkspaceFrame(t, scanner)
	if reset.ID != nil || reset.Event != "workspace_reset" {
		t.Fatalf("combined reset=%+v", reset)
	}
	conversationFrame := readConversationWorkspaceFrame(t, scanner)
	if conversationFrame.ID == nil || *conversationFrame.ID != conversationEvent || conversationFrame.Event != "message" {
		t.Fatalf("conversation after reset=%+v", conversationFrame)
	}
	workspaceEvent, err := server.store.WorkspaceEvent(workspaceScopeConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspaceFrame := readConversationWorkspaceFrame(t, scanner)
	if workspaceFrame.ID != nil || workspaceFrame.Event != "workspace" || workspaceFrame.Data["revision"] != float64(workspaceEvent) {
		t.Fatalf("combined workspace after reset=%+v", workspaceFrame)
	}
	cancel()
	resp.Body.Close()

	// Last-Event-ID still resumes the conversation independently of the
	// reset/workspace frames, which never carry an id line.
	workspaceEvent2, err := server.store.WorkspaceEvent(workspaceScopeGroups)
	if err != nil {
		t.Fatal(err)
	}
	resp, reconnectCancel := openConversationWorkspaceEvents(t, httpServer.URL, conversation.ID,
		"after=0&workspace_after="+strconv.FormatInt(workspaceEvent, 10),
		map[string]string{"Last-Event-ID": strconv.FormatInt(conversationEvent, 10)})
	defer reconnectCancel()
	defer resp.Body.Close()
	replayed := readConversationWorkspaceFrame(t, bufio.NewScanner(resp.Body))
	if replayed.ID != nil || replayed.Event != "workspace" || replayed.Data["revision"] != float64(workspaceEvent2) {
		t.Fatalf("replayed workspace=%+v", replayed)
	}
}
