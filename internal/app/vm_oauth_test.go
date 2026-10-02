package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
)

type oauthComputerFixture struct {
	mu                   sync.Mutex
	guestID, state, poll string
	cancelled, hold      bool
}

func (f *oauthComputerFixture) client(t *testing.T) *computer.Client {
	t.Helper()
	c, err := computer.New(computer.Config{Socket: "/tmp/oauth-fixture.sock", Client: &http.Client{Transport: secretTransport(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body any
		switch r.URL.Path {
		case "/v1/oauth/start":
			f.guestID = "guest-session"
			body = computer.OAuthStartResult{SessionID: f.guestID, RedirectURI: "http://127.0.0.1:43123/oauth/callback/random"}
		case "/v1/oauth/arm":
			var in struct {
				State string `json:"state"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.state = in.State
			body = computer.OAuthArmResult{OK: true}
		case "/v1/oauth/poll":
			status := f.poll
			if status == "" {
				status = "pending"
			}
			body = computer.OAuthPollResult{Status: status, Code: "fixture-code", State: f.state}
		case "/v1/oauth/cancel":
			f.cancelled = true
			body = computer.OAuthCancelResult{OK: true}
		case "/v1/action":
			var action computer.Action
			_ = json.NewDecoder(r.Body).Decode(&action)
			if action.Name == "desktop.hold" && action.Source == "human" && strings.HasPrefix(action.RunID, "human-control:") && f.hold {
				body = computer.ActionResult{OK: false, Error: "desktop busy"}
			} else {
				if action.Name == "desktop.hold" {
					f.hold = true
				}
				if action.Name == "desktop.release" {
					f.hold = false
				}
				body = computer.ActionResult{OK: true, Result: json.RawMessage(`{}`)}
			}
		default:
			return nil, io.ErrUnexpectedEOF
		}
		b, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func oauthProvider(t *testing.T) *httptest.Server {
	t.Helper()
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "code_challenge_methods_supported": []string{"S256"}})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "token_type": "bearer", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	return provider
}

func newVMOAuthFixture(t *testing.T) (*Server, *oauthComputerFixture, Bot) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	provider := oauthProvider(t)
	t.Cleanup(provider.Close)
	if err := s.extensions.SaveMCP("fixture", extensions.MCPServerConfig{URL: provider.URL + "/mcp", OAuth: &extensions.OAuthConfig{ClientID: "fixture-client", ClientSecret: "fixture-secret", AuthServerMetadataURL: provider.URL + "/.well-known/oauth-authorization-server"}}, false); err != nil {
		t.Fatal(err)
	}
	bot, err := s.store.CreateBot("OAuth fixture", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	f := &oauthComputerFixture{poll: "complete"}
	s.microVM = f.client(t)
	return s, f, bot
}

func TestVMOAuthUsesFlowIDForTokenExchangeAndGuestIDForPolling(t *testing.T) {
	s, f, bot := newVMOAuthFixture(t)
	v, err := s.startVMOAuth(context.Background(), bot.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-v.done:
	case <-time.After(3 * time.Second):
		t.Fatal("OAuth worker did not finish")
	}
	v.mu.Lock()
	status := v.status
	detail := v.err
	v.mu.Unlock()
	if status != "complete" {
		t.Fatalf("status=%q detail=%q", status, detail)
	}
	views, err := s.extensions.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].OAuth == nil || !views[0].OAuth.Connected {
		t.Fatalf("token was not persisted: %#v", views)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.guestID != "guest-session" || f.state == "" {
		t.Fatalf("guest flow not armed: %#v", f)
	}
}

func TestVMOAuthCancelReleasesReservationAndRejectsMismatchedRoute(t *testing.T) {
	s, f, bot := newVMOAuthFixture(t)
	f.mu.Lock()
	f.poll = "pending"
	f.mu.Unlock()
	v, err := s.startVMOAuth(context.Background(), bot.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/extensions/mcp/other/oauth/vm/"+v.id+"/status", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("mismatched route=%d", w.Code)
	}
	v.cancel()
	select {
	case <-v.done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish")
	}
	if !s.claimComputerOwner("other", "run-other") {
		t.Fatal("reservation was not released")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.cancelled {
		t.Fatal("guest cancel was not sent")
	}
}

func TestVMOAuthReservationBlocksOtherBotsButAllowsMatchingHumanControl(t *testing.T) {
	s := &Server{computerOwners: map[string]string{}}
	s.computerOwners["oauth:bot-a"] = "human-oauth:session"
	if !s.claimComputerOwner("bot-a", "human-control:ui") {
		t.Fatal("matching human control blocked")
	}
	if s.claimComputerOwner("bot-b", "run-b") {
		t.Fatal("other Bot bypassed OAuth reservation")
	}
}

func TestVMOAuthCloseCancelsActiveSession(t *testing.T) {
	s, f, bot := newVMOAuthFixture(t)
	f.mu.Lock()
	f.poll = "pending"
	f.mu.Unlock()
	v, err := s.startVMOAuth(context.Background(), bot.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.closeVMOAuth()
	select {
	case <-v.done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel OAuth")
	}
}

func TestVMOAuthReservationBlocksQueuedSameAndOtherBotsUntilRelease(t *testing.T) {
	s := &Server{computerOwners: map[string]string{}, desktopChanged: make(chan struct{})}
	s.computerOwners["oauth:bot-a"] = "human-oauth:session"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sameDone := make(chan error, 1)
	otherDone := make(chan error, 1)
	go func() { sameDone <- s.waitComputerOwner(ctx, Run{BotID: "bot-a", ID: "same-model"}) }()
	waitDesktopQueue(t, s, 1)
	go func() { otherDone <- s.waitComputerOwner(ctx, Run{BotID: "bot-b", ID: "other-model"}) }()
	waitDesktopQueue(t, s, 2)
	select {
	case err := <-sameDone:
		t.Fatalf("same Bot bypassed reservation: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case err := <-otherDone:
		t.Fatalf("other Bot bypassed reservation: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	s.releaseComputerOwner("oauth:bot-a", "human-oauth:session")
	if err := <-sameDone; err != nil {
		t.Fatal(err)
	}
	s.releaseComputerOwner("bot-a", "same-model")
	if err := <-otherDone; err != nil {
		t.Fatal(err)
	}
}

func TestVMOAuthMatchingHumanControlCanAcquireWithQueuedBots(t *testing.T) {
	s := &Server{computerOwners: map[string]string{}, desktopChanged: make(chan struct{})}
	s.computerOwners["oauth:bot-a"] = "human-oauth:session"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queued := make(chan error, 1)
	go func() { queued <- s.waitComputerOwner(ctx, Run{BotID: "bot-b", ID: "queued"}) }()
	waitDesktopQueue(t, s, 1)
	if !s.claimComputerOwner("bot-a", "human-control:ui") {
		t.Fatal("matching human control was blocked")
	}
	s.releaseComputerOwner("bot-a", "human-control:ui")
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued waiter err=%v", err)
	}
}

func TestVMOAuthStartBusyPreservesExistingHumanControl(t *testing.T) {
	s, _, bot := newVMOAuthFixture(t)
	if !s.claimComputerOwner(bot.ID, "human-control:existing") {
		t.Fatal("failed to establish existing human control")
	}
	if _, err := s.startVMOAuth(context.Background(), bot.ID, "fixture"); err == nil {
		t.Fatal("busy OAuth start unexpectedly succeeded")
	}
	if !s.ownsComputer(Run{BotID: bot.ID, ID: "human-control:existing"}) {
		t.Fatal("busy OAuth start displaced human control")
	}
	s.releaseComputerOwner(bot.ID, "human-control:existing")
}

func TestVMOAuthDuplicateStartAndRequestCancellationCleanup(t *testing.T) {
	s, f, bot := newVMOAuthFixture(t)
	f.mu.Lock()
	f.poll = "pending"
	f.mu.Unlock()
	first, err := s.startVMOAuth(context.Background(), bot.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.startVMOAuth(context.Background(), bot.ID, "fixture"); err == nil {
		t.Fatal("duplicate OAuth start succeeded")
	}
	first.cancel()
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("duplicate cleanup did not finish")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.startVMOAuth(ctx, bot.ID, "fixture"); err == nil {
		t.Fatal("cancelled request unexpectedly succeeded")
	}
	s.vmOAuthMu.Lock()
	for _, v := range s.vmOAuth {
		v.mu.Lock()
		active := v.ended.IsZero()
		v.mu.Unlock()
		if active {
			s.vmOAuthMu.Unlock()
			t.Fatal("cancelled request left active session")
		}
	}
	s.vmOAuthMu.Unlock()
}

func TestVMOAuthHoldTransfersAroundHumanControlAndCancellation(t *testing.T) {
	s, f, bot := newVMOAuthFixture(t)
	f.mu.Lock()
	f.poll = "pending"
	f.mu.Unlock()
	v, err := s.startVMOAuth(context.Background(), bot.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	held := f.hold
	f.mu.Unlock()
	if !held {
		t.Fatal("OAuth did not establish desktop hold")
	}
	call := func(action string, args any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "action": action, "args": args})
		req := httptest.NewRequest(http.MethodPost, "/api/computers/firecracker/actions", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w
	}
	acquire := call("desktop.control.acquire", map[string]any{})
	if acquire.Code != http.StatusOK {
		t.Fatalf("human acquire while OAuth hold: %d %s", acquire.Code, acquire.Body.String())
	}
	var envelope struct {
		Result struct {
			ID string `json:"control_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(acquire.Body.Bytes(), &envelope); err != nil || envelope.Result.ID == "" {
		t.Fatalf("missing control id: %s", acquire.Body.String())
	}
	f.mu.Lock()
	held = f.hold
	f.mu.Unlock()
	if !held {
		t.Fatal("Human input did not establish its own hold")
	}
	if release := call("desktop.control.release", map[string]any{"control_id": envelope.Result.ID}); release.Code != http.StatusOK {
		t.Fatalf("human release: %d %s", release.Code, release.Body.String())
	}
	f.mu.Lock()
	held = f.hold
	f.mu.Unlock()
	if !held {
		t.Fatal("OAuth hold was not restored after release")
	}
	v.cancel()
	select {
	case <-v.done:
	case <-time.After(time.Second):
		t.Fatal("OAuth cancellation did not finish")
	}
	f.mu.Lock()
	held, cancelled := f.hold, f.cancelled
	f.mu.Unlock()
	if held || !cancelled {
		t.Fatalf("cleanup hold=%v cancelled=%v", held, cancelled)
	}
}
