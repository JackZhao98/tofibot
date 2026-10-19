package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Exact Astra helper/handler interleavings from the 1ca5f68 review, now for
// the private-input answer path (a form question, AnswerUserForm). A fixture
// transaction gates the sole DB connection. Both portability helpers must
// finish while the answer waits for that connection; ordinary fixture
// rollback then lets the answer finish, without breaking a production lock
// cycle. That requires the answer to take the DB connection before the vault
// mutex, the order portability uses.

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
			input, err := normalizeQuestionInput(askQuestionInput{
				Question: "Synthetic private input", Type: questionForm, PrivateInput: true,
				Fields: []UserFormField{{ID: privateInputFieldID, Label: "Synthetic key", Type: "password", Required: true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			question, err := s.store.CreateQuestion(bot.DMConversationID, run, input)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			before := s.store.db.Stats().WaitCount
			secretDone := make(chan struct{})
			var answerErr error
			go func() {
				defer close(secretDone)
				_, _, answerErr = s.AnswerUserForm(question.ID, "human", map[string]string{privateInputFieldID: "Synthetic submitted value"})
			}()
			deadline := time.Now().Add(time.Second)
			for s.store.db.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if s.store.db.Stats().WaitCount == before {
				tx.Rollback()
				<-secretDone
				t.Fatal("private-input answer did not wait for DB")
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
				t.Fatal("private-input answer did not recover after transaction rollback")
			}
			select {
			case <-portabilityDone:
			case <-time.After(time.Second):
				t.Fatal("portability helper did not recover after transaction rollback")
			}
			if answerErr != nil {
				t.Fatalf("fixture secret submission failed: %v", answerErr)
			}
			if blocked {
				t.Error("confirmed lock cycle: portability holds sole DB connection and waits for vault.mu; private-input answer holds vault.mu and waits for DB; manual rollback was required")
			}
		})
	}
}
