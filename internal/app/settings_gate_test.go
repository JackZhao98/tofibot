package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// needApproval runs a set call that must be refused for approval and returns the token it names.
func (e *settingsEnv) needApproval(input string) string {
	e.t.Helper()
	_, o := e.call(input)
	if o == nil || o.Code != "settings_change_needs_approval" || o.Status != tooloutcome.NeedApproval || o.NextAction != "request_approval" {
		e.t.Fatalf("%s: want need_approval, got %+v", input, o)
	}
	start := strings.Index(o.Message, `"`)
	end := strings.Index(o.Message[start+1:], `"`)
	token := o.Message[start+1 : start+1+end]
	if !strings.HasPrefix(token, "setting ") || !strings.Contains(token, " #") {
		e.t.Fatalf("token %q", token)
	}
	return token
}

// decide asks the person about token through the request_approval tool and answers the card. It returns the card as the person saw it.
func (e *settingsEnv) decide(token string, approve bool) Question {
	e.t.Helper()
	approval := e.s.approvalTool(e.conv, e.run)
	raw, _ := json.Marshal(map[string]any{"question": "Change this setting?", "action": "Change a setting", "target": token, "impact": "As described"})
	done := make(chan error, 1)
	go func() {
		_, err := approval.Execute(context.Background(), raw)
		done <- err
	}()
	var q Question
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && q.ID == "" {
		qs, _ := e.s.store.ListQuestions(e.conv.ID)
		for _, cand := range qs {
			if cand.Approval != nil && cand.Approval.Target == token && cand.Status == questionPending {
				q = cand
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q.ID == "" {
		e.t.Fatal("the approval card was not created")
	}
	if _, _, err := e.s.store.AnswerQuestion(q.ID, "user", approve); err != nil {
		e.t.Fatal(err)
	}
	if err := <-done; err != nil {
		e.t.Fatal(err)
	}
	return q
}

func TestSettingsConfirmClassNeedsTheCardAndRunsOnce(t *testing.T) {
	e := newSettingsEnv(t)
	token := e.needApproval(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`)
	if mode, _ := e.s.store.getAutoReviewSettings(); mode.Mode != "off" {
		t.Fatalf("nothing may change before approval: %s", mode.Mode)
	}
	card := e.decide(token, true)

	// The card carries facts the server built: the model's own words stay secondary, and no remember or standing offer exists.
	f := card.Approval.Facts
	if f == nil || f.Kind != "setting_change" || f.Setting == nil {
		t.Fatalf("facts: %+v", f)
	}
	s := f.Setting
	if s.Key != "permissions.approval_mode" || s.Old != "ask" || s.New != "auto" || s.EffectCode != "approval_mode_auto" || s.Effect == "" || s.Link != "/settings/permissions#mode" || !s.Loosens || s.LabelKey != "settings:approval.label" {
		t.Fatalf("setting facts: %+v", s)
	}
	if card.Approval.Remember != nil || card.Approval.Standing != nil || card.Approval.Review != nil {
		t.Fatalf("a settings card offers no memory, standing allowance or reviewer: %+v", card.Approval)
	}
	out := e.mustCall(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`)
	if out["applied"] != true || out["approved"] != true {
		t.Fatalf("after approval: %+v", out)
	}
	if mode, _ := e.s.store.getAutoReviewSettings(); mode.Mode != "auto" {
		t.Fatalf("mode %s", mode.Mode)
	}
	rows := e.changes()
	if len(rows) != 1 || rows[0].Risk != "confirm" || rows[0].ApprovalQuestionID != card.ID || rows[0].Old != "ask" || rows[0].New != "auto" {
		t.Fatalf("audit: %+v", rows)
	}
	// A confirm-class change posts no Undo line of its own: the person decided it on the card.
	if rows[0].NoticeMessageID != "" {
		t.Fatalf("notice for a confirmed change: %+v", rows[0])
	}
	// The approval was for "auto" only: moving back is another payload and asks again.
	if back := e.needApproval(`{"action":"set","key":"permissions.approval_mode","value":"ask"}`); back == token {
		t.Fatal("a different payload shared a token")
	}
}

func TestSettingsApprovalIsBoundToTheExactPayload(t *testing.T) {
	e := newSettingsEnv(t)
	other, err := e.s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	first := e.needApproval(`{"action":"set","key":"bot.name","bot_id":"` + other.ID + `","value":"Alpha Two"}`)
	e.decide(first, true)
	// Another value, another Bot and another key all need their own card.
	second := e.needApproval(`{"action":"set","key":"bot.name","bot_id":"` + other.ID + `","value":"Something Else"}`)
	if second == first {
		t.Fatal("two payloads shared a token")
	}
	third := e.needApproval(`{"action":"set","key":"bot.instructions","bot_id":"` + other.ID + `","value":"Alpha Two"}`)
	if third == first {
		t.Fatal("two keys shared a token")
	}
	if b, _ := e.s.store.GetBot(other.ID); b.Name != "beta" {
		t.Fatalf("name changed without its own approval: %q", b.Name)
	}
	// The approved one runs, with the other Bot's name on the card; then it is spent.
	e.mustCall(`{"action":"set","key":"bot.name","bot_id":"` + other.ID + `","value":"Alpha Two"}`)
	if b, _ := e.s.store.GetBot(other.ID); b.Name != "Alpha Two" {
		t.Fatalf("name %q", b.Name)
	}
	again := e.needApproval(`{"action":"set","key":"bot.name","bot_id":"` + other.ID + `","value":"Beta Three"}`)
	if again == first {
		t.Fatal("a spent approval was reused")
	}
}

func TestSettingsDeclinedCardBlocksARetryInThisRun(t *testing.T) {
	e := newSettingsEnv(t)
	token := e.needApproval(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`)
	e.decide(token, false)
	e.refused(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`, "settings_change_denied")
	if mode, _ := e.s.store.getAutoReviewSettings(); mode.Mode != "off" {
		t.Fatalf("a declined change was applied: %s", mode.Mode)
	}
	// A different change is its own question.
	e.mustCall(`{"action":"set","key":"account.show_activity","value":true}`)
}

func TestSettingsCardNeedsAServerIssuedTokenAndModelFactsAreRefused(t *testing.T) {
	e := newSettingsEnv(t)
	approval := e.s.approvalTool(e.conv, e.run)
	ask := func(args map[string]any) error {
		raw, _ := json.Marshal(args)
		_, err := approval.Execute(context.Background(), raw)
		return err
	}
	err := ask(map[string]any{"question": "ok?", "action": "Change", "target": "setting permissions.approval_mode #000000000000", "impact": "none"})
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "setting_change_unknown" {
		t.Fatalf("a token the server never issued: %v", err)
	}
	token := e.needApproval(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`)
	if err := ask(map[string]any{"question": "ok?", "action": "Change", "target": token, "impact": "none", "facts": map[string]any{"kind": "setting_change", "name": "x"}}); err == nil {
		t.Fatal("request_approval accepted model-written facts")
	}
	// Another run does not hold the token either.
	bot2, _ := e.s.store.CreateBot("gamma", "", "model")
	_, run2, _, err2 := e.s.store.AddUserRun(bot2.DMConversationID, bot2.ID, "hi", "c2")
	if err2 != nil {
		t.Fatal(err2)
	}
	_, _ = e.s.store.SetRunStatus(run2.ID, "running", "")
	conv2, _ := e.s.store.GetConversation(bot2.DMConversationID)
	raw, _ := json.Marshal(map[string]any{"question": "ok?", "action": "Change", "target": token, "impact": "none"})
	_, err = e.s.approvalTool(conv2, run2).Execute(context.Background(), raw)
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "setting_change_unknown" {
		t.Fatalf("a token issued to another run: %v", err)
	}
}

func TestSettingsCardIsRefusedWhereNoPersonCanAnswer(t *testing.T) {
	e := newSettingsEnv(t)
	hidden := e.conv
	hidden.UserVisible = false
	raw, _ := json.Marshal(map[string]any{"question": "ok?", "action": "Change", "target": "setting x #1", "impact": "none"})
	_, err := e.s.approvalTool(hidden, e.run).Execute(context.Background(), raw)
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "conversation_not_user_visible" {
		t.Fatalf("hidden conversation: %v", err)
	}
	// And a confirm-class set from an unattended run is refused before any token exists.
	bare, _ := e.s.store.AddRun(e.bot.DMConversationID, e.bot.ID, "")
	_, _ = e.s.store.SetRunStatus(bare.ID, "running", "")
	_, err = e.s.settingsTool(e.conv, bare).Execute(context.Background(), json.RawMessage(`{"action":"set","key":"permissions.approval_mode","value":"auto"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "settings_change_needs_owner" {
		t.Fatalf("unattended confirm-class: %v", err)
	}
}

func TestSettingsConfirmKeysWriteThroughTheirStoreMethodsAfterApproval(t *testing.T) {
	e := newSettingsEnv(t)
	b, err := e.s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.s.store.CreateBot("charlie", "researcher", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.s.store.CreateGroup("review", []string{e.bot.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	addConnection(t, e.s, "notion")
	approved := func(input string) map[string]any {
		t.Helper()
		e.decide(e.needApproval(input), true)
		return e.mustCall(input)
	}

	approved(`{"action":"set","key":"account.primary_bot","value":"` + b.ID + `"}`)
	if id, _ := e.s.store.PrimaryBotID(); id != b.ID {
		t.Fatalf("primary %s", id)
	}
	approved(`{"action":"set","key":"bot.model","bot_id":"` + b.ID + `","value":"default"}`)
	if got, _ := e.s.store.GetBot(b.ID); got.Model != "default" {
		t.Fatalf("bot model %q", got.Model)
	}
	approved(`{"action":"set","key":"bot.connections","bot_id":"` + b.ID + `","value":[]}`)
	rows, _ := e.s.botConnectionAccess(b.ID)
	if len(rows) != 1 || rows[0].Enabled {
		t.Fatalf("connection access %+v", rows)
	}
	approved(`{"action":"set","key":"conversation.members","conversation_id":"` + g.ID + `","value":["` + e.bot.ID + `","` + b.ID + `","` + c.ID + `"]}`)
	if got, _ := e.s.store.GetConversation(g.ID); len(got.BotIDs) != 3 {
		t.Fatalf("members %v", got.BotIDs)
	}
	approved(`{"action":"set","key":"conversation.archived","conversation_id":"` + g.ID + `","value":true}`)
	if got, _ := e.s.store.GetConversation(g.ID); !got.Archived {
		t.Fatal("group not archived")
	}
	approved(`{"action":"set","key":"conversation.delete","conversation_id":"` + g.ID + `"}`)
	if _, err := e.s.store.GetConversation(g.ID); err == nil {
		t.Fatal("group not deleted")
	}
	approved(`{"action":"set","key":"bot.archived","bot_id":"` + c.ID + `","value":true}`)
	if got, _ := e.s.store.GetBot(c.ID); !got.Archived {
		t.Fatal("Bot not archived")
	}
	approved(`{"action":"set","key":"bot.delete","bot_id":"` + c.ID + `"}`)
	if _, err := e.s.store.GetBot(c.ID); err == nil {
		t.Fatal("Bot not deleted")
	}
	approved(`{"action":"set","key":"connection.disconnect","connection":"notion"}`)
	approved(`{"action":"set","key":"automation.remembered_approval_hours","value":48}`)
	if e.s.autoInt(autoRememberedApprovalHrs) != 48 {
		t.Fatal("approval window not set")
	}
	for _, row := range e.changes() {
		if row.Risk != "confirm" || row.ApprovalQuestionID == "" {
			t.Fatalf("every confirm-class change records its card: %+v", row)
		}
	}
}

func TestSettingsPrimaryAssistantIsNeverArchivedOrDeletedByABot(t *testing.T) {
	e := newSettingsEnv(t)
	b, _ := e.s.store.CreateBot("beta", "", "model")
	if err := e.s.store.SetPrimaryBot(b.ID); err != nil { // an idle Bot, so only the Primary rule can refuse
		t.Fatal(err)
	}
	primary, _ := e.s.store.PrimaryBotID()
	for _, in := range []string{
		`{"action":"set","key":"bot.archived","bot_id":"` + primary + `","value":true}`,
		`{"action":"set","key":"bot.delete","bot_id":"` + primary + `"}`,
	} {
		e.decide(e.needApproval(in), true)
		e.refused(in, "settings_primary_reassign_required")
	}
	if got, err := e.s.store.GetBot(primary); err != nil || got.Archived {
		t.Fatalf("the Primary Assistant was removed: %+v %v", got, err)
	}
}

func TestSettingsToolTierLooseningNeedsACardTighteningDoesNot(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	d := extensions.MCPToolDescriptor{Server: "notion", RemoteName: "search", Description: "Search", InputSchema: map[string]any{"type": "object"}}
	d.Digest = extensions.MCPToolDigest(d)
	if err := e.s.extensions.ObserveTools("notion", extensions.MCPServerConfig{URL: "https://notion.example.test/mcp"}, []extensions.MCPToolDescriptor{d}); err != nil {
		t.Fatal(err)
	}
	e.mustCall(`{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"search","value":"high_risk"}`)
	loosen := `{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"search","value":"read_only"}`
	card := e.decide(e.needApproval(loosen), true)
	if card.Approval.Facts.Setting.EffectCode != "tier_looser" || !card.Approval.Facts.Setting.Loosens || card.Approval.Facts.Setting.TargetName != "notion · search" {
		t.Fatalf("facts: %+v", card.Approval.Facts.Setting)
	}
	e.mustCall(loosen)
	if got := e.mustCall(`{"action":"get","key":"connection.tool_tier","connection":"notion","tool":"search"}`)["value"]; got != "read_only" {
		t.Fatalf("tier %v", got)
	}
}

func TestSettingsRevertOfAConfirmedChangeNeedsACardToo(t *testing.T) {
	e := newSettingsEnv(t)
	in := `{"action":"set","key":"permissions.approval_mode","value":"auto"}`
	e.decide(e.needApproval(in), true)
	e.mustCall(in)
	change := e.changes()[0]
	revert := `{"action":"revert","change_id":"` + change.ID + `"}`
	card := e.decide(e.needApproval(revert), true)
	if card.Approval.Facts.Setting.EffectCode != "revert" || card.Approval.Facts.Setting.New != "ask" {
		t.Fatalf("revert facts: %+v", card.Approval.Facts.Setting)
	}
	out := e.mustCall(revert)
	if out["applied"] != true || out["reverted"] != change.ID {
		t.Fatalf("revert: %+v", out)
	}
	if mode, _ := e.s.store.getAutoReviewSettings(); mode.Mode != "off" {
		t.Fatalf("mode after revert %s", mode.Mode)
	}
	got, _ := e.s.store.settingsChange(change.ID)
	if got.RevertedBy == "" {
		t.Fatal("the original change is not marked reverted")
	}
}

func TestSettingsApprovalIsGivenBackWhenTheChangeFailsBeforeAnyEffect(t *testing.T) {
	e := newSettingsEnv(t)
	other, _ := e.s.store.CreateBot("beta", "", "model")
	in := `{"action":"set","key":"bot.model","bot_id":"` + other.ID + `","value":"model-that-is-not-configured"}`
	token := e.needApproval(in)
	e.decide(token, true)
	// The model is refused by validation before anything changes, so the person's decision is not spent.
	_, o := e.call(in)
	if o == nil || o.Certainty != "not_executed" || o.Code == "settings_change_needs_approval" {
		t.Fatalf("invalid model: %+v", o)
	}
	if got, _ := e.s.store.GetBot(other.ID); got.Model != "model" {
		t.Fatalf("model changed: %q", got.Model)
	}
	var claims int
	_ = e.s.store.db.QueryRow(`SELECT COUNT(*) FROM click_approval_claims`).Scan(&claims)
	if claims != 0 {
		t.Fatalf("a refused change kept the approval spent (%d claims)", claims)
	}
}
