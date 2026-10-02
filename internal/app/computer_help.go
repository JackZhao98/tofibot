package app

import (
	"context"
	"encoding/json"
	"errors"
)

// Operational recipes are read on demand; the always-present environment
// policy retains the shared-VM boundary and routes installation work here.
const computerInstallationGuide = `Installation in the shared user VM:
Inspect the existing command and available runtime before changing it. Use trusted user-level packages (npm, Go, Cargo, or a persistent Python venv when available). Persistent executables belong in /workspace/home/.local/bin or /workspace/shared/bin and affect all Bots. Preserve existing tools, remove only installation-owned files, and verify a real operation before claiming success.
The system image is read-only: no sudo or system apt install. If needed, apt-get download and dpkg-deb -x can extract repository packages into user space; include dependencies and a wrapper. This does not run maintainer scripts or install services. Load project .env files explicitly. Do not expose credentials.`

const computerBrowserGuide = `All Bots share one Chrome profile and graphical session. Use declared VM tools directly; list computers only for device/grant questions. Files, cookies and login state persist, but a desktop restart opens a fresh page rather than restoring tabs. Viewer disconnection only ends viewing. Active model runs hold desktop idle cleanup; viewer polling does not extend it.
The latest user instruction determines the task; unfinished history is not permission to resume it. If the user refers to this page, corrects a search/site or may have moved Chrome, inspect browser.snapshot and do not infer the current page from history. If uncertain, say so. Foreground a tab before inspecting it. Inspect screenshot, interact visibly, then verify the resulting page. Scroll to see more; do not substitute hidden DOM, shell fetches or offscreen/full-page captures for visible evidence. Do not replace requested page interactions with guessed URLs; explicit URL requests may use browser.navigate. Stop the shared desktop only when the user intends it.` + computerResearchGuidance

func computerHelpTool() Tool {
	return Tool{
		Name:        "computer_help",
		Description: "Read VM procedures: browser before graphical work; installation before software changes. Instructions only, no VM actions.",
		Parameters:  objectSchema(map[string]any{"topic": map[string]any{"type": "string", "enum": []string{"browser", "installation"}}}, []string{"topic"}),
		Execute: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				Topic string `json:"topic"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			switch in.Topic {
			case "browser":
				return computerBrowserGuide, nil
			case "installation":
				return computerInstallationGuide, nil
			default:
				return "", errors.New("unsupported computer help topic")
			}
		},
	}
}
