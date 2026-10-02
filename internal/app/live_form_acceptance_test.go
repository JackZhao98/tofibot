package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// This is a deliberately opt-in, isolated acceptance. It exercises the
// production Server, store, worker, runtime and guest browser against a local
// fictional fixture only. The fixture runner owns setup and teardown outside
// this test; this test never reads a production data directory or credential.
const (
	liveFormAcceptanceGate          = "TOFI_LIVE_FORM_ACCEPTANCE"
	liveFormAcceptanceSocketEnv     = "TOFI_ACCEPTANCE_FORM_SOCKET"
	liveFormAcceptanceURL           = "TOFI_ACCEPTANCE_FORM_URL"
	liveFormAcceptanceCredentialEnv = "TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE"
	liveFormAcceptanceModelEnv      = "TOFI_ACCEPTANCE_FORM_MODEL"
	liveFormAcceptanceDefaultModel  = "codex-gpt-6-luna"
	liveFormAcceptanceBudget        = 6 * time.Minute
	liveFormAcceptanceMaxTools      = 60
	liveFormAcceptanceMaxModels     = 24
)

var liveFormAcceptanceSockets = map[string]string{
	"/run/tofi-computer/acceptance-a/control.sock": "acceptance-a",
	"/run/tofi-computer/acceptance-b/control.sock": "acceptance-b",
}

var liveFormAcceptanceModels = map[string]bool{
	"codex-gpt-6-luna":   true,
	"codex-gpt-5.6-luna": true,
}

type liveFormAcceptanceCredential struct {
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
	ExpiresAt   int64  `json:"expires_at"`
}

type liveFormExpectedField struct {
	Key      string
	Type     string
	Required bool
}

type liveFormTraceEvent struct {
	At     time.Time
	RunID  string
	Name   string
	Action string
	Status string
}

// liveFormAcceptanceBounds is test-only. It never changes the product tool
// schemas or behavior; it only stops an acceptance run once its explicit
// limits are crossed and records names/statuses without arguments or results.
type liveFormAcceptanceBounds struct {
	deadline time.Time

	mu                       sync.Mutex
	modelBoundaries          int
	toolCalls                int
	answerAt                 time.Time
	observedAfterAnswer      bool
	observationViolation     bool
	forbiddenOracleToolCalls int
	events                   []liveFormTraceEvent
}

type liveFormBoundedEngine struct {
	base   runtime.Engine
	bounds *liveFormAcceptanceBounds
}

func (e *liveFormBoundedEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	ctx, cancel := context.WithDeadline(ctx, e.bounds.deadline)
	defer cancel()

	beforeModelCall := req.BeforeModelCall
	req.BeforeModelCall = func() error {
		if err := e.bounds.beforeModelCall(); err != nil {
			return err
		}
		if beforeModelCall != nil {
			return beforeModelCall()
		}
		return nil
	}
	onToolEvent := req.OnToolEvent
	req.OnToolEvent = func(event runtime.ToolEvent) error {
		e.bounds.recordEvent(req.RunID, event)
		if onToolEvent != nil {
			return onToolEvent(event)
		}
		return nil
	}
	tools := make([]runtime.Tool, len(req.Tools))
	copy(tools, req.Tools)
	for i := range tools {
		toolName, execute := tools[i].Name, tools[i].Execute
		tools[i].Execute = func(toolCtx context.Context, raw json.RawMessage) (string, error) {
			if err := e.bounds.beforeTool(req.RunID, toolName, raw); err != nil {
				return "", err
			}
			result, err := execute(toolCtx, raw)
			if err == nil {
				e.bounds.afterTool(toolName, raw)
			}
			return result, err
		}
	}
	req.Tools = tools
	return e.base.Run(ctx, req)
}

func (b *liveFormAcceptanceBounds) beforeModelCall() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().After(b.deadline) {
		return errors.New("acceptance time budget exceeded")
	}
	b.modelBoundaries++
	if b.modelBoundaries > liveFormAcceptanceMaxModels {
		return errors.New("acceptance model-call budget exceeded")
	}
	return nil
}

func (b *liveFormAcceptanceBounds) beforeTool(runID, name string, raw json.RawMessage) error {
	action := liveFormToolAction(name, raw)
	at := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if at.After(b.deadline) {
		return errors.New("acceptance time budget exceeded")
	}
	b.toolCalls++
	b.events = append(b.events, liveFormTraceEvent{At: at, RunID: runID, Name: name, Action: action, Status: "executed"})
	if b.toolCalls > liveFormAcceptanceMaxTools {
		return errors.New("acceptance tool-call budget exceeded")
	}
	// The fixture oracle is intentionally outside the model. Do not permit a
	// model shell/file route to inspect /result, advance layout, or bypass the
	// visible browser interaction.
	if name == "computer_shell" || name == "computer_files" || name == "computer_terminal" ||
		(name == "computer_action" && (action == "shell.exec" || strings.HasPrefix(action, "files.") || strings.HasPrefix(action, "terminal."))) ||
		(name == "use_secret_input" && action != "browser_type") {
		b.forbiddenOracleToolCalls++
		return errors.New("acceptance fixture oracle is unavailable to the model")
	}
	if !b.answerAt.IsZero() && liveFormTypingTool(name, action) && !b.observedAfterAnswer {
		b.observationViolation = true
		return errors.New("acceptance requires a fresh browser observation before typing after form input")
	}
	return nil
}

func (b *liveFormAcceptanceBounds) afterTool(name string, raw json.RawMessage) {
	action := liveFormToolAction(name, raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.answerAt.IsZero() && ((name == "computer_browser" && action == "browser.snapshot") || (name == "computer_desktop" && action == "desktop.capture")) {
		b.observedAfterAnswer = true
	}
}

func (b *liveFormAcceptanceBounds) recordEvent(runID string, event runtime.ToolEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, liveFormTraceEvent{At: time.Now(), RunID: runID, Name: event.Name, Action: liveFormToolAction(event.Name, json.RawMessage(event.Arguments)), Status: event.Status})
}

func (b *liveFormAcceptanceBounds) markAnswered() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.answerAt = time.Now()
	b.observedAfterAnswer = false
}

func (b *liveFormAcceptanceBounds) eventsFor(runID string) []liveFormTraceEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []liveFormTraceEvent
	for _, event := range b.events {
		if event.RunID == runID {
			out = append(out, event)
		}
	}
	return out
}

func (b *liveFormAcceptanceBounds) initialInspectionError(initialRun string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	seenSnapshot := false
	for _, event := range b.events {
		if event.RunID != initialRun {
			continue
		}
		if event.Status == "completed" && liveFormObservation(event.Name, event.Action) {
			seenSnapshot = true
		}
		if event.Name == "ask_user_form" && event.Status == "executed" {
			if !seenSnapshot {
				return errors.New("ask_user_form was not preceded by a browser inspection")
			}
		}
	}
	return nil
}

func (b *liveFormAcceptanceBounds) workflowError(initialRun string, https bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.observationViolation {
		return errors.New("model attempted typing after form input without a fresh observation")
	}
	if b.forbiddenOracleToolCalls != 0 {
		return errors.New("model attempted a shell/file/terminal fixture oracle")
	}
	if b.answerAt.IsZero() {
		return errors.New("acceptance answer time was not recorded")
	}
	seenForm, typed, secretTyped := false, 0, 0
	var lastInteraction, finalSnapshot time.Time
	for _, event := range b.events {
		if event.RunID != initialRun {
			continue
		}
		if event.Status == "executed" && event.Name == "ask_user_form" {
			seenForm = true
		}
		if !seenForm || !event.At.After(b.answerAt) {
			continue
		}
		if event.Status == "executed" && event.Name == "computer_desktop" && event.Action == "desktop.type" {
			typed++
			lastInteraction = event.At
		}
		if event.Status == "executed" && event.Name == "use_secret_input" && event.Action == "browser_type" {
			secretTyped++
			lastInteraction = event.At
		}
		if event.Status == "executed" && event.Name == "computer_desktop" && (event.Action == "desktop.click" || event.Action == "desktop.key") {
			lastInteraction = event.At
		}
		if event.Status == "completed" && liveFormObservation(event.Name, event.Action) && event.At.After(lastInteraction) {
			finalSnapshot = event.At
		}
	}
	if !seenForm {
		return errors.New("model did not create ask_user_form")
	}
	if typed < 3 {
		return errors.New("model did not use desktop.type for all ordinary fields after form input")
	}
	if https && secretTyped == 0 {
		return errors.New("model did not use private browser input for the HTTPS access code")
	}
	if finalSnapshot.IsZero() || !finalSnapshot.After(lastInteraction) {
		return errors.New("model did not successfully inspect the page after its final form interaction")
	}
	return nil
}

func (b *liveFormAcceptanceBounds) traceSummary() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	parts := make([]string, 0, len(b.events))
	for _, event := range b.events {
		part := event.Name
		if event.Action != "" {
			part += ":" + event.Action
		}
		if event.Status != "" {
			part += ":" + event.Status
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func (b *liveFormAcceptanceBounds) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelBoundaries, b.toolCalls
}

func liveFormToolAction(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	if name == "use_secret_input" && in.Action == "" {
		return ""
	}
	return in.Action
}

func liveFormTypingTool(name, action string) bool {
	return (name == "computer_desktop" && action == "desktop.type") || (name == "use_secret_input" && action == "browser_type")
}

func liveFormObservation(name, action string) bool {
	return (name == "computer_browser" && action == "browser.snapshot") || (name == "computer_desktop" && action == "desktop.capture")
}

func TestLiveBrowserFormCompletion(t *testing.T) {
	if os.Getenv(liveFormAcceptanceGate) != "1" {
		t.Skip("set TOFI_LIVE_FORM_ACCEPTANCE=1 for the isolated local form acceptance")
	}

	credential := liveFormAcceptanceReadCredential(t)
	model := liveFormAcceptanceModel(t)
	fixtureURL := liveFormAcceptanceURLValue(t)
	socket := strings.TrimSpace(os.Getenv(liveFormAcceptanceSocketEnv))
	expectedComputerID, allowed := liveFormAcceptanceSockets[socket]
	if !allowed {
		t.Fatal("only the explicit acceptance-a or acceptance-b form socket is allowed")
	}

	bounds := &liveFormAcceptanceBounds{deadline: time.Now().Add(liveFormAcceptanceBudget)}
	defer func() {
		if t.Failed() {
			t.Logf("sanitized trace=%s", bounds.traceSummary())
		}
	}()
	server, err := NewServer(Config{
		DataDir:        t.TempDir(),
		Environment:    "acceptance",
		Provider:       "openai_codex",
		DefaultModel:   model,
		ComputerSocket: socket,
	})
	if err != nil {
		t.Fatal("could not create isolated acceptance server")
	}
	defer server.Close()
	if server.codex == nil {
		t.Fatal("isolated access-only credential manager is unavailable")
	}
	if err := server.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal("could not save isolated access-only credential")
	}
	realEngine, err := runtime.New(runtime.Config{
		Provider:    "openai_codex",
		Model:       model,
		Credential:  server.codex.CredentialReadOnly,
		MaxDuration: liveFormAcceptanceBudget,
	})
	if err != nil {
		t.Fatal("could not create read-only production runtime")
	}
	server.mu.Lock()
	server.engine = &liveFormBoundedEngine{base: realEngine, bounds: bounds}
	server.codexManaged = false // the wrapper owns the explicit read-only credential.
	server.mu.Unlock()

	info, err := server.microVMInfo(context.Background())
	if err != nil || info.ID != expectedComputerID || info.State != "ready" {
		t.Fatal("selected acceptance guest is not ready or does not match its socket identity")
	}

	bot, err := server.store.CreateBot("Local form acceptance", "Use the available tools to complete the user's local fictional browser task. Do not use accounts or external services. Keep the final response concise.", model)
	if err != nil {
		t.Fatal("could not create isolated acceptance Bot")
	}
	conversation, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal("could not load isolated acceptance conversation")
	}

	// Preserve the existing shared browser: start if needed, record its current
	// foreground tab, then create one dedicated fixture tab. Cleanup closes only
	// that target and restores the earlier foreground; it never stops Chrome.
	liveFormGuestAction(t, server, bot.ID, "live-form-setup", "desktop.start", struct{}{})
	before := liveFormGuestSnapshot(t, server, bot.ID, "live-form-before")
	previousTarget := ""
	if before.Current != nil {
		previousTarget = before.Current.TargetID
	}
	opened := liveFormGuestBrowserAction(t, server, bot.ID, "live-form-open", "new", fixtureURL.String(), "")
	fixtureTarget := opened.TargetID
	if fixtureTarget == "" {
		t.Fatal("fixture browser tab was not created")
	}
	defer func() {
		// Stop any still-running model before restoring a pre-existing tab.
		_ = server.Close()
		liveFormRestoreFixtureTab(t, server, bot.ID, fixtureTarget, previousTarget)
	}()
	if fixtureURL.Scheme == "https" && os.Getenv("TOFI_ACCEPTANCE_FORM_SELF_SIGNED") == "1" {
		liveFormTrustLocalFixture(t, server, bot.ID, fixtureTarget, fixtureURL.String())
	}
	if fixtureURL.Scheme == "https" {
		liveFormPrivateFocusGuard(t, server, bot.ID, fixtureTarget, fixtureURL)
	}

	initialResult := liveFormFixtureResult(t, server, bot.ID, fixtureURL)
	if initialResult.Submitted || initialResult.LayoutVersion != 1 || initialResult.SubmissionCount != 0 {
		t.Fatal("local form fixture did not start cleanly at layout version one")
	}

	prompt := "Please use the browser to complete and submit the local fictional registration at " + fixtureURL.String() + ". Ask me for the details with a form after you inspect the page. Do not invent any missing values. After submitting, inspect the confirmation and report its receipt."
	_, initialRun, _, err := server.store.AddUserRun(conversation.ID, bot.ID, prompt, "live-form-initial")
	if err != nil {
		t.Fatal("could not create initial acceptance run")
	}
	server.enqueue(conversation, initialRun)
	question := liveFormWaitForQuestion(t, server, conversation.ID, initialRun.ID, bounds.deadline)
	liveFormWaitForStatus(t, server, initialRun.ID, runWaiting, bounds.deadline)
	if err := bounds.initialInspectionError(initialRun.ID); err != nil {
		t.Fatalf("initial form workflow was invalid; trace=%s", bounds.traceSummary())
	}

	ordinary, password := liveFormAcceptanceValues(t, fixtureURL.Scheme == "https")
	formValues := liveFormAssertQuestionFields(t, question, fixtureURL, ordinary, password)

	// This out-of-model request changes the fixture while the user card is
	// parked. The next model turn must re-observe the reordered live page.
	liveFormFixtureAdvance(t, server, bot.ID, fixtureURL)
	advanced := liveFormFixtureResult(t, server, bot.ID, fixtureURL)
	if advanced.Submitted || advanced.LayoutVersion != 2 || advanced.SubmissionCount != 0 {
		t.Fatal("fixture did not advance its parked form layout")
	}

	appHTTP := httptest.NewServer(server.Handler())
	defer appHTTP.Close()
	bounds.markAnswered()
	liveFormAnswerThroughHTTP(t, appHTTP.URL, question.ID, formValues)
	liveFormWaitForStatus(t, server, initialRun.ID, "done", bounds.deadline)
	if err := bounds.workflowError(initialRun.ID, fixtureURL.Scheme == "https"); err != nil {
		t.Fatalf("form submission workflow was invalid; trace=%s", bounds.traceSummary())
	}
	finalResult := liveFormFixtureResult(t, server, bot.ID, fixtureURL)
	if !finalResult.Submitted || finalResult.LayoutVersion != 2 || finalResult.SubmissionCount != 1 || strings.TrimSpace(finalResult.Receipt) == "" || !liveFormSameFields(finalResult.Fields, ordinary) {
		t.Fatal("fixture did not record exactly one browser submission with its receipt")
	}
	if fixtureURL.Scheme == "https" && (!finalResult.SecretPresent || len(finalResult.SecretSHA256) != 64) {
		t.Fatal("HTTPS fixture did not receive the synthetic private access code")
	}
	if fixtureURL.Scheme != "https" && finalResult.SecretPresent {
		t.Fatal("HTTP fixture unexpectedly recorded a private access code")
	}
	if fixtureURL.Scheme == "https" {
		digest := sha256.Sum256([]byte(password))
		if finalResult.SecretSHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("HTTPS fixture secret digest did not match the synthetic access code")
		}
	}
	liveFormAssertStoredToolEvidence(t, server, conversation.ID, initialRun.ID, fixtureURL.Scheme == "https", finalResult.Receipt)
	liveFormAssertAssistantReceipt(t, server, conversation.ID, initialRun.ID, finalResult.Receipt)

	followupPrompt := "Please briefly summarize the ordinary details you previously entered in the local fictional registration. Do not change the browser or submit anything else."
	_, followupRun, _, err := server.store.AddUserRun(conversation.ID, bot.ID, followupPrompt, "live-form-summary")
	if err != nil {
		t.Fatal("could not create cached-summary follow-up")
	}
	server.enqueue(conversation, followupRun)
	liveFormWaitForStatus(t, server, followupRun.ID, "done", bounds.deadline)
	liveFormAssertCachedFollowup(t, server, conversation.ID, followupRun.ID, ordinary, bounds)

	modelCalls, toolCalls := bounds.counts()
	t.Logf("isolated live form acceptance passed: model_boundaries=%d tool_calls=%d trace=%s", modelCalls, toolCalls, bounds.traceSummary())
}

func liveFormAcceptanceReadCredential(t *testing.T) liveFormAcceptanceCredential {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(liveFormAcceptanceCredentialEnv))
	if path == "" {
		t.Fatal("an explicit access-only credential snapshot path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("could not read the explicit access-only credential snapshot")
	}
	var fields map[string]json.RawMessage
	var credential liveFormAcceptanceCredential
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &credential) != nil {
		t.Fatal("access-only credential snapshot is invalid")
	}
	if _, containsRefreshToken := fields["refresh_token"]; containsRefreshToken || strings.TrimSpace(credential.AccessToken) == "" || credential.ExpiresAt < time.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatal("credential snapshot must be access-only and valid for at least ten minutes")
	}
	return credential
}

func liveFormAcceptanceModel(t *testing.T) string {
	t.Helper()
	model := strings.TrimSpace(os.Getenv(liveFormAcceptanceModelEnv))
	if model == "" {
		model = liveFormAcceptanceDefaultModel
	}
	if !liveFormAcceptanceModels[model] {
		t.Fatal("acceptance model is outside the restricted local catalog")
	}
	return model
}

func liveFormAcceptanceURLValue(t *testing.T) *url.URL {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(liveFormAcceptanceURL))
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Host != "127.0.0.1:"+u.Port() || u.Path != "/form" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		t.Fatal("fixture URL must be exactly an HTTP(S) loopback /form URL with an explicit port")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("fixture URL has an invalid loopback port")
	}
	return u
}

type liveFormGuestPage struct {
	TargetID string `json:"target_id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

type liveFormGuestSnapshotResult struct {
	Current *liveFormGuestPage `json:"current"`
}

type liveFormGuestBrowserResult struct {
	TargetID string `json:"target_id"`
}

// The opt-in runner owns a one-day self-signed certificate for this exact
// loopback fixture. A tab-scoped interstitial exception is test setup only:
// no Chrome flags, trust store or product HTTPS policy is changed. Never run
// this for a public URL, a real credential, or an unknown page/tab.
func liveFormTrustLocalFixture(t *testing.T, server *Server, botID, target, pageURL string) {
	t.Helper()
	page := liveFormGuestSnapshot(t, server, botID, "live-form-certificate-check")
	if page.Current == nil || page.Current.TargetID != target {
		t.Fatal("local certificate fixture is not the foreground tab")
	}
	if page.Current.URL == pageURL && strings.Contains(page.Current.Title, "Fictional registration") {
		return
	}
	if page.Current.URL != pageURL && page.Current.URL != "chrome-error://chromewebdata/" {
		t.Fatal("refusing a certificate exception for a different page")
	}
	if !strings.Contains(strings.ToLower(page.Current.Title), "privacy error") {
		t.Fatal("expected the controlled loopback certificate interstitial")
	}
	for _, key := range "thisisunsafe" {
		liveFormGuestAction(t, server, botID, "live-form-local-certificate", "desktop.key", map[string]string{"key": string(key)})
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		page = liveFormGuestSnapshot(t, server, botID, "live-form-certificate-ready")
		if page.Current != nil && page.Current.TargetID == target && page.Current.URL == pageURL && strings.Contains(page.Current.Title, "Fictional registration") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("controlled HTTPS fixture did not become ready")
}

func liveFormGuestAction(t *testing.T, server *Server, botID, runID, name string, args any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal("could not encode guest fixture action")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := server.microVM.Action(ctx, computer.Action{BotID: botID, RunID: runID, Source: "human", Name: name, Args: raw})
	if err != nil {
		t.Fatalf("guest fixture action %s failed", name)
	}
	return result.Result
}

func liveFormGuestSnapshot(t *testing.T, server *Server, botID, runID string) liveFormGuestSnapshotResult {
	t.Helper()
	var out liveFormGuestSnapshotResult
	if json.Unmarshal(liveFormGuestAction(t, server, botID, runID, "browser.snapshot", struct{}{}), &out) != nil {
		t.Fatal("guest fixture snapshot was invalid")
	}
	return out
}

func liveFormGuestBrowserAction(t *testing.T, server *Server, botID, runID, action, pageURL, targetID string) liveFormGuestBrowserResult {
	t.Helper()
	var out liveFormGuestBrowserResult
	args := map[string]string{"action": action}
	if pageURL != "" {
		args["url"] = pageURL
	}
	if targetID != "" {
		args["target_id"] = targetID
	}
	if json.Unmarshal(liveFormGuestAction(t, server, botID, runID, "browser.action", args), &out) != nil {
		t.Fatal("guest fixture browser action was invalid")
	}
	return out
}

func liveFormPrivateFocusGuard(t *testing.T, server *Server, botID, target string, fixture *url.URL) {
	t.Helper()
	origin := fixture.Scheme + "://" + fixture.Host
	for _, scenario := range []struct {
		field, origin string
		allowed       bool
	}{
		{"full_name", origin, false},
		{"access_code", "https://other.example.test", false},
		{"access_code", origin, true},
	} {
		pageURL := *fixture
		pageURL.Fragment = "guard-" + scenario.field
		liveFormGuestBrowserAction(t, server, botID, "live-form-guard", "navigate", pageURL.String(), target)
		ready := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			snapshot := liveFormGuestSnapshot(t, server, botID, "live-form-guard")
			if snapshot.Current != nil && snapshot.Current.TargetID == target && snapshot.Current.Title == "Private-input guard — "+pageURL.Fragment {
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			t.Fatal("private guard fixture did not focus its synthetic test field")
		}
		raw, _ := json.Marshal(map[string]string{"origin": scenario.origin, "text": "synthetic-guard-only"})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err := server.microVM.Action(ctx, computer.Action{BotID: botID, RunID: "live-form-guard", Name: "browser.type_private", Source: "human", Args: raw})
		cancel()
		if (err == nil) != scenario.allowed {
			t.Fatalf("real Chrome private-field guard failed: field=%s allowed=%v", scenario.field, scenario.allowed)
		}
	}
	// Removing the test-only hash resets all fields, including the fake password.
	liveFormGuestBrowserAction(t, server, botID, "live-form-guard-reset", "navigate", fixture.String(), target)
	t.Log("real Chrome private-input checks passed: ordinary focus rejected, wrong origin rejected, password focus accepted")
}

func liveFormRestoreFixtureTab(t *testing.T, server *Server, botID, fixtureTarget, previousTarget string) {
	t.Helper()
	if fixtureTarget != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		raw, _ := json.Marshal(map[string]string{"action": "close", "target_id": fixtureTarget})
		_, err := server.microVM.Action(ctx, computer.Action{BotID: botID, RunID: "live-form-cleanup", Source: "human", Name: "browser.action", Args: raw})
		cancel()
		if err != nil {
			t.Error("could not close the dedicated fixture browser tab")
		}
	}
	if previousTarget != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		raw, _ := json.Marshal(map[string]string{"action": "switch", "target_id": previousTarget})
		_, err := server.microVM.Action(ctx, computer.Action{BotID: botID, RunID: "live-form-restore", Source: "human", Name: "browser.action", Args: raw})
		cancel()
		if err != nil {
			t.Error("could not restore the previous browser foreground tab")
		}
	}
}

type liveFormFixtureResponse struct {
	Submitted       bool              `json:"submitted"`
	Fields          map[string]string `json:"fields"`
	SecretPresent   bool              `json:"secret_present"`
	SecretSHA256    string            `json:"secret_sha256"`
	Receipt         string            `json:"receipt"`
	LayoutVersion   int               `json:"layout_version"`
	SubmissionCount int               `json:"submission_count"`
}

// Only this out-of-model, deterministic fixture oracle uses curl -k. It is
// restricted to the validated loopback URL and is never included in model
// tools, prompts, arguments, or the sanitized trace.
func liveFormGuestCurl(t *testing.T, server *Server, botID string, endpoint *url.URL, method string) []byte {
	t.Helper()
	if method != http.MethodGet && method != http.MethodPost {
		t.Fatal("invalid fixture oracle method")
	}
	command := "curl -k --fail --silent --show-error --max-time 5 -X " + method + " " + strconv.Quote(endpoint.String())
	raw := liveFormGuestAction(t, server, botID, "live-form-oracle", "shell.exec", map[string]any{"command": command, "timeout_sec": 10})
	var result struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ExitCode != 0 {
		t.Fatal("guest fixture oracle request failed")
	}
	return []byte(result.Stdout)
}

func liveFormFixtureResult(t *testing.T, server *Server, botID string, fixture *url.URL) liveFormFixtureResponse {
	t.Helper()
	endpoint := *fixture
	endpoint.Path = "/result"
	var result liveFormFixtureResponse
	if json.Unmarshal(liveFormGuestCurl(t, server, botID, &endpoint, http.MethodGet), &result) != nil {
		t.Fatal("fixture result response was invalid")
	}
	return result
}

func liveFormFixtureAdvance(t *testing.T, server *Server, botID string, fixture *url.URL) {
	t.Helper()
	endpoint := *fixture
	endpoint.Path = "/advance"
	_ = liveFormGuestCurl(t, server, botID, &endpoint, http.MethodPost)
}

func liveFormWaitForQuestion(t *testing.T, server *Server, conversationID, runID string, deadline time.Time) Question {
	t.Helper()
	for time.Now().Before(deadline) {
		questions, err := server.store.PendingQuestions(conversationID)
		if err != nil {
			t.Fatal("could not inspect pending form")
		}
		if len(questions) == 1 && questions[0].Type == questionForm {
			return questions[0]
		}
		run, err := server.store.GetRun(runID)
		if err != nil || (run.Status != "queued" && run.Status != "running" && run.Status != runWaiting) {
			t.Fatal("model ended without publishing the required form")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("model did not park a pending user form before the acceptance deadline")
	return Question{}
}

func liveFormWaitForStatus(t *testing.T, server *Server, runID, wanted string, deadline time.Time) {
	t.Helper()
	for time.Now().Before(deadline) {
		run, err := server.store.GetRun(runID)
		if err != nil {
			t.Fatal("could not inspect acceptance run")
		}
		if run.Status == wanted {
			return
		}
		if run.Status == "failed" || run.Status == "cancelled" || run.Status == "interrupted" {
			t.Fatal("acceptance run ended before reaching its required state")
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("acceptance run exceeded its time budget")
}

func liveFormAcceptanceValues(t *testing.T, https bool) (map[string]string, string) {
	t.Helper()
	var token [5]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal("could not create synthetic fixture values")
	}
	suffix := hex.EncodeToString(token[:])
	names := [][2]string{{"Mara", "Chen"}, {"Noah", "Patel"}, {"Leah", "Moreno"}, {"Evan", "Kim"}}
	name := names[int(token[0])%len(names)]
	ordinary := map[string]string{
		"full_name": name[0] + " " + name[1],
		"email":     strings.ToLower(name[0]+"."+name[1]) + "." + suffix + "@example.test",
		"notes":     "Please reserve a fictional introductory place for the local registration.",
	}
	if !https {
		return ordinary, ""
	}
	return ordinary, "Fictional-" + suffix + "-Code9"
}

func liveFormAssertQuestionFields(t *testing.T, question Question, fixture *url.URL, ordinary map[string]string, password string) map[string]string {
	t.Helper()
	if question.SourceURL != fixture.String() {
		t.Fatal("model form source URL did not match the observed local fixture page")
	}
	expected := []liveFormExpectedField{
		{Key: "full_name", Type: "text", Required: true},
		{Key: "email", Type: "email", Required: true},
		{Key: "notes", Type: "textarea", Required: true},
	}
	if fixture.Scheme == "https" {
		expected = append(expected, liveFormExpectedField{Key: "access_code", Type: "password", Required: true})
	}
	if len(question.Fields) != len(expected) {
		t.Fatal("model form omitted or invented fixture fields")
	}
	used := make([]bool, len(expected))
	answers := make(map[string]string, len(expected))
	for _, field := range question.Fields {
		match := -1
		for i, want := range expected {
			if !used[i] && field.Type == want.Type && liveFormMeaningfulLabel(field.Label, want.Key) {
				match = i
				break
			}
		}
		if match < 0 {
			t.Fatal("model form field label/type did not match the observed fixture")
		}
		if field.Required != expected[match].Required {
			t.Fatal("model form did not preserve the fixture field's requiredness")
		}
		used[match] = true
		if expected[match].Key == "access_code" {
			answers[field.ID] = password
		} else {
			answers[field.ID] = ordinary[expected[match].Key]
		}
	}
	for _, found := range used {
		if !found {
			t.Fatal("model form missed an observed fixture field")
		}
	}
	return answers
}

func liveFormMeaningfulLabel(label, expected string) bool {
	words := strings.FieldsFunc(strings.ToLower(label), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	has := func(word string) bool {
		for _, got := range words {
			if got == word {
				return true
			}
		}
		return false
	}
	switch expected {
	case "full_name":
		return has("full") && has("name")
	case "email":
		return has("email") || has("e") && has("mail")
	case "notes":
		return has("notes") || has("note")
	case "access_code":
		return has("access") && (has("code") || has("passcode"))
	default:
		return false
	}
}

func liveFormAnswerThroughHTTP(t *testing.T, baseURL, questionID string, fields map[string]string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		t.Fatal("could not encode form answer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/questions/"+questionID+"/answer", bytes.NewReader(payload))
	if err != nil {
		t.Fatal("could not create form-answer HTTP request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal("form-answer HTTP route was unavailable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		t.Fatal("form-answer HTTP route rejected the submitted fields")
	}
}

func liveFormAssertStoredToolEvidence(t *testing.T, server *Server, conversationID, runID string, https bool, receipt string) {
	t.Helper()
	activities, err := server.store.ToolActivities(conversationID, liveFormAcceptanceMaxTools)
	if err != nil {
		t.Fatal("could not read persisted tool evidence")
	}
	var browser, form, typed, secret, receiptObserved bool
	for _, activity := range activities {
		if activity.RunID != runID || activity.Status != "completed" {
			continue
		}
		switch activity.Name {
		case "computer_browser":
			browser = true
			if strings.Contains(activity.Arguments, "browser.snapshot") && strings.Contains(activity.Result, receipt) {
				receiptObserved = true
			}
		case "ask_user_form":
			form = true
		case "computer_desktop":
			if strings.Contains(activity.Arguments, "desktop.type") {
				typed = true
			}
			if strings.Contains(activity.Arguments, "desktop.capture") && strings.Contains(activity.Result, `"image_attached":true`) {
				// A real screenshot may carry the receipt visually. The independent
				// fixture and assistant-response checks still require the exact nonce.
				browser, receiptObserved = true, true
			}
		case "use_secret_input":
			secret = true
		}
	}
	if !browser || !form || !typed || !receiptObserved || https && !secret {
		t.Fatal("persisted production tool evidence is missing required browser/form actions")
	}
}

func liveFormSameFields(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func liveFormAssertAssistantReceipt(t *testing.T, server *Server, conversationID, runID, receipt string) {
	t.Helper()
	messages, _, err := server.store.Messages(conversationID, 0, 200)
	if err != nil {
		t.Fatal("could not read final browser acceptance response")
	}
	for _, message := range messages {
		if message.RunID == runID && message.Role == "assistant" && strings.Contains(message.Content, receipt) {
			return
		}
	}
	t.Fatal("final assistant response did not report the browser-observed receipt")
}

func liveFormAssertCachedFollowup(t *testing.T, server *Server, conversationID, runID string, ordinary map[string]string, bounds *liveFormAcceptanceBounds) {
	t.Helper()
	for _, event := range bounds.eventsFor(runID) {
		if event.Status != "executed" {
			continue
		}
		switch event.Name {
		case "list_computers", "computer_browser", "computer_desktop", "computer_shell", "computer_files", "computer_terminal", "computer_action", "list_mcp_servers", "search_mcp_tools", "call_mcp_tool", "list_skills", "read_skill", "read_skill_file":
			t.Fatal("cached explanatory follow-up made a redundant discovery or browser call")
		}
	}
	messages, _, err := server.store.Messages(conversationID, 0, 200)
	if err != nil {
		t.Fatal("could not read cached explanatory response")
	}
	var response strings.Builder
	for _, message := range messages {
		if message.RunID == runID && message.Role == "assistant" {
			response.WriteString(message.Content)
		}
	}
	for _, value := range ordinary {
		if !strings.Contains(response.String(), value) {
			t.Fatal("cached explanatory follow-up omitted a previously entered ordinary value")
		}
	}
}
