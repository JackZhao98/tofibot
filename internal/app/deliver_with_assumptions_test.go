package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// noteThenAnswerEngine makes the first addressed Bot leave its work as a say
// note and answer in a short line; the others answer with a fixed sentence.
type noteThenAnswerEngine struct {
	recordingEngine
	noteBot string
	note    string
	mu2     sync.Mutex
}

func (e *noteThenAnswerEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.noteBot {
		if _, err := callTool(ctx, req, "say", "note-"+req.RunID, map[string]any{"purpose": "note", "text": e.note}); err != nil {
			return runtime.Result{Content: "ERR " + err.Error()}, nil
		}
	}
	return e.recordingEngine.Run(ctx, req)
}

func TestLaterCoAddressedBotReadsEarlierBotsFoldedNoteAndNotesStayOffTheTimeline(t *testing.T) {
	e := &noteThenAnswerEngine{note: "DRAFT-INPUT plain words about the web"}
	f := newDedupeFixture(t, e)
	e.noteBot = f.tofi.ID
	e.say = map[string]string{f.tofi.ID: "My part is done.", f.meta.ID: "Final version."}
	f.send(t, "@tofi @metaphor one final version please")
	f.settle(t)
	if !strings.Contains(e.seen[f.meta.ID], "DRAFT-INPUT plain words about the web") {
		t.Fatalf("the later Bot did not read the earlier Bot's note:\n%s", e.seen[f.meta.ID])
	}
	if n := countMessages(t, f.server.store, f.group.ID, `kind='' AND content LIKE '%DRAFT-INPUT%'`); n != 0 {
		t.Fatalf("the note reached the visible timeline %d times", n)
	}
}

func TestCoAddressedNoteStatesTheSingleResultPrinciple(t *testing.T) {
	e := &recordingEngine{}
	f := newDedupeFixture(t, e)
	e.say = map[string]string{f.tofi.ID: "a", f.meta.ID: "b"}
	f.send(t, "@tofi @metaphor one final version please")
	f.settle(t)
	for _, id := range []string{f.tofi.ID, f.meta.ID} {
		if !strings.Contains(e.seen[id], "only the member that completes it publishes it") {
			t.Fatalf("co-addressed note lacks the single-result principle for %s:\n%s", id, e.seen[id])
		}
	}
}

func TestSharedGuidanceDeliversWithAssumptionsAndWritesTheText(t *testing.T) {
	for _, want := range []string{"proceed on sensible assumptions", "only when the gap blocks the work", "sending is only ever the person's step"} {
		if !strings.Contains(taskCompletionGuidance, want) {
			t.Fatalf("taskCompletionGuidance missing %q", want)
		}
	}
	s, conv, run := mailDraftFixture(t)
	for _, tool := range s.questionTools(conv, run) {
		if tool.Name == "ask_user_question" && !strings.Contains(tool.Description, "no reasonable stated assumption") {
			t.Fatal("ask_user_question does not limit itself to blocking questions")
		}
	}
}

func TestMailDraftWithoutRecipientIsEditableAndCannotBeSentUntilToExists(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	var tool Tool
	for _, c := range s.mailDraftTools(conv, run) {
		if c.Name == "prepare_email" {
			tool = c
		}
	}
	out, err := tool.Execute(context.Background(), []byte(`{"subject":"Heater repair","body":"Hello, the heater has been broken for a week."}`))
	if err != nil || !strings.Contains(out, "no recipient yet") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	drafts, _ := s.store.ListMailDrafts(conv.ID)
	if len(drafts) != 1 || drafts[0].To != "" || drafts[0].Subject != "Heater repair" {
		t.Fatalf("drafts=%+v", drafts)
	}
	d := drafts[0]
	r := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":1}`))
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, r, "mail-drafts/"+d.ID+"/send")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "recipient_missing") {
		t.Fatalf("send without To: %d %s", w.Code, w.Body.String())
	}
	if got, err := s.store.EditMailDraft(d.ID, 1, "landlord@example.com", d.Subject, d.Body); err != nil || got.To != "landlord@example.com" {
		t.Fatalf("edit: %+v %v", got, err)
	}
	if _, err := s.store.EditMailDraft(d.ID, 2, "not-an-address", d.Subject, d.Body); err == nil {
		t.Fatal("an invalid address must still be refused")
	}
}

func TestCoAddressedNoteMayCarryAWholeDraftButAPlainNoteStaysShort(t *testing.T) {
	long := strings.Repeat("plain words ", 200) // about 2400 characters
	e := &noteThenAnswerEngine{note: long}
	f := newDedupeFixture(t, e)
	e.noteBot = f.tofi.ID
	e.say = map[string]string{f.tofi.ID: "My part is done.", f.meta.ID: "Final version."}
	f.send(t, "@tofi @metaphor one final version please")
	f.settle(t)
	if !strings.Contains(e.seen[f.meta.ID], strings.TrimSpace(long)) {
		t.Fatal("a long note from a co-addressed member was refused")
	}
	if !strings.Contains(sayToolDescription, "never on a request for details it already assumed") {
		t.Fatal("say description lost the end-on-the-result principle")
	}
}

// noteThenSilentEngine makes one addressed Bot leave its part as a folded note
// and end its turn silently; the others answer with a fixed sentence.
type noteThenSilentEngine struct {
	recordingEngine
	inputBot  string
	note      string
	silentErr string
}

func (e *noteThenSilentEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.inputBot {
		if e.note != "" {
			if _, err := callTool(ctx, req, "say", "note-"+req.RunID, map[string]any{"purpose": "note", "text": e.note}); err != nil {
				return runtime.Result{Content: "ERR " + err.Error()}, nil
			}
		}
		for _, tool := range req.Tools {
			if tool.Name == "respond" {
				if _, err := tool.Execute(ctx, []byte(`{"disposition":"silent"}`)); err != nil {
					e.mu.Lock()
					e.silentErr = err.Error()
					e.mu.Unlock()
				}
			}
		}
		e.mu.Lock()
		e.order = append(e.order, req.BotID)
		e.mu.Unlock()
		return runtime.Result{}, nil
	}
	return e.recordingEngine.Run(ctx, req)
}

func TestCoAddressedInputMemberEndsSilentAndOwnerSeesOneMessage(t *testing.T) {
	e := &noteThenSilentEngine{note: "DRAFT-INPUT folded part"}
	f := newDedupeFixture(t, e)
	e.inputBot = f.tofi.ID
	e.say = map[string]string{f.meta.ID: "The single combined result."}
	f.send(t, "@tofi @metaphor work it out together, one recommendation back")
	runs := f.settle(t)
	if e.silentErr != "" {
		t.Fatalf("the input member's silent ending was refused: %s", e.silentErr)
	}
	if len(runs) != 2 || runs[0].Status != "done" || runs[1].Status != "done" {
		t.Fatalf("runs=%+v", runs)
	}
	if n := countMessages(t, f.server.store, f.group.ID, `role='assistant' AND kind='' AND TRIM(content)<>''`); n != 1 {
		t.Fatalf("owner sees %d visible assistant messages, want exactly 1", n)
	}
	if n := countMessages(t, f.server.store, f.group.ID, `kind='progress' AND client_message_id LIKE 'say-note:%' AND content LIKE '%DRAFT-INPUT%'`); n != 1 {
		t.Fatalf("folded notes=%d", n)
	}
	var origin string
	if err := f.server.store.db.QueryRow(`SELECT final_origin FROM runs WHERE id=?`, runs[0].ID).Scan(&origin); err != nil || origin != finalOriginNone {
		t.Fatalf("input member final_origin=%q err=%v", origin, err)
	}
	if !strings.Contains(e.seen[f.meta.ID], "DRAFT-INPUT folded part") {
		t.Fatal("the final member did not read the folded note")
	}
}

func TestCoAddressedLastMemberAndNoteLessMemberCannotEndSilent(t *testing.T) {
	// The last member owes the visible answer; so does a member that left no note.
	for _, tc := range []struct {
		name  string
		input string // which bot attempts the silent ending
		note  bool
	}{{"last member", "meta", true}, {"first member without a note", "tofi", false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := &noteThenSilentEngine{note: "x part"}
			f := newDedupeFixture(t, e)
			if tc.input == "meta" {
				e.inputBot = f.meta.ID
			} else {
				e.inputBot = f.tofi.ID
				e.note = ""
			}
			e.say = map[string]string{f.tofi.ID: "first answer", f.meta.ID: "second answer"}
			f.send(t, "@tofi @metaphor one recommendation please")
			f.settle(t)
			if e.silentErr == "" {
				t.Fatal("the silent ending was accepted")
			}
		})
	}
}
