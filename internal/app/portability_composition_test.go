package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Finite composition acceptance: disposable stores, synthetic values and stub
// summary output only. No listener, real account, grant or provider is used.
func portableCompositionHistory(t *testing.T, mode string) (*Store, Bot, Conversation, portableBundle, string) {
	t.Helper()
	store, _, bot, c := provenanceFixture(t)
	private := "Synthetic parent-only event payload never shared with the delegate."
	var run Run
	var err error
	if mode == "native" {
		_, run, _, err = store.AddUserRun(c.ID, bot.ID, "Synthetic original native instruction.", "composition-native")
	} else {
		_, run, err = store.AdmitWebhook(context.Background(), webhookBindFixture(t, store, c.ID), webhookEnvelope{EventID: "composition-source", Content: private}, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if mode == "delegated" || mode == "retry" {
		child, createErr := store.CreateBot("Synthetic portable delegate", "Keep scope.", "synthetic-model")
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, run, err = store.AddForwardHandoff(c.ID, bot.ID, child.ID, run.ID, "Synthetic externally derived assignment.")
		if err != nil {
			t.Fatal(err)
		}
		bot = child
		c, err = store.GetConversation(child.DMConversationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "retry" {
		if _, err = store.SetRunStatus(run.ID, "failed", "synthetic retry"); err != nil {
			t.Fatal(err)
		}
		run, err = store.RetryRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	output, _, err := store.AddMessage(c.ID, "assistant", bot.ID, run.ID, "Synthetic original result 中文 with exact whitespace.  ", "")
	if err != nil {
		t.Fatal(err)
	}
	setProvenanceKind(t, store, output, "progress")
	if _, err = store.SetRunStatus(run.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	_, human, _, err := store.AddUserRun(c.ID, bot.ID, "Synthetic later native source request.", "composition-later-source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetRunStatus(human.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := store.exportPortable(ctx, "synthetic-composition-source", portableSelection{Categories: []string{"bot_config", "chats"}, BotIDs: []string{bot.ID}}, "bot")
	if err != nil {
		t.Fatal("bounded export failed", err)
	}
	return store, bot, c, b, private
}

func portableCompositionApply(t *testing.T, destination *Store, b portableBundle) (portableBundle, portableResult) {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parsePortableBundle(raw)
	if err != nil {
		t.Fatal("strict parse failed", err)
	}
	selection := portableSelection{Categories: parsed.Included}
	if len(parsed.VaultEnvironment) > 0 {
		selection = portableSensitiveSelection(parsed)
	}
	selected, err := selectPortable(parsed, selection)
	if err != nil {
		t.Fatal("selection failed", err)
	}
	preview, err := destination.previewPortable(context.Background(), selected)
	if err != nil {
		t.Fatal("preview failed", err)
	}
	result, err := destination.applyPortable(context.Background(), selected, preview.ID)
	if err != nil {
		t.Fatal("apply failed", err)
	}
	replay, err := destination.applyPortable(context.Background(), selected, preview.ID)
	if err != nil || !reflect.DeepEqual(result, replay) {
		t.Fatal("receipt changed on retry", err)
	}
	return selected, result
}

func TestPortableCompositionHistoryRoundTrip(t *testing.T) {
	for _, mode := range []string{"native", "direct", "delegated", "retry"} {
		t.Run(mode, func(t *testing.T) {
			source, bot, c, b, private := portableCompositionHistory(t, mode)
			if len(b.Bots) != 1 || len(b.Conversations) != 1 || b.Conversations[0].ID != c.ID {
				t.Fatal("selected descendant scope broadened")
			}
			for _, exported := range b.Messages {
				original, err := source.GetMessage(exported.ID)
				if err != nil || exported.Role != original.Role || exported.Content != original.Content || exported.Origin.Kind != original.Kind || exported.Origin.RunID != original.RunID {
					t.Fatal("export changed original role/content/origin", err)
				}
				external, err := source.webhookRunOrigin(original.RunID)
				if err != nil || (exported.Kind == messageKindWebhookEvent) != external {
					t.Fatal("actual ancestry discriminator was lost or invented", err)
				}
				if (mode == "delegated" || mode == "retry") && strings.Contains(exported.Content, private) {
					t.Fatal("export copied private ancestor content")
				}
			}
			destination, server, _, _ := provenanceFixture(t)
			selected, result := portableCompositionApply(t, destination, b)
			if webhookCount(t, destination, "user_message_ingress") != 0 || webhookCount(t, destination, "runs") != 0 {
				t.Fatal("import fabricated native ingress or runs")
			}
			for _, exported := range selected.Messages {
				id := result.IDMap[exported.ID]
				got, err := destination.GetMessage(id)
				if err != nil || id == exported.ID || got.Role != exported.Role || got.Content != exported.Content || got.Kind != exported.Kind || got.RunID != "" {
					t.Fatal("import changed content/role/kind or restored a run", err)
				}
				var origin portableOrigin
				var raw string
				if err = destination.db.QueryRow(`SELECT source_json FROM portability_provenance WHERE kind='message' AND target_id=?`, id).Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &origin) != nil || !reflect.DeepEqual(origin, exported.Origin) {
					t.Fatal("durable inert origin was changed", err)
				}
				if got.Kind == messageKindWebhookEvent {
					hits, err := destination.Search(got.ConversationID, got.Content, 10)
					if err != nil || !strings.Contains(boundedSearchJSON(hits), webhookEventContextGuidance[:40]) {
						t.Fatal("search lost imported external frame", err)
					}
				}
			}
			newBot, err := destination.GetBot(result.IDMap[bot.ID])
			if err != nil {
				t.Fatal(err)
			}
			newC, err := destination.GetConversation(newBot.DMConversationID)
			if err != nil {
				t.Fatal(err)
			}
			again, err := destination.exportPortable(context.Background(), "synthetic-destination", portableSelection{Categories: b.Included, BotIDs: []string{newBot.ID}}, "bot")
			if err != nil || len(again.Messages) != len(selected.Messages) {
				t.Fatal("re-export failed", err)
			}
			for i, m := range again.Messages {
				old := selected.Messages[i]
				if m.ID != result.IDMap[old.ID] || m.Kind != old.Kind || m.Role != old.Role || m.Content != old.Content || !reflect.DeepEqual(m.Origin, old.Origin) {
					t.Fatal("external discriminator or inert origin changed on re-export")
				}
			}
			// Force the imported history into synthetic long-term summarization.
			for i := 0; i < keepRecentMessages+2; i++ {
				if _, _, err = destination.AddMessage(newC.ID, "assistant", newBot.ID, "", "Synthetic neutral history.", ""); err != nil {
					t.Fatal(err)
				}
			}
			human, run, _, err := destination.AddUserRun(newC.ID, newBot.ID, "Synthetic actual human request after import.", "composition-destination-human")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = destination.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			x, _, err := server.readMCPReviewContext(destination.db, newC, run)
			consent := mcpAuthorizationSources(x)
			if err != nil || len(consent) != 1 || consent[0].MessageID != human.ID || !server.botProfileRunEligible(newC, run) {
				t.Fatal("imported history gained consent or tainted later human authority", err)
			}
			messages, system := server.buildContext(newC, run, newBot)
			resultProjected := false
			for _, m := range messages {
				if strings.Contains(m.Content, "Synthetic original result") {
					resultProjected = true
					if mode != "native" && !strings.Contains(m.Content, webhookEventContextGuidance) {
						t.Fatal("model history lost imported external frame")
					}
				}
				if (mode == "delegated" || mode == "retry") && strings.Contains(m.Content, private) {
					t.Fatal("model history leaked private ancestor")
				}
			}
			if !resultProjected {
				t.Fatal("imported result was absent from model history")
			}
			if strings.Contains(system, webhookRunOriginGuidance) {
				t.Fatal("history tainted the later human run")
			}
			engine := &webhookSummaryRecorder{}
			if _, err = server.prepareLongTermContext(context.Background(), engine, newC, run, newBot); err != nil || len(engine.requests) == 0 {
				t.Fatal("synthetic summary missing", err)
			}
			transcript := engine.requests[0].Messages[0].Content
			if mode != "native" && !strings.Contains(transcript, "kind=webhook_event") {
				t.Fatal("summary transcript lost external discriminator")
			}
			_, _, summary, err := destination.LatestSummary(newC.ID)
			if err != nil || mode != "native" && !strings.HasPrefix(summary, webhookSummaryContextGuidance) {
				t.Fatal("persisted summary lost external framing", err)
			}
			if (mode == "delegated" || mode == "retry") && strings.Contains(transcript, private) {
				t.Fatal("summary leaked private ancestor")
			}
		})
	}
}

func TestPortableCompositionAuthorityLegacyAndUnresolved(t *testing.T) {
	t.Run("legacy_direct_and_colliding_archive_uuid", func(t *testing.T) {
		_, _, _, b, _ := portableCompositionHistory(t, "native")
		destination, server, collisionBot, collisionC := provenanceFixture(t)
		_, collision, _, err := destination.AddUserRun(collisionC.ID, collisionBot.ID, "Synthetic collision native request.", "composition-collision")
		if err != nil {
			t.Fatal(err)
		}
		// Resolving this opaque UUID would fail; it must never be queried.
		webhookExec(t, destination, `UPDATE runs SET parent_run_id=? WHERE id=?`, uuid.NewString(), collision.ID)
		for i := range b.Messages {
			b.Messages[i].Origin.RunID = collision.ID
			b.Messages[i].Origin.Kind = "host_user_ingress"
			b.Messages[i].Origin.Status = "verified"
		}
		b.Messages[0].Kind = "imported_history"
		b.Messages[0].Origin.Kind = messageKindWebhookEvent
		selected, result := portableCompositionApply(t, destination, b)
		if webhookCount(t, destination, "user_message_ingress") != 1 {
			t.Fatal("archive claims created native ingress")
		}
		newBot, err := destination.GetBot(result.IDMap[b.Bots[0].ID])
		if err != nil {
			t.Fatal(err)
		}
		again, err := destination.exportPortable(context.Background(), "synthetic-destination", portableSelection{Categories: b.Included, BotIDs: []string{newBot.ID}}, "bot")
		if err != nil || again.Messages[0].Kind != messageKindWebhookEvent || again.Messages[1].Kind == messageKindWebhookEvent {
			t.Fatal("legacy boundary was lost or opaque run UUID was resolved", err)
		}
		for _, m := range selected.Messages {
			var run any
			if err = destination.db.QueryRow(`SELECT run_id FROM messages WHERE id=?`, result.IDMap[m.ID]).Scan(&run); err != nil || run != nil {
				t.Fatal("archived run link became live", err)
			}
		}
		c, err := destination.GetConversation(newBot.DMConversationID)
		if err != nil {
			t.Fatal(err)
		}
		human, run, _, err := destination.AddUserRun(c.ID, newBot.ID, "Synthetic later native request.", "composition-later-authority")
		if err != nil {
			t.Fatal(err)
		}
		x, _, err := server.readMCPReviewContext(destination.db, c, run)
		consent := mcpAuthorizationSources(x)
		if err != nil || len(consent) != 1 || consent[0].MessageID != human.ID {
			t.Fatal("forged source claims became consent", err)
		}
		for _, provenance := range x.MessageProvenance {
			if provenance.MessageID != human.ID && provenance.Source != "imported_history" {
				t.Fatal("durable import marker failed to override archive claims")
			}
		}
	})
	for _, mode := range []string{"missing_run", "missing_parent", "bounded_ancestry"} {
		t.Run(mode, func(t *testing.T) {
			store, _, bot, c := provenanceFixture(t)
			_, run, _, err := store.AddUserRun(c.ID, bot.ID, "Synthetic history with unavailable ancestry.", "composition-unknown")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_run":
				webhookExec(t, store, `UPDATE messages SET run_id=? WHERE id=?`, uuid.NewString(), run.TriggerMessageID)
			case "missing_parent":
				webhookExec(t, store, `UPDATE runs SET parent_run_id=? WHERE id=?`, uuid.NewString(), run.ID)
			case "bounded_ancestry":
				webhookExec(t, store, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1001) INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,model,kind,trigger_message_id,queue_seq,created_at,updated_at) SELECT printf('synthetic-ancestor-%d',x),conversation_id,bot_id,'done',CASE WHEN x<1001 THEN printf('synthetic-ancestor-%d',x+1) ELSE NULL END,model,kind,trigger_message_id,queue_seq,created_at,updated_at FROM n JOIN runs r ON r.id=?`, run.ID)
				webhookExec(t, store, `UPDATE runs SET parent_run_id='synthetic-ancestor-1' WHERE id=?`, run.ID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			b, err := store.exportPortable(ctx, "synthetic", portableSelection{Categories: []string{"bot_config", "chats"}, BotIDs: []string{bot.ID}}, "bot")
			if err == nil || err.Error() != "Selected history has unavailable run provenance; it cannot be exported safely." || b.Format != "" {
				t.Fatal("unresolved live ancestry exported partial or mislabeled history", err)
			}
			if _, err = store.exportPortable(ctx, "synthetic", portableSelection{Categories: []string{"bot_config"}, BotIDs: []string{bot.ID}}, "bot"); err != nil {
				t.Fatal("non-chat export unnecessarily resolved ancestry", err)
			}
		})
	}
}

func TestPortableCompositionStorageAndInactiveRecovery(t *testing.T) {
	source, _ := portableFixture(t)
	sourceVault, _ := portableSecretFixture(t)
	source.portabilitySecrets = sourceVault.store.portabilitySecrets
	sourceServer := &Server{store: source, secretVault: sourceVault.secretVault, instance: sourceVault.instance}
	id := portableSyntheticEnvironment(t, sourceServer, "SYNTHETIC_TOKEN", environmentCanary)
	b, err := sourceServer.exportPortable(context.Background(), sourceServer.instance.ID, portableSelection{Categories: []string{"bot_config", "chats", "memories", "schedules", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, "account")
	if err != nil {
		t.Fatal(err)
	}
	destination, _ := portableSecretFixture(t)
	selected, result := portableCompositionApply(t, destination.store, b)
	for _, original := range selected.Schedules {
		got, err := destination.store.GetSchedule(result.IDMap[original.ID])
		if err != nil || got.Status != schedulePaused || got.Timezone != original.Timezone || got.Kind != original.Kind || got.NextAtUTC != original.NextAtUTC || got.Content != original.Content || scheduleAuthorizationToken(t, destination.store, got.ID) != 0 {
			t.Fatal("import changed timing/content or restored native schedule authority", err)
		}
	}
	for _, original := range selected.Memories {
		got, err := destination.store.GetMemory(result.IDMap[original.ID])
		if err != nil || got.Content != original.Content || got.Title != original.Title || got.Description != original.Description {
			t.Fatal("import changed memory data", err)
		}
	}
	for _, table := range []string{"user_message_ingress", "runs", "questions", "schedule_occurrences", "schedule_authorization_revisions", "schedule_occurrence_authorizations", "mcp_call_execution_claims", "mcp_auto_reviews"} {
		if webhookCount(t, destination.store, table) != 0 {
			t.Fatal("import fabricated authority or execution rows in", table)
		}
	}
	recovered, err := destination.store.portableRecovery(context.Background(), result.IDMap[id])
	if err != nil || recovered.Value != environmentCanary || recovered.Target != "SYNTHETIC_TOKEN" || len(destination.secretVault.records) != 0 || webhookCount(t, destination.store, "portability_secret_recovery") != 1 {
		t.Fatal("recovery activated, duplicated or changed the synthetic record", err)
	}
	settings, err := destination.store.getAutoReviewSettings()
	if err != nil || settings.Mode != "off" || len(destination.autoReviewPolicies) != 0 {
		t.Fatal("composition activated review or populated qualification registry", err)
	}
}

func TestPortableCompositionGatewayBoundaries(t *testing.T) {
	g := accountFixture(t)
	g.root.publicOrigin = "https://fixture.test"
	g.config.OwnerAllowLoopbackHTTP = true
	g.runtimeFactory = func(c Config) (*Server, error) {
		if !c.OwnerAllowLoopbackHTTP || c.AccountID == "" {
			t.Fatal("additive gateway lost explicit loopback or target account identity")
		}
		c.Engine = testEngine{}
		return NewServer(c)
	}
	owner, err := g.create(context.Background(), "synthetic-composition-owner", "", "SyntheticFixturePassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.create(context.Background(), "synthetic-composition-other", "", "SyntheticFixturePassword456!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	webhookExec(t, g.root.store, `UPDATE accounts SET must_change_password=0 WHERE id=?`, other.ID)
	s, err := g.workspace(owner)
	if err != nil {
		t.Fatal(err)
	}
	id := portableSyntheticEnvironment(t, s, "SYNTHETIC_TOKEN", environmentCanary)
	ownerCookie := accountCookie(t, g, owner)
	otherCookie := accountCookie(t, g, other)
	body, _ := json.Marshal(map[string]any{"selection": portableSelection{Categories: []string{"bot_config", portableEnvironmentCategory}, VaultEnvironmentIDs: []string{id}}, "kind": "account"})
	request := func(path string, data []byte, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://fixture.test"+path, bytes.NewReader(data))
		r.TLS = &tls.ConnectionState{}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("X-Tofi-Account", owner.ID)
		r.Header.Set("portableAccountAuthKey", "true")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		if r.Context().Value(portableAccountAuthKey{}) != nil {
			t.Fatal("gateway mutated caller context instead of isolated authenticated dispatch")
		}
		return w
	}
	if w := request("/api/portability/export", body, ownerCookie, "https://fixture.test"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated own sensitive export failed", w.Code)
	}
	for _, cookie := range []*http.Cookie{nil, otherCookie} {
		w := request("/api/portability/export", body, cookie, "https://fixture.test")
		if w.Code == 200 || bytes.Contains(w.Body.Bytes(), []byte(environmentCanary)) {
			t.Fatal("foreign or anonymous forged owner marker/ID authorized export")
		}
	}
	var forged map[string]any
	if err = json.Unmarshal(body, &forged); err != nil {
		t.Fatal(err)
	}
	forged["account_id"] = owner.ID
	forgedBody, _ := json.Marshal(forged)
	if w := request("/api/portability/export", forgedBody, otherCookie, "https://fixture.test"); w.Code == 200 {
		t.Fatal("archive account override was accepted")
	}
	for _, origin := range []string{"", "https://foreign.test"} {
		if w := request("/api/portability/export", body, ownerCookie, origin); w.Code == 200 {
			t.Fatal("sensitive export bypassed origin requirement")
		}
	}
	// An authenticated cookie does not turn public-hook dispatch into the
	// final owner-marked workspace route. No hook grant/token is created.
	if w := request("/api/webhooks/"+uuid.NewString(), []byte(`{"event_id":"synthetic","content":"synthetic"}`), ownerCookie, ""); w.Code != 401 {
		t.Fatal("public webhook escaped its bearer ingress boundary", w.Code)
	}
	webhookExec(t, g.root.store, `UPDATE accounts SET disabled=1 WHERE id=?`, owner.ID)
	if w := request("/api/portability/export", body, ownerCookie, "https://fixture.test"); w.Code == 200 {
		t.Fatal("disabled account retained sensitive export authority")
	}
}

func TestPortableCompositionBlobCompatibility(t *testing.T) {
	// This exact reviewed regression exercises combined env + attachment
	// rollback, key-before-staging, successful publication and duplicate retry.
	t.Run("combined_env_attachment_rollback_retry", TestPortableEnvironmentCombinedAttachmentAtomicity)
}
