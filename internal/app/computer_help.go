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

const computerBrowserGuide = `For an authorized browser research fallback, first check the VM's reported readiness. A ready VM does not prove Chrome is running. If desktop readiness is unknown/stopped, use computer_desktop with desktop.start before computer_browser with browser.snapshot, then visibly search/verify pages. Desktop startup is idempotent. A recent successful snapshot already proves the desktop is running; do not start it again. Do not repeatedly retry a failed snapshot. If the VM is not configured/unavailable or startup fails, report the precise blocker and evidence gap. Do not claim a search succeeded without page evidence. Never bypass a denied target/action or browser prohibition; alternate methods need independent authorization and their own approvals. Expired approval stops its action; verify uncertain writes before retrying or switching routes.
All Bots share one Chrome profile and graphical session. Use declared VM tools directly; list computers only for device/grant questions. Files, cookies and login state persist, but a desktop restart opens a fresh page rather than restoring tabs. Viewer disconnection only ends viewing. Active model runs hold desktop idle cleanup; viewer polling does not extend it.
The latest user instruction determines the task; unfinished history is not permission to resume it. If the user refers to this page, corrects a search/site or may have moved Chrome, inspect browser.snapshot and do not infer the current page from history. If uncertain, say so. Foreground a tab before inspecting it. Read page content with browser.read; use browser.snapshot for layout and click coordinates, then verify the resulting page. Searches and site pages may be opened directly with browser.navigate. Stop the shared desktop only when the user intends it.` + computerResearchGuidance

func computerHelpTool() Tool {
	return Tool{
		Name:        "computer_help",
		Description: "Read VM procedures: installation before software changes; browser for optional detail on shared-desktop startup, tabs and visible search. Instructions only, no VM actions.",
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
