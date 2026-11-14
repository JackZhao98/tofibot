package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func originForTest(t *testing.T, s *Server, r Run, id string) Run {
	t.Helper()
	if _, err := s.store.db.Exec(`UPDATE messages SET origin_computer_id=? WHERE id=?`, id, r.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	s.environmentRuns.Delete(r.ID)
	return r
}
func TestMessageOriginStoredOnlyForOwnPairedMac(t *testing.T) {
	s, m, _, r := browserFixture(t)
	other := computerTestServer(t)
	foreign := enrollMac(t, other, "Other account", "files.read")
	revoked := enrollMac(t, s, "Revoked", "files.read")
	_, _ = s.store.db.Exec(`UPDATE computers SET revoked_at=? WHERE id=?`, now(), revoked.id)
	for _, id := range []string{m.id, "missing", microVMComputerID, revoked.id, foreign.id, strings.Repeat("a", 129)} {
		conv, err := s.store.GetConversation(r.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"content": "synthetic origin", "client_message_id": "origin-" + id, "origin_computer_id": id})
		w := httptest.NewRecorder()
		s.postMessage(w, httptest.NewRequest("POST", "/api/conversations/"+r.ConversationID+"/messages", strings.NewReader(string(payload))), conv)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("send %q: %d %s", id, w.Code, w.Body.String())
		}
		var got, messageID string
		if err := s.store.db.QueryRow(`SELECT id,origin_computer_id FROM messages WHERE conversation_id=? AND client_message_id=?`, r.ConversationID, "origin-"+id).Scan(&messageID, &got); err != nil {
			t.Fatal(err)
		}
		want := ""
		if id == m.id {
			want = id
		}
		if got != want {
			t.Errorf("%q: got %q want %q", id, got, want)
		}
		_, _, _, dup, err := s.store.addUserRunsSteeringOrigin(r.ConversationID, "retry", "origin-"+id, []runSpec{{BotID: r.BotID}}, nil, "changed")
		if err != nil || !dup {
			t.Fatalf("retry %v %v", dup, err)
		}
		_ = s.store.db.QueryRow(`SELECT origin_computer_id FROM messages WHERE id=?`, messageID).Scan(&got)
		if got != want {
			t.Fatal("retry changed origin")
		}
	}
}
func TestRunOriginFollowsRootTrigger(t *testing.T) {
	s, m, _, r := browserFixture(t)
	r = originForTest(t, s, r, m.id)
	child := Run{ID: "child-origin", ParentRunID: r.ID}
	if got := s.runOrigin(child); got != m.id {
		t.Fatalf("child %q", got)
	}
	if got := s.runOrigin(Run{ID: "scheduled-origin", ParentRunID: r.ID, Kind: runKindSchedule}); got != "" {
		t.Fatal(got)
	}
	if got := s.runOrigin(Run{ID: "scheduled-task-origin", ParentRunID: r.ID, scheduleTask: &Message{}}); got != "" {
		t.Fatal(got)
	}
	_, _ = s.store.db.Exec(`UPDATE computers SET revoked_at=? WHERE id=?`, now(), m.id)
	if got := s.runOrigin(Run{ID: "after-revoke", ParentRunID: r.ID}); got != "" {
		t.Fatal(got)
	}
}
func TestEnvironmentFeaturesFromCapabilities(t *testing.T) {
	_, _, h, _ := browserFixture(t)
	row := ComputerRow{Kind: computer.RowKindPersonal, Capabilities: []string{"browser.open", "desktop.click", "screen.capture", "files.read", "sandbox.exec"}}
	for _, tc := range []struct {
		name         string
		online       bool
		modify       func()
		status, code string
	}{
		{"connected", true, func() {}, "connected", ""},
		{"offline", false, func() {}, "disconnected", "computer_offline"},
		{"off switch", true, func() { h.state.Settings.Enabled = false }, "disconnected", "browser_not_enabled"},
		{"unsupported", true, func() { h.supported = false }, "disconnected", "browser_client_unsupported"},
		{"transport", true, func() { h.supported = true; h.upload = nil }, "disconnected", "browser_transport_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.modify()
			f := environmentFeatures(row, tc.online, h)
			if f[0].Status != tc.status || f[0].Code != tc.code {
				t.Fatalf("%+v", f)
			}
			for _, v := range f[1:] {
				want := "connected"
				if !tc.online {
					want = "disconnected"
				}
				if v.Status != want {
					t.Fatal(v)
				}
			}
		})
	}
	for _, f := range environmentFeatures(ComputerRow{Kind: computer.RowKindPersonal}, true, nil) {
		if f.Status != "off" {
			t.Fatal(f)
		}
	}
	for _, online := range []bool{false, true} {
		for _, f := range environmentFeatures(ComputerRow{Kind: computer.RowKindVM}, online, nil) {
			want := "disconnected"
			if online {
				want = "connected"
			}
			if f.Status != want {
				t.Fatal(f)
			}
		}
	}
}
func TestEnvironmentOrderPerOriginAndSetting(t *testing.T) {
	s, m, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	second := enrollMac(t, s, "Second Mac", "files.read")
	for _, tc := range []struct {
		origin, key, value string
		want               []string
	}{
		{"", "environment_order_web", "bot_computer", []string{microVMComputerID, m.id, second.id}},
		{"", "environment_order_web", "mac", []string{m.id, second.id, microVMComputerID}},
		{second.id, "environment_order_app", "this_mac", []string{second.id, microVMComputerID, m.id}},
		{second.id, "environment_order_app", "bot_computer", []string{microVMComputerID, second.id, m.id}},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			_ = s.saveAutomationValue(tc.key, &tc.value)
			r = originForTest(t, s, r, tc.origin)
			order := s.environmentOrder(r)
			for i, id := range tc.want {
				if order[i].ID != id {
					t.Fatalf("%+v", order)
				}
			}
		})
	}
	s.microVM = nil
	if got := s.environmentOrder(r); len(got) != 2 {
		t.Fatal(got)
	}
	_, _ = s.store.db.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano), m.id)
	r = originForTest(t, s, r, "")
	if got := s.environmentOrder(r); got[0].ID != second.id {
		t.Fatal(got)
	}
}
func TestBrowserRouteOmittedID(t *testing.T) {
	s, m, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	if got := s.resolveBrowserComputer(r, ""); got != microVMComputerID {
		t.Fatal(got)
	}
	r = originForTest(t, s, r, m.id)
	if got := s.resolveBrowserComputer(r, ""); got != m.id {
		t.Fatal(got)
	}
	dispatch := func(_ context.Context, raw json.RawMessage) (string, error) { return string(raw), nil }
	out, err := s.routeBrowserCall(context.Background(), r, json.RawMessage(`{"action":"read","computer_id":"firecracker"}`), dispatch)
	if err != nil || !strings.Contains(out, "served_by") {
		t.Fatalf("%s %v", out, err)
	}
	if got := s.resolveBrowserComputer(r, ""); got != microVMComputerID {
		t.Fatal(got)
	}
}
func TestBrowserFailoverCodes(t *testing.T) {
	codes := []string{"computer_offline", "computer_lost", "device_revoked", "browser_transport_unavailable", "browser_client_unsupported", "browser_server_unsupported", "browser_not_enabled", "bot_not_allowed", "app_not_allowed", "computer_not_configured", "browser_login_required", "browser_action_unsupported", "user_stop", "user_takeover", "browser_control_lost", "browser_target_gone", "browser_session_changed", "browser_viewport_changed"}
	for _, code := range codes {
		for _, mode := range []string{"entry", "session", "unknown", "explicit", "off"} {
			t.Run(code+"/"+mode, func(t *testing.T) {
				s, m, _, r := browserFixture(t)
				s.microVM = newFakeComputer(microVMComputerID)
				r = originForTest(t, s, r, m.id)
				if mode == "off" {
					v := "false"
					_ = s.saveAutomationValue("environment_failover", &v)
				}
				calls := []string{}
				dispatch := func(_ context.Context, raw json.RawMessage) (string, error) {
					var in map[string]any
					_ = json.Unmarshal(raw, &in)
					id := in["computer_id"].(string)
					calls = append(calls, id)
					if id == m.id {
						certainty := "not_executed"
						if mode == "unknown" {
							certainty = "unknown"
						}
						return "", browserError(code, certainty)
					}
					if in["action"] != "browser.navigate" {
						t.Fatal(in)
					}
					return `{}`, nil
				}
				in := map[string]any{"action": "browser.open", "url": "https://example.test"}
				if mode == "session" {
					in["session_epoch"] = "session"
				}
				if mode == "explicit" {
					in["computer_id"] = m.id
				}
				raw, _ := json.Marshal(in)
				out, err := s.routeBrowserCall(context.Background(), r, raw, dispatch)
				want := automaticBrowserCodes[code] && mode == "entry"
				if want {
					if err != nil || len(calls) != 2 || !strings.Contains(out, `"failover"`) {
						t.Fatalf("%s %v %+v", out, err, calls)
					}
				} else {
					if err == nil || len(calls) != 1 {
						t.Fatalf("replayed: %s %v %+v", out, err, calls)
					}
				}
				if code == "bot_not_allowed" && mode != "unknown" {
					for _, c := range s.browserOrder(r) {
						if c.Computer.ID == m.id {
							t.Fatal("denied Mac remains")
						}
					}
				}
			})
		}
	}
}
func TestBrowserFailoverDesktopNextAndEffectReview(t *testing.T) {
	s, m, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	r = originForTest(t, s, r, m.id)
	_, _ = s.store.db.Exec(`UPDATE computers SET capabilities=? WHERE id=?`, `["browser.open","desktop.click","desktop.capture"]`, m.id)
	calls := 0
	dispatch := func(context.Context, json.RawMessage) (string, error) {
		calls++
		return "", browserError("browser_not_enabled", "not_executed")
	}
	_, err := s.routeBrowserCall(context.Background(), r, json.RawMessage(`{"action":"open","url":"https://example.test"}`), dispatch)
	o, _ := tooloutcome.FromError(err)
	b, _ := json.Marshal(o.Next)
	if calls != 1 || !strings.Contains(string(b), "computer_desktop") {
		t.Fatalf("%+v calls %d", o, calls)
	}
	// A second candidate's approval refusal remains a refusal, with the identity of that candidate.
	calls = 0
	_, _ = s.store.db.Exec(`UPDATE computers SET capabilities='["browser.open"]' WHERE id=?`, m.id)
	dispatch = func(context.Context, json.RawMessage) (string, error) {
		calls++
		if calls == 1 {
			return "", browserError("bot_not_allowed", "not_executed")
		}
		return "", tooloutcome.New(tooloutcome.NeedApproval, "approval_required", "not_executed", "Review required", "ask").Err()
	}
	_, err = s.routeBrowserCall(context.Background(), r, json.RawMessage(`{"action":"open","url":"https://example.test","effect":"submit"}`), dispatch)
	o, _ = tooloutcome.FromError(err)
	if calls != 2 || o.Status != tooloutcome.NeedApproval {
		t.Fatalf("%+v calls %d", o, calls)
	}
}
func TestServedByOnEveryBrowserResult(t *testing.T) {
	s, m, _, _ := browserFixture(t)
	for _, id := range []string{m.id, microVMComputerID} {
		for _, browser := range []bool{false, true} {
			for _, text := range []string{`{"data":{"image_url":"data:image/jpeg;base64,synthetic"}}`, `{"text":"hello"}`, "relay text"} {
				out, err := s.identifyResult(id, browser, text, nil, nil)
				if err != nil || !strings.Contains(out, "served_by") {
					t.Fatalf("%s %v", out, err)
				}
			}
			_, err := s.identifyResult(id, browser, "", browserError("user_stop", "not_executed"), nil)
			o, _ := tooloutcome.FromError(err)
			if o.ServedBy == nil || !strings.HasPrefix(o.Message, "served_by:") {
				t.Fatal(o)
			}
		}
	}
}
func TestBrowserEngineToolsOnlyWhenBotComputerFirst(t *testing.T) {
	s, m, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	v := "playwright"
	_ = s.saveAutomationValue(autoBrowserEngine, &v)
	for _, origin := range []string{"", m.id} {
		r = originForTest(t, s, r, origin)
		tools := s.microVMTools(r)
		has := false
		for _, tool := range tools {
			if strings.HasPrefix(tool.Name, "browser_") {
				has = true
			}
			if tool.Name == "computer_browser" || tool.Name == "computer_desktop" {
				n := utf8.RuneCountInString(tool.Description)
				t.Logf("origin=%q %s: %d characters", origin, tool.Name, n)
				if n >= 1024 {
					t.Fatal(n)
				}
			}
		}
		if has != (origin == "") {
			t.Fatalf("origin %q tools %v", origin, has)
		}
		prompt := s.microVMEnvironmentPrompt(context.Background(), r.BotID, r)
		if strings.Contains(prompt, "browser_ tools") != (origin == "") {
			t.Fatal(prompt)
		}
	}
	longest := strings.Join([]string{computerFallbackNote, browserRouteDescription, browserBotDescription, browserMacDescription, browserEngineDescription}, " ")
	if n := utf8.RuneCountInString(longest); n >= 1024 {
		t.Fatalf("longest browser description: %d", n)
	}
}
func TestRunContextEnvironmentBlock(t *testing.T) {
	s, m, _, r := browserFixture(t)
	for _, origin := range []string{"", m.id} {
		r = originForTest(t, s, r, origin)
		text := s.environmentsRunContext(r)
		if !strings.Contains(text, "Environments, in the order of this run:") || !strings.Contains(text, "browser: connected") || !strings.Contains(text, "served_by") {
			t.Fatal(text)
		}
		if strings.Contains(text, "This message came from the Web.") != (origin == "") {
			t.Fatal(text)
		}
	}
}
func TestDesktopOriginRouting(t *testing.T) {
	s, m, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	_, _ = s.store.db.Exec(`UPDATE computers SET capabilities='["desktop.click","desktop.capture"]' WHERE id=?`, m.id)
	r = originForTest(t, s, r, m.id)
	for _, action := range []string{"capture", "click", "start", "stop", "trace"} {
		raw := s.desktopRouteInput(r, "computer_desktop", json.RawMessage(`{"action":"`+action+`"}`))
		want := m.id
		if action == "start" || action == "stop" || action == "trace" {
			want = microVMComputerID
		}
		if got := s.desktopToolComputer("computer_desktop", raw); got != want {
			t.Fatalf("%s: %s", action, got)
		}
	}
}

func TestBrowserFailoverKeepsEffectReview(t *testing.T) {
	s, _, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	reviewer := &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return &provider.ChatResponse{Content: `{"decision":"confirm","reason":"synthetic"}`}, nil
	}}
	s.autoReviewProvider = reviewer
	calls := 0
	vm := Tool{Parameters: objectSchema(map[string]any{}, nil), Execute: func(context.Context, json.RawMessage) (string, error) {
		calls++
		return "", browserError("computer_not_configured", "not_executed")
	}}
	tool := s.browserTool(r, &vm)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"browser.navigate","url":"https://synthetic.invalid","effect":"submit"}`))
	if toolCode(err) != "user_confirmation_required" || calls != 1 {
		t.Fatalf("review skipped: %v calls %d", err, calls)
	}
	var jobs int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE run_id=?`, r.ID).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("dispatched before review")
	}
}

func TestEnvironmentToolDescriptionsLongestCombination(t *testing.T) {
	s, _, _, r := browserFixture(t)
	s.microVM = newFakeComputer(microVMComputerID)
	for _, engine := range []string{desktopEngineClassic, desktopEngineA11y} {
		_ = s.saveAutomationValue(autoDesktopEngine, &engine)
		for _, browser := range []string{browserEngineClassic, browserEnginePlaywright} {
			_ = s.saveAutomationValue(autoBrowserEngine, &browser)
			for _, tool := range s.microVMTools(r) {
				n := utf8.RuneCountInString(tool.Description)
				if n >= 1024 {
					t.Errorf("%s/%s %s: %d characters", engine, browser, tool.Name, n)
				}
				if tool.Name == "computer_browser" || tool.Name == "computer_desktop" {
					t.Logf("%s/%s %s: %d characters: %s", engine, browser, tool.Name, n, tool.Description)
				}
			}
		}
	}
}

type environmentStateComputer struct {
	*fakeComputer
	state string
}

func (c *environmentStateComputer) Info(context.Context) (computer.Info, error) {
	return computer.Info{State: c.state}, nil
}
func TestBrowserFailoverStoppedBotComputer(t *testing.T) {
	for _, state := range []string{"failed", "stopped"} {
		for _, enabled := range []string{"true", "false"} {
			t.Run(state+enabled, func(t *testing.T) {
				s, m, _, r := browserFixture(t)
				s.microVM = &environmentStateComputer{newFakeComputer(microVMComputerID), state}
				_ = s.saveAutomationValue("environment_failover", &enabled)
				calls := 0
				dispatch := func(_ context.Context, raw json.RawMessage) (string, error) {
					calls++
					var in map[string]any
					_ = json.Unmarshal(raw, &in)
					if in["computer_id"] != m.id || in["action"] != "browser.open" {
						t.Fatal(in)
					}
					return `{}`, nil
				}
				out, err := s.routeBrowserCall(context.Background(), r, json.RawMessage(`{"action":"navigate","url":"https://example.test"}`), dispatch)
				if enabled == "true" {
					if err != nil || calls != 1 || !strings.Contains(out, `"failover"`) {
						t.Fatalf("%s %v %d", out, err, calls)
					}
				} else {
					o, _ := tooloutcome.FromError(err)
					if calls != 0 || o.Code != "computer_not_configured" || o.Next == nil {
						t.Fatalf("%+v %d", o, calls)
					}
				}
			})
		}
	}
}

// An opening request takes the verb of the browser that serves it (run aa65be46's follow-up, 2026-11-13: the model sent
// browser.navigate to the Mac, which has no page to navigate, and was told the page no longer existed).
func TestBrowserEntryActionFollowsTheBrowser(t *testing.T) {
	for _, c := range []struct {
		name string
		mac  bool
		in   string
		want string
	}{
		{"mac navigate opens", true, `{"action":"browser.navigate","url":"https://example.test"}`, "browser.open"},
		{"mac bare navigate opens", true, `{"action":"navigate","url":"https://example.test"}`, "browser.open"},
		{"bot computer open navigates", false, `{"action":"browser.open","url":"https://example.test"}`, "browser.navigate"},
		{"a page in a session keeps its verb", true, `{"action":"browser.navigate","url":"https://example.test","session_epoch":"s","target_id":"t"}`, "browser.navigate"},
		{"no url keeps its verb", true, `{"action":"browser.read"}`, "browser.read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A fresh run each time: a served call fixes the run's browser.
			s, m, _, r := browserFixture(t)
			s.microVM = newFakeComputer(microVMComputerID)
			if c.mac {
				r = originForTest(t, s, r, m.id)
			}
			var got string
			dispatch := func(_ context.Context, raw json.RawMessage) (string, error) {
				var in map[string]any
				_ = json.Unmarshal(raw, &in)
				got, _ = in["action"].(string)
				return `{}`, nil
			}
			if _, err := s.routeBrowserCall(context.Background(), r, json.RawMessage(c.in), dispatch); err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("action %q, want %q", got, c.want)
			}
		})
	}
}

// A hibernated Bot computer wakes on its next call, so it keeps its place in the order (live check on rc.13: the Web order
// left it out and sent a Web message to the Mac).
func TestHibernatedBotComputerStaysFirstFromTheWeb(t *testing.T) {
	for _, state := range []string{"ready", computer.StateHibernating, computer.StateHibernated, computer.StateResuming} {
		t.Run(state, func(t *testing.T) {
			s, _, _, r := browserFixture(t)
			s.microVM = &environmentStateComputer{newFakeComputer(microVMComputerID), state}
			order := s.browserOrder(r)
			if len(order) == 0 || order[0].Computer.ID != microVMComputerID {
				t.Fatalf("%s: browser order %+v", state, order)
			}
		})
	}
}
