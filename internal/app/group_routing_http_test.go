package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestHTTPUnaddressedGroupMessageStartsRealMemberWithoutTriage(t *testing.T) {
	e := collaborationCaptureEngine{requests: make(chan runtime.Request, 2)}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("alpha", "", "alpha-model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("bravo", "", "bravo-model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.store.CreateGroup("routing", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{a.ID, b.ID}
	body, _ := json.Marshal(map[string]string{"content": "please start", "client_message_id": "http-routing-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+group.ID+"/messages", bytes.NewReader(body))
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	select {
	case executed := <-e.requests:
		if executed.BotID != a.ID && executed.BotID != b.ID {
			t.Fatalf("selected non-member bot=%s", executed.BotID)
		}
		want[0] = executed.BotID
	case <-time.After(2 * time.Second):
		t.Fatal("group run was not executed")
	}
	runs, err := s.store.Runs(group.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if runs[0].Kind != runKindGroupChat || runs[0].BotID != want[0] {
		t.Fatalf("run=%+v want ordinary real-member run", runs[0])
	}
	waitRun(t, s, runs[0].ID, "done")
	messages, _, err := s.store.Messages(group.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var assistant *Message
	for i := range messages {
		if messages[i].RunID == runs[0].ID && messages[i].Role == "assistant" {
			assistant = &messages[i]
			break
		}
	}
	if assistant == nil || assistant.SenderBotID != want[0] || assistant.Content != "done" {
		t.Fatalf("assistant=%+v want sender=%s content=done", assistant, want[0])
	}
}

func postGroupHTTP(t *testing.T, s *Server, conv, content, client string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "client_message_id": client})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+conv+"/messages", bytes.NewReader(body))
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

// A token that names no member is ordinary text, and a CJK member name may be
// followed directly by text.
func TestHTTPNonMemberMentionIsPlainTextAndCJKNameMatches(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("小明", "", "m")
	b, _ := s.store.CreateBot("bravo", "", "m")
	group, _ := s.store.CreateGroup("routing", []string{a.ID, b.ID})
	for i, text := range []string{"看看 @elonmusk 今天发了什么", "how does @media work", "@所有人 大家好"} {
		if res := postGroupHTTP(t, s, group.ID, text, "plain-"+string(rune('a'+i))); res.Code != http.StatusAccepted {
			t.Fatalf("%q status=%d body=%s", text, res.Code, res.Body.String())
		}
	}
	res := postGroupHTTP(t, s, group.ID, "@小明帮我查一下明天的天气", "cjk-1")
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var out struct{ Run Run }
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || out.Run.BotID != a.ID || out.Run.Kind == runKindGroupChat {
		t.Fatalf("run=%+v err=%v: want a direct run for the named member", out.Run, err)
	}
}
