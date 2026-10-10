package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func prog(text string) textPart { return textPart{Text: text, Progress: true} }
func ans(text string) textPart  { return textPart{Text: text} }

func TestSplitProgress(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []textPart
	}{
		{"no tags", "Plain answer.", []textPart{ans("Plain answer.")}},
		{"mixed in order", "Looking.\n<progress>checking the page</progress>\nFound it.", []textPart{ans("Looking."), prog("checking the page"), ans("Found it.")}},
		{"only progress", "<progress>checking the page</progress>", []textPart{prog("checking the page")}},
		{"unclosed runs to the end", "Hi. <progress>working on it", []textPart{ans("Hi."), prog("working on it")}},
		{"whitespace-only parts dropped", "  <progress>note</progress>\n\n  ", []textPart{prog("note")}},
		{"empty tag dropped", "<progress></progress>Answer", []textPart{ans("Answer")}},
		{"chinese", "已改为 2:30。<progress>正在检查页面…</progress>完成。", []textPart{ans("已改为 2:30。"), prog("正在检查页面…"), ans("完成。")}},
		{"markdown inside", "<progress>- step **one**\n- `step two`</progress>", []textPart{prog("- step **one**\n- `step two`")}},
		{"case-insensitive", "<PROGRESS>x</Progress>y", []textPart{prog("x"), ans("y")}},
		{"nested open dropped", "<progress>a<progress>b</progress>c", []textPart{prog("ab"), ans("c")}},
		{"stray close dropped", "a</progress>b", []textPart{ans("ab")}},
		{"garbage angle brackets stay text", "if a < b and <progres> and <progress x>", []textPart{ans("if a < b and <progres> and <progress x>")}},
		{"adjacent same-kind merge", "a<progress>b</progress><progress>c</progress>d", []textPart{ans("a"), prog("bc"), ans("d")}},
		{"empty", "", nil},
		{"inline code keeps the tag literal", "The HTML `<progress>` element. <progress>checking</progress>", []textPart{ans("The HTML `<progress>` element."), prog("checking")}},
		{"double-backtick span may hold a backtick", "``a ` <progress>`` <progress>n</progress>", []textPart{ans("``a ` <progress>``"), prog("n")}},
		{"fenced block keeps tags literal", "<progress>x</progress>\n```html\n<progress></progress>\n```\n<progress>y</progress>", []textPart{prog("x"), ans("```html\n<progress></progress>\n```"), prog("y")}},
		{"tilde fence", "~~~\n<progress>\n~~~\n<progress>y</progress>", []textPart{ans("~~~\n<progress>\n~~~"), prog("y")}},
		{"longer fence closes a shorter one, not vice versa", "````\n```\n<progress>\n````\n<progress>y</progress>", []textPart{ans("````\n```\n<progress>\n````"), prog("y")}},
		{"a mid-line triple run is an inline span, not a fence", "a ``` <progress> ``` <progress>n</progress>\nz", []textPart{ans("a ``` <progress> ```"), prog("n"), ans("z")}},
		{"unclosed inline span ends at the line end", "a `b\n<progress>n</progress>", []textPart{ans("a `b"), prog("n")}},
		{"code inside a note stays in the note", "<progress>run `</progress>` now</progress>done", []textPart{prog("run `</progress>` now"), ans("done")}},
		{"tildes mid-line are text", "a ~~~ b <progress>n</progress>", []textPart{ans("a ~~~ b"), prog("n")}},
	} {
		if got := splitProgress(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v want %#v", tc.name, got, tc.want)
		}
	}
}

func TestScannerSplitAcrossChunksMatchesWholeText(t *testing.T) {
	text := "已完成。<progress>checking <the> page</progress>Done <progress>tail"
	whole := stripProgressTags(text)
	for cut := 0; cut <= len(text); cut++ {
		var sc progressScanner
		var b strings.Builder
		for _, chunk := range []string{text[:cut], text[cut:]} {
			for _, p := range sc.feed(chunk) {
				b.WriteString(p.Text)
			}
		}
		for _, p := range sc.finish() {
			b.WriteString(p.Text)
		}
		if b.String() != whole {
			t.Fatalf("cut %d: %q != %q", cut, b.String(), whole)
		}
	}
	if whole != "已完成。checking <the> pageDone tail" {
		t.Fatalf("whole=%q", whole)
	}
}

// Every way of cutting the text into chunks must equal the whole-text result,
// including cuts inside tags, backtick runs and fences.
func TestScannerCodeAwareChunksMatchWholeText(t *testing.T) {
	text := "Use `<progress>` here.\n<progress>scan ``a`b``</progress>\n```\n<progress></progress>\n```\nDone<progress>tail"
	whole := stripProgressTags(text)
	if whole != "Use `<progress>` here.\nscan ``a`b``\n```\n<progress></progress>\n```\nDonetail" {
		t.Fatalf("whole=%q", whole)
	}
	scan := func(chunks ...string) string {
		var sc progressScanner
		var b strings.Builder
		for _, chunk := range chunks {
			for _, p := range sc.feed(chunk) {
				b.WriteString(p.Text)
			}
		}
		for _, p := range sc.finish() {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	for cut := 0; cut <= len(text); cut++ {
		if got := scan(text[:cut], text[cut:]); got != whole {
			t.Fatalf("cut %d: %q != %q", cut, got, whole)
		}
	}
	short := "a ``<progress>`` <progress>n</progress>\n```\n<progress>\n```"
	wholeShort := stripProgressTags(short)
	for i := 0; i <= len(short); i++ {
		for j := i; j <= len(short); j++ {
			if got := scan(short[:i], short[i:j], short[j:]); got != wholeShort {
				t.Fatalf("cuts %d,%d: %q != %q", i, j, got, wholeShort)
			}
		}
	}
	// A finished scanner starts the next turn outside code and outside a note.
	var sc progressScanner
	sc.feed("```\n<progress>")
	sc.finish()
	if got := splitProgressWith(&sc, "<progress>n</progress>ok"); !reflect.DeepEqual(got, []textPart{prog("n"), ans("ok")}) {
		t.Fatalf("state leaked across finish: %#v", got)
	}
}

func splitProgressWith(sc *progressScanner, text string) []textPart {
	var parts []textPart
	for _, p := range append(sc.feed(text), sc.finish()...) {
		if p.Text = strings.TrimSpace(p.Text); p.Text != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func TestFinalSplit(t *testing.T) {
	notes, answer := finalSplit("<progress>checking</progress>All done.")
	if !reflect.DeepEqual(notes, []textPart{prog("checking")}) || answer != "All done." {
		t.Fatalf("%#v %q", notes, answer)
	}
	notes, answer = finalSplit("<progress>only a note</progress>")
	if len(notes) != 0 || answer != "only a note" {
		t.Fatalf("all-progress final must be the answer: %#v %q", notes, answer)
	}
	notes, answer = finalSplit("<progress>a</progress><progress>b</progress>")
	if len(notes) != 0 || answer != "ab" {
		t.Fatalf("%#v %q", notes, answer)
	}
	notes, answer = finalSplit("Answer.<progress>trailing</progress>")
	if answer != "Answer." || !reflect.DeepEqual(notes, []textPart{prog("trailing")}) {
		t.Fatalf("%#v %q", notes, answer)
	}
}

func TestPublishAssistantTurnSplitsByProgressTag(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	draft, err := s.BeginStream(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendStreamDelta(ctx, r.ID, "x"); err != nil {
		t.Fatal(err)
	}
	// Incident shape: an untagged answer stays visible; a tagged note folds.
	first, ok, err := s.PublishAssistantTurn(ctx, r.ID, 1, "OK, changed to 2:30 daily.<progress>checking the page</progress>")
	if err != nil || !ok || first.ID != draft.MessageID || first.Kind != "segment" || first.Content != "OK, changed to 2:30 daily." {
		t.Fatalf("first=%+v ok=%v err=%v", first, ok, err)
	}
	// Replay returns the first message and adds nothing.
	again, ok, err := s.PublishAssistantTurn(ctx, r.ID, 1, "OK, changed to 2:30 daily.<progress>checking the page</progress>")
	if err != nil || !ok || again.ID != first.ID {
		t.Fatalf("replay=%+v ok=%v err=%v", again, ok, err)
	}
	if _, _, err := s.PublishAssistantTurn(ctx, r.ID, 2, "<progress>only a note</progress>"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishAssistantTurn(ctx, r.ID, 3, "<progress> </progress>"); err == nil {
		t.Fatal("a turn with no content must not publish")
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "Other work finished."); err != nil || !done {
		t.Fatal(err, done)
	}
	msgs, _, err := s.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var prev int64
	for _, m := range msgs {
		got = append(got, m.Kind+"|"+m.Content)
		if m.Seq <= prev {
			t.Fatalf("seq not increasing: %+v", msgs)
		}
		prev = m.Seq
		if strings.Contains(m.Content, "progress>") {
			t.Fatalf("tag leaked: %q", m.Content)
		}
	}
	want := []string{"segment|OK, changed to 2:30 daily.", "progress|checking the page", "progress|only a note", "|Other work finished."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStreamDraftNeverShowsProgressTags(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	onDelta, flush, _, err := s.StreamControls(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"Hi ", "<pro", "gress>checking</prog", "ress> done </", "progress", "> ok <pr"} {
		onDelta(chunk)
		time.Sleep(60 * time.Millisecond) // past the flush interval so each chunk persists
		d, _ := s.StreamDraft(r.ID)
		if strings.Contains(d.Content, "<progress") || strings.Contains(d.Content, "</progress") {
			t.Fatalf("draft after %q leaked a tag: %q", chunk, d.Content)
		}
	}
	flush()
	d, _ := s.StreamDraft(r.ID)
	if d.Content != "Hi checking done  ok <pr" {
		t.Fatalf("draft=%q", d.Content)
	}
}

func TestFinalTurnNotesPrecedeVisibleAnswer(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendStreamDelta(ctx, r.ID, "streamed"); err != nil {
		t.Fatal(err)
	}
	notes, answer := finalSplit("<progress>verifying</progress>The report is ready.")
	if _, ok, err := s.publishAssistantParts(ctx, r.ID, 0, notes, false); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, answer); err != nil || !done {
		t.Fatal(done, err)
	}
	msgs, _, err := s.Messages(c.ID, 0, 50)
	if err != nil || len(msgs) != 2 || msgs[0].Kind != "progress" || msgs[1].Kind != "" || msgs[1].Content != "The report is ready." || msgs[0].Seq >= msgs[1].Seq {
		t.Fatalf("msgs=%+v err=%v", msgs, err)
	}
}

// A budget-exhausted final turn goes through the same split as a normal final
// turn: its <progress> notes fold, the rest is the answer, no tag is stored.
func TestBudgetExhaustedPartialOutputSplitsProgressTags(t *testing.T) {
	s, run, messages := runReviewDraft(t, func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
		return func(_ context.Context, req runtime.Request) (runtime.Result, error) {
			req.OnDelta("<progress>re-checking</progress>Two mails so far.")
			return runtime.Result{BudgetExhausted: true, BudgetReason: "time", Content: "<progress>re-checking</progress>Two mails so far."}, nil
		}
	})
	if got, err := s.store.GetRun(run.ID); err != nil || got.Status != "failed" {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	var got []string
	for _, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		if strings.Contains(m.Content, "progress>") {
			t.Fatalf("tag leaked into a stored message: %q", m.Content)
		}
		got = append(got, m.Kind+"|"+m.Content)
	}
	want := []string{"segment|" + demotedAnswer, "progress|re-checking", "|Two mails so far."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestScheduleFailureDetailStripsProgressTags(t *testing.T) {
	got := scheduleFailureDetail("<progress>looking</progress> No receipt.", Bot{})
	if strings.Contains(got, "progress>") || !strings.HasSuffix(got, "looking No receipt.") {
		t.Fatalf("detail=%q", got)
	}
}

type taggedExpiryEngine struct{}

func (taggedExpiryEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	return runtime.Result{Content: "<progress>reviewing the stopped steps</progress>The write was not executed; nothing else changed."}, nil
}

// The approval-expiry recovery stage resumes the run's transcript, so the
// model may keep using <progress>; its conclusion must store no tag.
func TestApprovalExpiryConclusionStoresNoProgressTag(t *testing.T) {
	dir := t.TempDir()
	s, _, r, q := expiryStoreFixture(t, dir)
	if _, err := s.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), q.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	server, err := NewServer(Config{DataDir: dir, IsolatedWorkspace: true, Engine: taggedExpiryEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	waitForRunStatus(t, server, r.ID, "failed")
	assertExpiryConcluded(t, server.store, r, q)
	var content string
	if err := server.store.db.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND kind<>'progress'`, r.ID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "progress>") || !strings.HasSuffix(content, "The write was not executed; nothing else changed.") || strings.Contains(content, "reviewing the stopped steps") {
		t.Fatalf("conclusion=%q", content)
	}
}
