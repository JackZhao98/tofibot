package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in local browser fixture. No provider, credential, real computer or
// production data is loaded; a deterministic engine requests a private input.
type secretAcceptanceEngine struct{}

func (secretAcceptanceEngine) Run(ctx context.Context, req Request) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for _, tool := range req.Tools {
		if tool.Name == "request_secret_input" {
			_, err := tool.Execute(ctx, json.RawMessage(`{"label":"测试 API Key","purpose":"仅用于独立验收：确认内容通过私密输入提交，不写入聊天。请输入任意合成测试值。"}`))
			if err != nil {
				return Result{}, err
			}
			return Result{Content: "已收到私密输入。原文没有写入聊天；本次合成验收未发送给外部服务。"}, nil
		}
	}
	return Result{}, errors.New("private input tool missing")
}
func TestSecretBrowserAcceptanceFixture(t *testing.T) {
	manifest := os.Getenv("TOFI_SECRET_UI_ACCEPTANCE_MANIFEST")
	if manifest == "" {
		t.Skip("opt-in browser fixture")
	}
	ui, err := filepath.Abs("../../ui/dist")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{Environment: "acceptance", DataDir: t.TempDir(), UIDir: ui, Engine: secretAcceptanceEngine{}, Provider: "acceptance", DefaultModel: "codex-gpt-5.6-luna", ComputerSocket: "/tmp/tofi-secret-synthetic.sock"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	fake, _ := secretTestServer(t)
	server.microVM = fake.microVM
	bot, err := server.store.CreateBot("私密输入验收", "Only synthetic private input is supported in this fixture.", "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	data, _ := json.Marshal(map[string]any{"base_url": httpServer.URL, "bot_id": bot.ID, "conversation_id": bot.DMConversationID, "expires_in_seconds": 900, "instructions": "Send any message to request a synthetic private input. No provider or actual computer is connected."})
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Independent private-input browser fixture ready: %s", httpServer.URL)
	<-time.After(15 * time.Minute)
}
