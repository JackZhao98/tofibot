package deploy_test

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/app"
)

// In-process source fixture only. B=c0 and N=725 have identical production App
// code. This proves actual account/SQLite/handler semantics across reopen, not
// compatibility or provenance of distinct real images. No listener or Worker.
func TestD100CurrentDataPairSemantics(t *testing.T) {
	dir := t.TempDir()
	config := app.Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"}
	open := func() *app.AccountGateway {
		g, err := app.NewAccountGateway(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Close() })
		return g
	}
	request := func(g *app.AccountGateway, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		r.TLS = &tls.ConnectionState{}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w
	}
	login := func(g *app.AccountGateway, name string) (*http.Cookie, app.Account) {
		w := request(g, "POST", "/api/auth/login", map[string]string{"identifier": name, "password": "SyntheticPassword123!"}, nil)
		var session struct {
			Owner         app.Account `json:"owner"`
			Authenticated bool        `json:"authenticated"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil || !session.Authenticated || session.Owner.Username != name || len(w.Result().Cookies()) != 1 {
			t.Fatalf("fresh password login failed: status=%d", w.Code)
		}
		return w.Result().Cookies()[0], session.Owner
	}
	baseline := open()
	secret, err := os.ReadFile(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	setup := map[string]string{"bootstrap_secret": strings.TrimSpace(string(secret)), "username": "synthetic-pair-admin", "email": "pair@example.test", "password": "SyntheticPassword123!"}
	if w := request(baseline, "POST", "/api/auth/setup", setup, nil); w.Code != 200 {
		t.Fatalf("baseline setup status=%d", w.Code)
	}
	if err := baseline.Close(); err != nil {
		t.Fatal(err)
	}

	// N commits a new account and relational workspace records to CURRENT data.
	n := open()
	nCookie, admin := login(n, "synthetic-pair-admin")
	created := request(n, "POST", "/api/admin/accounts", map[string]string{"username": "synthetic-under-n", "password": "SyntheticPassword123!"}, nCookie)
	var newAccount app.Account
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &newAccount) != nil || newAccount.ID == "" {
		t.Fatalf("N account write status=%d", created.Code)
	}
	store, err := app.OpenStore(filepath.Join(dir, "accounts", admin.ID))
	if err != nil {
		t.Fatal(err)
	}
	bot, err := store.CreateBot("Synthetic N workspace", "", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := store.AddMessage(bot.DMConversationID, "user", "", "", "Synthetic structured current-data message ☃", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}

	// B reopens the SAME databases; no copy, restore, token injection or mock DB.
	b := open()
	bCookie, restoredAdmin := login(b, "synthetic-pair-admin")
	_, restoredAccount := login(b, "synthetic-under-n")
	if restoredAdmin.ID != admin.ID || restoredAccount.ID != newAccount.ID || restoredAccount.Role != "user" || !restoredAccount.MustChangePassword {
		t.Fatal("N-written account semantics changed")
	}
	bots := request(b, "GET", "/api/bots", nil, bCookie)
	var foundBots struct {
		Bots []app.Bot `json:"bots"`
	}
	if bots.Code != 200 || json.Unmarshal(bots.Body.Bytes(), &foundBots) != nil {
		t.Fatal("B could not read workspace bots")
	}
	found := false
	for _, current := range foundBots.Bots {
		if current.ID == bot.ID && current.Name == bot.Name && current.DMConversationID == bot.DMConversationID {
			found = true
		}
	}
	if !found {
		t.Fatal("N bot/conversation relations were not retained")
	}
	response := request(b, "GET", "/api/conversations/"+bot.DMConversationID+"/messages", nil, bCookie)
	var page struct {
		Messages []app.Message `json:"messages"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 1 {
		t.Fatal("B could not read N messages")
	}
	read := page.Messages[0]
	if read.ID != message.ID || read.ConversationID != message.ConversationID || read.Seq != message.Seq || read.Role != message.Role || read.Content != message.Content {
		t.Fatal("N-written message semantics changed")
	}
	session := request(b, "GET", "/api/auth/session", nil, nil)
	var state map[string]any
	if session.Code != 200 || json.Unmarshal(session.Body.Bytes(), &state) != nil || state["setup_required"] != false {
		t.Fatal("B reopened setup")
	}
	if w := request(b, "POST", "/api/auth/setup", setup, nil); w.Code != 401 {
		t.Fatalf("B consumed-secret replay status=%d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "owner-bootstrap.secret")); !os.IsNotExist(err) {
		t.Fatal("B revived bootstrap file")
	}

	observed := func(accountID string, m app.Message) map[string]any {
		return map[string]any{"account_id": accountID, "bot_id": bot.ID, "conversation_id": m.ConversationID, "message_id": m.ID, "message_seq": m.Seq, "message_role": m.Role, "message_content": m.Content}
	}
	checks := map[string]string{}
	for _, name := range []string{"n_data_written", "current_data_retained", "b_data_read", "b_login", "b_setup_closed", "b_consumed_replay_rejected"} {
		checks[name] = "pass"
	}
	result, err := json.Marshal(map[string]any{"schema": 1, "kind": "d100-current-data-semantic-result", "model": "account-workspace-messages-v1", "n_written": observed(newAccount.ID, message), "b_read": observed(restoredAccount.ID, read), "checks": checks})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("D100_PAIR_RESULT %s", result)
}
