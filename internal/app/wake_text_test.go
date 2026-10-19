package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const wakeBrief = "Give one colloquial sentence of your own view, in Chinese, with at most one comparison, no headings or lists."

// Under the flag an @mention inside a Bot's posted text is only a reference:
// it starts no run, whether the text arrives through say(answer) or as a text
// final.
func TestMemberMentionInPostedTextWakesNobodyUnderTheFlag(t *testing.T) {
	for _, viaSay := range []bool{true, false} {
		var f dedupeFixture
		engine := &mentionEngine{}
		f = newDedupeFixture(t, engine)
		engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
			if req.BotID != f.tofi.ID {
				return "must not run"
			}
			words := "@metaphor " + wakeBrief
			if viaSay {
				return reply(ctx, req, words, nil)
			}
			return words
		}
		f.post(t, "@tofi one sentence on breakfast", f.tofi)
		f.start(engine)
		runs := f.settle(t)
		if len(runs) != 1 {
			t.Fatalf("viaSay=%v: a mention in posted text started %d runs", viaSay, len(runs)-1)
		}
	}
}

// Flag off: a Bot-text leading mention still dispatches, as before.
func TestMemberMentionInPostedTextStillWakesFlagOff(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	if err := f.server.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.tofi.ID && !strings.Contains(text, "[completed colleague task]") {
			return "@metaphor " + wakeBrief
		}
		return "Done."
	}
	f.post(t, "@tofi one sentence on breakfast", f.tofi)
	f.start(engine)
	woken := 0
	for _, r := range f.settle(t) {
		if r.BotID == f.meta.ID {
			woken++
		}
	}
	if woken == 0 {
		t.Fatal("flag-off leading mention no longer dispatches")
	}
}

// A wake still wakes, and its request reaches the member and the group only as
// the compact handed-over line (a handoff-style notice carrying the brief), never
// as a chat message. Flag off publishes no such line.
func TestWakeRequestIsAHandedOverLineNotChatText(t *testing.T) {
	for _, structured := range []bool{true, false} {
		var f dedupeFixture
		engine := &mentionEngine{}
		f = newDedupeFixture(t, engine)
		if err := f.server.store.putUserStructuredOutput(structured); err != nil {
			t.Fatal(err)
		}
		engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
			if req.BotID == f.meta.ID {
				return reply(ctx, req, "A comparison of my own.", nil)
			}
			return reply(ctx, req, "My sentence.", wakeOf(f.meta.ID, wakeBrief))
		}
		f.post(t, "@tofi one sentence on breakfast", f.tofi)
		f.start(engine)
		runs := f.settle(t)
		var wakeRun Run
		for _, r := range runs {
			if r.BotID == f.meta.ID {
				wakeRun = r
			}
		}
		if wakeRun.ID == "" {
			t.Fatalf("structured=%v: the wake started nobody", structured)
		}
		var chat, notices int
		var noticeData string
		_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role='assistant' AND kind IN ('','segment','progress') AND content LIKE ?`, "%"+wakeBrief+"%").Scan(&chat)
		_ = f.server.store.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(notice_data),'') FROM messages WHERE kind='notice' AND content=?`, wakeBrief).Scan(&notices, &noticeData)
		if chat != 0 {
			t.Fatalf("structured=%v: the request was posted as chat text", structured)
		}
		if structured && (notices != 1 || !strings.Contains(noticeData, wakeRun.ID) || !strings.Contains(noticeData, `"type":"handoff"`)) {
			t.Fatalf("notices=%d data=%s", notices, noticeData)
		}
		if !structured && notices != 0 {
			t.Fatalf("flag-off published a notice for a wake")
		}
	}
}

func TestSayDescriptionAndGroupGuidanceKeepRequestsInWake(t *testing.T) {
	for _, text := range []string{sayToolDescription, groupDispositionGuidanceStructured} {
		if !strings.Contains(text, "wake") || !strings.Contains(text, "person") {
			t.Fatalf("missing the speak-to-the-person / wake principle: %q", text)
		}
	}
}
