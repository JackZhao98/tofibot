package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/mailread"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestTrustedMailEndpointFrozenBackendOrigin(t *testing.T) {
	const operator = "http://runner.fixture.test:8080/base"
	const drift = "http://other.fixture.test:8080/base"
	for _, mode := range []string{"explicit", "environment", "tenant", "microvm", "tenant_without_origin", "absent", "invalid", "pseudo_without_microvm"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TOFI_MCP_RUNNER_URL", operator)
			s := &Server{localRunnerTokenFile: "/synthetic-never-read-token"}
			wantBase := operator
			switch mode {
			case "explicit":
				s.localRunnerURL = operator
			case "environment":
			case "tenant":
				s.isolatedWorkspace = true
				s.localRunnerURL = operator
			case "microvm":
				s.isolatedWorkspace = true
				s.localRunnerURL = operator
				s.microVM = &computer.Client{}
				wantBase = computer.RunnerOrigin
			case "tenant_without_origin":
				s.isolatedWorkspace = true
				wantBase = ""
			case "absent":
				t.Setenv("TOFI_MCP_RUNNER_URL", "")
				wantBase = ""
			case "invalid":
				s.localRunnerURL = "http://user@runner.fixture.test:8080/base"
				wantBase = ""
			case "pseudo_without_microvm":
				s.isolatedWorkspace = true
				s.localRunnerURL = computer.RunnerOrigin
				wantBase = ""
			}
			s.snapshotLocalRunnerMCP()
			if s.localRunnerMCPHTTP != nil {
				t.Cleanup(s.localRunnerMCPHTTP.CloseIdleConnections)
			}
			// Mutation cannot redefine this server's read-provenance authority.
			t.Setenv("TOFI_MCP_RUNNER_URL", drift)
			s.localRunnerURL = drift
			if s.trustedMailEndpoint(drift+"/mcp/gog", "gog") {
				t.Fatal("origin drift rebound trusted mail")
			}
			if wantBase == "" {
				if s.trustedMailEndpoint(operator+"/mcp/gog", "gog") || s.trustedMailEndpoint(computer.RunnerOrigin+"/mcp/gog", "gog") {
					t.Fatal("absent/invalid tenant origin acquired trust")
				}
				return
			}
			if !s.trustedMailEndpoint(wantBase+"/mcp/gog", "gog") {
				t.Fatal("frozen backend origin not trusted")
			}
			if mode == "microvm" && s.trustedMailEndpoint(operator+"/mcp/gog", "gog") {
				t.Fatal("microVM precedence lost")
			}
			for _, suffix := range []string{"/mcp/other", "/mcp/gog/extra", "/mcp/gog?", "/mcp/gog?x=y", "/mcp/gog#fragment", "/mcp/%67og", "/mcp/../gog", "/v1/plugins", "/mcp/gog\\extra"} {
				if s.trustedMailEndpoint(wantBase+suffix, "gog") {
					t.Fatalf("non-exact endpoint trusted: %s", suffix)
				}
			}
			for _, id := range []string{"", "../gog", "gog/other", "gog?x", "gog#x"} {
				if s.trustedMailEndpoint(wantBase+"/mcp/"+id, id) {
					t.Fatalf("invalid connection trusted: %q", id)
				}
			}
			for _, endpoint := range []string{"https" + strings.TrimPrefix(wantBase, "http") + "/mcp/gog", "http://foreign.fixture.test/mcp/gog", "http://user@" + strings.TrimPrefix(wantBase, "http://") + "/mcp/gog", "http://runner.fixture.test:8081/base/mcp/gog", "mailto:gog@example.test", ":malformed"} {
				if s.trustedMailEndpoint(endpoint, "gog") {
					t.Fatalf("foreign/malformed origin trusted: %q", endpoint)
				}
			}
		})
	}
}

func mailFixture(t *testing.T) (*Server, Conversation, Run, []MailSource) {
	t.Helper()
	s, e := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return mailFixtureOnServer(t, s)
}
func mailFixtureOnServer(t *testing.T, s *Server) (*Server, Conversation, Run, []MailSource) {
	t.Helper()
	bot, e := s.store.CreateBot("Synthetic Mail Bot", "", "model")
	if e != nil {
		t.Fatal(e)
	}
	conv, e := s.store.GetConversation(bot.DMConversationID)
	if e != nil {
		t.Fatal(e)
	}
	_, run, _, e := s.store.AddUserRun(conv.ID, bot.ID, "Show previously returned synthetic emails", "mail-fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.SetRunStatus(run.ID, "running", ""); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../mailread/testdata/gog-v0400-search.json")
	if e != nil {
		t.Fatal(e)
	}
	recordPinnedMailFixture(t, s, conv, run, "read-fixture", "reader-a@example.test", "gmail_search", raw)
	refs := []MailSource{}
	for i := 0; i < 3; i++ {
		refs = append(refs, MailSource{RunID: run.ID, CallID: "read-fixture", MessageID: fmt.Sprintf("synthetic-%d", i)})
	}
	raw, e = os.ReadFile("../mailread/testdata/gog-v0400-get-message.json")
	if e != nil {
		t.Fatal(e)
	}
	recordPinnedMailFixture(t, s, conv, run, "read-fixture-other-mailbox", "reader-b@example.test", "gmail_get_message", raw)
	refs = append(refs, MailSource{RunID: run.ID, CallID: "read-fixture-other-mailbox", MessageID: "synthetic-0"})
	return s, conv, run, refs
}

// Synthetic fixtures model the read boundary. No mailbox or credentials used.
func recordPinnedMailFixture(t *testing.T, s *Server, c Conversation, r Run, call, mailbox, tool string, raw []byte) {
	t.Helper()
	var root any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid pinned fixture")
	}
	args, _ := json.Marshal(map[string]any{"name": "mcp_local_synthetic_gog__" + tool, "arguments": map[string]any{}})
	for _, status := range []string{"queued", "running"} {
		if err := s.store.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call, Name: "call_mcp_tool", Arguments: string(args), Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := mailread.NormalizeGog(mailread.Identity{Version: 1, Provider: "gmail", Connection: "synthetic-gog", Mailbox: mailbox}, tool, root, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.recordMailRead(context.Background(), c.ID, r.BotID, r.ID, call, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.store.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call, Name: "call_mcp_tool", Arguments: string(args), Status: "completed", Result: string(raw)}); err != nil {
		t.Fatal(err)
	}
}
func mailTool(t *testing.T, s *Server, c Conversation, r Run, name string) Tool {
	t.Helper()
	for _, tool := range s.displayTools(c, r) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing %s", name)
	return Tool{}
}
func presentFixture(t *testing.T, s *Server, c Conversation, r Run, refs []MailSource) Message {
	t.Helper()
	items := []map[string]any{}
	for i, ref := range refs {
		items = append(items, map[string]any{"source": ref, "summary": "Synthetic AI note", "priority": i == 0, "tag": "需回复"})
	}
	raw, _ := json.Marshal(map[string]any{"title": "Synthetic four emails", "emails": items})
	out, e := mailTool(t, s, c, r, "display_emails").Execute(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		ID string `json:"presentation_id"`
	}
	if json.Unmarshal([]byte(out), &result) != nil {
		t.Fatal(out)
	}
	m, e := s.store.mailPresentation(context.Background(), c.ID, result.ID)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestMailPresentationVerifiedIdentityProgressiveDetailAndReplay(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	m := presentFixture(t, s, c, r, refs)
	p := m.Card.Mail
	if len(p.Emails) != 4 {
		t.Fatal(p)
	}
	if p.Emails[0].Key == p.Emails[1].Key || p.Emails[0].Key == p.Emails[3].Key {
		t.Fatal("message/account identity collapsed")
	}
	if strings.Contains(m.Content, "<script>") || strings.Contains(string(mustJSON(m.Card)), "Synthetic body") {
		t.Fatal("tray exposed body before selection")
	}
	detail, e := s.store.presentedDetail(context.Background(), c.ID, m.ID, p.Emails[0].Key, 1)
	if e != nil {
		t.Fatal(e)
	}
	if !detail.BodyAvailable || !strings.Contains(detail.Body, "Synthetic body 0") || detail.Email.Source.Digest == "" || detail.Email.RetrievedAt == "" {
		t.Fatal(detail)
	}
	if len(detail.Attachments) != 1 || detail.Attachments[0].Name != "synthetic.txt" || detail.Attachments[0].URL != "" {
		t.Fatal("active URL accepted")
	}
	missing, e := s.store.presentedDetail(context.Background(), c.ID, m.ID, p.Emails[2].Key, 1)
	if e != nil || missing.BodyAvailable || missing.Body != "" {
		t.Fatalf("missing body fabricated: %+v %v", missing, e)
	}
	if _, e = s.store.selectPresentedEmail(context.Background(), c.ID, r, m.ID, p.Emails[1].Key, 1, "synthetic-open-one"); e != nil {
		t.Fatal(e)
	}
	events1, _ := s.store.Events(c.ID, 0)
	if _, e = s.store.selectPresentedEmail(context.Background(), c.ID, r, m.ID, p.Emails[1].Key, 1, "synthetic-open-one"); e != nil {
		t.Fatal(e)
	}
	events2, _ := s.store.Events(c.ID, 0)
	if len(events1) != len(events2) {
		t.Fatal("duplicate selection produced another event")
	}
	replay, e := s.store.mailPresentation(context.Background(), c.ID, m.ID)
	if e != nil || replay.Card.Mail.SelectedKey != p.Emails[1].Key || replay.Card.Mail.Revision != 2 {
		t.Fatalf("selection not durable: %+v %v", replay, e)
	}
	if _, e = s.store.presentedDetail(context.Background(), c.ID, m.ID, p.Emails[0].Key, 1); e == nil {
		t.Fatal("stale revision accepted")
	}
	if _, e = s.store.selectPresentedEmail(context.Background(), c.ID, r, m.ID, p.Emails[3].Key, 1, "synthetic-open-stale"); e == nil {
		t.Fatal("stale new selection accepted")
	}
}
func mustJSON(value any) []byte {
	data, e := json.Marshal(value)
	if e != nil {
		panic(e)
	}
	return data
}
func TestMailPresentationSourceAndScopeFailClosed(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	m := presentFixture(t, s, c, r, refs)
	other, e := s.store.CreateBot("Other synthetic bot", "", "model")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.resolvePresentedEmail(context.Background(), other.DMConversationID, r.BotID, refs[0]); e == nil {
		t.Fatal("cross-conversation source accepted")
	}
	if _, e = s.store.resolvePresentedEmail(context.Background(), c.ID, other.ID, refs[0]); e == nil {
		t.Fatal("cross-bot source accepted")
	}
	otherServer, e := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if e != nil {
		t.Fatal(e)
	}
	defer otherServer.Close()
	if _, e = otherServer.store.mailPresentation(context.Background(), c.ID, m.ID); e == nil {
		t.Fatal("cross-account presentation accepted")
	}
	for _, change := range []struct {
		name  string
		sql   string
		value any
	}{{"failed", "UPDATE tool_activities SET status=? WHERE call_id='read-fixture'", "failed"}, {"truncated", "UPDATE tool_activities SET truncated=? WHERE call_id='read-fixture'", 1}, {"changed", "UPDATE tool_activities SET result=? WHERE call_id='read-fixture'", `{"emails":[]}`}, {"untrusted presentation", "UPDATE tool_activities SET name=? WHERE call_id='read-fixture'", "display_emails"}} {
		t.Run(change.name, func(t *testing.T) {
			if _, e := s.store.db.Exec(`SAVEPOINT mail_source`); e != nil {
				t.Fatal(e)
			}
			defer s.store.db.Exec(`ROLLBACK TO mail_source; RELEASE mail_source`)
			s.store.db.Exec(change.sql, change.value)
			if _, e := s.store.presentedDetail(context.Background(), c.ID, m.ID, m.Card.Mail.Emails[0].Key, 1); e == nil {
				t.Fatal("unavailable source accepted")
			}
		})
	}
	if _, e = s.store.presentedDetail(context.Background(), c.ID, m.ID, "unknown", 1); e == nil {
		t.Fatal("unknown key accepted")
	}
	if _, e = s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, MailSource{RunID: r.ID, CallID: "read-fixture", MessageID: "unknown-message"}); e == nil {
		t.Fatal("invalid pointer accepted")
	}
	if _, e = mailTool(t, s, c, r, "display_content").Execute(context.Background(), mustJSON(m.Card)); e == nil {
		t.Fatal("legacy tool bypassed source resolver")
	}
	duplicate := []MailSource{refs[0], refs[0]}
	items := []map[string]any{}
	for _, ref := range duplicate {
		items = append(items, map[string]any{"source": ref})
	}
	if _, e = mailTool(t, s, c, r, "display_emails").Execute(context.Background(), mustJSON(map[string]any{"emails": items})); e == nil {
		t.Fatal("duplicate message accepted")
	}
	if _, e = s.store.SetRunStatus(r.ID, "cancelled", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = mailTool(t, s, c, r, "display_emails").Execute(context.Background(), mustJSON(map[string]any{"emails": []any{}})); e == nil {
		t.Fatal("cancelled run published")
	}
}
func TestMailPresentationScopedReadRouteHasNoMailMutation(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	m := presentFixture(t, s, c, r, refs)
	path := fmt.Sprintf("/api/conversations/%s/mail-presentations/%s/%s?revision=1", c.ID, m.ID, m.Card.Mail.Emails[0].Key)
	before, _ := s.store.Events(c.ID, 0)
	w := httptest.NewRecorder()
	s.route(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "Synthetic body 0") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		w = httptest.NewRecorder()
		s.route(w, httptest.NewRequest(method, path, nil))
		if w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.route(w, httptest.NewRequest("GET", strings.Replace(path, c.ID, "other-conversation", 1), nil))
	if w.Code != 404 {
		t.Fatal("cross conversation", w.Code)
	}
	after, _ := s.store.Events(c.ID, 0)
	if len(before) != len(after) {
		t.Fatal("display GET changed timeline")
	}
	var drafts int
	s.store.db.QueryRow("SELECT COUNT(*) FROM mail_drafts").Scan(&drafts)
	if drafts != 0 {
		t.Fatal("presentation created mail draft")
	}
}
func TestMailPresentationEmptyAndBounds(t *testing.T) {
	s, c, r, _ := mailFixture(t)
	m := presentFixture(t, s, c, r, nil)
	if m.Card.Mail.Emails == nil || len(m.Card.Mail.Emails) != 0 {
		t.Fatal("empty tray missing")
	}
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,hello", "http://example.test", "https://user:password@example.test", "https://example.test\n/x"} {
		if safeMailURL(bad) != "" {
			t.Fatal(bad)
		}
	}

}

func TestMailPresentationAuthenticatedAccountAndRevocation(t *testing.T) {
	g := accountFixture(t)
	a, err := g.create(context.Background(), "synthetic-mail-owner", "owner@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.create(context.Background(), "synthetic-mail-other", "other@example.test", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, b.ID); err != nil {
		t.Fatal(err)
	}
	b.MustChangePassword = false
	own, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	_, c, r, refs := mailFixtureOnServer(t, own)
	m := presentFixture(t, own, c, r, refs)
	path := fmt.Sprintf("/api/conversations/%s/mail-presentations/%s/%s?revision=1", c.ID, m.ID, m.Card.Mail.Emails[0].Key)
	ownerCookie := accountCookie(t, g, a)
	otherCookie := accountCookie(t, g, b)
	if response := accountRequest(g, "GET", path, "", ownerCookie); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, cookie := range []*http.Cookie{nil, otherCookie} {
		response := accountRequest(g, "GET", path, "", cookie)
		if response.Code < 400 || strings.Contains(response.Body.String(), "Synthetic body") {
			t.Fatal("foreign cached email access", response.Code, response.Body.String())
		}
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	response := accountRequest(g, "GET", path, "", ownerCookie)
	if response.Code < 400 || strings.Contains(response.Body.String(), "Synthetic body") {
		t.Fatal("revoked account retained access", response.Code)
	}
}
func TestMailPresentationMissingMailboxOrUnstructuredSourceRejected(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	for _, result := range []string{`{"emails":[{"id":"fake","from":"fixture@example.test","subject":"No mailbox"}]}`, `unstructured prose is not an email result`} {
		if _, err := s.store.db.Exec(`UPDATE tool_activities SET result=? WHERE call_id='read-fixture'`, result); err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, refs[0]); err == nil {
			t.Fatal("unproven mailbox/source accepted")
		}
	}
}

func TestMailPresentationCanonicalFactsAndIdentity(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	search, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../mailread/testdata/gog-v0400-get-message.json")
	if err != nil {
		t.Fatal(err)
	}
	recordPinnedMailFixture(t, s, c, r, "read-same-mailbox-get", "reader-a@example.test", "gmail_get_message", raw)
	get, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, MailSource{RunID: r.ID, CallID: "read-same-mailbox-get", MessageID: "synthetic-0"})
	if err != nil {
		t.Fatal(err)
	}
	if search.Email.Key != get.Email.Key || get.Email.Account != "reader-a@example.test" || get.Email.From != "Sender <sender@example.test>" || get.Email.Subject != "Same synthetic subject" {
		t.Fatal("search/get canonical identity/facts differ", search.Email, get.Email)
	}
	other, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, refs[3])
	if err != nil {
		t.Fatal(err)
	}
	if other.Email.Key == get.Email.Key {
		t.Fatal("authenticated mailboxes collapsed")
	}
	for _, ref := range []MailSource{{RunID: r.ID, CallID: "read-fixture", MessageID: "synthetic-0", Fields: map[string]string{"account_id": "/subject", "from": "/to", "subject": "/from"}}, {RunID: r.ID, CallID: "read-fixture", MessageID: "synthetic-0", Pointer: "/stdout/messages/0"}} {
		if _, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, ref); err == nil {
			t.Fatal("model field substitution accepted")
		}
	}
	forged := mustJSON(map[string]any{"emails": []any{map[string]any{"source": refs[0], "from": "forged sender"}}})
	if _, err := mailTool(t, s, c, r, "display_emails").Execute(context.Background(), forged); err == nil {
		t.Fatal("model original facts accepted")
	}
	// Byte-compatible mail-shaped data in an ordinary result has no trusted receipt.
	for _, status := range []string{"queued", "running", "completed"} {
		event := runtime.ToolEvent{CallID: "non-mail-row", Name: "call_mcp_tool", Arguments: `{"name":"mcp_sheets_read_range"}`, Status: status}
		if status == "completed" {
			event.Result = string(raw)
		}
		if err := s.store.RecordToolEvent(c.ID, r.BotID, r.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.resolvePresentedEmail(context.Background(), c.ID, r.BotID, MailSource{RunID: r.ID, CallID: "non-mail-row", MessageID: "synthetic-0"}); err == nil {
		t.Fatal("non-mail result accepted")
	}
}
func TestMailPresentationFreshIntentReopensAndRetryDoesNot(t *testing.T) {
	s, c, r, refs := mailFixture(t)
	m := presentFixture(t, s, c, r, refs)
	key := m.Card.Mail.Emails[1].Key
	first, err := s.store.selectPresentedEmail(context.Background(), c.ID, r, m.ID, key, 1, "open-first")
	if err != nil {
		t.Fatal(err)
	}
	if first.Card.Mail.Revision != 2 {
		t.Fatal(first)
	}
	// Local Close/Escape or switching another envelope does not change server state.
	if _, err = s.store.SetRunStatus(r.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	_, r2, _, err := s.store.AddUserRun(c.ID, r.BotID, "Reopen same previously returned synthetic email", "new-open-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(r2.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.Events(c.ID, 0)
	reopened, err := s.store.selectPresentedEmail(context.Background(), c.ID, r2, m.ID, key, 2, "open-new")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.store.Events(c.ID, 0)
	if reopened.Card.Mail.Revision != 3 || len(after) != len(before)+1 {
		t.Fatal("fresh intent did not publish revision/event")
	}
	retry, err := s.store.selectPresentedEmail(context.Background(), c.ID, r2, m.ID, key, 2, "open-new")
	if err != nil {
		t.Fatal(err)
	}
	afterRetry, _ := s.store.Events(c.ID, 0)
	if len(afterRetry) != len(after) || retry.Card.Mail.Revision != 3 {
		t.Fatal("retry created duplicate open")
	}
	if _, err = s.store.selectPresentedEmail(context.Background(), c.ID, r2, m.ID, key, 2, "another-open"); err == nil {
		t.Fatal("stale fresh intent accepted")
	}
	switched, err := s.store.selectPresentedEmail(context.Background(), c.ID, r2, m.ID, key, 3, "after-local-switch")
	if err != nil || switched.Card.Mail.Revision != 4 {
		t.Fatal("same run fresh intent failed", err)
	}
	if _, err = s.store.selectPresentedEmail(context.Background(), c.ID, r2, m.ID, m.Card.Mail.Emails[0].Key, 2, "open-new"); err == nil {
		t.Fatal("retry arguments changed")
	}
}
