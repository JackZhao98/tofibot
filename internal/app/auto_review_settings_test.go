package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestAutoReviewPurgeResetsOffAndHumanMCPApprovalWorksWithoutRestart(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "auto"} {
		for _, allow := range []bool{false, true} {
			name := mode + "/deny"
			if allow {
				name = mode + "/allow"
			}
			t.Run(name, func(t *testing.T) {
				f := newAutoReviewFixture(t)
				old := createReviewedUnclaimed(t, f)
				if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, old.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.s.store.putAutoReviewMode(mode); err != nil {
					t.Fatal(err)
				}
				if err := f.s.store.purgeWorkspaceData(); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				f.s.autoReviewSettings(w, httptest.NewRequest(http.MethodGet, "/api/auto-review-settings", nil))
				var settings autoReviewSettings
				if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil || w.Code != 200 || settings.Mode != "off" || settings.Revision != 0 {
					t.Fatalf("post-purge settings HTTP %d %s: %v", w.Code, w.Body.String(), err)
				}
				var count int
				if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM auto_review_settings`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("purge did not persist default: count=%d err=%v", count, err)
				}
				for _, table := range []string{"mcp_auto_reviews", "mcp_call_approvals", "mcp_call_execution_claims"} {
					if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("purge retained %s: count=%d err=%v", table, count, err)
					}
				}
				f.p.calls.Store(0)
				bot, err := f.s.store.CreateBot("post-purge synthetic bot", "", "model")
				if err != nil {
					t.Fatal(err)
				}
				if f.c, err = f.s.store.GetConversation(bot.DMConversationID); err != nil {
					t.Fatal(err)
				}
				if f.r, err = f.s.store.AddRun(f.c.ID, bot.ID, ""); err != nil {
					t.Fatal(err)
				}
				if _, err = f.s.store.SetRunStatus(f.r.ID, "running", ""); err != nil {
					t.Fatal(err)
				}
				msg, _, err := f.s.store.AddMessage(f.c.ID, "user", "", "", "Read the synthetic public fact for alpha.", "")
				if err != nil {
					t.Fatal(err)
				}
				f.r.TriggerMessageID = msg.ID
				if _, err = f.s.store.db.Exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, msg.ID, f.r.ID); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() { done <- f.execute(ctx) }()
				q := waitReviewQuestion(t, f, "")
				if q.Status != questionPending || q.AnsweredBy != "" || q.Approval.Review != nil || f.p.calls.Load() != 0 || f.effects.Load() != 0 {
					t.Fatalf("OFF did not wait for human: %+v", q)
				}
				if _, _, err = f.s.store.AnswerQuestion(q.ID, "human-after-purge", allow); err != nil {
					t.Fatal(err)
				}
				err = waitReviewDone(t, done)
				if allow && err != nil || !allow && err == nil {
					t.Fatalf("human answer %t: %v", allow, err)
				}
				want := int32(0)
				if allow {
					want = 1
				}
				if f.effects.Load() != want || f.p.calls.Load() != 0 {
					t.Fatalf("effects=%d reviewer=%d", f.effects.Load(), f.p.calls.Load())
				}
				if err = f.execute(ctx); err == nil || f.effects.Load() != want || f.p.calls.Load() != 0 {
					t.Fatal("duplicate post-purge resume bypassed human/claim fence", err)
				}
			})
		}
	}
}

func TestAutoReviewMissingSettingsReadAndUpdatesFailClosed(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "auto"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			q := createReviewedUnclaimed(t, f)
			before, err := f.s.store.getAutoReviewSettings()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.store.db.Exec(`DELETE FROM auto_review_settings`); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			f.s.autoReviewSettings(w, httptest.NewRequest(http.MethodGet, "/api/auto-review-settings", nil))
			var settings autoReviewSettings
			if err = json.Unmarshal(w.Body.Bytes(), &settings); err != nil || w.Code != 200 || settings.Mode != "off" {
				t.Fatalf("absent setting was not OFF: HTTP %d %s", w.Code, w.Body.String())
			}
			if err = f.s.store.putAutoReviewMode(mode); err != nil {
				t.Fatal(err)
			}
			current, err := f.s.store.getAutoReviewSettings()
			if err != nil || current.Mode != mode || current.Revision <= before.Revision {
				t.Fatalf("recreated settings reused decision epoch: %+v -> %+v: %v", before, current, err)
			}
			q, err = f.s.store.GetQuestion(q.ID)
			if err != nil || q.Status != questionPending || q.AnsweredBy != "" || len(q.Answer) != 0 || q.Approval.Review.Status != "invalidated" {
				t.Fatal("missing-row recovery retained automatic answer", q, err)
			}
			if _, err = f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); !errors.Is(err, errAutoReviewInvalidated) {
				t.Fatal("old decision survived setting recovery", err)
			}
			if err = f.s.store.putAutoReviewMode(mode); err != nil {
				t.Fatal(err)
			}
			repeated, err := f.s.store.getAutoReviewSettings()
			if err != nil || repeated != current || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("same-mode update changed epoch or executed", repeated, err)
			}
		})
	}
}

func TestAutoReviewMissingSettingsDuringReviewCannotReuseEpoch(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.store.getAutoReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	f.p.reply = func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		close(started)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return reviewReply(req, "allow"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.execute(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("review request did not start")
	}
	if _, err := f.s.store.db.Exec(`DELETE FROM auto_review_settings`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	close(finish)
	q := waitReviewQuestion(t, f, "invalidated")
	if q.Status != questionPending || q.AnsweredBy != "" || len(q.Answer) != 0 {
		t.Fatal("in-flight allow reused lost epoch", q)
	}
	after, err := f.s.store.getAutoReviewSettings()
	if err != nil || after.Revision <= before.Revision {
		t.Fatalf("singleton recovery reused epoch: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err == nil {
		t.Fatal("invalidated in-flight decision claimed execution")
	}
	var claimed string
	if err := f.s.store.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claimed); err != nil || claimed != "" {
		t.Fatal("invalidated decision persisted a claim", err)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if err := waitReviewDone(t, done); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
		t.Fatal("in-flight recovery executed or re-reviewed", err)
	}
}

func TestAutoReviewMigrationPreservesSettingsAndRecoversMissingEpoch(t *testing.T) {
	f := newAutoReviewFixture(t)
	q := createReviewedUnclaimed(t, f)
	before, err := f.s.store.getAutoReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateAutoReview(f.s.store.db); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.store.getAutoReviewSettings()
	if err != nil || current != before {
		t.Fatal("migration changed existing mode/epoch", current, err)
	}
	if _, err = f.s.store.db.Exec(`DELETE FROM auto_review_settings`); err != nil {
		t.Fatal(err)
	}
	if err = migrateAutoReview(f.s.store.db); err != nil {
		t.Fatal(err)
	}
	current, err = f.s.store.getAutoReviewSettings()
	if err != nil || current.Mode != "off" || current.Revision <= before.Revision {
		t.Fatal("migration failed to recover safely", current, err)
	}
	if err = f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); !errors.Is(err, errAutoReviewInvalidated) || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
		t.Fatal("migration/mode switch resurrected old decision", err)
	}
}

func TestAutoReviewMissingSettingsRecoveryPreservesTerminalAndClaimedFences(t *testing.T) {
	for _, state := range []string{questionExpired, questionCancelled, questionRunDone, "claimed"} {
		t.Run(state, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			q := createReviewedUnclaimed(t, f)
			if state == "claimed" {
				if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.s.store.db.Exec(`UPDATE questions SET status=? WHERE id=?`, state, q.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.store.db.Exec(`DELETE FROM auto_review_settings`); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.putAutoReviewMode("off"); err != nil {
				t.Fatal(err)
			}
			current, err := f.s.store.GetQuestion(q.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state == "claimed" {
				if current.Status != questionAnswered || current.AnsweredBy != autoReviewActor || current.Approval.Review.Status != "approved" {
					t.Fatal("recovery rewrote claimed provenance", current)
				}
			} else if current.Status != state || current.AnsweredBy != "" || len(current.Answer) != 0 {
				t.Fatal("recovery revived terminal automatic decision", current)
			}
			if result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err == nil {
				if n, _ := result.RowsAffected(); n != 0 {
					t.Fatal("recovery claimed old decision")
				}
			}
		})
	}
}

func TestAutoReviewSettingsReadDoesNotHideStorageErrors(t *testing.T) {
	store, _, _ := questionFixture(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.getAutoReviewSettings(); err == nil {
		t.Fatal("closed database was treated as a missing singleton")
	}
}

func TestAutoReviewMinimalSchemaPreservesMainHistoryAndImportMarkers(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := store.CreateBot("Synthetic upgrade", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := store.AddMessage(bot.DMConversationID, "user", "", "", "原有历史 remains unchanged", "synthetic-legacy")
	if err != nil {
		t.Fatal(err)
	}
	// Model the pre-AutoReview store: none of the new settings/claim/ingress tables.
	if _, err = store.db.Exec(`DROP TABLE user_message_ingress; DROP TABLE auto_review_settings; DROP TABLE mcp_auto_reviews; DROP TABLE mcp_call_execution_claims; INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('message',?,?)`, message.ID, `{"kind":"host_user_ingress","untrusted_archive_claim":true}`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err = OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		mode, err := store.getAutoReviewSettings()
		if err != nil || mode.Mode != "off" || mode.Revision != 0 {
			t.Fatalf("upgrade default: %+v %v", mode, err)
		}
		var text, marker string
		var ingress int
		if err = store.db.QueryRow(`SELECT content FROM messages WHERE id=?`, message.ID).Scan(&text); err != nil || text != message.Content {
			t.Fatal("historical content changed", err)
		}
		if err = store.db.QueryRow(`SELECT source_json FROM portability_provenance WHERE kind='message' AND target_id=?`, message.ID).Scan(&marker); err != nil || marker != `{"kind":"host_user_ingress","untrusted_archive_claim":true}` {
			t.Fatal("existing marker changed", err)
		}
		if err = store.db.QueryRow(`SELECT count(*) FROM user_message_ingress`).Scan(&ingress); err != nil || ingress != 0 {
			t.Fatal("migration backfilled user roles", err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoReviewMinimalProvenanceSchemaRejectsMalformedTables(t *testing.T) {
	for _, table := range []string{"user_message_ingress", "portability_provenance"} {
		t.Run(table, func(t *testing.T) {
			dir := t.TempDir()
			store, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(`DROP TABLE ` + table + `; CREATE TABLE ` + table + `(untrusted TEXT)`); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(dir)
			if err == nil {
				reopened.Close()
				t.Fatal("incompatible evidence table accepted")
			}
		})
	}
}
