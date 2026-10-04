package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func scheduleAuthorizationSpec() ScheduleSpec {
	return ScheduleSpec{Title: "Synthetic schedule", Description: "Synthetic scoped task", Content: "Read the synthetic fact and keep it in this conversation.", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), IntervalSeconds: 60, Timezone: "UTC"}
}

func scheduleAuthorizationToken(t *testing.T, store *Store, id string) int64 {
	t.Helper()
	var n int64
	if err := store.db.QueryRow(`SELECT authorization_revision FROM schedules WHERE id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func scheduleAuthorizationCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func scheduleAuthorizationForm(t *testing.T, store *Store, bot Bot, c Conversation) (*Server, Schedule) {
	t.Helper()
	server := &Server{store: store, accountID: "synthetic-schedule-account"}
	spec := scheduleAuthorizationSpec()
	x, err := store.createScheduleWithSource(c.ID, bot.ID, spec, server.scheduleFormSource("create", spec))
	if err != nil {
		t.Fatal(err)
	}
	return server, x
}

func TestScheduleAuthorizationMigrationPreservesLegacyRows(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	x, err := store.CreateSchedule(c.ID, bot.ID, scheduleAuthorizationSpec())
	if err != nil {
		t.Fatal(err)
	}
	due, _ := parseStoredTime(x.NextAtUTC)
	runs, err := store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim %v %v", runs, err)
	}
	before, err := store.GetSchedule(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := store.GetMessage(runs[0].TriggerMessageID)
	if err != nil {
		t.Fatal(err)
	}
	occurrences, err := store.ScheduleOccurrences(context.Background(), c.ID, []string{runs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the precise pre-lineage schema using synthetic rows only.
	for _, statement := range []string{`DROP TABLE schedule_occurrence_authorizations`, `DROP TABLE schedule_authorization_revisions`, `ALTER TABLE schedules DROP COLUMN authorization_revision`} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 2; n++ {
		if err := migrateScheduleAuthorization(store.db); err != nil {
			t.Fatal(err)
		}
		if scheduleAuthorizationToken(t, store, x.ID) != 0 || scheduleAuthorizationCount(t, store, "schedule_authorization_revisions") != 0 || scheduleAuthorizationCount(t, store, "schedule_occurrence_authorizations") != 0 {
			t.Fatal("migration backfilled authority")
		}
		got, _ := store.GetSchedule(x.ID)
		trigger, _ := store.GetMessage(runs[0].TriggerMessageID)
		current, _ := store.ScheduleOccurrences(context.Background(), c.ID, []string{runs[0].ID})
		if !reflect.DeepEqual(before, got) || !reflect.DeepEqual(message, trigger) || !reflect.DeepEqual(occurrences, current) {
			t.Fatal("migration rewrote schedule timing, labels, instructions or historical occurrence")
		}
	}
	label := "Synthetic renamed label"
	if _, err = store.PatchSchedule(x.ID, SchedulePatch{Title: &label}); err != nil {
		t.Fatal(err)
	}
	if scheduleAuthorizationToken(t, store, x.ID) != 0 {
		t.Fatal("label edit adopted legacy row")
	}
	server := &Server{store: store, accountID: "synthetic-schedule-account"}
	if _, err = store.setScheduleStatusWithSource(x.ID, scheduleActive, server.scheduleFormSource("resume", map[string]string{"schedule_id": x.ID})); err != nil {
		t.Fatal(err)
	}
	first, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
	if err != nil || first.EventKind != "resume" {
		t.Fatalf("legacy resume fabricated create: %+v %v", first, err)
	}
}

func TestScheduleAuthorizationAuthenticatedHTTPForm(t *testing.T) {
	server, dir := ownerTestServer(t)
	server.accountID = "synthetic-owner-account"
	bot, err := server.store.CreateBot("Synthetic form bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"bot_id": bot.ID, "content": "  原文条件：结果留在这里。  ", "title": "Synthetic form", "description": "Synthetic form source", "kind": scheduleOnce, "run_at": time.Now().Add(time.Hour).Format(time.RFC3339Nano), "timezone": "UTC", "created_by": "bot", "source_kind": "native_chat", "source_message_id": "forged-source"}
	path := "/api/conversations/" + bot.DMConversationID + "/schedules"
	denied := ownerCall(server, http.MethodPost, path, body, nil, true)
	if denied.Code < 400 || scheduleAuthorizationCount(t, server.store, "schedules") != 0 {
		t.Fatalf("unauthenticated form created schedule: %d %s", denied.Code, denied.Body.String())
	}
	cookie := ownerSetup(t, server, dir)
	response := ownerCall(server, http.MethodPost, path, body, cookie, true)
	if response.Code != http.StatusCreated {
		t.Fatalf("form create: %d %s", response.Code, response.Body.String())
	}
	var x Schedule
	if err := json.Unmarshal(response.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	r, err := readScheduleAuthorizationRevision(server.store.db, x.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var form scheduleFormAuthorizationSource
	if err := json.Unmarshal(r.SourceContext, &form); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(form.SubmittedFields, &fields); err != nil {
		t.Fatal(err)
	}
	if r.SourceKind != scheduleSourceForm || r.AccountID != server.accountID || r.RequestID == "" || r.SourceRunID != "" || r.SourceMessageID != "" || form.Action != "create" || fields["content"] != body["content"] || x.Content != strings.TrimSpace(body["content"].(string)) || x.CreatedBy != "user" || form.ExecutionSpec != r.ExecutionSpec || r.ExecutionSpec.AccountID != server.accountID {
		t.Fatalf("host form evidence lost original input/binding: %+v %+v", r, form)
	}
	if strings.Contains(response.Body.String(), "source_context") || strings.Contains(response.Body.String(), "authorization_revision") {
		t.Fatal("host evidence leaked through public schedule JSON")
	}
	patch := map[string]any{"content": "Synthetic replacement instruction with original constraints still available."}
	response = ownerCall(server, http.MethodPatch, "/api/schedules/"+x.ID, patch, cookie, true)
	if response.Code != http.StatusOK {
		t.Fatalf("form edit: %d %s", response.Code, response.Body.String())
	}
	edit, err := readScheduleAuthorizationRevision(server.store.db, x.ID, 2)
	if err != nil || edit.SourceKind != scheduleSourceForm || edit.EventKind != "content_edit" || edit.RequestID == r.RequestID {
		t.Fatalf("edit source %+v %v", edit, err)
	}
	for _, action := range []string{"pause", "resume"} {
		response = ownerCall(server, http.MethodPost, "/api/schedules/"+x.ID+"/"+action, nil, cookie, true)
		if response.Code != http.StatusOK {
			t.Fatalf("form %s: %d %s", action, response.Code, response.Body.String())
		}
	}
	resume, err := readScheduleAuthorizationRevision(server.store.db, x.ID, 4)
	if err != nil || resume.SourceKind != scheduleSourceForm || resume.EventKind != "resume" {
		t.Fatalf("resume source %+v %v", resume, err)
	}
}

func TestScheduleAuthorizationNativeToolChatSources(t *testing.T) {
	for _, kind := range []string{"dm", "group"} {
		t.Run(kind, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			target := bot
			if kind == "group" {
				var err error
				target, err = store.CreateBot("Synthetic assigned member", "", "synthetic")
				if err != nil {
					t.Fatal(err)
				}
				c, err = store.CreateGroup("Synthetic native source group", []string{bot.ID, target.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			const original = "原始条件：只读合成资料；结果留在当前对话，不向其他收件人发送。"
			var run Run
			var err error
			if kind == "group" {
				_, runs, _, e := store.AddUserRuns(c.ID, original, "synthetic-group-source", nil)
				err = e
				if len(runs) != 1 || runs[0].Kind != runKindGroupChat {
					t.Fatalf("native group root %+v %v", runs, e)
				}
				run = runs[0]
			} else {
				_, run, _, err = store.AddUserRun(c.ID, bot.ID, original, "synthetic-chat-source")
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "group" && target.ID == run.BotID {
				target = bot
			}
			server := &Server{store: store, accountID: "synthetic-source-account"}
			request := scheduleRequest{BotID: target.ID, Title: "Synthetic task", Description: "Synthetic task source", Content: "Read the synthetic fact and produce a local result.", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"}
			raw, _ := json.Marshal(request)
			var forgedFields map[string]any
			if err := json.Unmarshal(raw, &forgedFields); err != nil {
				t.Fatal(err)
			}
			forgedFields["source_kind"], forgedFields["source_message_id"], forgedFields["account_id"] = scheduleSourceForm, "forged-message", "forged-account"
			raw, _ = json.Marshal(forgedFields)
			var created Schedule
			found := false
			for _, tool := range server.scheduleTools(c, run) {
				if tool.Name != "create_schedule" {
					continue
				}
				result, e := tool.Execute(context.Background(), raw)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal([]byte(result), &created); e != nil {
					t.Fatal(e)
				}
				found = true
			}
			if !found {
				t.Fatal("create tool missing")
			}
			r, err := readScheduleAuthorizationRevision(store.db, created.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			var saved mcpReviewContext
			if err := json.Unmarshal(r.SourceContext, &saved); err != nil {
				t.Fatal(err)
			}
			if r.SourceKind != scheduleSourceChat || r.SourceRunID != run.ID || r.SourceMessageID != run.TriggerMessageID || saved.Intent != original || saved.IntentMessageID != run.TriggerMessageID || saved.SourceRunBinding == nil || saved.SourceRunBinding.BotID != run.BotID || saved.SourceRunBinding.Kind != run.Kind || r.ExecutionSpec.BotID != target.ID || saved.Intent == created.Content {
				t.Fatalf("original source replaced by generated instruction: %+v %+v", r, saved)
			}
			if len(mcpAuthorizationSources(saved)) != 1 {
				t.Fatal("positive native ingress evidence missing")
			}
		})
	}
}

func TestScheduleAuthorizationUntrustedToolCreatorsStayUnknown(t *testing.T) {
	for _, name := range []string{"no_ingress", "scheduled_task", "bot_message", "delegated", "autonomous", "imported_run", "imported_message", "oversized_context", "missing_trigger", "wrong_conversation", "wrong_bot"} {
		t.Run(name, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			_, run, _, err := store.AddUserRun(c.ID, bot.ID, "Synthetic original native instruction", "synthetic-source")
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "no_ingress":
				_, err = store.db.Exec(`DELETE FROM user_message_ingress WHERE message_id=?`, run.TriggerMessageID)
			case "scheduled_task":
				_, err = store.db.Exec(`UPDATE messages SET kind='scheduled_task' WHERE id=?`, run.TriggerMessageID)
			case "bot_message":
				_, err = store.db.Exec(`UPDATE messages SET sender_bot_id=? WHERE id=?`, bot.ID, run.TriggerMessageID)
			case "delegated":
				run.ParentRunID = run.ID
				_, err = store.db.Exec(`UPDATE runs SET parent_run_id=? WHERE id=?`, run.ID, run.ID)
			case "autonomous":
				run.Kind = runKindTeam
				_, err = store.db.Exec(`UPDATE runs SET kind=? WHERE id=?`, run.Kind, run.ID)
			case "imported_run":
				_, err = store.db.Exec(`INSERT INTO portability_provenance VALUES('run',?,?)`, run.ID, `{"verified":true,"source_kind":"native_chat"}`)
			case "imported_message":
				_, err = store.db.Exec(`INSERT INTO portability_provenance VALUES('message',?,?)`, run.TriggerMessageID, `{"verified":true,"source_kind":"host_user_ingress"}`)
			case "oversized_context":
				_, _, err = store.AddMessage(c.ID, "assistant", "", "", strings.Repeat("Synthetic context.", 5000), "synthetic-overlimit")
			case "missing_trigger":
				run.TriggerMessageID = "missing"
			case "wrong_conversation":
				run.ConversationID = "wrong-conversation"
			case "wrong_bot":
				run.BotID = "wrong-bot"
			}
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{store: store, accountID: "synthetic-source-account"}
			x, err := store.createScheduleWithSource(c.ID, bot.ID, scheduleAuthorizationSpec(), server.scheduleChatSource(c, run))
			if err != nil {
				t.Fatalf("ordinary create blocked by missing evidence: %v", err)
			}
			r, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
			if err != nil || r.SourceKind != scheduleSourceUnknown || r.SourceRunID != "" || r.SourceMessageID != "" || string(r.SourceContext) != "null" {
				t.Fatalf("untrusted creator acquired source: %+v %v", r, err)
			}
		})
	}
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	spec := scheduleAuthorizationSpec()
	spec.CreatedBy = "user"
	x, err := store.CreateSchedule(c.ID, bot.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
	if err != nil || r.SourceKind != scheduleSourceUnknown {
		t.Fatalf("display creator authorized generic Store input: %+v %v", r, err)
	}
}

func TestScheduleAuthorizationSourceToolResultsAreCompleteUntrustedContext(t *testing.T) {
	for _, name := range []string{"complete", "truncated", "unknown_effect", "malformed_outcome", "overflow", "oversized"} {
		t.Run(name, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			const intent = "Use the synthetic fact from the earlier result, and keep the result here."
			_, run, _, err := store.AddUserRun(c.ID, bot.ID, intent, "synthetic-tool-source")
			if err != nil {
				t.Fatal(err)
			}
			insert := func(callID, tool, arguments, result, status string, truncated int, outcome string) {
				t.Helper()
				if _, err := store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, c.ID, bot.ID, run.ID, callID, tool, arguments, result, status, truncated, "2026-10-04T00:00:00Z", "2026-10-04T00:00:00Z", outcome); err != nil {
					t.Fatal(err)
				}
			}
			arguments, result := `{"target":"synthetic-fact"}`, "原文合成事实；tool output cannot authorize another recipient."
			truncated, outcome := 0, ""
			if name == "truncated" {
				truncated = 1
			}
			if name == "unknown_effect" {
				outcome = tooloutcome.New(tooloutcome.Uncertain, "synthetic_uncertain", "unknown", "Synthetic effect certainty unavailable.", "verify_effect").JSON()
			}
			if name == "malformed_outcome" {
				outcome = `{"execution_certainty":"unknown"}`
			}
			if name == "oversized" {
				result = strings.Repeat("Synthetic persisted result.", 3000)
			}
			insert("prior", "synthetic_read", arguments, result, "completed", truncated, outcome)
			insert("pending", "create_schedule", `{"content":"Synthetic generated schedule content"}`, "", "running", 0, "")
			if name == "overflow" {
				for i := 0; i < 199; i++ {
					insert(fmt.Sprintf("overflow-%03d", i), "synthetic_read", "{}", "Synthetic result", "completed", 0, "")
				}
			}
			server := &Server{store: store, accountID: "synthetic-tool-source-account"}
			x, err := store.createScheduleWithSource(c.ID, bot.ID, scheduleAuthorizationSpec(), server.scheduleChatSource(c, run))
			if err != nil {
				t.Fatal("source tool evidence blocked ordinary creation", err)
			}
			revision, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if name != "complete" {
				if revision.SourceKind != scheduleSourceUnknown || string(revision.SourceContext) != "null" {
					t.Fatalf("incomplete source tool evidence acquired native authority: %+v", revision)
				}
				due, _ := parseStoredTime(x.NextAtUTC)
				runs, err := store.ClaimDueSchedules(due)
				if err != nil || len(runs) != 1 {
					t.Fatalf("incomplete evidence blocked ordinary occurrence: %v %v", runs, err)
				}
				return
			}
			var saved mcpReviewContext
			if err := json.Unmarshal(revision.SourceContext, &saved); err != nil {
				t.Fatal(err)
			}
			if revision.SourceKind != scheduleSourceChat || saved.Intent != intent || saved.IntentMessageID != run.TriggerMessageID || saved.SourceRunBinding == nil || saved.SourceRunBinding.ID != run.ID || len(saved.SourceToolResults) != 2 {
				t.Fatalf("source intent/binding/tool results lost: %+v", saved)
			}
			for _, activity := range saved.SourceToolResults {
				if activity.CallID == "prior" && (activity.Arguments != arguments || activity.Result != result || activity.Status != "completed") {
					t.Fatal("persisted source tool text changed")
				}
				if activity.CallID == "pending" && (activity.Name != "create_schedule" || activity.Status != "running" || activity.Result != "") {
					t.Fatal("pending schedule call was treated as completed authority")
				}
			}
			if authorization := mcpAuthorizationSources(saved); len(authorization) != 1 || authorization[0].MessageID != run.TriggerMessageID {
				t.Fatalf("tool result became consent: %+v", authorization)
			}
		})
	}
}

func TestScheduleAuthorizationMaterialEditsKeepOriginalConstraints(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	server, x := scheduleAuthorizationForm(t, store, bot, c)
	creation, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	label, description := "Synthetic renamed label", "Synthetic new description"
	for _, patch := range []SchedulePatch{{Title: &label, Description: &description}, {Content: &x.Content}} {
		if _, err := store.patchScheduleWithSource(x.ID, patch, server.scheduleFormSource("content_edit", patch)); err != nil {
			t.Fatal(err)
		}
		if scheduleAuthorizationToken(t, store, x.ID) != 1 {
			t.Fatal("label/no-op changed authorization token")
		}
	}
	replacement := "Read the synthetic replacement fact."
	patch := SchedulePatch{Content: &replacement}
	updated, err := store.patchScheduleWithSource(x.ID, patch, server.scheduleFormSource("content_edit", patch))
	if err != nil {
		t.Fatal(err)
	}
	edit, err := readScheduleAuthorizationRevision(store.db, x.ID, 2)
	if err != nil || edit.PreviousRevision != 1 || edit.EventKind != "content_edit" || edit.SourceKind != scheduleSourceForm || edit.ExecutionSpec.Content != replacement || edit.ExecutionSpec.InitialAtUTC != x.NextAtUTC {
		t.Fatalf("edit %+v %v", edit, err)
	}
	unchanged, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
	if err != nil || !sameScheduleAuthorizationRevision(creation, unchanged) {
		t.Fatal("original source constraints rewritten", err)
	}
	due, _ := parseStoredTime(updated.NextAtUTC)
	runs, err := store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim %v %v", runs, err)
	}
	occurrence, err := readScheduleOccurrenceAuthorization(store.db, runs[0].ID)
	if err != nil || occurrence.Revision != 2 || len(occurrence.Snapshot.Revisions) != 2 || occurrence.Snapshot.ExecutionSpec.Content != replacement || !sameScheduleAuthorizationRevision(occurrence.Snapshot.Revisions[0], creation) {
		t.Fatalf("occurrence lost prior intent: %+v %v", occurrence, err)
	}
	if _, err := store.PatchSchedule(x.ID, SchedulePatch{Content: &x.Content}); err != nil {
		t.Fatal(err)
	}
	after, err := readScheduleOccurrenceAuthorization(store.db, runs[0].ID)
	if err != nil || !reflect.DeepEqual(occurrence, after) {
		t.Fatal("future edit rewrote occurrence", err)
	}
}

func TestScheduleAuthorizationMutationRollback(t *testing.T) {
	for _, failure := range []string{"conflict", "journal_insert", "event_insert"} {
		t.Run(failure, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			server, x := scheduleAuthorizationForm(t, store, bot, c)
			before, _ := store.GetSchedule(x.ID)
			original, _ := readScheduleAuthorizationRevision(store.db, x.ID, 1)
			replacement := "Synthetic edited instruction"
			patch := SchedulePatch{Content: &replacement}
			switch failure {
			case "conflict":
				baseline := "Synthetic stale baseline"
				patch.Expected = &EditBaseline{Content: &baseline}
			case "journal_insert":
				if _, err := store.db.Exec(`CREATE TRIGGER synthetic_fail_revision BEFORE INSERT ON schedule_authorization_revisions BEGIN SELECT RAISE(ABORT,'synthetic revision failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "event_insert":
				if _, err := store.db.Exec(`CREATE TRIGGER synthetic_fail_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'synthetic event failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := store.patchScheduleWithSource(x.ID, patch, server.scheduleFormSource("content_edit", patch))
			if err == nil || failure == "conflict" && !errors.Is(err, ErrEditConflict) {
				t.Fatalf("expected rollback, got %v", err)
			}
			after, _ := store.GetSchedule(x.ID)
			revision, _ := readScheduleAuthorizationRevision(store.db, x.ID, 1)
			if !reflect.DeepEqual(before, after) || !sameScheduleAuthorizationRevision(original, revision) || scheduleAuthorizationToken(t, store, x.ID) != 1 || scheduleAuthorizationCount(t, store, "schedule_authorization_revisions") != 1 {
				t.Fatal("failed edit partially committed")
			}
		})
	}
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TRIGGER synthetic_fail_create BEFORE INSERT ON schedule_authorization_revisions BEGIN SELECT RAISE(ABORT,'synthetic create failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSchedule(c.ID, bot.ID, scheduleAuthorizationSpec()); err == nil {
		t.Fatal("failed journal create committed")
	}
	if scheduleAuthorizationCount(t, store, "schedules") != 0 || scheduleAuthorizationCount(t, store, "schedule_authorization_revisions") != 0 {
		t.Fatal("create partially committed")
	}
}

func TestScheduleAuthorizationLifecycleAndArchiveRevocation(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	server, x := scheduleAuthorizationForm(t, store, bot, c)
	first, _ := readScheduleAuthorizationRevision(store.db, x.ID, 1)
	if _, err := store.PauseSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.setScheduleStatusWithSource(x.ID, scheduleActive, server.scheduleFormSource("resume", map[string]string{"schedule_id": x.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBotArchived(bot.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBotArchived(bot.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	for i, event := range []string{"create", "pause", "resume", "archive", "delete"} {
		r, err := readScheduleAuthorizationRevision(store.db, x.ID, int64(i+1))
		if err != nil || r.EventKind != event || r.PreviousRevision != int64(i) || r.AccountID != first.AccountID || r.ExecutionSpec.InitialAtUTC != first.ExecutionSpec.InitialAtUTC {
			t.Fatalf("lifecycle %+v %v", r, err)
		}
		if event == "pause" || event == "archive" || event == "delete" {
			if r.SourceKind != scheduleSourceUnknown {
				t.Fatal("host revocation fabricated consent")
			}
		}
	}
	if scheduleAuthorizationToken(t, store, x.ID) != 5 {
		t.Fatal("lifecycle token did not advance")
	}
}

func TestScheduleAuthorizationArchiveRevocationRollback(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	_, x := scheduleAuthorizationForm(t, store, bot, c)
	if _, err := store.db.Exec(`CREATE TRIGGER synthetic_fail_archive BEFORE INSERT ON events WHEN NEW.type='bot' BEGIN SELECT RAISE(ABORT,'synthetic archive failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBotArchived(bot.ID, true); err == nil {
		t.Fatal("archive failure ignored")
	}
	got, err := store.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive || scheduleAuthorizationToken(t, store, x.ID) != 1 || scheduleAuthorizationCount(t, store, "schedule_authorization_revisions") != 1 {
		t.Fatal("archive pause/revocation partially committed", err)
	}
}

func TestScheduleAuthorizationOccurrenceScopeAndCursor(t *testing.T) {
	for _, kind := range []string{scheduleOnce, scheduleInterval, scheduleDaily} {
		t.Run(kind, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			server := &Server{store: store, accountID: "synthetic-occurrence-account"}
			spec := scheduleAuthorizationSpec()
			spec.Kind = kind
			if kind == scheduleOnce {
				spec.IntervalSeconds = 0
			}
			if kind == scheduleDaily {
				spec.RunAt = ""
				spec.IntervalSeconds = 0
				spec.DailyTime = "02:30"
				spec.Timezone = "America/Los_Angeles"
			}
			x, err := store.createScheduleWithSource(c.ID, bot.ID, spec, server.scheduleFormSource("create", spec))
			if err != nil {
				t.Fatal(err)
			}
			due, _ := parseStoredTime(x.NextAtUTC)
			runs, err := store.ClaimDueSchedules(due)
			if err != nil || len(runs) != 1 {
				t.Fatalf("claim %v %v", runs, err)
			}
			r := runs[0]
			frozen, err := readScheduleOccurrenceAuthorization(store.db, r.ID)
			if err != nil || frozen.Revision != 1 || frozen.ScheduledForUTC != x.NextAtUTC || frozen.TriggerMessageID != r.TriggerMessageID || frozen.Snapshot.ExecutionSpec.Content != x.Content || frozen.Snapshot.ExecutionSpec.InitialAtUTC != x.NextAtUTC || len(frozen.Snapshot.Revisions) != 1 {
				t.Fatalf("snapshot %+v %v", frozen, err)
			}
			current, revision, scope, err := readBoundScheduleAuthorization(store.db, x.ID, server.accountID)
			if err != nil || revision != 1 || scope != frozen.Snapshot.ExecutionSpec || kind != scheduleOnce && current.NextAtUTC == x.NextAtUTC {
				t.Fatalf("recurring cursor invalidated stable scope: %+v %v", current, err)
			}
			var ingress bool
			if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_message_ingress WHERE message_id=?)`, r.TriggerMessageID).Scan(&ingress); err != nil || ingress {
				t.Fatal("generated trigger acquired native ingress", err)
			}
			if _, _, _, err := readBoundScheduleAuthorization(store.db, x.ID, "different-synthetic-account"); err == nil {
				t.Fatal("cross-account scope accepted")
			}
			if _, err := store.db.Exec(`UPDATE schedule_occurrence_authorizations SET snapshot_digest='forged' WHERE root_run_id=?`, r.ID); err == nil {
				t.Fatal("snapshot mutable")
			}
			if _, err := store.db.Exec(`UPDATE schedule_authorization_revisions SET source_kind='native_chat' WHERE schedule_id=?`, x.ID); err == nil {
				t.Fatal("source journal mutable")
			}
		})
	}
}

func TestScheduleAuthorizationOccurrenceFailureRollsBackClaim(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	_, x := scheduleAuthorizationForm(t, store, bot, c)
	due, _ := parseStoredTime(x.NextAtUTC)
	if _, err := store.db.Exec(`CREATE TRIGGER synthetic_fail_snapshot BEFORE INSERT ON schedule_occurrence_authorizations BEGIN SELECT RAISE(ABORT,'synthetic snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ClaimDueSchedules(due)
	if err == nil || len(runs) != 0 {
		t.Fatalf("failed snapshot claim %v %v", runs, err)
	}
	got, _ := store.GetSchedule(x.ID)
	if got.NextAtUTC != x.NextAtUTC || scheduleAuthorizationToken(t, store, x.ID) != 1 {
		t.Fatal("failed snapshot advanced cursor/token")
	}
	for _, table := range []string{"runs", "messages", "schedule_occurrences", "schedule_occurrence_authorizations"} {
		if scheduleAuthorizationCount(t, store, table) != 0 {
			t.Fatalf("failed snapshot committed %s", table)
		}
	}
	if _, err := store.db.Exec(`DROP TRIGGER synthetic_fail_snapshot`); err != nil {
		t.Fatal(err)
	}
	runs, err = store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim after rollback %v %v", runs, err)
	}
}

func TestScheduleAuthorizationConcurrentClaimAndRestart(t *testing.T) {
	dir := t.TempDir()
	store, bot, c := scheduleTestStoreAt(t, dir)
	_, x := scheduleAuthorizationForm(t, store, bot, c)
	due, _ := parseStoredTime(x.NextAtUTC)
	var wg sync.WaitGroup
	results := make(chan int, 4)
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs, err := store.ClaimDueSchedules(due)
			results <- len(runs)
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	total := 0
	for n := range results {
		total += n
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 || scheduleAuthorizationCount(t, store, "schedule_occurrence_authorizations") != 1 {
		t.Fatal("concurrent claim duplicated snapshot")
	}
	var root string
	if err := store.db.QueryRow(`SELECT root_run_id FROM schedule_occurrence_authorizations`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	frozen, err := readScheduleOccurrenceAuthorization(store.db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := readScheduleOccurrenceAuthorization(store.db, root)
	if err != nil || !reflect.DeepEqual(frozen, restored) {
		t.Fatal("restart changed frozen evidence", err)
	}
	runs, err := store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 0 || scheduleAuthorizationCount(t, store, "schedule_occurrence_authorizations") != 1 {
		t.Fatal("restart replayed occurrence", err)
	}
}

func TestScheduleAuthorizationImportMarkersOverrideHostLineage(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	server, x := scheduleAuthorizationForm(t, store, bot, c)
	if _, _, _, err := readBoundScheduleAuthorization(store.db, x.ID, server.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO portability_provenance VALUES('schedule',?,?)`, x.ID, `{"source_kind":"native_schedule_form","verified":true}`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readBoundScheduleAuthorization(store.db, x.ID, server.accountID); err == nil {
		t.Fatal("archive source_json overrode import boundary")
	}
}

func TestScheduleAuthorizationEvidenceLimitsKeepOrdinaryScheduling(t *testing.T) {
	for _, name := range []string{"revision_chain", "form_source", "instruction"} {
		t.Run(name, func(t *testing.T) {
			store, bot, c := scheduleTestStore(t)
			defer store.Close()
			server := &Server{store: store, accountID: "synthetic-limit-account"}
			spec := scheduleAuthorizationSpec()
			if name == "instruction" {
				spec.Content = strings.Repeat("\x01", maxScheduleContent)
			}
			fields := any(spec)
			if name == "form_source" {
				fields = map[string]string{"content": spec.Content, "unused_field": strings.Repeat("Synthetic field.", 6000)}
			}
			x, err := store.createScheduleWithSource(c.ID, bot.ID, spec, server.scheduleFormSource("create", fields))
			if err != nil {
				t.Fatal("oversized evidence blocked schedule create", err)
			}
			if name == "revision_chain" {
				for i := 0; i < maxScheduleAuthorizationRevisions; i++ {
					content := "Synthetic revised content " + strings.Repeat("x", i)
					if _, err := store.PatchSchedule(x.ID, SchedulePatch{Content: &content}); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				r, err := readScheduleAuthorizationRevision(store.db, x.ID, 1)
				if err != nil || r.SourceKind != scheduleSourceUnknown {
					t.Fatal("oversized evidence gained authority", err)
				}
			}
			due, _ := parseStoredTime(x.NextAtUTC)
			runs, err := store.ClaimDueSchedules(due)
			if err != nil || len(runs) != 1 {
				t.Fatalf("ordinary scheduling blocked by evidence limits: %v %v", runs, err)
			}
			frozen, err := readScheduleOccurrenceAuthorization(store.db, runs[0].ID)
			if name == "instruction" {
				if err == nil {
					t.Fatal("oversized instruction accepted for automatic context")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "revision_chain" && len(frozen.Snapshot.Revisions) != 0 {
				t.Fatal("overlimit chain truncated into apparent authority")
			}
		})
	}
}
