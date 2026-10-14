package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func recordedResult(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"content": content, "sources": []string{"https://example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestScheduledFinalWithResult(t *testing.T) {
	report := "Today's report: Simile Cat posted the 00:40 metaphor; News Cat posted three items at 00:42; Ledger Cat logged dinner but the rows could not be read back, so it is unconfirmed; the pizza tip is still unverified."
	t.Run("pointer-only answer gets the result first", func(t *testing.T) {
		got := scheduledFinalWithResult("That's roughly the report above. One correction: the dinner entry is unconfirmed.", recordedResult(t, report))
		if !strings.HasPrefix(got, report+"\n\n") || !strings.HasSuffix(got, "unconfirmed.") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("full answer is kept as written", func(t *testing.T) {
		final := "Simile Cat posted the metaphor at 00:40, News Cat posted three items at 00:42, and Ledger Cat logged dinner though the rows could not be read back; the pizza tip is unverified."
		if got := scheduledFinalWithResult(final, recordedResult(t, report)); got != final {
			t.Fatalf("rewrote a complete answer: %q", got)
		}
	})
	t.Run("answer containing the result is kept", func(t *testing.T) {
		final := report + "\nSources: example.com"
		if got := scheduledFinalWithResult(final, recordedResult(t, report)); got != final {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("progress notes stay before the answer", func(t *testing.T) {
		final := "<progress>Checked the ledger.</progress>As above."
		got := scheduledFinalWithResult(final, recordedResult(t, report))
		if got != "<progress>Checked the ledger.</progress>"+report+"\n\nAs above." {
			t.Fatalf("got %q", got)
		}
		notes, answer := finalSplit(got)
		if len(notes) != 1 || !strings.HasPrefix(strings.TrimSpace(answer), report) {
			t.Fatalf("notes=%v answer=%q", notes, answer)
		}
	})
	t.Run("empty answer becomes the result", func(t *testing.T) {
		if got := scheduledFinalWithResult("", recordedResult(t, report)); got != report {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("unreadable record leaves the answer alone", func(t *testing.T) {
		if got := scheduledFinalWithResult("As above.", `{"content":"trunc`); got != "As above." {
			t.Fatalf("got %q", got)
		}
	})
}

// A scheduled run that records its result and then answers only "as above"
// must still publish the result in its conversation.
func TestScheduledRunPublishesRecordedResultWhenAnswerOnlyRefersToIt(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	report := "Synthetic daily report: the metaphor post went out at 00:40, three news items at 00:42, and the ledger entry is still unconfirmed because its rows could not be read back."
	engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		for _, tool := range req.Tools {
			if tool.Name != "complete_scheduled_task" {
				continue
			}
			args := recordedResult(t, report)
			for _, status := range []string{"queued", "running"} {
				if err := req.OnToolEvent(runtime.ToolEvent{CallID: "receipt", Name: tool.Name, Arguments: args, Status: status}); err != nil {
					return Result{}, err
				}
			}
			if _, err := tool.Execute(ctx, json.RawMessage(args)); err != nil {
				return Result{}, err
			}
			if err := req.OnToolEvent(runtime.ToolEvent{CallID: "receipt", Name: tool.Name, Arguments: args, Status: "completed", Result: "accepted"}); err != nil {
				return Result{}, err
			}
		}
		return Result{Content: "That's roughly the report above. One correction: the dinner entry is unconfirmed."}, nil
	})
	server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	server.execute(c, root)
	run, err := s.GetRun(root.ID)
	if err != nil || run.Status != "done" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	var published string
	if err := s.db.QueryRow(`SELECT group_concat(content, '|') FROM messages WHERE run_id=? AND role='assistant' AND coalesce(kind,'')<>'progress'`, root.ID).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(published, report) || !strings.Contains(published, "One correction") {
		t.Fatalf("published answer lost the recorded result: %q", published)
	}
}
