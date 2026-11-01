package mcprunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGogSendPassesCopiesAndThread(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls.log")
	r := newFakeGogRunner(t, map[string]string{"TOFI_FAKE_GOG_LOG": log})
	setGogAccount(t, r, "read-send", "gmail")
	request := GogSendRequest{To: "a@example.com", Cc: "c@example.com", Bcc: "b@example.com", ThreadID: "18c0ffee", ReplyToMessageID: "18c0ffef", Subject: "Re: plan", Body: "private draft body"}
	if _, err := r.GogSend(context.Background(), "gog", request); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	line := ""
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "gmail send") {
			line = l
		}
	}
	for _, want := range []string{"--cc c@example.com", "--bcc b@example.com", "--thread-id 18c0ffee", "--reply-to-message-id 18c0ffef"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
	for _, bad := range []GogSendRequest{
		{To: "a@example.com", Cc: "c@example.com\nBcc: x@example.com", Subject: "S", Body: "B"},
		{To: "a@example.com", Bcc: "Name <b@example.com>", Subject: "S", Body: "B"},
		{To: "a@example.com", ThreadID: "--attach", Subject: "S", Body: "B"},
		{To: "a@example.com", ReplyToMessageID: "a b", Subject: "S", Body: "B"},
	} {
		if _, err := r.GogSend(context.Background(), "gog", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
