package app

import "testing"

// Stored rows from before the say tool was renamed keep counting wherever
// send_message counts.
func TestLegacySayToolNameStillCounts(t *testing.T) {
	for _, name := range []string{"send_message", "say"} {
		if !isSendMessageTool(name) {
			t.Errorf("%q not read as send_message", name)
		}
		if completionReviewTool(name) {
			t.Errorf("%q arms the completion review", name)
		}
	}
	if isSendMessageTool("send_chat_message") {
		t.Error("send_chat_message is a different tool")
	}
	if sendMessageToolName != "send_message" {
		t.Errorf("tool name = %q", sendMessageToolName)
	}
}
