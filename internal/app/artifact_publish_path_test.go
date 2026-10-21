package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A publish_file call with a wrong source path fails definitely (nothing was
// read, nothing changed), and the next call with a good path publishes the
// good file: a failed attempt leaves no record the next call can replay.
func TestPublishFileBadPathThenGoodPathPublishesTheGoodFile(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "build the export", "pf-1", nil, nil)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "export.zip"), []byte("zip bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := s.publishAttachmentTools(conv, run)[0]

	garbled := filepath.Join(root, "bots", bot.ID+"-"+bot.ID, "export.zip")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":`+jsonQuote(garbled)+`,"name":"export.zip"}`))
	o, typed := tooloutcome.FromError(err)
	if !typed || o.Code != tooloutcome.FileNotFound || o.Status != tooloutcome.Permanent || o.Certainty != "not_executed" || !strings.Contains(o.Message, "No file at "+garbled) {
		t.Fatalf("missing source must be a definite, named refusal: err=%v typed=%v outcome=%+v", err, typed, o)
	}

	// Relative path, the same file the shell would have created in its cwd.
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"export.zip","name":"export.zip","request_key":"v2"}`))
	if err != nil {
		t.Fatalf("good path after a bad one: %v", err)
	}
	var res struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || res.Name != "export.zip" || res.Size != int64(len("zip bytes")) {
		t.Fatalf("published=%s", out)
	}
}

func jsonQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
