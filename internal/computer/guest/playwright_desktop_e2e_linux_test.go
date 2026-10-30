//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPlaywrightThroughTheGuestActionOnTheRealDesktop sends browser.mcp through the guest's action path, so the
// desktop (Xvfb, fluxbox, the image's Chrome with its profile) starts the way it does for a Bot, the relay attaches
// to that Chrome, and the classic browser.snapshot sees the same tab. It needs the computer image (run as uid 1000
// with the pinned package installed) and TOFI_PLAYWRIGHT_E2E=1.
func TestPlaywrightThroughTheGuestActionOnTheRealDesktop(t *testing.T) {
	if os.Getenv("TOFI_PLAYWRIGHT_E2E") != "1" {
		t.Skip("set TOFI_PLAYWRIGHT_E2E=1 inside the computer image")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		http.SetCookie(w, &http.Cookie{Name: "login", Value: "kept", Path: "/", MaxAge: 3600})
		fmt.Fprint(w, `<!doctype html><title>Shared tab</title><label>City <input id=c></label><button onclick="document.title='clicked '+document.getElementById('c').value">Go</button>`)
	}))
	defer srv.Close()
	root, err := os.MkdirTemp("", "tofi-pw-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	s, err := NewWithIdleTimeout(root, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	bot := "11111111-1111-4111-8111-111111111111"
	do := func(action, args string) map[string]any {
		t.Helper()
		begin := time.Now()
		out, err := s.action(ctx, ActionRequest{BotID: bot, RunID: "run-1", Action: action, Args: json.RawMessage(args), Source: ActionSourceModel})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		t.Logf("%s took %s", action, time.Since(begin).Round(time.Millisecond))
		m, _ := out.(map[string]any)
		return m
	}
	// No desktop yet: the relay starts it, as browser.navigate does.
	res := do("browser.mcp", fmt.Sprintf(`{"tool":"browser_navigate","arguments":{"url":%q}}`, srv.URL))
	text, _ := res["text"].(string)
	if !strings.Contains(text, "Shared tab") || !strings.Contains(text, "[ref=") {
		t.Fatalf("navigate result has no snapshot with refs: %v", res)
	}
	ref := strings.Split(strings.Split(text[strings.Index(text, `textbox "City"`):], "[ref=")[1], "]")[0]
	do("browser.mcp", fmt.Sprintf(`{"tool":"browser_type","arguments":{"target":%q,"element":"City","text":"Oslo"}}`, ref))
	snap := do("browser.mcp", `{"tool":"browser_snapshot","arguments":{}}`)
	goRef := strings.Split(strings.Split(snap["text"].(string)[strings.Index(snap["text"].(string), `button "Go"`):], "[ref=")[1], "]")[0]
	do("browser.mcp", fmt.Sprintf(`{"tool":"browser_click","arguments":{"target":%q,"element":"Go"}}`, goRef))
	// The classic action sees the tab the relay changed: one Chrome, one profile.
	pages := do("browser.snapshot", `{}`)
	b, _ := json.Marshal(pages)
	if !strings.Contains(string(b), "clicked Oslo") {
		t.Fatalf("classic snapshot does not see the relay's work in the shared Chrome: %s", b)
	}
	// Stopping the desktop ends the relay's server; the next call starts a new one on the new Chrome.
	do("desktop.stop", `{}`)
	res = do("browser.mcp", fmt.Sprintf(`{"tool":"browser_navigate","arguments":{"url":%q}}`, srv.URL))
	if !strings.Contains(res["text"].(string), "Shared tab") {
		t.Fatalf("after a desktop restart: %v", res)
	}
	do("desktop.stop", `{}`)
}
