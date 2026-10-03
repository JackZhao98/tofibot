package app

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMemoryEditBaselinePreservesNewFactsAndRevision(t *testing.T) {
	store, bot, conv := scheduleTestStore(t)
	defer store.Close()
	initial, err := store.AddMemoryWithMetadata(conv.ID, bot.ID, MemoryInput{Title: "Original title", Description: "Original description", Content: "原始事实"})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := store.PatchMemory(initial.ID, MemoryPatch{Description: metadataString("Corrected description"), Content: metadataString("修正的事实")})
	if err != nil {
		t.Fatal(err)
	}
	edited, err := store.PatchMemory(initial.ID, MemoryPatch{Title: metadataString("Local title"), Expected: &EditBaseline{Title: &initial.Title}})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Content != remote.Content || edited.Description != remote.Description || edited.Revision != remote.Revision+1 {
		t.Fatalf("metadata edit reverted refreshed data: %+v", edited)
	}
	before := edited
	_, err = store.PatchMemory(initial.ID, MemoryPatch{Title: metadataString("Must not partially commit"), Content: metadataString("Local conflicting facts"), Expected: &EditBaseline{Title: &edited.Title, Content: &initial.Content}})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("expected content conflict, got %v", err)
	}
	current, _ := store.GetMemory(initial.ID)
	if !reflect.DeepEqual(before, current) {
		t.Fatalf("conflict changed record/revision: %+v", current)
	}
	_, err = store.PatchMemory(initial.ID, MemoryPatch{Description: metadataString("Local description"), Expected: &EditBaseline{Description: &initial.Description}})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("description conflict missing: %v", err)
	}
	_, err = store.PatchMemory(initial.ID, MemoryPatch{Content: metadataString("Incomplete expectation"), Expected: &EditBaseline{Title: &edited.Title}})
	if err == nil {
		t.Fatal("incomplete baseline accepted")
	}
}

func TestScheduleEditBaselinePreservesPromptAndFireTimes(t *testing.T) {
	for _, kind := range []string{"once", "interval", "daily"} {
		t.Run(kind, func(t *testing.T) {
			store, bot, conv := scheduleTestStore(t)
			defer store.Close()
			spec := ScheduleSpec{Title: "Original title", Description: "Original description", Content: "Original English instructions.", Kind: kind, Timezone: "America/Los_Angeles"}
			if kind == "daily" {
				spec.DailyTime = "02:30"
			} else {
				spec.RunAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			}
			if kind == "interval" {
				spec.IntervalSeconds = 3600
			}
			initial, err := store.CreateSchedule(conv.ID, bot.ID, spec)
			if err != nil {
				t.Fatal(err)
			}
			remote, err := store.PatchSchedule(initial.ID, SchedulePatch{Description: metadataString("Remote description"), Content: metadataString("Corrected English prompt.")})
			if err != nil {
				t.Fatal(err)
			}
			edited, err := store.PatchSchedule(initial.ID, SchedulePatch{Title: metadataString("Local title"), Expected: &EditBaseline{Title: &initial.Title}})
			if err != nil {
				t.Fatal(err)
			}
			if edited.Content != remote.Content || edited.Description != remote.Description || edited.NextAtUTC != initial.NextAtUTC || edited.Timezone != initial.Timezone || edited.Kind != initial.Kind || edited.DailyTime != initial.DailyTime || edited.IntervalSeconds != initial.IntervalSeconds {
				t.Fatalf("edit reverted prompt/timing: %+v", edited)
			}
			before := edited
			_, err = store.PatchSchedule(initial.ID, SchedulePatch{Title: metadataString("Must not partially commit"), Content: metadataString("Conflicting local prompt."), Expected: &EditBaseline{Title: &edited.Title, Content: &initial.Content}})
			if !errors.Is(err, ErrEditConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			current, _ := store.GetSchedule(initial.ID)
			if !reflect.DeepEqual(before, current) {
				t.Fatalf("conflict changed record: %+v", current)
			}
			// A worker tick is unrelated to changed metadata and must not cause a
			// false conflict or be reverted by a guarded title-only PATCH.
			at, _ := parseStoredTime(edited.NextAtUTC)
			runs, err := store.ClaimDueSchedules(at)
			if err != nil || len(runs) != 1 {
				t.Fatalf("claim %v %v", runs, err)
			}
			afterClaim, _ := store.GetSchedule(initial.ID)
			edited, err = store.PatchSchedule(initial.ID, SchedulePatch{Title: metadataString("After claim title"), Expected: &EditBaseline{Title: &before.Title}})
			if err != nil || edited.NextAtUTC != afterClaim.NextAtUTC || edited.Content != afterClaim.Content {
				t.Fatalf("claim edit conflict/timing: %+v %v", edited, err)
			}
		})
	}
}

func TestExpectedFieldWritesAreAtomicForConcurrentEditors(t *testing.T) {
	store, bot, conv := scheduleTestStore(t)
	defer store.Close()
	memory, _ := store.AddMemoryWithMetadata(conv.ID, bot.ID, MemoryInput{Title: "Title", Description: "Description", Content: "Initial facts"})
	plan, _ := store.CreateSchedule(conv.ID, bot.ID, ScheduleSpec{Title: "Title", Description: "Description", Content: "Initial English instruction.", Kind: "once", RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"})
	for _, kind := range []string{"memory", "schedule"} {
		t.Run(kind, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, value := range []string{"First new value", "Second new value"} {
				go func(value string) {
					<-start
					var err error
					if kind == "memory" {
						_, err = store.PatchMemory(memory.ID, MemoryPatch{Content: &value, Expected: &EditBaseline{Content: &memory.Content}})
					} else {
						_, err = store.PatchSchedule(plan.ID, SchedulePatch{Content: &value, Expected: &EditBaseline{Content: &plan.Content}})
					}
					results <- err
				}(value)
			}
			close(start)
			successes, conflicts := 0, 0
			for range 2 {
				err := <-results
				if err == nil {
					successes++
				} else if errors.Is(err, ErrEditConflict) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
			}
			if kind == "memory" {
				current, _ := store.GetMemory(memory.ID)
				if current.Revision != 2 {
					t.Fatalf("failed edit incremented revision: %+v", current)
				}
			}
		})
	}
}

func TestMetadataEditConflictHTTP(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, _ := server.store.CreateBot("bot", "", "model")
	conv, _ := server.store.GetConversation(bot.DMConversationID)
	memory, _ := server.store.AddMemoryWithMetadata(conv.ID, bot.ID, MemoryInput{Title: "Title", Description: "Description", Content: "Current facts"})
	plan, _ := server.store.CreateSchedule(conv.ID, bot.ID, ScheduleSpec{Title: "Title", Description: "Description", Content: "Current English instructions.", Kind: "once", RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"})
	for _, path := range []string{"/api/memories/" + memory.ID, "/api/schedules/" + plan.ID} {
		for _, test := range []struct {
			body   string
			status int
		}{
			{`{"content":"New local value","expected":{"content":"Stale value"}}`, 409},
			{`{"content":"New local value","expected":{"title":"Title"}}`, 400},
		} {
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, httptest.NewRequest("PATCH", path, strings.NewReader(test.body)))
			if w.Code != test.status {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			if test.status == 409 {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				json.Unmarshal(w.Body.Bytes(), &body)
				if body.Error.Code != "edit_conflict" {
					t.Fatalf("unexpected conflict: %s", w.Body.String())
				}
			}
		}
	}
}
