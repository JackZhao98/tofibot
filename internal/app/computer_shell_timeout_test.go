package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestShellTimeoutIsTransientAndDoesNotFence(t *testing.T) {
	old := computerDispatchTimeoutFor
	computerDispatchTimeoutFor = func(string, json.RawMessage) time.Duration { return 60 * time.Millisecond }
	t.Cleanup(func() { computerDispatchTimeoutFor = old })
	s, _ := hungComputerServer(t, func() computer.GuestHealth { return computer.GuestHealth{State: "ready", Guest: "ok"} })
	r := Run{ID: "run-shell-timeout", BotID: "bot-shell-timeout"}
	_, err := s.microVMActionFromSource(context.Background(), r, "shell.exec", json.RawMessage(`{"command":"sleep 500"}`), "model")
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != runtime.ToolTimeoutCode || o.Status != tooloutcome.Transient || o.NextAction != "verify_effect" {
		t.Fatalf("shell timeout = %#v, %v", o, err)
	}
}
