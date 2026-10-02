package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkExecutionHistoryHTTPIsBoundedAndReadOnly(t *testing.T) {
	store, _, item := executionFixture(t)
	first, _, err := store.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.SetRunStatus(first.ID, "failed", "first attempt could not finish"); !ok || err != nil {
		t.Fatal(err)
	}
	second, _, err := store.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	finishWorkExecution(t, store, second, "Public result for review")
	server := &Server{store: store}
	counts := func() [3]int {
		var out [3]int
		if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM runs),(SELECT COUNT(*) FROM messages),(SELECT COUNT(*) FROM events)`).Scan(&out[0], &out[1], &out[2]); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := counts()
	request := func(method, id, query string) *httptest.ResponseRecorder {
		t.Helper()
		path := "work-items/" + id + "/executions"
		w := httptest.NewRecorder()
		if !server.routeWorkItems(w, httptest.NewRequest(method, "/api/"+path+query, nil), path) {
			t.Fatal("execution history route was not handled")
		}
		return w
	}
	for _, query := range []string{"", "?limit=1"} {
		w := request(http.MethodGet, item.ID, query)
		var body struct {
			Executions []WorkItemExecution `json:"executions"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("history response: %d %s", w.Code, w.Body.String())
		}
		want := 2
		if query != "" {
			want = 1
		}
		if len(body.Executions) != want || body.Executions[0].RootRunID != second.ID || body.Executions[0].Result != "Public result for review" || body.Executions[0].Active {
			t.Fatalf("history projection: %+v", body.Executions)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=101", "?limit=bad", "?limit=", "?limit=1&limit=2"} {
		if got := request(http.MethodGet, item.ID, query); got.Code != 400 {
			t.Fatalf("invalid history limit accepted: %s %d", query, got.Code)
		}
	}
	if got := request(http.MethodPost, item.ID, ""); got.Code != 405 {
		t.Fatal("history route allowed a mutation method")
	}
	if got := request(http.MethodGet, "missing", ""); got.Code != 404 {
		t.Fatal("missing work item did not return 404")
	}
	if after := counts(); after != before {
		t.Fatalf("history reads created work or notifications: before=%v after=%v", before, after)
	}
}

func TestWorkExecutionHistoryToolKeepsConversationScope(t *testing.T) {
	store, bot, item := executionFixture(t)
	conversation, err := store.GetConversation(item.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	var tool Tool
	for _, candidate := range server.workItemTools(conversation, Run{BotID: bot.ID}) {
		if candidate.Name == "list_work_item_executions" {
			tool = candidate
		}
	}
	if tool.Execute == nil {
		t.Fatal("history read tool missing")
	}
	input, _ := json.Marshal(map[string]any{"work_item_id": item.ID})
	result, err := tool.Execute(context.Background(), input)
	if err != nil || result != `{"executions":[]}` {
		t.Fatalf("empty history: %s %v", result, err)
	}
	otherBot, err := store.CreateBot("another conversation", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	other := workItemCreate(t, store, otherBot.DMConversationID, otherBot.ID, "task", "Not current scope")
	foreign, _ := json.Marshal(map[string]string{"work_item_id": other.ID})
	if _, err = tool.Execute(context.Background(), foreign); !errors.Is(err, ErrWorkItemScope) {
		t.Fatalf("cross-conversation history was not rejected: %v", err)
	}
	for _, raw := range []string{`{}`, `{"work_item_id":"` + item.ID + `","limit":0}`, `{"work_item_id":"` + item.ID + `","limit":101}`, `{"work_item_id":"` + item.ID + `","limit":"20"}`, `{"work_item_id":"` + item.ID + `","scope":"all"}`} {
		if _, err = tool.Execute(context.Background(), []byte(raw)); err == nil {
			t.Fatal("invalid history tool arguments accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = tool.Execute(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled tool executed")
	}
	if !strings.Contains(tool.Description, "does not start, retry or resume") {
		t.Fatal("history tool misrepresents its effect")
	}
}

func TestWorkExecutionHistoryActiveAttemptHasNoFinalReceipt(t *testing.T) {
	store, _, item := executionFixture(t)
	run, _, err := store.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,run_id,content,created_at) VALUES(?,?,2,'assistant',?,'Still working, not a final receipt',?)`, newID(), item.ConversationID, run.ID, now()); err != nil {
		t.Fatal(err)
	}
	history, err := store.ListWorkItemExecutions(item.ID, 1)
	if err != nil || len(history) != 1 || !history[0].Active || history[0].Result != "" || history[0].ResultMessageID != "" {
		t.Fatalf("active attempt was treated as a final result: %+v %v", history, err)
	}
}
