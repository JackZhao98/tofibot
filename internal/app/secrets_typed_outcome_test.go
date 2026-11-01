package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestSecretAndSSHKeepTypedGuestOutcomes(t *testing.T) {
	body := `{"ok":false,"error":"busy","outcome":{"version":1,"status":"validation_error","code":"action_rejected","execution_certainty":"not_executed","message":"refused by the guest","next_action":"repair_arguments"}}`
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-typed.sock", Client: &http.Client{Transport: secretTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Status: "400 Bad Request", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerOwners: map[string]string{}}
	run := Run{ID: "run", BotID: "bot"}

	_, err = s.applySecret(context.Background(), run, "env", "API_KEY", "synthetic", "")
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "action_rejected" || o.Certainty != "not_executed" {
		t.Fatalf("applySecret lost the typed outcome: %v", err)
	}
	if strings.Contains(err.Error(), "synthetic") {
		t.Fatal("secret value in error")
	}
	_, err = s.computerSSHKeys(context.Background(), run, sshKeyOperation{Action: "list"})
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "action_rejected" {
		t.Fatalf("ssh keys lost the typed outcome: %v", err)
	}
}
