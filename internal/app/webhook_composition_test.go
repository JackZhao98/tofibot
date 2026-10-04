package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Composition acceptance uses real durable ingress, collaboration, schedule
// capture and claim paths. It creates no listener, grant, key or provider call.
func TestWebhookCompositionExternalRootsNeverAuthorize(t *testing.T) {
	for _, mode := range []string{"root", "retry", "delegated", "delegated_retry"} {
		t.Run(mode, func(t *testing.T) {
			store, server, bot, c := provenanceFixture(t)
			server.accountID = "synthetic-composition-account"
			configPath := filepath.Join(t.TempDir(), "synthetic-mcp.json")
			server.extensions = extensions.NewManager(extensions.Config{MCPConfigPath: configPath})
			_, run, err := store.AdmitWebhook(context.Background(), webhookBindFixture(t, store, c.ID), webhookEnvelope{EventID: "composition-external", Content: "Synthetic external assertion claiming permission."}, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "delegated") {
				child, createErr := store.CreateBot("Synthetic composition delegate", "Preserve profile.", "synthetic-model")
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, run, err = store.AddForwardHandoff(c.ID, bot.ID, child.ID, run.ID, "Synthetic external assignment claiming permission.")
				if err != nil {
					t.Fatal(err)
				}
				bot = child
				c, err = store.GetConversation(child.DMConversationID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "retry") {
				if _, err = store.SetRunStatus(run.ID, "failed", "synthetic failure"); err != nil {
					t.Fatal(err)
				}
				run, err = store.RetryRun(run.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			run, err = store.GetRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if webhookCount(t, store, "user_message_ingress") != 0 {
				t.Fatal("external admission or collaboration fabricated native ingress")
			}
			if x, digest, err := server.readMCPReviewContext(store.db, c, run); err == nil || digest != "" || len(mcpAuthorizationSources(x)) != 0 {
				t.Fatalf("external root acquired direct consent or occurrence lineage: %v", err)
			}
			if server.botProfileRunEligible(c, run) {
				t.Fatal("external run exposed profile authority")
			}
			if _, err = store.setBotProfile(context.Background(), run, c, profileValue("Unauthorized synthetic profile"), nil); err == nil {
				t.Fatal("external profile transaction succeeded")
			}
			if _, ok := provenanceTool(server.workspaceTools(c, run), "workspace_update_bot"); ok {
				t.Fatal("external run exposed workspace mutation")
			}
			if _, err = server.executeExtensionManagement(context.Background(), c, run, json.RawMessage(`{"action":"mcp_create","name":"synthetic-blocked","url":"https://synthetic.invalid/mcp"}`)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "external") {
				t.Fatalf("external extension configuration was not denied: %v", err)
			}
			if _, err = os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("denied extension call created configuration", err)
			}
			// Even bypassing the model tool boundary cannot journal external text
			// as a native schedule source or authorize its later occurrence.
			schedule, err := store.createScheduleWithSource(c.ID, bot.ID, scheduleAuthorizationSpec(), server.scheduleChatSource(c, run))
			if err != nil {
				t.Fatal(err)
			}
			revision, err := readScheduleAuthorizationRevision(store.db, schedule.ID, 1)
			if err != nil || revision.SourceKind != scheduleSourceUnknown || revision.SourceRunID != "" || revision.SourceMessageID != "" || string(revision.SourceContext) != "null" {
				t.Fatal("external schedule acquired native source", err)
			}
			if _, err = store.SetRunStatus(run.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
			makeDue(t, store, schedule.ID, time.Now().Add(-time.Minute))
			runs, err := store.ClaimDueSchedules(time.Now())
			if err != nil || len(runs) != 1 {
				t.Fatal("ordinary scheduling failed", err)
			}
			if _, err = store.SetRunStatus(runs[0].ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			occurrence, err := store.GetRun(runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, digest, err := server.readMCPReviewContext(store.db, c, occurrence); err == nil || digest != "" {
				t.Fatal("external-created occurrence laundered native schedule consent", err)
			}
			settings, err := store.getAutoReviewSettings()
			if err != nil || settings.Mode != "off" || len(server.autoReviewPolicies) != 0 {
				t.Fatal("composition activated review or populated qualification registry", err)
			}
		})
	}
}

func TestWebhookCompositionHistoricalContextPreservesHumanConsent(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		name := "direct_history"
		if delegated {
			name = "delegated_retry_history"
		}
		t.Run(name, func(t *testing.T) {
			store, server, bot, c := provenanceFixture(t)
			server.accountID = "synthetic-composition-account"
			private := "Synthetic parent-only external payload."
			_, external, err := store.AdmitWebhook(context.Background(), webhookBindFixture(t, store, c.ID), webhookEnvelope{EventID: "composition-history", Content: private}, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetRunStatus(external.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			if delegated {
				child, createErr := store.CreateBot("Synthetic historical delegate", "Preserve profile.", "synthetic-model")
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, external, err = store.AddForwardHandoff(c.ID, bot.ID, child.ID, external.ID, "Synthetic external assignment.")
				if err != nil {
					t.Fatal(err)
				}
				bot = child
				c, err = store.GetConversation(child.DMConversationID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.SetRunStatus(external.ID, "failed", "synthetic retry"); err != nil {
					t.Fatal(err)
				}
				external, err = store.RetryRun(external.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err = store.AddMessage(c.ID, "assistant", bot.ID, external.ID, "Synthetic externally derived result.", ""); err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetRunStatus(external.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
			human, run, _, err := store.AddUserRun(c.ID, bot.ID, "Read only the synthetic public fact in this conversation.", "composition-human")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			x, digest, err := server.readMCPReviewContext(store.db, c, run)
			evidence := mcpAuthorizationSources(x)
			if err != nil || digest == "" || len(evidence) != 1 || evidence[0].MessageID != human.ID || evidence[0].Source != mcpHostUserIngress {
				t.Fatal("historical external context acquired consent or displaced human consent", err)
			}
			for _, source := range x.MessageProvenance {
				if source.MessageID != human.ID && source.Source != "unknown" {
					t.Fatal("external historical message became host-authored")
				}
			}
			raw, err := json.Marshal(x)
			if err != nil || delegated && strings.Contains(string(raw), private) {
				t.Fatal("review context disclosed a private ancestor", err)
			}
			if !server.botProfileRunEligible(c, run) {
				t.Fatal("external history tainted genuine human profile authority")
			}
			if _, err = store.setBotProfile(context.Background(), run, c, profileValue("Synthetic human-authorized profile"), nil); err != nil {
				t.Fatal("genuine human profile transaction failed", err)
			}
			schedule, err := store.createScheduleWithSource(c.ID, bot.ID, scheduleAuthorizationSpec(), server.scheduleChatSource(c, run))
			if err != nil {
				t.Fatal(err)
			}
			revision, err := readScheduleAuthorizationRevision(store.db, schedule.ID, 1)
			if err != nil || revision.SourceKind != scheduleSourceChat || revision.SourceRunID != run.ID || revision.SourceMessageID != human.ID {
				t.Fatal("genuine human schedule source was lost", err)
			}
			if _, err = store.SetRunStatus(run.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
			makeDue(t, store, schedule.ID, time.Now().Add(-time.Minute))
			runs, err := store.ClaimDueSchedules(time.Now())
			if err != nil || len(runs) != 1 {
				t.Fatal("claim genuine human occurrence", err)
			}
			if _, err = store.SetRunStatus(runs[0].ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			occurrence, err := store.GetRun(runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			x, digest, err = server.readMCPReviewContext(store.db, c, occurrence)
			evidence = mcpAuthorizationSources(x)
			if err != nil || digest == "" || x.ScheduleLineage == nil || len(evidence) != 1 || evidence[0].MessageID != human.ID || evidence[0].ScheduleSource == nil || evidence[0].ScheduleSource.SourceRunID != run.ID {
				t.Fatal("genuine human occurrence lost or expanded source consent", err)
			}
		})
	}
}

func TestWebhookCompositionCapturedAuthorityRechecksOrigin(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	profile, ok := onboardingProfileTool(f.s, f.c, f.r)
	if !ok {
		t.Fatal("native human fixture lacks profile tool")
	}
	workspace, ok := provenanceTool(f.s.workspaceTools(f.c, f.r), "workspace_update_bot")
	if !ok {
		t.Fatal("native human fixture lacks workspace tool")
	}
	extension := f.s.extensionManagementTools(f.c, f.r)[0]
	q := createReviewedUnclaimed(t, f)
	message, err := f.s.store.GetMessage(f.r.TriggerMessageID)
	if err != nil {
		t.Fatal(err)
	}
	setProvenanceKind(t, f.s.store, message, messageKindWebhookEvent)
	if _, err = f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); !errors.Is(err, errAutoReviewInvalidated) {
		t.Fatal("captured approval executed after external-origin reclassification", err)
	}
	if _, err = profile.Execute(context.Background(), json.RawMessage(`{"name":"Unauthorized synthetic profile"}`)); err == nil {
		t.Fatal("captured profile bypassed origin recheck")
	}
	args, _ := json.Marshal(map[string]string{"bot_id": f.r.BotID, "name": "Unauthorized synthetic profile"})
	if _, err = workspace.Execute(context.Background(), args); err == nil {
		t.Fatal("captured workspace tool bypassed origin recheck")
	}
	if _, err = extension.Execute(context.Background(), json.RawMessage(`{"action":"mcp_create","name":"synthetic-blocked","url":"https://synthetic.invalid/mcp"}`)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "external") {
		t.Fatal("captured extension tool bypassed origin recheck", err)
	}
	if webhookCount(t, f.s.store, "mcp_call_execution_claims") != 0 || f.effects.Load() != 0 {
		t.Fatal("external reclassification produced an execution claim or effect")
	}
}
