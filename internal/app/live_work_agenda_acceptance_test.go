package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// This opt-in acceptance uses only synthetic conversations in a temporary
// store. The access-only credential snapshot is copied, never modified or
// logged. No production data directory, computer, MCP config, or skills load.
func TestLiveWorkAgendaAcrossGroups(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if os.Getenv("TOFI_LIVE_WORK_AGENDA") != "1" || source == "" {
		t.Skip("opt-in synthetic work agenda acceptance")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("access snapshot could not be read")
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(raw, &credential) != nil || credential.AccessToken == "" || credential.ExpiresAt < time.Now().Add(13*time.Minute).UnixMilli() {
		t.Fatal("access snapshot unavailable or expires too soon")
	}
	t.Setenv("TOFI_COMPUTER_SOCKET", "")
	dir := t.TempDir()
	server, err := NewServer(Config{DataDir: dir, Environment: "acceptance", Provider: "openai_codex", DefaultModel: "codex-gpt-5.6-luna", MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: filepath.Join(dir, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if server.microVM != nil {
		t.Fatal("synthetic acceptance must not connect a computer")
	}
	if err = server.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal("could not save isolated access snapshot")
	}
	instructions := "You help track the user's work across conversations using the tools actually provided. Keep confirmations and answers concise. These are synthetic planning records only: do not access computers, external content, or external services, and do not delegate work to another Bot. Creating a task records a commitment; creating a future schedule arranges its later execution. Do not execute scheduled work early."
	a, err := server.store.CreateBot("Agenda A", instructions, "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.store.CreateBot("Agenda B", instructions, "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	first, err := server.store.CreateGroup("Synthetic exhibit planning", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.store.CreateGroup("Synthetic reading planning", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	var executed []Run
	runStep := func(group Conversation, prompt, client string) Run {
		t.Helper()
		_, runs, duplicate, err := server.store.AddUserRuns(group.ID, prompt, client, []runSpec{{BotID: a.ID, Model: a.Model}})
		if err != nil || duplicate || len(runs) != 1 {
			t.Fatalf("could not create one explicitly targeted synthetic run: duplicate=%v count=%d err=%v", duplicate, len(runs), err)
		}
		run := runs[0]
		server.enqueue(group, run)
		deadline := time.Now().Add(3 * time.Minute)
		for {
			current, err := server.store.GetRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status == "done" {
				break
			}
			if current.Status != "running" && current.Status != "queued" {
				t.Fatalf("synthetic step %s ended with status %s", client, current.Status)
			}
			if time.Now().After(deadline) {
				_ = server.store.CancelRunTree(run.ID)
				server.cancelInactiveRuns()
				t.Fatalf("synthetic step %s exceeded three minutes", client)
			}
			time.Sleep(200 * time.Millisecond)
		}
		executed = append(executed, run)
		var count int
		if err = server.store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != len(executed) {
			t.Fatalf("synthetic step unexpectedly launched additional execution: got %d runs, want %d", count, len(executed))
		}
		messages, _, err := server.store.Messages(group.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		replies := 0
		for _, message := range messages {
			if message.RunID != run.ID || message.Role != "assistant" {
				continue
			}
			replies++
			if message.SenderBotID != a.ID || message.Kind == "notice" {
				t.Fatal("targeted synthetic step did not stay with its assigned Bot")
			}
			length := utf8.RuneCountInString(message.Content)
			t.Logf("synthetic step %s reply (%d runes): %s", client, length, message.Content)
			if length > 800 {
				t.Logf("concision review: step %s exceeded the 800-rune guideline", client)
			}
		}
		if replies == 0 {
			t.Fatalf("synthetic step %s produced no confirmation", client)
		}
		return run
	}
	dueFirst := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	dueSecond := dueFirst.Add(15 * time.Minute)
	runStep(first, fmt.Sprintf(`This is a fictional planning exercise. Please record exactly one goal titled "Fictional exhibit plan" and one subordinate task titled "Select exhibit theme", both assigned to yourself in this group. Link the task to that goal. Also create exactly one one-time schedule assigned to yourself at %s to draft a short fictional exhibit introduction here. Record the goal, task, and schedule using the available tools; do not do the scheduled work now. No other records or delegation are needed. Confirm briefly.`, dueFirst.Format(time.RFC3339)), "agenda-first")
	firstItems, err := server.store.ListWorkItems(first.ID, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstItems) != 2 {
		t.Fatalf("first group has %d open records; want one goal and one task", len(firstItems))
	}
	var goal, task WorkItem
	for _, item := range firstItems {
		switch item.Kind {
		case "goal":
			goal = item
		case "task":
			task = item
		}
	}
	if goal.ID == "" || task.ID == "" || task.ParentGoalID != goal.ID || goal.Title != "Fictional exhibit plan" || task.Title != "Select exhibit theme" {
		t.Fatal("first group did not persist the requested goal and subordinate task")
	}
	runStep(second, fmt.Sprintf(`In this separate fictional planning group, record exactly one task titled "Prepare reading notes", assigned to yourself. Also create exactly one one-time schedule assigned to yourself at %s to draft short fictional reading notes here. Do not execute that future work now, create a goal, or change any records in the other group. Use the available tools and confirm briefly.`, dueSecond.Format(time.RFC3339)), "agenda-second")
	secondItems, err := server.store.ListWorkItems(second.ID, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondItems) != 1 || secondItems[0].Kind != "task" || secondItems[0].Title != "Prepare reading notes" {
		t.Fatal("second group did not persist its independent task")
	}
	secondTask := secondItems[0]
	own, err := server.store.ListWorkItems("", a.ID, false)
	if err != nil || len(own) != 3 {
		t.Fatalf("self work view count=%d err=%v", len(own), err)
	}
	itemSources := map[string]bool{}
	for _, item := range own {
		itemSources[item.ConversationID] = true
		if item.BotID != a.ID || item.BotName != a.Name || item.ConversationName == "" {
			t.Fatal("self work view lost ownership or origin")
		}
	}
	if !itemSources[first.ID] || !itemSources[second.ID] {
		t.Fatal("self work view omitted a group")
	}
	plans, err := server.store.ListBotSchedules(a.ID, false)
	if err != nil || len(plans) != 2 {
		t.Fatalf("self schedule view count=%d err=%v", len(plans), err)
	}
	scheduleSources := map[string]bool{}
	for _, plan := range plans {
		scheduleSources[plan.ConversationID] = true
		due, err := time.Parse(time.RFC3339Nano, plan.NextAtUTC)
		if err != nil {
			t.Fatal(err)
		}
		want := dueFirst
		if plan.ConversationID == second.ID {
			want = dueSecond
		}
		if plan.BotID != a.ID || plan.Kind != scheduleOnce || plan.Status != scheduleActive || plan.LastRunID != "" || !due.Equal(want) || !due.After(time.Now()) {
			t.Fatal("schedule is not the requested unexecuted future occurrence")
		}
	}
	if !scheduleSources[first.ID] || !scheduleSources[second.ID] {
		t.Fatal("self schedule view omitted a source group")
	}
	completion := runStep(first, fmt.Sprintf(`The synthetic task "Select exhibit theme" is now complete. Update only that existing task (work item ID %s) to done. Leave its parent goal, the other group's task, and all schedules unchanged. Do not create any records or execute future work. Confirm briefly.`, task.ID), "agenda-complete")
	completed, err := server.store.GetWorkItem(task.ID)
	if err != nil || completed.Status != "done" || completed.CompletedAt == "" {
		t.Fatalf("completed task did not persist terminal state: %v", err)
	}
	open, err := server.store.ListWorkItems("", a.ID, false)
	if err != nil || len(open) != 2 {
		t.Fatalf("completion left %d open records: %v", len(open), err)
	}
	for _, item := range open {
		if item.ID == task.ID {
			t.Fatal("completed task remains in open work")
		}
	}
	history, err := server.store.ListWorkItems("", a.ID, true)
	if err != nil || len(history) != 1 || history[0].ID != task.ID {
		t.Fatalf("completed task missing from history: %v", err)
	}
	unchangedGoal, err := server.store.GetWorkItem(goal.ID)
	if err != nil || unchangedGoal != goal {
		t.Fatal("task completion changed its goal")
	}
	unchangedTask, err := server.store.GetWorkItem(secondTask.ID)
	if err != nil || unchangedTask != secondTask {
		t.Fatal("task completion changed the second group's task")
	}
	afterPlans, err := server.store.ListBotSchedules(a.ID, false)
	if err != nil || len(afterPlans) != len(plans) {
		t.Fatal("task completion changed scheduled work")
	}
	beforePlans := map[string]Schedule{}
	for _, plan := range plans {
		beforePlans[plan.ID] = plan
	}
	for _, plan := range afterPlans {
		if previous, ok := beforePlans[plan.ID]; !ok || previous != plan {
			t.Fatal("task completion changed a future schedule")
		}
	}
	answer := runStep(first, "What future work have you scheduled for yourself across both of our synthetic groups? Use list_schedules with scope self to check the actual stored schedule records, and name each source group and scheduled time. Do not change anything or execute future work. Keep the answer concise.", "agenda-self-summary")
	activities, err := server.store.ToolActivities(first.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	updated, listed := false, false
	for _, activity := range activities {
		if activity.Status != "completed" {
			continue
		}
		if activity.RunID == completion.ID && activity.Name == "update_work_item" {
			var args struct {
				ID     string `json:"work_item_id"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(activity.Arguments), &args) == nil && args.ID == task.ID && args.Status == "done" {
				updated = true
			}
		}
		if activity.RunID == answer.ID && activity.Name == "list_schedules" {
			var args struct {
				Scope string `json:"scope"`
			}
			if json.Unmarshal([]byte(activity.Arguments), &args) == nil && args.Scope == "self" {
				listed = true
			}
		}
	}
	if !updated || !listed {
		t.Fatalf("missing successful tool evidence: completion=%v self-schedule-query=%v", updated, listed)
	}
	messages, _, err := server.store.Messages(first.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var summary strings.Builder
	for _, message := range messages {
		if message.RunID == answer.ID && message.Role == "assistant" {
			summary.WriteString(message.Content)
		}
	}
	if !strings.Contains(summary.String(), first.Name) || !strings.Contains(summary.String(), second.Name) {
		t.Fatal("self-schedule answer did not identify both origin groups")
	}
	finalOpen, err := server.store.ListWorkItems("", a.ID, false)
	if err != nil || len(finalOpen) != 2 {
		t.Fatal("read-only schedule question changed open work")
	}
	for _, item := range finalOpen {
		if item != goal && item != secondTask {
			t.Fatal("read-only schedule question changed an existing commitment")
		}
	}
	finalHistory, err := server.store.ListWorkItems("", a.ID, true)
	if err != nil || len(finalHistory) != 1 || finalHistory[0] != completed {
		t.Fatal("read-only schedule question changed task history")
	}
	finalPlans, err := server.store.ListBotSchedules(a.ID, false)
	if err != nil || len(finalPlans) != len(plans) {
		t.Fatal("read-only schedule question changed future schedules")
	}
	for _, plan := range finalPlans {
		if previous, ok := beforePlans[plan.ID]; !ok || previous != plan {
			t.Fatal("read-only schedule question mutated a future schedule")
		}
	}
	t.Log("synthetic goals, subordinate tasks, cross-group schedules, completion history, and actual self-schedule tool use verified")
}
