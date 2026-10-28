package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// A UTC instant just past midnight belongs to the previous calendar day in a
// zone behind UTC and to the next one in a zone ahead of it.
func TestLocalMomentAcrossUTCMidnight(t *testing.T) {
	cases := []struct{ instant, zone, date, weekday, local string }{
		{"2026-10-28T02:14:00Z", "America/Los_Angeles", "2026-10-27", "Tuesday", "2026-10-27 19:14 PDT"},
		{"2026-10-27T20:30:00Z", "Asia/Tokyo", "2026-10-28", "Wednesday", "2026-10-28 05:30 JST"},
		{"2026-10-28T02:14:00Z", "", "2026-10-28", "Wednesday", "2026-10-28 02:14 UTC"},
		{"2026-11-01T08:30:00Z", "America/Los_Angeles", "2026-11-01", "Sunday", "2026-11-01 01:30 PDT"},
	}
	for _, c := range cases {
		m, ok := localMoment(c.instant, c.zone)
		if !ok || m.Date != c.date || m.Weekday != c.weekday || m.Local != c.local {
			t.Errorf("%s in %q: %+v ok=%v, want %s %s %s", c.instant, c.zone, m, ok, c.date, c.weekday, c.local)
		}
	}
	if _, ok := localMoment("2026-10-28T02:14:00Z", "Not/AZone"); ok {
		t.Error("unknown zone produced a local time")
	}
	if _, ok := localMoment("garbage", "UTC"); ok {
		t.Error("bad instant produced a local time")
	}
}

func TestReviewerPolicyVersionAndText(t *testing.T) {
	if autoReviewPolicyVersion != "mcp-all-external-v7" || autoReviewProvenance != "host-mcp-review-binding-v7" {
		t.Fatalf("policy identity not bumped: %s %s", autoReviewPolicyVersion, autoReviewProvenance)
	}
	for _, want := range []string{
		"scheduled_for_local",
		"host_clock",
		"never derive the expected date from a UTC value",
		"narrower or less destructive than the authorised one",
		"consistent with a later user-stated preference, stays within the authorisation",
		"Memories can narrow what is authorised but never create consent",
	} {
		if !strings.Contains(mcpAutoReviewPrompt, want) {
			t.Errorf("reviewer prompt lost %q", want)
		}
	}
}

func TestReviewMemoriesBoundsAndProvenance(t *testing.T) {
	var memories []Memory
	origins := map[string]string{}
	for i := 0; i < mcpReviewMemoryEntries+7; i++ {
		id := fmt.Sprintf("id-%04d-xxxxxxxx", i)
		memories = append(memories, Memory{ID: id, Content: fmt.Sprintf("memory %d", i), UpdatedAt: fmt.Sprintf("2026-10-%02dT00:00:00Z", i+1)})
		if i%2 == 0 {
			origins[id] = memoryOriginOwner
		}
	}
	got := buildMCPReviewMemories(memories, origins)
	if len(got.Entries) != mcpReviewMemoryEntries || got.Omitted != 7 {
		t.Fatalf("count bound: %d entries, %d omitted", len(got.Entries), got.Omitted)
	}
	if got.Entries[0].SavedAt > got.Entries[len(got.Entries)-1].SavedAt || !strings.Contains(got.Entries[len(got.Entries)-1].Content, fmt.Sprint(mcpReviewMemoryEntries+6)) {
		t.Fatalf("newest memories must be kept, oldest first: %+v", got.Entries)
	}
	for _, e := range got.Entries {
		if e.Origin != memoryOriginOwner && e.Origin != memoryOriginUnrecorded {
			t.Fatalf("unexpected origin %q", e.Origin)
		}
	}
	long := strings.Repeat("字", mcpReviewMemoryEntryRunes*3)
	var big []Memory
	for i := 0; i < 10; i++ {
		big = append(big, Memory{ID: fmt.Sprintf("big-%04d-xxxxxxxx", i), Content: long, UpdatedAt: fmt.Sprintf("2026-10-%02dT00:00:00Z", i+1)})
	}
	got = buildMCPReviewMemories(big, nil)
	total := 0
	for _, e := range got.Entries {
		if !e.Truncated {
			t.Fatal("oversized memory was not marked truncated")
		}
		total += len([]rune(e.Content))
	}
	// Each shown entry carries the truncation marker beyond its rune budget.
	if total > mcpReviewMemoryTotalRunes+len(got.Entries)*len("…[truncated]") || got.Omitted == 0 {
		t.Fatalf("size bound: total %d omitted %d", total, got.Omitted)
	}
	if buildMCPReviewMemories(nil, nil) != nil {
		t.Fatal("no memories must add nothing")
	}
}

// End to end: the reviewer packet of a scheduled run carries the occurrence on
// the schedule's clock and the Bot's memories with provenance.
func TestScheduledReviewPacketCarriesLocalDateAndMemories(t *testing.T) {
	zone := "America/Los_Angeles"
	f := newScheduledMCPFixtureSpec(t, false, scheduleSourceChat, &ScheduleSpec{Content: "Update the page for today.", Kind: scheduleInterval, IntervalSeconds: 60, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: zone})
	if _, err := f.s.store.addMemoryWithOrigin(f.c.ID, f.bot.ID, MemoryInput{Content: "When updating the page, add the new edition at the top and keep earlier editions."}, memoryOriginUserTurn); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.AddMemoryWithMetadata(f.c.ID, f.bot.ID, MemoryInput{Content: "legacy note"}); err != nil {
		t.Fatal(err)
	}
	var packet string
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		packet = req.Messages[0].Content
		return reviewReply(req, "allow"), nil
	}
	if err := f.review(f.question()); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Context struct {
			Lineage struct {
				Occurrence struct {
					UTC   string               `json:"scheduled_for_utc"`
					Local *scheduleLocalMoment `json:"scheduled_for_local"`
				} `json:"occurrence"`
			} `json:"schedule_lineage"`
		} `json:"context"`
		Clock    *scheduleLocalMoment `json:"host_clock"`
		Memories *mcpReviewMemories   `json:"bot_memories"`
		Custom   string               `json:"custom_contract"`
	}
	if err := json.Unmarshal([]byte(packet), &in); err != nil {
		t.Fatal(err)
	}
	occ := in.Context.Lineage.Occurrence
	want, ok := localMoment(occ.UTC, zone)
	if occ.Local == nil || !ok || *occ.Local != want || occ.Local.Zone != zone {
		t.Fatalf("occurrence local = %+v, want %+v", occ.Local, want)
	}
	if in.Clock == nil || in.Clock.Zone != zone {
		t.Fatalf("host clock must use the schedule zone: %+v", in.Clock)
	}
	if in.Memories == nil || len(in.Memories.Entries) != 2 {
		t.Fatalf("memories: %+v", in.Memories)
	}
	origins := map[string]bool{}
	for _, e := range in.Memories.Entries {
		origins[e.Origin] = true
	}
	if !origins[memoryOriginUserTurn] || !origins[memoryOriginUnrecorded] || in.Custom != "tofi-mcp-risk-advice-v7" {
		t.Fatalf("provenance labels missing: %v %s", origins, in.Custom)
	}
	// The Bot's own run sees the same local date.
	note := f.s.scheduleOccurrenceNote(f.root)
	if !strings.Contains(note, "local date is "+want.Date) || !strings.Contains(note, want.Weekday) || !strings.Contains(note, zone) {
		t.Fatalf("bot run note: %q", note)
	}
}

// Memories and the clock live outside the digest-bound context: saving a
// memory during a run must not invalidate a reservation.
func TestMemoriesDoNotMoveReviewDigest(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	_, before, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.addMemoryWithOrigin(f.c.ID, f.bot.ID, MemoryInput{Content: "a preference"}, memoryOriginOwner); err != nil {
		t.Fatal(err)
	}
	_, after, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || before != after {
		t.Fatalf("digest moved with memory: %v %s %s", err, before, after)
	}
}
