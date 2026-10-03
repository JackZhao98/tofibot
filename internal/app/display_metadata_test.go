package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func metadataString(value string) *string { return &value }

func TestDisplayMetadataLegacyMigrationPreservesData(t *testing.T) {
	dir := t.TempDir()
	store, bot, conv := scheduleTestStoreAt(t, dir)
	memory, err := store.AddMemory(conv.ID, bot.ID, "事实：用户喜欢中文。\nKeep the exact quoted wording.")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateSchedule(conv.ID, bot.ID, ScheduleSpec{Title: "旧标题", Content: "Original instructions with 中文 quoted data.", Kind: "interval", RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), IntervalSeconds: 7200, Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	at, _ := parseStoredTime(plan.NextAtUTC)
	runs, err := store.ClaimDueSchedules(at)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim: %v %v", runs, err)
	}
	plan, _ = store.GetSchedule(plan.ID)
	// Reconstruct the actual pre-feature schema using only this synthetic DB.
	for _, ddl := range []string{"ALTER TABLE memories DROP COLUMN title", "ALTER TABLE memories DROP COLUMN description", "ALTER TABLE schedules DROP COLUMN description", "ALTER TABLE schedule_occurrences DROP COLUMN description"} {
		if _, err := store.db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	beforePlan, _ := json.Marshal(plan)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err = OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.GetMemory(memory.ID)
		if err != nil || !reflect.DeepEqual(memory, got) {
			t.Fatalf("memory changed in migration: %+v %v", got, err)
		}
		gotPlan, err := store.GetSchedule(plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Claiming advances interval timing; migration must preserve that state.
		if !reflect.DeepEqual(gotPlan, plan) {
			t.Fatalf("plan changed: %s %+v", beforePlan, gotPlan)
		}
		occurrences, err := store.ScheduleOccurrences(context.Background(), conv.ID, []string{runs[0].ID})
		if err != nil || len(occurrences) != 1 || occurrences[0].Description != "" || occurrences[0].Title != "旧标题" {
			t.Fatalf("legacy occurrence: %+v %v", occurrences, err)
		}
		store.Close()
	}
}

func TestScheduleMetadataEditsPreserveTimingAndOccurrenceSnapshots(t *testing.T) {
	for _, kind := range []string{"once", "interval", "daily"} {
		t.Run(kind, func(t *testing.T) {
			store, bot, conv := scheduleTestStore(t)
			defer store.Close()
			spec := ScheduleSpec{Title: "原始标题", Description: "原始说明", Content: "Read the exact label ‘上海’ and deliver a Chinese summary.\nPreserve every execution step.", Kind: kind, Timezone: "America/Los_Angeles"}
			if kind == "daily" {
				spec.DailyTime = "02:30"
			} else {
				spec.RunAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			}
			if kind == "interval" {
				spec.IntervalSeconds = 7200
			}
			plan, err := store.CreateSchedule(conv.ID, bot.ID, spec)
			if err != nil {
				t.Fatal(err)
			}
			at, _ := parseStoredTime(plan.NextAtUTC)
			runs, err := store.ClaimDueSchedules(at)
			if err != nil || len(runs) != 1 {
				t.Fatalf("claim %v %v", runs, err)
			}
			before, _ := store.GetSchedule(plan.ID)
			edited, err := store.PatchSchedule(plan.ID, SchedulePatch{Title: metadataString("新标题"), Description: metadataString("新说明")})
			if err != nil {
				t.Fatal(err)
			}
			if edited.Content != plan.Content || edited.NextAtUTC != before.NextAtUTC || edited.Kind != before.Kind || edited.Timezone != before.Timezone || edited.DailyTime != before.DailyTime || edited.IntervalSeconds != before.IntervalSeconds || edited.CreatedAt != before.CreatedAt || edited.Status != before.Status {
				t.Fatalf("metadata edit changed execution: %+v %+v", before, edited)
			}
			occurrences, err := store.ScheduleOccurrences(context.Background(), conv.ID, []string{runs[0].ID})
			if err != nil || len(occurrences) != 1 || occurrences[0].Title != spec.Title || occurrences[0].Description != spec.Description || occurrences[0].OccurrenceNumber != 1 {
				t.Fatalf("snapshot changed %+v %v", occurrences, err)
			}
			if _, err := store.PatchSchedule(plan.ID, SchedulePatch{Content: metadataString("New English instructions for future occurrences.")}); err != nil {
				t.Fatal(err)
			}
			trigger, _ := store.GetMessage(runs[0].TriggerMessageID)
			if trigger.Content != spec.Content {
				t.Fatal("claimed instruction was rewritten")
			}
			store.SetRunStatus(runs[0].ID, "failed", "synthetic failure")
			retry, err := store.RetryRun(runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if retry.TriggerMessageID != trigger.ID {
				t.Fatal("retry changed original trigger")
			}
			occurrences, _ = store.ScheduleOccurrences(context.Background(), conv.ID, []string{runs[0].ID})
			if occurrences[0].Description != spec.Description || occurrences[0].OccurrenceNumber != 1 {
				t.Fatal("retry changed snapshot or occurrence numbering")
			}
		})
	}
}

func TestMemoryMetadataPreservesFactsAndContentOnlyCompatibility(t *testing.T) {
	store, bot, conv := scheduleTestStore(t)
	defer store.Close()
	body := "事实：称呼小李；喜欢茶。\nLiteral data: 東京 / München."
	memory, err := store.AddMemoryWithMetadata(conv.ID, bot.ID, MemoryInput{Title: "偏好", Description: "用户称呼和饮品偏好。", Content: body})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.PatchMemory(memory.ID, MemoryPatch{Description: metadataString("新的说明")})
	if err != nil || updated.Content != body || updated.Title != memory.Title || updated.ID != memory.ID {
		t.Fatalf("metadata edit lost data %+v %v", updated, err)
	}
	updated, err = store.UpdateMemory(memory.ID, body+"\nNew fact.")
	if err != nil || updated.Title != memory.Title || updated.Description != "新的说明" {
		t.Fatalf("content-only compatibility %+v %v", updated, err)
	}
	updated, err = store.PatchMemory(memory.ID, MemoryPatch{Title: metadataString(""), Description: metadataString(" ")})
	if err != nil || updated.Title != "" || updated.Description != "" || updated.Content != body+"\nNew fact." {
		t.Fatalf("explicit clear %+v %v", updated, err)
	}
	before := updated
	_, err = store.PatchMemory(memory.ID, MemoryPatch{Title: metadataString(strings.Repeat("长", 121)), Content: metadataString("must not commit")})
	if err == nil {
		t.Fatal("long metadata accepted")
	}
	updated, _ = store.GetMemory(memory.ID)
	if !reflect.DeepEqual(before, updated) {
		t.Fatal("invalid edit mutated memory")
	}
	_, core := (&Server{store: store}).buildContextParts(conv, Run{BotID: bot.ID}, bot)
	if !strings.Contains(core, "Author instruction prompts in English") {
		t.Fatal("English prompt guidance missing")
	}
}

func TestMetadataHTTPAndAIToolContracts(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, _ := server.store.CreateBot("bot", "", "model")
	conv, _ := server.store.GetConversation(bot.DMConversationID)
	request := func(method, path, body string, expected int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != expected {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var memory Memory
	json.Unmarshal(request("POST", "/api/conversations/"+conv.ID+"/memories", `{"content":"事实保持原文"}`, 201), &memory)
	request("PATCH", "/api/memories/"+memory.ID, `{"title":"事实","description":"用户事实"}`, 200)
	json.Unmarshal(request("PATCH", "/api/memories/"+memory.ID, `{"content":"更新的事实"}`, 200), &memory)
	if memory.Title != "事实" || memory.Description != "用户事实" {
		t.Fatal("content-only HTTP cleared metadata")
	}
	request("PATCH", "/api/memories/"+memory.ID, `{"title":""}`, 200)
	for _, tool := range server.tools(conv, Run{BotID: bot.ID}) {
		if tool.Name != "save_memory" && tool.Name != "create_schedule" {
			continue
		}
		required := tool.Parameters["required"].([]string)
		for _, key := range []string{"title", "description", "content"} {
			found := false
			for _, value := range required {
				found = found || value == key
			}
			if !found {
				t.Fatalf("%s schema missing %s", tool.Name, key)
			}
		}
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"content":"English instruction.","kind":"once"}`)); err == nil {
			t.Fatalf("%s accepted missing metadata", tool.Name)
		}
		if tool.Name == "save_memory" {
			if _, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"偏好","description":"保留原始事实","content":"用户喜欢中文。"}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
