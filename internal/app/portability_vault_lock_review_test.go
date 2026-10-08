package app

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Exact Astra helper/HTTP-handler interleavings from the 1ca5f68 review.
// A fixture transaction gates the sole DB connection. Both portability helpers
// must finish while the real private-input handler waits for that connection;
// ordinary fixture rollback then lets the handler finish, without breaking a
// production lock cycle. The original two regression cases are unchanged.

func TestReviewVaultRecoveryLockOrder(t *testing.T) {
	for _, helper := range []string{"preview_targets", "bound_digest"} {
		t.Run(helper, func(t *testing.T) {
			s, _ := portableSecretFixture(t)
			id := portableSyntheticEnvironment(t, s, "SYNTHETIC_REVIEW", "Synthetic fixture only")
			b := portableSyntheticSensitiveBundle(t, s, id)
			bot, err := s.store.CreateBot("Synthetic lock fixture", "", "")
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			requestID := uuid.NewString()
			s.secretVault.records[requestID] = secretRecord{ID: requestID, RunID: run.ID, BotID: bot.ID, ConversationID: bot.DMConversationID, Status: "pending", CreatedAt: now()}
			tx, err := s.store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			before := s.store.db.Stats().WaitCount
			secretDone := make(chan struct{})
			response := httptest.NewRecorder()
			go func() {
				defer close(secretDone)
				s.handleSecrets(response, httptest.NewRequest("POST", "/api/secret-inputs/"+requestID, strings.NewReader(`{"value":"Synthetic submitted value"}`)))
			}()
			deadline := time.Now().Add(time.Second)
			for s.store.db.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if s.store.db.Stats().WaitCount == before {
				tx.Rollback()
				<-secretDone
				t.Fatal("secret handler did not wait for DB")
			}
			portabilityDone := make(chan struct{})
			go func() {
				defer close(portabilityDone)
				if helper == "preview_targets" {
					_, _ = s.store.portableActiveTargets()
				} else {
					_, _, _ = s.store.portableBoundDigest(context.Background(), tx, b, uuid.NewString(), "synthetic destination")
				}
			}()
			blocked := false
			select {
			case <-portabilityDone:
			case <-time.After(200 * time.Millisecond):
				blocked = true
			}
			// Manually break the cycle so this synthetic test cleans up promptly.
			tx.Rollback()
			select {
			case <-secretDone:
			case <-time.After(time.Second):
				t.Fatal("secret handler did not recover after transaction rollback")
			}
			select {
			case <-portabilityDone:
			case <-time.After(time.Second):
				t.Fatal("portability helper did not recover after transaction rollback")
			}
			if response.Code != 200 {
				t.Fatalf("fixture secret submission failed: %d", response.Code)
			}
			if blocked {
				t.Error("confirmed lock cycle: portability holds sole DB connection and waits for vault.mu; real secret-input HTTP handler holds vault.mu and waits for DB; manual rollback was required")
			}
		})
	}
}

func TestSecretSubmissionRevalidatesAfterDatabaseWait(t *testing.T) {
	for _, change := range []string{"deleted", "fulfilled", "rebound", "expired", "cancelled", "inactive_run", "save_failure"} {
		t.Run(change, func(t *testing.T) {
			s, _ := portableSecretFixture(t)
			bot, err := s.store.CreateBot("Synthetic input race", "", "")
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			other, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.SetRunStatus(other.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			original := secretRecord{ID: id, RunID: run.ID, BotID: bot.ID, ConversationID: bot.DMConversationID, Status: "pending", CreatedAt: now()}
			s.secretVault.records[id] = original
			tx, err := s.store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			before := s.store.db.Stats().WaitCount
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest("POST", "/api/secret-inputs/"+id, strings.NewReader(`{"value":"Synthetic submitted race value only"}`)).WithContext(ctx)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); s.handleSecrets(response, request) }()
			deadline := time.Now().Add(time.Second)
			for s.store.db.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if s.store.db.Stats().WaitCount == before {
				cancel()
				tx.Rollback()
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("submission did not reach gated database")
			}
			if !s.secretVault.mu.TryLock() {
				tx.Rollback()
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("submission held vault mutex while waiting for database")
			}
			expected := original
			exists := true
			expectedCode := 409
			switch change {
			case "deleted":
				delete(s.secretVault.records, id)
				exists = false
			case "fulfilled":
				expected.Status = "ready"
				expected.Ciphertext, err = s.secretVault.seal(id, "Synthetic competing submission only")
				if err != nil {
					s.secretVault.mu.Unlock()
					tx.Rollback()
					<-done
					t.Fatal(err)
				}
				s.secretVault.records[id] = expected
			case "rebound":
				expected.RunID = other.ID
				s.secretVault.records[id] = expected
			case "expired":
				expected.CreatedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				s.secretVault.records[id] = expected
			case "cancelled":
				cancel()
			case "inactive_run":
				if _, err = tx.Exec(`UPDATE runs SET status='completed' WHERE id=?`, run.ID); err != nil {
					s.secretVault.mu.Unlock()
					tx.Rollback()
					<-done
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					s.secretVault.mu.Unlock()
					tx.Rollback()
					<-done
					t.Fatal(err)
				}
			case "save_failure":
				s.secretVault.path = filepath.Join(t.TempDir(), "missing-parent", "vault.json")
				expectedCode = 500
			}
			s.secretVault.mu.Unlock()
			tx.Rollback()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("submission did not finish after fixture gate release")
			}
			if response.Code != expectedCode {
				t.Fatalf("unexpected response: %d", response.Code)
			}
			if strings.Contains(response.Body.String(), "Synthetic submitted race value only") {
				t.Fatal("response exposed submitted value")
			}
			s.secretVault.mu.Lock()
			actual, actualExists := s.secretVault.records[id]
			s.secretVault.mu.Unlock()
			if actualExists != exists || (exists && !reflect.DeepEqual(actual, expected)) {
				t.Fatal("stale/cancelled/failed submission overwrote or revived request")
			}
		})
	}
}
