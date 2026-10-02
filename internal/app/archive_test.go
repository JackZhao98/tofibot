package app

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func nowPlusMinute() string { return time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano) }

func TestGroupArchivePausesSchedulesKeepsHistoryAndRestoresWithoutResume(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("archive-a", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("archive-b", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.store.CreateGroup("archive group", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.AddMessage(g.ID, "user", "", "", "history", "archive-history"); err != nil {
		t.Fatal(err)
	}
	schedule, err := s.store.CreateSchedule(g.ID, a.ID, ScheduleSpec{Content: "scheduled", Kind: scheduleInterval, RunAt: nowPlusMinute(), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetGroupArchived(g.ID, true); err != nil {
		t.Fatal(err)
	}
	archived, err := s.store.GetConversation(g.ID)
	if err != nil || !archived.Archived {
		t.Fatalf("archived=%+v err=%v", archived, err)
	}
	wantMembers := []string{a.ID, b.ID}
	sort.Strings(wantMembers)
	if !reflect.DeepEqual(archived.BotIDs, wantMembers) {
		t.Fatalf("members changed=%#v", archived.BotIDs)
	}
	messages, _, err := s.store.Messages(g.ID, 0, 20)
	if err != nil || len(messages) != 1 || messages[0].Content != "history" {
		t.Fatalf("history=%#v err=%v", messages, err)
	}
	paused, err := s.store.GetSchedule(schedule.ID)
	if err != nil || paused.Status != schedulePaused {
		t.Fatalf("paused schedule=%+v err=%v", paused, err)
	}
	if _, _, _, err = s.store.AddUserRuns(g.ID, "blocked", "blocked-archive", []runSpec{{BotID: a.ID}}); !errors.Is(err, ErrArchiveBlocked) {
		t.Fatalf("archived send err=%v", err)
	}
	activeGroups, err := s.store.ListConversations()
	if err != nil {
		t.Fatalf("default conversation list err=%v", err)
	}
	for _, listed := range activeGroups {
		if listed.ID == g.ID {
			t.Fatalf("archived group leaked into default list: %#v", activeGroups)
		}
	}
	allGroups, err := s.store.ListConversations(true)
	if err != nil {
		t.Fatalf("archived conversation list=%#v err=%v", allGroups, err)
	}
	var foundGroup bool
	for _, listed := range allGroups {
		if listed.ID == g.ID {
			foundGroup = listed.Archived
		}
	}
	if !foundGroup {
		t.Fatalf("archived group missing from include_archived list: %#v", allGroups)
	}
	if _, err = s.store.SetGroupArchived(g.ID, false); err != nil {
		t.Fatal(err)
	}
	restored, err := s.store.GetConversation(g.ID)
	if err != nil || restored.Archived {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	paused, err = s.store.GetSchedule(schedule.ID)
	if err != nil || paused.Status != schedulePaused {
		t.Fatalf("schedule silently resumed=%+v err=%v", paused, err)
	}
	if _, err = s.store.ResumeSchedule(schedule.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBotArchivePairsCanonicalDMAndPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("archive bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := s.store.CreateSchedule(b.DMConversationID, b.ID, ScheduleSpec{Content: "dm schedule", Kind: scheduleInterval, RunAt: nowPlusMinute(), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetBotArchived(b.ID, true); err != nil {
		t.Fatal(err)
	}
	archived, err := s.store.GetBot(b.ID)
	if err != nil || !archived.Archived || archived.DMConversationID != b.DMConversationID {
		t.Fatalf("archived bot=%+v err=%v", archived, err)
	}
	dm, err := s.store.GetConversation(b.DMConversationID)
	if err != nil || !dm.Archived {
		t.Fatalf("archived dm=%+v err=%v", dm, err)
	}
	if got, err := s.store.GetSchedule(schedule.ID); err != nil || got.Status != schedulePaused {
		t.Fatalf("archived schedule=%+v err=%v", got, err)
	}
	active, err := s.store.ListBots()
	if err != nil || len(active) != 0 {
		t.Fatalf("default bot list=%#v err=%v", active, err)
	}
	all, err := s.store.ListBots(true)
	if err != nil || len(all) != 1 || !all[0].Archived {
		t.Fatalf("archived bot list=%#v err=%v", all, err)
	}
	activeConversations, err := s.store.ListConversations()
	if err != nil || len(activeConversations) != 0 {
		t.Fatalf("default DM list=%#v err=%v", activeConversations, err)
	}
	allConversations, err := s.store.ListConversations(true)
	if err != nil || len(allConversations) != 1 || !allConversations[0].Archived {
		t.Fatalf("archived DM list=%#v err=%v", allConversations, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	persisted, err := s.store.GetBot(b.ID)
	if err != nil || !persisted.Archived {
		t.Fatalf("after restart bot=%+v err=%v", persisted, err)
	}
	if _, err = s.store.SetBotArchived(b.ID, false); err != nil {
		t.Fatal(err)
	}
	restored, err := s.store.GetConversation(b.DMConversationID)
	if err != nil || restored.Archived {
		t.Fatalf("restored dm=%+v err=%v", restored, err)
	}
	if got, err := s.store.GetSchedule(schedule.ID); err != nil || got.Status != schedulePaused {
		t.Fatalf("schedule silently resumed=%+v err=%v", got, err)
	}
}

func TestArchiveRejectsActiveWorkAndRollsBackOnEventFailure(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("busy bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.store.AddRun(b.DMConversationID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetBotArchived(b.ID, true); !errors.Is(err, ErrArchiveBusy) {
		t.Fatalf("busy archive err=%v", err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "cancelled", "test"); err != nil {
		t.Fatal(err)
	}
	trigger := `CREATE TRIGGER fail_archive BEFORE INSERT ON events WHEN NEW.conversation_id='` + b.DMConversationID + `' AND NEW.type='bot' BEGIN SELECT RAISE(ABORT,'archive rejected'); END`
	if _, err = s.store.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetBotArchived(b.ID, true); err == nil {
		t.Fatal("event failure unexpectedly succeeded")
	}
	got, err := s.store.GetBot(b.ID)
	if err != nil || got.Archived {
		t.Fatalf("bot changed after rollback=%+v err=%v", got, err)
	}
	dm, err := s.store.GetConversation(b.DMConversationID)
	if err != nil || dm.Archived {
		t.Fatalf("dm changed after rollback=%+v err=%v", dm, err)
	}
}
