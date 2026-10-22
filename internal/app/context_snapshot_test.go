package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// legacyAssembleRunSystem is assembleRunSystem exactly as it was before
// segments existed (main at 8ff3a44). The prompt must stay byte for byte equal.
func legacyAssembleRunSystem(parts runSystemPrompt) (string, error) {
	discoveryPolicy, directory := extensions.DiscoveryInstructionParts(parts.Extensions)
	fixed := []string{parts.Core, toolEvidencePolicy, parts.Durable, parts.Computer, parts.Capabilities, discoveryPolicy}
	required := strings.Join(fixed, "\n")
	const botStart = "\nBot instructions:\n"
	const botEnd = "\nEnd of Bot instructions.\n"
	remaining := maxSystemRunes - runtime.SystemPromptOverheadRunes() - len([]rune(required)) - len([]rune(voiceGuidance)) - 1
	if parts.BotInstructions != "" {
		remaining -= len([]rune(botStart + botEnd))
	}
	if remaining < 0 {
		return "", fmt.Errorf("fixed system policies exceed the %d-rune context budget", maxSystemRunes)
	}
	botSize, dirSize := len([]rune(parts.BotInstructions)), len([]rune(directory))
	botBudget := remaining
	if dirSize > 0 {
		botBudget = remaining * 3 / 4
		if dirSize < remaining-botBudget {
			botBudget = remaining - dirSize
		}
		reserve := dirSize
		if reserve > 256 {
			reserve = 256
		}
		if botBudget > remaining-reserve {
			botBudget = remaining - reserve
		}
	}
	if botSize < botBudget {
		botBudget = botSize
	}
	if botBudget < 0 {
		botBudget = 0
	}
	botText := parts.BotInstructions
	if botSize > botBudget {
		const notice = "\n[Bot instructions truncated to fit context.]"
		if botBudget >= len([]rune(notice)) {
			botText = trimRunes(botText, botBudget-len([]rune(notice))) + notice
		} else {
			botText = trimRunes(botText, botBudget)
		}
	}
	dirText := extensions.BoundCapabilityDirectory(directory, remaining-len([]rune(botText)))
	if parts.BotInstructions != "" {
		required += botStart + botText + botEnd
	}
	// How to talk sits right after the persona, the last thing the model reads.
	return required + voiceGuidance + "\n" + dirText, nil
}

func TestAssembleRunSystemSegmentsKeepPromptBytes(t *testing.T) {
	cases := []runSystemPrompt{
		{Core: "core"},
		{Core: "core", BotInstructions: "persona"},
		{Core: "core", BotInstructions: strings.Repeat("persona ", 5000), Durable: "durable", Computer: "computer", Capabilities: "caps"},
		{Core: "", BotInstructions: "persona", Computer: "computer"},
		{Core: "core", Extensions: "ext", BotInstructions: strings.Repeat("长", 20000)},
		{Core: strings.Repeat("x", maxSystemRunes*2)},
	}
	for i, in := range cases {
		want, wantErr := legacyAssembleRunSystem(in)
		segs, err := assembleRunSystemSegments(in)
		if (err != nil) != (wantErr != nil) {
			t.Fatalf("case %d: err=%v want=%v", i, err, wantErr)
		}
		if wantErr != nil {
			continue
		}
		if got := joinSystemSegments(segs); got != want {
			t.Fatalf("case %d: segments differ from the legacy prompt (%d vs %d runes)", i, len([]rune(got)), len([]rune(want)))
		}
		if got, _ := assembleRunSystem(in); got != want {
			t.Fatalf("case %d: assembleRunSystem changed", i)
		}
	}
}

func contextSnapshotRun(t *testing.T) (*Server, *captureEngine, Bot, Conversation, Run) {
	t.Helper()
	e := &captureEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.store.CreateBot("Synthetic secretary", "Answer briefly.", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AddMemory(conv.ID, bot.ID, "The cat is called Pixel."); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		role, sender := "user", ""
		if i%2 == 1 {
			role, sender = "assistant", bot.ID
		}
		if _, _, err = s.store.AddMessage(conv.ID, role, sender, "", fmt.Sprintf("earlier line %d %s", i, strings.Repeat("filler ", 20)), fmt.Sprintf("seed-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "What is my cat called?", "snapshot-trigger")
	if err != nil {
		t.Fatal(err)
	}
	return s, e, bot, conv, run
}

func settleRuns(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.store.db.Exec(`UPDATE runs SET status='done' WHERE status IN ('queued','running')`); err != nil {
		t.Fatal(err)
	}
}

func TestRunCapturesContextSnapshotInOrderAndSumsToTheRequest(t *testing.T) {
	s, e, _, conv, run := contextSnapshotRun(t)
	s.execute(conv, run)
	snap, err := s.store.runContextSnapshot(run.ID)
	if err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
	// system, then the agent suffix, then tools, then messages.
	phase := 0
	var system, messages strings.Builder
	kinds := map[string]int{}
	history, request, gisted := 0, 0, 0
	for _, p := range snap.Parts {
		kinds[p.Kind]++
		if p.Chars != len([]rune(p.Text)) {
			t.Fatalf("%s: chars=%d text=%d", p.Kind, p.Chars, len([]rune(p.Text)))
		}
		switch {
		case p.Kind == ctxKindTools || p.Kind == ctxKindToolsOnDemand:
			if phase > 1 {
				t.Fatalf("tools after messages")
			}
			phase = 1
		case p.Role == "system":
			if phase != 0 {
				t.Fatalf("system part %s after phase %d", p.Kind, phase)
			}
			if p.Kind != ctxKindAgentReport {
				system.WriteString(p.Text)
			}
		default:
			phase = 2
			messages.WriteString(p.Text)
		}
		if p.Kind == ctxKindHistory {
			history++
			if p.Message == nil || p.Message.ID == "" {
				t.Fatalf("history part without message meta: %+v", p)
			}
			if p.Message.Request {
				request++
			}
			if p.Message.Gisted {
				gisted++
			}
		}
	}
	if system.String() != e.req.System {
		t.Fatalf("system parts do not add up to the system prompt sent")
	}
	var sent strings.Builder
	for _, m := range e.req.Messages {
		sent.WriteString(m.Content)
	}
	if messages.String() != sent.String() {
		t.Fatalf("message parts do not add up to the messages sent")
	}
	for _, kind := range []string{ctxKindSystemGuidance, ctxKindBotInstruction, ctxKindMemory, ctxKindHistory, ctxKindRunContext, ctxKindRequestNote, ctxKindTools} {
		if kinds[kind] == 0 {
			t.Fatalf("kind %s missing: %v", kind, kinds)
		}
	}
	if history != 10 || request != 1 {
		t.Fatalf("history=%d request=%d", history, request)
	}
	if gisted == 0 {
		t.Fatalf("older own replies should show as gisted")
	}
	toolCount := 0
	for _, tl := range e.req.Tools {
		if !tl.Deferred {
			toolCount++
		}
	}
	for _, p := range snap.Parts {
		if p.Kind == ctxKindTools && len(p.Items) != toolCount {
			t.Fatalf("tools part lists %d tools, request has %d", len(p.Items), toolCount)
		}
	}
	view := snap.View()
	if view.Totals.Chars == 0 || view.Totals.Tokens == 0 || len(view.Totals.ByKind) == 0 {
		t.Fatalf("totals empty: %+v", view.Totals)
	}
	if view.History.Considered != 10 || view.History.Skipped != 0 {
		t.Fatalf("history stats=%+v", view.History)
	}
}

func TestContextCaptureDoesNotChangePromptBytes(t *testing.T) {
	s, _, bot, conv, run := contextSnapshotRun(t)
	plain, plainSystem := s.buildContextParts(conv, run, bot)
	captured := run
	captured.ctxCapture = newContextCapture()
	got, gotSystem := s.buildContextParts(conv, captured, bot)
	if plainSystem != gotSystem || len(plain) != len(got) {
		t.Fatalf("capture changed the prompt: %d vs %d messages", len(plain), len(got))
	}
	for i := range plain {
		if strings.HasPrefix(plain[i].Content, "[run context]") {
			continue // minute-precision clock
		}
		if plain[i].Role != got[i].Role || plain[i].Content != got[i].Content {
			t.Fatalf("message %d changed", i)
		}
	}
}

func TestContextSnapshotReportsSkippedHistory(t *testing.T) {
	s, _, bot, conv, _ := contextSnapshotRun(t)
	settleRuns(t, s)
	for i := 0; i < 40; i++ {
		if _, _, err := s.store.AddMessage(conv.ID, "user", "", "", strings.Repeat("长文", 600), fmt.Sprintf("bulk-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "Anything left?", "skip-trigger")
	if err != nil {
		t.Fatal(err)
	}
	run.ctxCapture = newContextCapture()
	pm, _ := s.buildContextParts(conv, run, bot)
	snap := run.ctxCapture.snapshot(run, conv.ID, "model", "", nil, false)
	if snap.History.Skipped == 0 || snap.History.Included+snap.History.Skipped != snap.History.Considered {
		t.Fatalf("history stats=%+v", snap.History)
	}
	found := false
	for _, m := range pm {
		found = found || strings.HasPrefix(m.Content, "[earlier conversation]")
	}
	earlier := 0
	for _, p := range snap.Parts {
		if p.Kind == ctxKindEarlierNote {
			earlier++
		}
	}
	if !found || earlier != 1 {
		t.Fatalf("earlier note in prompt=%v in snapshot=%d", found, earlier)
	}
}

func TestContextCaptureKindsAreExtensibleAndUnknownIsLabelled(t *testing.T) {
	c := newContextCapture()
	c.markNote(2, "[run context]\nnow\n[/run context]", "")
	pm := []runtime.Message{
		{Role: "user", Content: "[brand new block]\nbody\n[/brand new block]"},
		{Role: "user", Content: "[run context]\nnow\n[/run context]\nsay line"},
		{Role: "user", Content: "the request"},
	}
	c.finishMessages(pm, 0, false)
	if len(c.messages) != 4 || c.messages[0].Kind != ctxKindOther || c.messages[0].Label != "[brand new block]" {
		t.Fatalf("parts=%+v", c.messages)
	}
	if c.messages[1].Kind != ctxKindRunContext || c.messages[2].Kind != ctxKindRequestNote || c.messages[2].Text != "\nsay line" {
		t.Fatalf("note not split: %+v", c.messages)
	}
	if joined := c.messages[1].Text + c.messages[2].Text; joined != pm[1].Content {
		t.Fatalf("note parts lose bytes: %q", joined)
	}
}

func TestRedactContextText(t *testing.T) {
	cases := map[string]string{
		"key sk-ant-api03-ABCDEFGHIJKLMNOP1234 here":                        "key [redacted key] here",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 x":                  "token [redacted key] x",
		"Authorization: Bearer abcdefghijklmnop12345678":                    "Authorization: [redacted bearer token]",
		"aws AKIAABCDEFGHIJKLMNOP end":                                      "aws [redacted key] end",
		"db postgres://app:hunter2hunter2@db.example/x":                     "db postgres://[redacted credentials]@db.example/x",
		`{"password": "correct-horse-battery"}`:                             `{"password": "[redacted value]"}`,
		"api_key=0123456789abcdef":                                          "api_key=[redacted value]",
		"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----":      "[redacted private key]",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop": "[redacted token]",
		"the secret_ref is secret_ref_ab12 and a plain note":                "the secret_ref is secret_ref_ab12 and a plain note",
		"password reset email sent":                                         "password reset email sent",
	}
	for in, want := range cases {
		if got, _ := redactContextText(in); got != want {
			t.Errorf("redact(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSnapshotRedactsStoredTextNotThePrompt(t *testing.T) {
	s, e, bot, conv, _ := contextSnapshotRun(t)
	settleRuns(t, s)
	const secret = "sk-ant-api03-SYNTHETICSYNTHETIC1234"
	if _, _, err := s.store.AddMessage(conv.ID, "user", "", "", "use this key "+secret+" please", "leaky"); err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "and now?", "leak-trigger")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(conv, run)
	sent := ""
	for _, m := range e.req.Messages {
		sent += m.Content
	}
	if !strings.Contains(sent, secret) {
		t.Fatal("test premise: the prompt carries the message verbatim")
	}
	snap, err := s.store.runContextSnapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), "SYNTHETICSYNTHETIC") {
		t.Fatal("secret stored in the snapshot")
	}
	redactions := 0
	for _, p := range snap.Parts {
		redactions += p.Redactions
	}
	if redactions == 0 {
		t.Fatal("redaction not counted")
	}
}

func TestBoundContextSnapshotDropsLargestTextsKeepsSizes(t *testing.T) {
	snap := ContextSnapshot{Parts: []ContextPart{
		{Kind: "a", Chars: 10, Tokens: 3, Text: strings.Repeat("a", 10)},
		{Kind: "big", Chars: 5000, Tokens: 1200, Text: strings.Repeat("b", 5000)},
		{Kind: "mid", Chars: 800, Tokens: 200, Text: strings.Repeat("c", 800)},
	}}
	boundContextSnapshot(&snap, 1500)
	if raw, _ := json.Marshal(snap); len(raw) > 1500 {
		t.Fatalf("still %d bytes", len(raw))
	}
	if !snap.Parts[1].TextDropped || snap.Parts[1].Text != "" || snap.Parts[1].Chars != 5000 || snap.Parts[1].Tokens != 1200 {
		t.Fatalf("largest part: %+v", snap.Parts[1])
	}
	if snap.Parts[0].TextDropped || snap.Parts[2].TextDropped {
		t.Fatalf("small parts were dropped: %+v", snap.Parts)
	}
	if view := snap.View(); view.Totals.DroppedTexts != 1 || view.Totals.Chars != 5810 {
		t.Fatalf("totals=%+v", view.Totals)
	}
}

func TestContextSnapshotRetentionKeepsNewestPerConversation(t *testing.T) {
	s, _, bot, conv, _ := contextSnapshotRun(t)
	settleRuns(t, s)
	var ids []string
	save := func(id, at, kind string) {
		t.Helper()
		snap := ContextSnapshot{Version: 1, RunID: id, ConversationID: conv.ID, BotID: bot.ID, CapturedAt: at, Parts: []ContextPart{{Kind: kind, Text: "t", Chars: 1}}}
		if err := s.store.saveRunContextSnapshot(snap); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < contextSnapshotKeep+5; i++ {
		_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, fmt.Sprintf("m%d", i), fmt.Sprintf("ret-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, run.ID)
		settleRuns(t, s)
		save(run.ID, fmt.Sprintf("2026-10-22T10:%02d:00.000000000Z", i), "x")
	}
	var n int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM run_context_snapshots WHERE conversation_id=?`, conv.ID).Scan(&n)
	if n != contextSnapshotKeep {
		t.Fatalf("kept %d, want %d", n, contextSnapshotKeep)
	}
	if _, err := s.store.runContextSnapshot(ids[0]); err == nil {
		t.Fatal("oldest snapshot survived")
	}
	newest := ids[len(ids)-1]
	if _, err := s.store.runContextSnapshot(newest); err != nil {
		t.Fatalf("newest snapshot lost: %v", err)
	}
	// A retried run keeps its first snapshot.
	save(newest, "2026-10-22T11:59:00.000000000Z", "y")
	again, _ := s.store.runContextSnapshot(newest)
	if again.Parts[0].Kind != "x" {
		t.Fatal("a second capture replaced the first")
	}
	// Deleting the run deletes its snapshot.
	if _, err := s.store.db.Exec(`DELETE FROM runs WHERE id=?`, newest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.runContextSnapshot(newest); err == nil {
		t.Fatal("snapshot outlived its run")
	}
}

func TestRouteRunContext(t *testing.T) {
	s, _, _, conv, run := contextSnapshotRun(t)
	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		if !s.routeRunContext(w, httptest.NewRequest(http.MethodGet, "/api/runs/"+id+"/context", nil), "runs/"+id+"/context") {
			t.Fatal("route did not claim the path")
		}
		return w
	}
	errCode := func(w *httptest.ResponseRecorder) string {
		var body struct{ Error struct{ Code string } }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body.Error.Code
	}
	// A run with no snapshot is a stable error, never an empty success.
	if w := get(run.ID); w.Code != http.StatusNotFound || errCode(w) != contextSnapshotUnavailable {
		t.Fatalf("missing snapshot: %d %s", w.Code, w.Body.String())
	}
	if w := get("run_does_not_exist"); w.Code != http.StatusNotFound || errCode(w) != "not_found" {
		t.Fatalf("unknown run: %d %s", w.Code, w.Body.String())
	}
	s.execute(conv, run)
	w := get(run.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d %s", w.Code, w.Body.String())
	}
	var view ContextSnapshotView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view.Parts) == 0 || view.Totals.Chars == 0 || view.RunID != run.ID {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	w = httptest.NewRecorder()
	s.routeRunContext(w, httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/context", nil), "runs/"+run.ID+"/context")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post=%d", w.Code)
	}
	if s.routeRunContext(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/runs/x", nil), "runs/x") {
		t.Fatal("claimed an unrelated path")
	}
}

// Only the signed-in owner reads a snapshot: the route sits behind the same
// session gate as the tool activity details.
func TestRunContextRequiresTheOwnerSession(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	bot, err := s.store.CreateBot("bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(bot.DMConversationID, bot.ID, "hi", "auth-trigger")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.saveRunContextSnapshot(ContextSnapshot{Version: 1, RunID: run.ID, ConversationID: bot.DMConversationID, BotID: bot.ID, CapturedAt: now(), Parts: []ContextPart{{Kind: "x", Text: "t", Chars: 1}}}); err != nil {
		t.Fatal(err)
	}
	path := "/api/runs/" + run.ID + "/context"
	if w := ownerCall(s, "GET", path, nil, nil, true); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read=%d %s", w.Code, w.Body.String())
	}
	forged := &http.Cookie{Name: cookie.Name, Value: "not-a-session"}
	if w := ownerCall(s, "GET", path, nil, forged, true); w.Code != http.StatusUnauthorized {
		t.Fatalf("forged session read=%d", w.Code)
	}
	if w := ownerCall(s, "GET", path, nil, cookie, true); w.Code != http.StatusOK {
		t.Fatalf("owner read=%d %s", w.Code, w.Body.String())
	}
}

func TestToolPartsSeparateDeferredSchemas(t *testing.T) {
	tools := []Tool{
		{Name: "say", Description: "Say <b>this</b>", Parameters: map[string]any{"type": "object"}},
		{Name: "rare", Description: "held back", Deferred: true},
	}
	parts := toolParts(tools)
	if len(parts) != 2 || parts[0].Kind != ctxKindTools || parts[0].OnDemand || parts[1].Kind != ctxKindToolsOnDemand || !parts[1].OnDemand {
		t.Fatalf("parts=%+v", parts)
	}
	if !strings.Contains(parts[0].Text, "<b>this</b>") {
		t.Fatal("schema text was HTML-escaped")
	}
	view := ContextSnapshot{Parts: parts}.View()
	if view.Totals.OnDemandChars != parts[1].Chars || view.Totals.Chars != parts[0].Chars {
		t.Fatalf("totals=%+v", view.Totals)
	}
}

var _ = extensions.DiscoveryInstructionParts
