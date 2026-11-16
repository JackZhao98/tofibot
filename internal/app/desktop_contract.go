package app

// DesktopContract is the version of the interface between the Web UI build that
// the desktop app bundles and this server's /api. The desktop app compares it
// with the desktop_contract of its bundled ui-artifact.json at connect time and
// refuses to open a workspace on a mismatch. Bump it, and DesktopMinClient when
// needed, whenever the UI <-> server API changes incompatibly (see AGENTS.md,
// "Desktop UI artifact and version skew"). scripts/export-desktop-ui.cjs reads
// this constant, so the artifact and the server always agree.
const DesktopContract = 1

// DesktopMinClient is the oldest desktop app version (x.y.z) that may connect.
const DesktopMinClient = "0.2.1"

func desktopServerInfo() map[string]any {
	return map[string]any{"contract": DesktopContract, "min_client": DesktopMinClient}
}

func botBrowserServerInfo() map[string]any {
	return map[string]any{"version": 1, "upload": "websocket-json", "view": "ndjson-jpeg"}
}

// botBrowserFeatures names the result fields this server accepts beyond bot_browser v1. It is a key of its own: the
// desktop app keeps bot_browser only when it has exactly its three keys, so a new key there would cost older apps
// the browser. A Mac app sends a field only when its name is here (frames_unread: browser.read counts the page's frames
// whose content is not in its text).
func botBrowserFeatures() []string {
	return []string{"frames_unread"}
}
