package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestComputerInstallationGuideLoadsOnDemand(t *testing.T) {
	// This tool requires no server or VM: it must remain read-only documentation.
	tool := computerHelpTool()
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"topic":"installation"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"apt-get download and dpkg-deb -x", "dependencies and a wrapper", "does not run maintainer scripts or install services", "remove only installation-owned files", "verify a real operation", "trusted user-level", "Do not expose credentials"} {
		if !strings.Contains(got, required) {
			t.Errorf("missing installation contract %q", required)
		}
	}
	if completionReviewTool(tool.Name) {
		t.Fatal("reading help counted as substantive work")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"topic":"unknown"}`)); err == nil {
		t.Fatal("unknown guide accepted")
	}
	prompt := syntheticComputerPrompt(t, "guide-bot")
	if strings.Contains(prompt, "apt-get download") || !strings.Contains(prompt, "installation before software changes") {
		t.Fatal("installation recipe is not deferred behind its router")
	}
}
