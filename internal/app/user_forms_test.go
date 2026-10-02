package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func formDefinition() askQuestionInput {
	return askQuestionInput{Type: questionForm, Question: "Complete the sign-in form", SourceURL: "https://example.test/login?code=not-retained#fragment", Fields: []UserFormField{
		{ID: "email", Label: "Email", Type: "email", Required: true},
		{ID: "password", Label: "Password", Type: "password", Required: true},
		{ID: "notes", Label: "Notes", Type: "textarea"},
	}}
}

func formFixture(t *testing.T) (*Server, Conversation, Run, Question) {
	t.Helper()
	store, c, run := questionFixture(t)
	t.Cleanup(func() { store.Close() })
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, secretVault: vault}
	in, err := normalizeQuestionInput(formDefinition())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, run, q
}

func formValues() map[string]string {
	return map[string]string{"email": "test@example.test", "password": " synthetic-private-value ", "notes": "Keep this ordinary note"}
}

func TestUserFormDefinitionAndBounds(t *testing.T) {
	in, err := normalizeQuestionInput(formDefinition())
	if err != nil || in.SourceURL != "https://example.test/login" {
		t.Fatalf("normalization: %+v %v", in, err)
	}
	for _, change := range []func(*askQuestionInput){
		func(in *askQuestionInput) { in.Fields = nil },
		func(in *askQuestionInput) { in.Fields[1].ID = in.Fields[0].ID },
		func(in *askQuestionInput) { in.Fields[0].ID = "__proto__" },
		func(in *askQuestionInput) { in.Fields[0].Type = "script" },
		func(in *askQuestionInput) { in.Fields[0].Label = " " },
		func(in *askQuestionInput) { in.SourceURL = "javascript:alert(1)" },
		func(in *askQuestionInput) { in.SourceURL = "https://user:pass@example.test" },
		func(in *askQuestionInput) { in.SourceURL = "http://example.test" },
		func(in *askQuestionInput) { in.Fields = append(in.Fields, make([]UserFormField, 10)...) },
	} {
		bad := formDefinition()
		change(&bad)
		if _, err := normalizeQuestionInput(bad); err == nil {
			t.Fatal("invalid form accepted")
		}
	}
	ordinary := formDefinition()
	ordinary.SourceURL = "http://example.test"
	ordinary.Fields = ordinary.Fields[:1]
	if _, err := normalizeQuestionInput(ordinary); err != nil {
		t.Fatal("ordinary HTTP form rejected", err)
	}
	for _, raw := range []string{"https://EXAMPLE.test:443/login", "https://example.test"} {
		u, _ := formSourceURL(raw)
		if formOrigin(u) != "https://example.test" {
			t.Fatal("origin normalization")
		}
	}
}

func TestUserFormValuesValidation(t *testing.T) {
	for _, change := range []func(map[string]string){
		func(v map[string]string) { delete(v, "email") },
		func(v map[string]string) { v["password"] = "" },
		func(v map[string]string) { v["email"] = "not-email" },
		func(v map[string]string) { v["email"] = "Person <test@example.test>" },
		func(v map[string]string) { v["unknown"] = "extra" },
		func(v map[string]string) { v["notes"] = strings.Repeat("字", 4001) },
		func(v map[string]string) { v["password"] = strings.Repeat("x", 65537) },
		func(v map[string]string) { v["notes"] = "a\x00b" },
	} {
		values := formValues()
		change(values)
		if err := validateFormValues(formDefinition().Fields, values); err == nil {
			t.Fatal("invalid answer accepted")
		}
	}
	values := formValues()
	values["password"] = " "
	values["notes"] = strings.Repeat("字", 4000)
	if err := validateFormValues(formDefinition().Fields, values); err != nil {
		t.Fatal("significant spaces/unicode rejected", err)
	}
}

func TestUserFormAtomicPrivateAnswerAndReplay(t *testing.T) {
	s, c, run, q := formFixture(t)
	values := formValues()
	answered, duplicate, err := s.AnswerUserForm(q.ID, "human", values)
	if err != nil || duplicate || answered.Status != questionAnswered {
		t.Fatalf("submit: %v", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(answered.Answer, &result); err != nil {
		t.Fatal(err)
	}
	var ref formSecretAnswer
	if err := json.Unmarshal(result["password"], &ref); err != nil || ref.SecretRef == "" || !ref.ValueHidden {
		t.Fatal("missing private reference")
	}
	record := s.secretVault.records[ref.SecretRef]
	if record.Kind != "browser_form" || record.Target != "https://example.test" || record.RunID != run.ID || record.ConversationID != c.ID {
		t.Fatal("reference not scoped")
	}
	if value, err := s.secretVault.reveal(record); err != nil || value != values["password"] {
		t.Fatal("private value changed")
	}
	if !strings.Contains(string(answered.Answer), values["notes"]) {
		t.Fatal("ordinary field missing")
	}
	loaded, _ := s.store.GetQuestion(q.ID)
	if len(loaded.Fields) != 3 || loaded.SourceURL != "https://example.test/login" {
		t.Fatal("form definition did not persist")
	}
	card, _ := json.Marshal(loaded.Card())
	disk, _ := os.ReadFile(s.secretVault.path)
	for _, surface := range []string{string(answered.Answer), string(card), string(disk)} {
		if strings.Contains(surface, strings.TrimSpace(values["password"])) {
			t.Fatal("private value leaked into public/persisted surface")
		}
	}
	values["password"] = "replacement"
	values["notes"] = "replacement"
	replayed, duplicate, err := s.AnswerUserForm(q.ID, "human", values)
	if err != nil || !duplicate || string(replayed.Answer) != string(answered.Answer) || len(s.secretVault.records) != 1 {
		t.Fatal("replay changed answer or duplicated secrets")
	}
	if _, _, err := s.store.AnswerQuestion(q.ID, "human", values); err != nil {
		t.Fatal("existing duplicate behavior changed", err)
	}
	s.clearRunSecrets(run.ID)
	if len(s.secretVault.records) != 0 {
		t.Fatal("run cleanup retained private form value")
	}
}

func TestUserFormRejectsInactiveCancelledExpiredAndInvalid(t *testing.T) {
	for _, scenario := range []string{"inactive", "cancelled", "expired", "invalid", "bot", "vault_failure", "sql_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, run, q := formFixture(t)
			values, actor := formValues(), "human"
			switch scenario {
			case "inactive":
				s.store.SetRunStatus(run.ID, "completed", "")
			case "cancelled":
				s.store.CancelQuestion(q.ID)
			case "expired":
				s.store.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), q.ID)
			case "invalid":
				values["email"] = ""
			case "bot":
				actor = run.BotID
			case "vault_failure":
				s.secretVault.path = t.TempDir()
			case "sql_failure":
				_, err := s.store.db.Exec(`CREATE TRIGGER fail_form_update BEFORE UPDATE ON questions BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END;`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := s.AnswerUserForm(q.ID, actor, values); err == nil {
				t.Fatal("invalid state accepted")
			}
			if len(s.secretVault.records) != 0 {
				t.Fatal("failed submit left a private reference")
			}
			latest, _ := s.store.GetQuestion(q.ID)
			if latest.Status == questionAnswered {
				t.Fatal("partial answer persisted")
			}
		})
	}
}

func TestUserFormConcurrentSubmitPreservesFirst(t *testing.T) {
	s, _, _, q := formFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.secretVault.records) != 1 {
		t.Fatal("concurrent submit duplicated private inputs")
	}
}

func TestUserFormHTTPResumeAndMetadata(t *testing.T) {
	s, c, _, q := formFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan json.RawMessage, 1)
	go func() { answer, _ := s.WaitQuestion(ctx, q.ID); result <- answer }()
	data, _ := json.Marshal(map[string]any{"fields": formValues()})
	w := httptest.NewRecorder()
	s.routeQuestions(w, httptest.NewRequest("POST", "/api/questions/"+q.ID+"/answer", strings.NewReader(string(data))), "questions/"+q.ID+"/answer")
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private-value") {
		t.Fatalf("response status %d", w.Code)
	}
	select {
	case answer := <-result:
		if !strings.Contains(string(answer), "secret_ref") || strings.Contains(string(answer), "synthetic-private-value") {
			t.Fatal("unsafe/missing tool answer")
		}
	case <-ctx.Done():
		t.Fatal("form did not resume the waiting tool")
	}
	w = httptest.NewRecorder()
	s.routeQuestions(w, httptest.NewRequest("GET", "/api/questions?conversation_id="+c.ID, nil), "questions")
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private-value") || !strings.Contains(w.Body.String(), `"question_type":"form"`) {
		t.Fatal("unsafe history response")
	}
	var eventCount int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND data LIKE ?`, c.ID, "%synthetic-private-value%").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatal("private value in event history")
	}
}

func TestUserFormDoesNotPermitOrdinaryAnswerPath(t *testing.T) {
	s, _, _, q := formFixture(t)
	if _, _, err := s.store.AnswerQuestion(q.ID, "human", formValues()); !errors.Is(err, ErrQuestionInvalidAnswer) {
		t.Fatal("private form bypassed protected submission")
	}
}

func TestUserFormOptionalPasswordAndMissingVault(t *testing.T) {
	s, _, _, q := formFixture(t)
	q.Fields[1].Required = false
	fields, _ := json.Marshal(q.Fields)
	s.store.db.Exec(`UPDATE questions SET fields_json=? WHERE id=?`, string(fields), q.ID)
	values := formValues()
	delete(values, "password")
	s.secretVault = nil
	answered, _, err := s.AnswerUserForm(q.ID, "human", values)
	if err != nil || !strings.Contains(string(answered.Answer), `"password":""`) {
		t.Fatal("optional blank private field failed", err)
	}
}

func TestUserFormPasswordDestinationAndOutput(t *testing.T) {
	if microVMActions["browser.type_private"] {
		t.Fatal("private vault action must not be exposed as a public/model computer action")
	}
	for _, scenario := range []string{"same", "different_origin", "unfocused", "shell", "computer_error", "unobserved", "wrong_field", "unsupported_guest"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, run, q := formFixture(t)
			answer, _, err := s.AnswerUserForm(q.ID, "human", formValues())
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			json.Unmarshal(answer.Answer, &fields)
			var ref formSecretAnswer
			json.Unmarshal(fields["password"], &ref)
			typed := 0
			client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-form.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
				var action computer.Action
				json.NewDecoder(req.Body).Decode(&action)
				result := any(map[string]any{"ok": true})
				if action.Name == "browser.snapshot" {
					if scenario == "computer_error" {
						return nil, errors.New("synthetic failed read")
					}
					site, source := "https://example.test/login", "focused"
					if scenario == "different_origin" {
						site = "https://other.test"
					}
					if scenario == "unfocused" {
						source = "single"
					}
					result = map[string]any{"current": map[string]string{"url": site}, "current_source": source}
				}
				if action.Name == "desktop.type" {
					t.Fatal("private input must never fall back to unguarded typing")
				}
				if action.Name == "browser.type_private" {
					var args struct {
						Text   string `json:"text"`
						Origin string `json:"origin"`
					}
					json.Unmarshal(action.Args, &args)
					if args.Text != formValues()["password"] || args.Origin != "https://example.test" {
						t.Error("private browser input or approved origin was rewritten")
					}
					if scenario == "wrong_field" || scenario == "unsupported_guest" {
						return nil, errors.New("synthetic rejected call containing " + args.Text)
					}
					typed++
					result = map[string]string{"echo": args.Text}
				}
				encoded, _ := json.Marshal(map[string]any{"ok": true, "result": result})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: make(http.Header)}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			s.microVM = client
			s.claimComputerOwner(run.BotID, run.ID)
			if scenario != "unobserved" {
				s.markDesktopObserved(run)
			}
			action := "browser_type"
			if scenario == "shell" {
				action = "shell_exec"
			}
			args, _ := json.Marshal(map[string]string{"secret_ref": ref.SecretRef, "action": action, "target": "PASS", "command": "true"})
			out, err := s.secretTools(run)[1].Execute(context.Background(), args)
			if scenario == "same" {
				if err != nil || typed != 1 || out != `{"ok":true}` {
					t.Fatalf("fill failed typed=%d out=%s err=%v", typed, out, err)
				}
			} else if err == nil || typed != 0 {
				t.Fatalf("unsafe destination accepted typed=%d", typed)
			}
			if strings.Contains(out, "synthetic-private-value") || (err != nil && strings.Contains(err.Error(), "synthetic-private-value")) {
				t.Fatal("private computer output returned to model")
			}
		})
	}
}

func TestUserFormVaultFailureDoesNotPersistPlaintext(t *testing.T) {
	s, _, _, q := formFixture(t)
	s.secretVault.path = filepath.Join(t.TempDir(), "missing", "vault.json")
	if _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); err == nil {
		t.Fatal("write should fail")
	}
	if len(s.secretVault.records) != 0 {
		t.Fatal("failed encryption persistence left in-memory value")
	}
}
