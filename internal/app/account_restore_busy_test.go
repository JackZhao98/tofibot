package app

import (
	"encoding/json"
	"testing"
)

// A single-account restore replaces the account's data, so it first waits for the account's Bot
// runs that are working (queued or running) instead of cutting them off mid-step: while one is
// active the restore and the undo are refused by name with the count, nothing is changed, and the
// run goes on. `interrupt: true` is the explicit choice to stop them. A run parked on a question or a
// command (`waiting`) does not hold the restore: replacing the data ends it either way.
func TestAccountRestoreWaitsForTheAccountsRuns(t *testing.T) {
	b := newBackupFixture(t)
	hostBackupFixture(t, b)
	ws, err := b.g.workspace(b.member)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := ws.store.CreateBot("first", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	st := b.run(t)
	if _, err = ws.store.CreateBot("later", "", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err = ws.store.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES('run_live',?,?,'running',?,?),('run_parked',?,?,'waiting',?,?)`,
		bot.DMConversationID, bot.ID, now(), now(), bot.DMConversationID, bot.ID, now(), now()); err != nil {
		t.Fatal(err)
	}
	restore := `{"snapshot":"` + st.LastSnapshot + `","confirm_username":"org-member"`
	out := b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore", restore+`}`, 409)
	if code := orgErrCode(t, out); code != "account_busy" {
		t.Fatalf("code %s", code)
	}
	var body struct{ Error struct{ Runs int } }
	if json.Unmarshal([]byte(out), &body) != nil || body.Error.Runs != 1 {
		t.Fatalf("count %s", out)
	}
	ws, err = b.g.workspace(b.member)
	if err != nil {
		t.Fatalf("the refused restore closed the workspace: %v", err)
	}
	var status string
	ws.store.db.QueryRow(`SELECT status FROM runs WHERE id='run_live'`).Scan(&status)
	if bots, _ := ws.store.ListBots(); len(bots) != 2 || status != "running" {
		t.Fatalf("changed by a refused restore: %d bots, run %s", len(bots), status)
	}
	// The host channel refuses the same way.
	w := hostCall(b.g, "POST", "/api/host/backup/account-restore", `{"account":"org-member","snapshot":"`+st.LastSnapshot+`"}`, bearer())
	if w.Code != 409 || orgErrCode(t, w.Body.String()) != "account_busy" {
		t.Fatalf("host %d %s", w.Code, w.Body.String())
	}
	// Interrupting is the explicit choice.
	b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore", restore+`,"interrupt":true}`, 200)
	ws, _ = b.g.workspace(b.member)
	if bots, _ := ws.store.ListBots(); len(bots) != 1 {
		t.Fatalf("after restore: %d bots", len(bots))
	}
	// Undo waits the same way.
	if _, err = ws.store.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES('run_after',?,?,'queued',?,?)`, bot.DMConversationID, bot.ID, now(), now()); err != nil {
		t.Fatal(err)
	}
	if code := orgErrCode(t, b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore/undo", `{}`, 409)); code != "account_busy" {
		t.Fatalf("undo code %s", code)
	}
	b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore/undo", `{"interrupt":true}`, 200)
	ws, _ = b.g.workspace(b.member)
	if bots, _ := ws.store.ListBots(); len(bots) != 2 {
		t.Fatalf("after undo: %d bots", len(bots))
	}
}
