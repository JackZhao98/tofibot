package agent

import (
	"strings"
	"testing"
)

func TestProgressReportWordingExplainsProgressTag(t *testing.T) {
	for name, text := range map[string]string{"prompt": ProgressReportPrompt(8), "reminder": progressReportReminder(8)} {
		if !strings.Contains(text, "<progress>") || strings.Contains(text, "purpose") {
			t.Fatalf("%s must explain the <progress> tag and not the old purpose label: %q", name, text)
		}
	}
	if !strings.Contains(ProgressReportPrompt(8), "must not be inside <progress>") {
		t.Fatal("prompt must say answers stay outside the tag")
	}
}
