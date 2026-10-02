package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/computer"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretVaultEncryptsAndBindsRecords(t *testing.T) {
	dir := t.TempDir()
	v, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := "synthetic-secret-DO-NOT-ECHO"
	ciphertext, err := v.seal("one", fixture)
	if err != nil {
		t.Fatal(err)
	}
	record := secretRecord{ID: "one", Kind: "env", Target: "TEST_KEY", Ciphertext: ciphertext}
	v.records[record.ID] = record
	if err = v.saveLocked(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), fixture) {
		t.Fatal("plaintext persisted")
	}
	info, _ := os.Stat(v.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("vault permissions")
	}
	info, _ = os.Stat(filepath.Join(dir, "secrets", "key"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	restored, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	value, err := restored.reveal(restored.records["one"])
	if err != nil || value != fixture {
		t.Fatal("persistent decryption failed")
	}
	record.ID = "other"
	if _, err = v.reveal(record); err == nil {
		t.Fatal("ciphertext allowed different record")
	}
	if len(publicSecret(record).Ciphertext) != 0 {
		t.Fatal("public metadata includes ciphertext")
	}
}
func TestSecretVaultRunReferencesExpireOnRestart(t *testing.T) {
	dir := t.TempDir()
	v, _ := initializeSecretVault(dir)
	ciphertext, _ := v.seal("ref", "synthetic")
	v.records["ref"] = secretRecord{ID: "ref", RunID: "run", Status: "ready", Ciphertext: ciphertext}
	v.saveLocked()
	restarted, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := restarted.records["ref"]; exists {
		t.Fatal("run secret survived restart")
	}
}
func TestSecretEnvTargetBoundary(t *testing.T) {
	for _, name := range []string{"PATH", "HOME", "LD_PRELOAD", "BASH_ENV", "ENV", "DISPLAY", "1KEY", "API-KEY", "X; echo BAD"} {
		if allowedSecretEnv(name) {
			t.Fatalf("accepted %s", name)
		}
	}
	if !allowedSecretEnv("API_KEY") {
		t.Fatal("valid variable rejected")
	}
	for _, value := range []string{"", "a\x00b", strings.Repeat("x", 65537)} {
		if validSecretValue(value) {
			t.Fatal("invalid value accepted")
		}
	}
}

type secretTransport func(*http.Request) (*http.Response, error)

func (f secretTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func secretTestServer(t *testing.T) (*Server, *int) {
	t.Helper()
	count := new(int)
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-secret.sock", Client: &http.Client{Transport: secretTransport(func(r *http.Request) (*http.Response, error) {
		*count++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"exit_code":0,"stdout":"fixture-secret","stderr":"fixture-secret"}}`)), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Server{microVM: client, secretVault: v, computerOwners: map[string]string{}}, count
}
func TestSecretToolWithholdsAllOutputAndScopesReference(t *testing.T) {
	s, count := secretTestServer(t)
	run := Run{ID: "run", BotID: "bot", ConversationID: "conversation"}
	ciphertext, _ := s.secretVault.seal("ref", "fixture-secret")
	s.secretVault.records["ref"] = secretRecord{ID: "ref", RunID: run.ID, BotID: run.BotID, ConversationID: run.ConversationID, Status: "ready", CreatedAt: now(), Ciphertext: ciphertext}
	args := json.RawMessage(`{"secret_ref":"ref","action":"shell_exec","target":"API_KEY","command":"printf %s \"$API_KEY\""}`)
	out, err := s.secretTools(run)[1].Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "fixture-secret") || !strings.Contains(out, `"output_withheld":true`) {
		t.Fatal("secret output not withheld")
	}
	if *count != 1 {
		t.Fatal("action not performed")
	}
	for _, other := range []Run{{ID: "other", BotID: run.BotID, ConversationID: run.ConversationID}, {ID: run.ID, BotID: "other", ConversationID: run.ConversationID}, {ID: run.ID, BotID: run.BotID, ConversationID: "other"}} {
		if _, err = s.secretTools(other)[1].Execute(context.Background(), args); err == nil {
			t.Fatal("cross-scope secret accepted")
		}
	}
	rec := s.secretVault.records["ref"]
	rec.CreatedAt = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	s.secretVault.records["ref"] = rec
	if _, err = s.secretTools(run)[1].Execute(context.Background(), args); err == nil {
		t.Fatal("expired reference accepted")
	}
	if *count != 1 {
		t.Fatal("invalid reference reached computer")
	}
	s.clearRunSecrets(run.ID)
	if len(s.secretVault.records) != 0 {
		t.Fatal("run teardown did not clear values")
	}
}
func TestSecretRequestCancellationRemovesValue(t *testing.T) {
	s, _ := secretTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.secretTools(Run{ID: "run", BotID: "bot", ConversationID: "conversation"})[0].Execute(ctx, json.RawMessage(`{"label":"API key","purpose":"Synthetic test"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.secretVault.records) != 0 {
		t.Fatal("cancelled pending request retained")
	}
}
func TestSecretCredentialMetadataNeverReturnsValue(t *testing.T) {
	s, _ := secretTestServer(t)
	req := httptest.NewRequest("POST", "/api/computer/credentials", strings.NewReader(`{"name":"Example","kind":"env","target":"API_KEY","value":"fixture-secret"}`))
	w := httptest.NewRecorder()
	s.handleSecrets(w, req)
	if w.Code != 201 || strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatalf("unexpected create: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("GET", "/api/computer/credentials", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatal("secret exposed in metadata")
	}
}

func TestSecretInputHTTPSubmissionScopeAndCompletion(t *testing.T) {
	s, _ := secretTestServer(t)
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.store = store
	bot, err := store.CreateBot("fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.AddRun(bot.DMConversationID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	store.SetRunStatus(run.ID, "running", "")
	rec := secretRecord{ID: "request", RunID: run.ID, BotID: bot.ID, ConversationID: bot.DMConversationID, Status: "pending", CreatedAt: now(), Label: "API key"}
	s.secretVault.records[rec.ID] = rec
	w := httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("GET", "/api/secret-inputs?conversation_id=other", nil))
	if strings.Contains(w.Body.String(), "API key") {
		t.Fatal("request leaked to another conversation")
	}
	w = httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("POST", "/api/secret-inputs/request", strings.NewReader(`{"value":"synthetic-private"}`)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private") {
		t.Fatalf("submission failed: %d", w.Code)
	}
	if value, err := s.secretVault.reveal(s.secretVault.records[rec.ID]); err != nil || value != "synthetic-private" {
		t.Fatal("value not privately persisted")
	}
	w = httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("POST", "/api/secret-inputs/request", strings.NewReader(`{"value":"other"}`)))
	if w.Code != 409 {
		t.Fatal("fulfilled request accepted twice")
	}
	rec.ID = "cancelled"
	s.secretVault.records[rec.ID] = rec
	store.SetRunStatus(run.ID, "cancelled", "")
	w = httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("POST", "/api/secret-inputs/cancelled", strings.NewReader(`{"value":"other"}`)))
	if w.Code != 409 {
		t.Fatal("inactive run accepted secret")
	}
	rec.ID = "expired"
	rec.CreatedAt = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	s.secretVault.records[rec.ID] = rec
	w = httptest.NewRecorder()
	s.handleSecrets(w, httptest.NewRequest("POST", "/api/secret-inputs/expired", strings.NewReader(`{"value":"other"}`)))
	if w.Code != 409 {
		t.Fatal("expired input accepted")
	}
}

func TestSecretDeleteFailureRestoresCurrentRecord(t *testing.T) {
	for _, request := range []bool{false, true} {
		t.Run(fmt.Sprint(request), func(t *testing.T) {
			s, _ := secretTestServer(t)
			rec := secretRecord{ID: "entry", Kind: "env", Target: "API_KEY", Status: "stored"}
			endpoint := "/api/computer/credentials/entry"
			if request {
				rec.RunID = "run"
				rec.Status = "pending"
				endpoint = "/api/secret-inputs/entry"
			}
			s.secretVault.records[rec.ID] = rec
			// Renaming a file onto a directory reliably fails, even for a root test runner.
			broken := filepath.Join(t.TempDir(), "directory")
			if err := os.Mkdir(broken, 0700); err != nil {
				t.Fatal(err)
			}
			s.secretVault.path = broken
			w := httptest.NewRecorder()
			s.handleSecrets(w, httptest.NewRequest("DELETE", endpoint, nil))
			if w.Code != 500 {
				t.Fatal("delete should fail")
			}
			restored, ok := s.secretVault.records[rec.ID]
			if !ok || restored.Status != rec.Status {
				t.Fatal("failed deletion lost the in-memory record")
			}
		})
	}
}
