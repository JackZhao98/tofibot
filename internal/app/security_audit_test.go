package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func secLogin(s *Server, forwarded string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", forwarded)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestSessionCookieSecureBehindTLSProxy(t *testing.T) {
	s, dir := ownerTestServer(t)
	s.ownerAuth.allowLAN = true
	s.ownerAuth.secureCookies = true
	secret, err := readOwnerBootstrap(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"bootstrap_secret": secret, "username": "proxy-owner", "email": "p@example.invalid", "password": testOwnerPassword})
	r := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(string(body)))
	r.RemoteAddr = "172.17.0.1:100"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].Secure {
		t.Fatalf("status=%d cookies=%v", w.Code, w.Result().Cookies())
	}
}

func TestAnonymousTrafficCannotLockEveryLogin(t *testing.T) {
	t.Setenv("TOFI_TRUSTED_PROXIES", "127.0.0.1/32")
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	// Many addresses, each with junk credentials against made-up identifiers.
	for i := 0; i < 90; i++ {
		body := fmt.Sprintf(`{"identifier":"nobody-%d","password":"x"}`, i)
		if w := secLogin(s, fmt.Sprintf("203.0.113.%d", i+1), body); w.Code != 401 {
			t.Fatalf("junk %d: %d", i, w.Code)
		}
	}
	good := fmt.Sprintf(`{"identifier":"workspace-owner","password":%q}`, testOwnerPassword)
	if w := secLogin(s, "198.51.100.7", good); w.Code != 200 {
		t.Fatalf("owner locked out by anonymous traffic: %d", w.Code)
	}
}

func TestMalformedLoginBodiesAreNotCharged(t *testing.T) {
	t.Setenv("TOFI_TRUSTED_PROXIES", "127.0.0.1/32")
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	for i := 0; i < 30; i++ {
		if w := secLogin(s, "198.51.100.9", `not json`); w.Code != 400 {
			t.Fatalf("malformed %d: %d", i, w.Code)
		}
	}
	good := fmt.Sprintf(`{"identifier":"workspace-owner","password":%q}`, testOwnerPassword)
	if w := secLogin(s, "198.51.100.9", good); w.Code != 200 {
		t.Fatalf("malformed traffic spent the budget: %d", w.Code)
	}
}

func TestOneAccountIsBoundedAcrossAddresses(t *testing.T) {
	t.Setenv("TOFI_TRUSTED_PROXIES", "127.0.0.1/32")
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	limited := false
	for i := 0; i < accountAttemptLimit+2; i++ {
		body := `{"identifier":"workspace-owner","password":"wrong"}`
		if w := secLogin(s, fmt.Sprintf("203.0.113.%d", i+1), body); w.Code == 429 {
			limited = true
		}
	}
	if !limited {
		t.Fatal("guessing one account from many addresses was not bounded")
	}
}

func TestIPv6PeersShareTheirPrefixBucket(t *testing.T) {
	s, _ := ownerTestServer(t)
	mk := func(addr string) *http.Request {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = "[" + addr + "]:1"
		return r
	}
	if s.ownerAuth.peerBucket(mk("2001:db8:1:2::1")) != s.ownerAuth.peerBucket(mk("2001:db8:1:2:ffff::9")) {
		t.Fatal("addresses in one /64 got separate budgets")
	}
	if s.ownerAuth.peerBucket(mk("2001:db8:1:2::1")) == s.ownerAuth.peerBucket(mk("2001:db8:1:3::1")) {
		t.Fatal("different /64 prefixes share a budget")
	}
}

func TestClickApprovalIsExactAndSingleUse(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "reply to Alice", "click-claim")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	s.autoReviewProvider = &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return &provider.ChatResponse{Content: `{"decision":"confirm","reason":"stub"}`}, nil
	}}
	q, err := s.store.CreateQuestion(conv.ID, run, askQuestionInput{Question: "Send the reply?", Type: "approval", Approval: &ApprovalDetails{Action: "Click", Target: "Send the reply", Impact: "Sends mail"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.AnswerQuestion(q.ID, "user", true); err != nil {
		t.Fatal(err)
	}
	click := func(label string) error {
		_, err := s.guardAction(context.Background(), run, actionReview{Kind: "click", Effect: "send", Element: label})
		return err
	}
	for _, label := range []string{"Send", "Reply", "Send the reply and delete account"} {
		if click(label) == nil {
			t.Fatalf("%q passed on an approval for a different element", label)
		}
	}
	if err := click("  send  THE reply "); err != nil {
		t.Fatalf("exact (normalized) label must pass once: %v", err)
	}
	if click("Send the reply") == nil {
		t.Fatal("an approval allowed a second dispatch")
	}
}

func extensionOutcomeCode(err error) string {
	if o, ok := tooloutcome.FromError(err); ok {
		return o.Code
	}
	return ""
}

func TestExtensionChangeNeedsExactSingleUseHumanApproval(t *testing.T) {
	s, c, r, tool := extensionToolFixtureRaw(t)
	s.allowInternalMCPHosts = true
	call := func(tl Tool, args map[string]any) error {
		raw, _ := json.Marshal(args)
		_, err := tl.Execute(context.Background(), raw)
		return err
	}
	install := map[string]any{"action": "skill_install", "name": "gated", "files": map[string]string{"SKILL.md": "---\nname: gated\ndescription: fixture\n---\nbody"}}
	if code := extensionOutcomeCode(call(tool, install)); code != "extension_change_needs_approval" {
		t.Fatalf("unapproved install: %q", code)
	}
	if skills := installedSkills(s); len(skills) != 0 {
		t.Fatal("unapproved install wrote files")
	}
	// An approval for a different payload does not cover this one.
	other := map[string]any{"action": "skill_install", "name": "gated", "files": map[string]string{"SKILL.md": "---\nname: gated\ndescription: other\n---\nbody"}}
	var ox extensionToolArgs
	raw, _ := json.Marshal(other)
	_ = json.Unmarshal(raw, &ox)
	approveExtensionChange(t, s, c, r, ox)
	if code := extensionOutcomeCode(call(tool, install)); code != "extension_change_needs_approval" {
		t.Fatalf("approval for another payload was accepted: %q", code)
	}
	var x extensionToolArgs
	raw, _ = json.Marshal(install)
	_ = json.Unmarshal(raw, &x)
	approveExtensionChange(t, s, c, r, x)
	if err := call(tool, install); err != nil {
		t.Fatalf("approved install: %v", err)
	}
	del := map[string]any{"action": "skill_delete", "name": "gated"}
	if code := extensionOutcomeCode(call(tool, del)); code != "extension_change_needs_approval" {
		t.Fatalf("an install approval authorized a delete: %q", code)
	}
	// Reads need no approval.
	if err := call(tool, map[string]any{"action": "skill_list"}); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionChangeRefusedWhenNobodyAttends(t *testing.T) {
	s, c, r, _ := extensionToolFixtureRaw(t)
	s.allowInternalMCPHosts = true
	c.UserVisible = false
	tool := s.extensionManagementTools(c, r)[0]
	args := extensionToolArgs{Action: "mcp_create", Name: "x", URL: "https://example.invalid/mcp"}
	approveExtensionChange(t, s, c, r, args)
	raw, _ := json.Marshal(args)
	_, err := tool.Execute(context.Background(), raw)
	if code := extensionOutcomeCode(err); code != "extension_change_needs_owner" {
		t.Fatalf("hidden run: %q %v", code, err)
	}
}

func TestBotCreatedMCPServerCannotTargetInternalHosts(t *testing.T) {
	s, c, r, tool := extensionToolFixtureRaw(t)
	for _, u := range []string{"http://127.0.0.1:9/mcp", "http://10.0.0.5/mcp", "http://169.254.169.254/latest", "http://localhost:8321/mcp", "http://app:8321/mcp", "http://[::1]/mcp"} {
		x := extensionToolArgs{Action: "mcp_create", Name: "inner", URL: u}
		approveExtensionChange(t, s, c, r, x)
		raw, _ := json.Marshal(x)
		_, err := tool.Execute(context.Background(), raw)
		if code := extensionOutcomeCode(err); code != "mcp_internal_host" {
			t.Fatalf("%s: %q %v", u, code, err)
		}
	}
}
