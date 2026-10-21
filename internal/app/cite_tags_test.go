package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Citation markup from search results is removed before text is stored or
// served; the cited words stay.
func TestCiteTagsNeverReachStoredText(t *testing.T) {
	in := `好看。<cite index="2-2">烂番茄 90%</cite>，(cite index="4-2,4-3">观众 85%</cite>。 Please (cite the source) when a > b.`
	want := `好看。烂番茄 90%，观众 85%。 Please (cite the source) when a > b.`
	if got := stripCiteTags(in); got != want {
		t.Fatalf("got %q", got)
	}
	if got := stripProgressTags(in); got != want {
		t.Fatalf("stripProgressTags kept markup: %q", got)
	}
	parts := splitProgress("<progress>checking</progress>" + in)
	if len(parts) != 2 || parts[1].Text != want {
		t.Fatalf("splitProgress kept markup: %+v", parts)
	}

	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	c, _ := s.store.GetConversation(run.ConversationID)
	bot, _ := s.store.GetBot(run.BotID)
	_, live, _, err := s.store.AddUserRun(c.ID, bot.ID, "q", "cite-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), live.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live.structured = true
	ctx := runtime.WithToolCallID(context.Background(), "cite1")
	raw, _ := json.Marshal(map[string]string{"purpose": "answer", "text": in})
	if _, err := s.sayTool(c, live).Execute(ctx, raw); err != nil {
		t.Fatal(err)
	}
	rec, ok := s.store.sayAnswer(live.ID)
	if !ok || strings.Contains(rec.Text, "cite") && strings.Contains(rec.Text, "index") || !strings.Contains(rec.Text, "烂番茄 90%") {
		t.Fatalf("stored answer = %+v ok=%v", rec, ok)
	}
}
