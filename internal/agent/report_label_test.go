package agent

import (
	"strings"
	"testing"
)

func TestProgressReportWordingExplainsPurposeLabels(t *testing.T) {
	for name, text := range map[string]string{"prompt": ProgressReportPrompt(8), "reminder": progressReportReminder(8)} {
		if !strings.Contains(text, `purpose="status"`) {
			t.Fatalf("%s does not mention purpose=\"status\": %q", name, text)
		}
	}
	if !strings.Contains(ProgressReportPrompt(8), `purpose="answer"`) {
		t.Fatal("prompt must explain purpose=answer")
	}
}
