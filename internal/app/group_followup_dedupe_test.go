package app

import (
	"context"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// A group_followup run that posted a paragraph (segment) and then ends with the
// identical text as its final message, with or without respond, posts it once.
func TestGroupFollowupNeverRepostsIdenticalFinal(t *testing.T) {
	for _, viaRespond := range []bool{false, true} {
		name := "plain final"
		if viaRespond {
			name = "respond reply"
		}
		t.Run(name, func(t *testing.T) {
			var f dedupeFixture
			engine := &mentionEngine{}
			f = newDedupeFixture(t, engine)
			const paragraph = "The finished metaphor: a lighthouse that never sleeps."
			engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
				run, err := f.server.store.GetRun(req.RunID)
				if err != nil {
					return "ERR " + err.Error()
				}
				switch {
				case run.Kind == runKindFollowup:
					if req.OnAssistantTurn != nil {
						_ = req.OnAssistantTurn(1, paragraph)
					}
					if viaRespond {
						return reply(ctx, req, paragraph, nil)
					}
					return paragraph
				case run.Kind == runKindGroupTask:
					return "helper result"
				}
				for _, tool := range req.Tools {
					if tool.Name == "handoff" {
						if _, err := tool.Execute(ctx, handoffArgs(f.meta.ID, "write the metaphor")); err != nil {
							return "ERR " + err.Error()
						}
					}
				}
				return ""
			}
			f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
			f.start(engine)
			runs := f.settle(t)
			var followup Run
			for _, r := range runs {
				if r.Kind == runKindFollowup {
					followup = r
				}
			}
			if followup.ID == "" || followup.Status != "done" {
				t.Fatalf("no finished followup: %+v", runs)
			}
			var n int
			_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant' AND kind IN ('','segment') AND content=?`, followup.ID, paragraph).Scan(&n)
			if n != 1 {
				t.Fatalf("paragraph visible %d times", n)
			}
		})
	}
}
