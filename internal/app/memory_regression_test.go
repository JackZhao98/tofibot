package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"strings"
	"testing"
)

type summaryRecorder struct {
	requests []runtime.Request
	failAt   int
}

func (e *summaryRecorder) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	e.requests = append(e.requests, req)
	if e.failAt > 0 && len(e.requests) == e.failAt {
		return runtime.Result{}, errors.New("summary failed")
	}
	return runtime.Result{Content: fmt.Sprintf("semantic summary %d", len(e.requests))}, nil
}

func TestSummaryOrderBoundaryAndRecentSuffixAcrossUpdates(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, _ := st.CreateBot("reader", "", "m")
	c, _ := st.GetConversation(b.DMConversationID)
	for i := 1; i <= 270; i++ {
		st.AddMessage(c.ID, "user", "", "", fmt.Sprintf("marker-%03d", i), "")
	}
	trigger, _, _ := st.AddMessage(c.ID, "user", "", "", "current marker", "")
	r := Run{ID: "summary-test", BotID: b.ID, TriggerMessageID: trigger.ID}
	st.AddMessage(c.ID, "user", "", "", "future secret", "")
	e := &summaryRecorder{}
	server := &Server{store: st}
	if _, err := server.prepareLongTermContext(context.Background(), e, c, r, b); err != nil {
		t.Fatal(err)
	}
	_, covered, _, err := st.LatestSummary(c.ID)
	if err != nil || covered != 191 {
		t.Fatalf("covered=%d err=%v", covered, err)
	}
	joined := ""
	for _, req := range e.requests {
		if len(req.Tools) != 0 || req.System == "" {
			t.Fatal("summary must be tool-free and instructed")
		}
		joined += req.Messages[0].Content
	}
	previous := -1
	for i := 1; i <= 191; i++ {
		at := strings.Index(joined, fmt.Sprintf("marker-%03d", i))
		if at < previous || at < 0 {
			t.Fatalf("lost or reordered marker %d", i)
		}
		previous = at
	}
	if strings.Contains(joined, "marker-192") || strings.Contains(joined, "current marker") || strings.Contains(joined, "future secret") {
		t.Fatal("summary crossed retained/trigger boundary")
	}
	calls := len(e.requests)
	if _, err := server.prepareLongTermContext(context.Background(), e, c, r, b); err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != calls {
		t.Fatal("unchanged context resummarized recent suffix")
	}
}

func TestOversizedSummaryFailureDoesNotCoverUnprocessedMessage(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, _ := st.CreateBot("reader", "", "m")
	c, _ := st.GetConversation(b.DMConversationID)
	huge, _, _ := st.AddMessage(c.ID, "user", "", "", strings.Repeat("中", maxSummaryInputRunes*3), "")
	for i := 0; i < 80; i++ {
		st.AddMessage(c.ID, "user", "", "", "recent", "")
	}
	trigger, _, _ := st.AddMessage(c.ID, "user", "", "", "now", "")
	e := &summaryRecorder{failAt: 2}
	_, err = (&Server{store: st}).prepareLongTermContext(context.Background(), e, c, Run{ID: "split", BotID: b.ID, TriggerMessageID: trigger.ID}, b)
	if err == nil {
		t.Fatal("expected partial segmentation failure")
	}
	_, covered, _, _ := st.LatestSummary(c.ID)
	if covered >= huge.Seq {
		t.Fatal("incomplete large message checkpointed")
	}
	if len(e.requests) != 2 {
		t.Fatalf("calls=%d", len(e.requests))
	}
	for _, req := range e.requests {
		if len([]rune(req.Messages[0].Content)) > maxSummaryInputRunes+maxSummaryRunes+100 {
			t.Fatal("input exceeded budget")
		}
	}
}

func TestGroupSummaryNeverReadsPrivateConversation(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _ := st.CreateBot("A", "", "m")
	b, _ := st.CreateBot("B", "", "m")
	g, _ := st.CreateGroup("group", []string{a.ID, b.ID})
	st.AddMessage(a.DMConversationID, "user", "", "", "private-secret", "")
	for i := 0; i < 90; i++ {
		st.AddMessage(g.ID, "user", "", "", "public-fact", "")
	}
	m, _, _ := st.AddMessage(g.ID, "user", "", "", "current", "")
	e := &summaryRecorder{}
	_, err = (&Server{store: st}).prepareLongTermContext(context.Background(), e, g, Run{ID: "group-summary", BotID: a.ID, TriggerMessageID: m.ID}, a)
	if err != nil || len(e.requests) == 0 {
		t.Fatalf("no group summary: %v", err)
	}
	for _, req := range e.requests {
		if strings.Contains(req.Messages[0].Content, "private-secret") {
			t.Fatal("private conversation leaked")
		}
	}
}

func TestDuplicateMemoryKeepsMeaningfulPunctuation(t *testing.T) {
	for _, pair := range [][2]string{
		{"meeting on 1/12", "meeting on 11/2"},
		{"threshold -5", "threshold 5"},
		{"version 3.5", "version 35"},
		{"I use C++", "I use C"},
	} {
		if _, ok := duplicateMemory([]Memory{{ID: "m1", Content: pair[0]}}, pair[1]); ok {
			t.Fatalf("%q collapsed into %q", pair[1], pair[0])
		}
	}
	for _, pair := range [][2]string{
		{"Meeting on 1/12", "  meeting   ON 1/12 "},
		{"I use C++", "i  use\tc++"},
		{"likes green tea, not coffee", "Likes green tea not coffee"},
	} {
		if _, ok := duplicateMemory([]Memory{{ID: "m1", Content: pair[0]}}, pair[1]); !ok {
			t.Fatalf("%q did not dedup with %q", pair[1], pair[0])
		}
	}
}
