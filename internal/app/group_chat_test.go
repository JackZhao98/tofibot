package app

import (
	"context"
	"strings"
	"testing"
)

func TestGroupRoundContextAndCancellationFollowDelegation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	c, _ := s.store.CreateBot("charlie", "", "model")
	group, _ := s.store.CreateGroup("group", []string{a.ID, b.ID, c.ID})
	_, round, _, err := s.store.AddUserRuns(group.ID, "original question", "round", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetRunStatus(round[0].ID, "running", "")
	var peers []string
	for _, id := range group.BotIDs {
		if id != round[0].BotID {
			peers = append(peers, id)
		}
	}
	invited, err := s.store.InviteGroupMembers(round[0].ID, peers)
	if err != nil || len(invited) != 2 {
		t.Fatalf("invitation=%+v %v", invited, err)
	}
	round = append(round, invited...)
	// This is a context-boundary fixture, not live human ingress. Keep the
	// no-steering delegation/duplicate test separate from cancellation caused
	// by AddUserRuns while a round is active (covered below).
	_, _, err = s.store.AddMessage(group.ID, "user", "", "", "unrelated later input", "later")
	if err != nil {
		t.Fatal(err)
	}
	later, err := s.store.AddRun(group.ID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetRunStatus(round[0].ID, "running", "")
	_, child, err := s.store.AddHandoff(group.ID, round[0].BotID, round[1].BotID, round[0].ID, "concrete assignment")
	if err != nil {
		t.Fatal(err)
	}
	s.store.FinishRun(round[0].ID, group.ID, round[0].BotID, "earlier answer")
	next, ok, err := s.store.nextQueuedRun(group.ID)
	if err != nil || !ok || next.ID != child.ID {
		t.Fatalf("delegation priority: %+v %v", next, err)
	}
	s.store.SetRunStatus(child.ID, "running", "")
	s.store.FinishRun(child.ID, group.ID, child.BotID, "colleague findings")
	follow, ok, err := s.store.GroupFollowupForRun(child.ID)
	if err != nil || !ok {
		t.Fatalf("followup: %v", err)
	}
	bot, _ := s.store.GetBot(round[2].BotID)
	messages, _ := s.buildContext(group, round[2], bot)
	var text strings.Builder
	for _, m := range messages {
		text.WriteString(m.Content)
	}
	for _, want := range []string{"original question", "earlier answer", "colleague findings"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("missing %q: %s", want, text.String())
		}
	}
	if strings.Contains(text.String(), "unrelated later input") {
		t.Fatal("later input leaked into round")
	}
	// The assigned peer's passive opportunity is consumed, without another call.
	s.executeForQueue(round[1])
	got, _ := s.store.GetRun(round[1].ID)
	if got.Status != "done" {
		t.Fatalf("peer duplicate: %+v", got)
	}
	s.store.SetRunStatus(follow.ID, "running", "")
	if err = s.store.CancelRunTree(follow.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{follow.ID, round[2].ID} {
		got, _ := s.store.GetRun(id)
		if got.Status != "cancelled" {
			t.Fatalf("round cancellation: %+v", got)
		}
	}
	got, _ = s.store.GetRun(later.ID)
	if got.Status != "queued" {
		t.Fatalf("unrelated input cancelled: %+v", got)
	}
}

func TestActiveGroupSteeringCancelsOnlyCurrentRoundPeers(t *testing.T) {
	for _, activeChild := range []bool{false, true} {
		name := "root_running"
		if activeChild {
			name = "delegated_child_running"
		}
		t.Run(name, func(t *testing.T) {
			s, group, root := steeringReviewFixture(t, true)
			_, independent, _, err := s.store.AddUserRuns(group.ID, "Independent queued assignment", "group-independent", []runSpec{{BotID: root.BotID, Model: root.Model}})
			if err != nil {
				t.Fatal(err)
			}
			if changed, err := s.store.SetRunStatus(root.ID, "running", ""); err != nil || !changed {
				t.Fatalf("start round: changed=%v err=%v", changed, err)
			}
			var peerID string
			for _, id := range group.BotIDs {
				if id != root.BotID {
					peerID = id
				}
			}
			peers, err := s.store.InviteGroupMembers(root.ID, []string{peerID})
			if err != nil || len(peers) != 1 {
				t.Fatalf("invite peer: peers=%+v err=%v", peers, err)
			}
			active := root
			if activeChild {
				_, child, err := s.store.AddHandoff(group.ID, root.BotID, peerID, root.ID, "A bounded delegated assignment")
				if err != nil {
					t.Fatal(err)
				}
				if _, done, err := s.store.FinishRun(root.ID, group.ID, root.BotID, "Delegated the bounded assignment."); err != nil || !done {
					t.Fatalf("finish parent: done=%v err=%v", done, err)
				}
				if changed, err := s.store.SetRunStatus(child.ID, "running", ""); err != nil || !changed {
					t.Fatalf("start child: changed=%v err=%v", changed, err)
				}
				active = child
			}
			steeringReviewInterject(t, s, group, root)
			for _, expected := range []struct{ id, status string }{
				{peers[0].ID, "cancelled"},
				{independent[0].ID, "queued"},
				{active.ID, "running"},
			} {
				got, err := s.store.GetRun(expected.id)
				if err != nil || got.Status != expected.status {
					t.Errorf("steering scope: run=%+v want status=%s err=%v", got, expected.status, err)
				}
			}
		})
	}
}

func TestGroupRoundSilentCompletionAndRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	group, _ := store.CreateGroup("group", []string{a.ID, b.ID})
	_, round, _, err := store.AddUserRuns(group.ID, "question", "stable", nil)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, again, duplicate, err := store.AddUserRuns(group.ID, "question", "stable", nil)
	if err != nil || !duplicate || len(again) != 1 || again[0].ID != round[0].ID {
		t.Fatalf("restart idempotence: %+v %v", again, err)
	}
	server := &Server{store: store}
	for _, r := range round {
		store.SetRunStatus(r.ID, "running", "")
		tool := server.groupChatTools(group, r)[0]
		if _, err = tool.Execute(context.Background(), []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		got, _ := store.GetRun(r.ID)
		if got.Status != "done" {
			t.Fatalf("silent status: %+v", got)
		}
	}
	messages, _, err := store.Messages(group.ID, 0, 20)
	if err != nil || len(messages) != 1 {
		t.Fatalf("silent messages: %+v %v", messages, err)
	}
	if _, ok, err := store.nextQueuedRun(group.ID); err != nil || ok {
		t.Fatalf("round did not end: %v", err)
	}
}

func TestGroupSilenceCannotRetractPublishedWork(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	group, _ := s.store.CreateGroup("group", []string{a.ID, b.ID})
	_, round, _, err := s.store.AddUserRuns(group.ID, "question", "round", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := round[0]
	s.store.SetRunStatus(r.ID, "running", "")
	if _, err = s.store.AddAssistant(group.ID, r.BotID, r.ID, "already contributed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.groupChatTools(group, r)[0].Execute(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("silence erased a public contribution")
	}
	got, _ := s.store.GetRun(r.ID)
	if got.Status != "running" {
		t.Fatalf("rejected silence completed run: %+v", got)
	}
	if tools := s.groupChatTools(group, Run{Kind: runKindGroupTask}); len(tools) != 0 {
		t.Fatal("explicit task can silently drop its assignment")
	}
}

func TestGroupInvitationsValidateAtomicallyAndDeduplicateAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	c, _ := store.CreateBot("charlie", "", "model")
	outside, _ := store.CreateBot("outside", "", "model")
	group, _ := store.CreateGroup("group", []string{a.ID, b.ID, c.ID})
	_, runs, _, err := store.AddUserRuns(group.ID, "question", "input", nil)
	if err != nil || len(runs) != 1 {
		t.Fatalf("starter=%+v %v", runs, err)
	}
	starter := runs[0]
	store.SetRunStatus(starter.ID, "running", "")
	var peers []string
	for _, id := range group.BotIDs {
		if id != starter.BotID {
			peers = append(peers, id)
		}
	}
	if _, err = store.InviteGroupMembers(starter.ID, []string{peers[0], outside.ID}); err == nil {
		t.Fatal("accepted non-member")
	}
	got, _ := store.Runs(group.ID)
	if len(got) != 1 {
		t.Fatalf("partial invitation survived rejection: %+v", got)
	}
	invited, err := store.InviteGroupMembers(starter.ID, []string{starter.BotID, peers[0], peers[0]})
	if err != nil || len(invited) != 1 || invited[0].BotID != peers[0] {
		t.Fatalf("subset=%+v %v", invited, err)
	}
	if repeated, err := store.InviteGroupMembers(starter.ID, []string{peers[0]}); err != nil || len(repeated) != 0 {
		t.Fatalf("repeat=%+v %v", repeated, err)
	}
	store.FinishRun(starter.ID, group.ID, starter.BotID, "starter answer")
	if _, err = store.InviteGroupMembers(starter.ID, []string{peers[1]}); err == nil {
		t.Fatal("finished parent invited new work")
	}
	store.Close()
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	peer := invited[0]
	store.SetRunStatus(peer.ID, "running", "")
	rest, err := store.InviteGroupMembers(peer.ID, []string{starter.BotID, peer.BotID, peers[1]})
	if err != nil || len(rest) != 1 || rest[0].BotID != peers[1] {
		t.Fatalf("restart expansion=%+v %v", rest, err)
	}
	store.FinishRun(peer.ID, group.ID, peer.BotID, "peer answer")
	if _, ok, err := store.GroupFollowupForRun(peer.ID); err != nil || ok {
		t.Fatalf("ordinary invitation created return: %v", err)
	}
	store.SetRunStatus(rest[0].ID, "running", "")
	if cycle, err := store.InviteGroupMembers(rest[0].ID, group.BotIDs); err != nil || len(cycle) != 0 {
		t.Fatalf("cycle=%+v %v", cycle, err)
	}
	if err = store.CancelRunTree(rest[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InviteGroupMembers(rest[0].ID, group.BotIDs); err == nil {
		t.Fatal("cancelled turn invited work")
	}
}

func TestGroupInvitationDoesNotDuplicateExplicitTaskRecipient(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	group, _ := store.CreateGroup("group", []string{a.ID, b.ID})
	_, runs, _, _ := store.AddUserRuns(group.ID, "question", "input", nil)
	starter := runs[0]
	target := a.ID
	if target == starter.BotID {
		target = b.ID
	}
	store.SetRunStatus(starter.ID, "running", "")
	if _, _, err = store.AddHandoff(group.ID, starter.BotID, target, starter.ID, "concrete task"); err != nil {
		t.Fatal(err)
	}
	if invited, err := store.InviteGroupMembers(starter.ID, []string{target}); err != nil || len(invited) != 0 {
		t.Fatalf("task recipient invitation=%+v %v", invited, err)
	}
}

func TestGroupInvitationRejectsUnavailableMembershipAndContextBoundsProfiles(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	s := server.store
	a, _ := s.CreateBot("alpha", strings.Repeat("role reference ", 1000), "model")
	b, _ := s.CreateBot("bravo", "研究与分析", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	_, runs, _, _ := s.AddUserRuns(group.ID, "question", "input", nil)
	r := runs[0]
	s.SetRunStatus(r.ID, "running", "")
	target := a.ID
	if target == r.BotID {
		target = b.ID
	}
	for _, tc := range []struct {
		name, mutate, restore string
		args                  []any
	}{
		{"archived group", `UPDATE conversations SET archived=1 WHERE id=?`, `UPDATE conversations SET archived=0 WHERE id=?`, []any{group.ID}},
		{"archived parent", `UPDATE bots SET archived=1 WHERE id=?`, `UPDATE bots SET archived=0 WHERE id=?`, []any{r.BotID}},
		{"archived target", `UPDATE bots SET archived=1 WHERE id=?`, `UPDATE bots SET archived=0 WHERE id=?`, []any{target}},
		{"removed parent", `DELETE FROM members WHERE conversation_id=? AND bot_id=?`, `INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, []any{group.ID, r.BotID}},
		{"removed target", `DELETE FROM members WHERE conversation_id=? AND bot_id=?`, `INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, []any{group.ID, target}},
	} {
		if _, err = s.db.Exec(tc.mutate, tc.args...); err != nil {
			t.Fatal(err)
		}
		if _, err = s.InviteGroupMembers(r.ID, []string{target}); err == nil {
			t.Fatalf("accepted %s", tc.name)
		}
		if _, err = s.db.Exec(tc.restore, tc.args...); err != nil {
			t.Fatal(err)
		}
	}
	bot, _ := s.GetBot(r.BotID)
	messages, system := server.buildContext(group, r, bot)
	found := false
	for _, m := range messages {
		if strings.Contains(m.Content, "[untrusted group profile metadata]") {
			found = true
			if len([]rune(m.Content)) > 2200 || !strings.Contains(m.Content, "研究与分析") {
				t.Fatalf("profile metadata lost text or bound: %s", m.Content)
			}
		}
	}
	if !found || !strings.Contains(system, "invite_group_members") {
		t.Fatal("missing role metadata or invitation instructions")
	}
}

func TestInterruptedGroupStarterLeavesDurableInvitations(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	_, runs, _, _ := s.AddUserRuns(group.ID, "question", "input", nil)
	r := runs[0]
	s.SetRunStatus(r.ID, "running", "")
	target := a.ID
	if target == r.BotID {
		target = b.ID
	}
	invited, err := s.InviteGroupMembers(r.ID, []string{target})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, _ := s.GetRun(r.ID)
	if root.Status != "interrupted" {
		t.Fatalf("root=%+v", root)
	}
	if _, err = s.InviteGroupMembers(r.ID, []string{target}); err == nil {
		t.Fatal("interrupted starter invited work")
	}
	next, ok, err := s.nextQueuedRun(group.ID)
	if err != nil || !ok || next.ID != invited[0].ID {
		t.Fatalf("lost durable invitation %+v %v", next, err)
	}
	if err = s.CancelRunTree(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(next.ID)
	if got.Status != "cancelled" {
		t.Fatalf("interrupted root cancellation=%+v", got)
	}
}
