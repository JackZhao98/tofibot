package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func teamTestTool(t *testing.T, s *Server, c Conversation, r Run, name string) Tool {
	t.Helper()
	for _, tool := range s.teamTools(c, r) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing team tool %q", name)
	return Tool{}
}

func teamTestRun(t *testing.T, s *Server, bot Bot) (Conversation, Run) {
	t.Helper()
	c, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.store.AddRun(c.ID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate run: %v %v", ok, err)
	}
	r.Status = "running"
	return c, r
}

func TestTeamCreateBotAndGroupRetriesAreIdempotent(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := migrateTeams(s.store.db); err != nil {
		t.Fatal(err)
	}
	owner, err := s.store.CreateBot("owner", "coordinate", "model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.store.CreateBot("other", "research", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, r := teamTestRun(t, s, owner)
	create := teamTestTool(t, s, c, r, "create_bot")
	args := json.RawMessage(`{"name":"analyst","instructions":"read filings","model":"model"}`)
	a, err := create.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	b, err := create.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("retry changed result: %s != %s", a, b)
	}
	var count int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE name='analyst'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("created %d analysts", count)
	}
	group := teamTestTool(t, s, c, r, "create_group")
	gargs := json.RawMessage(`{"name":"filing review","bot_ids":["` + other.ID + `"]}`)
	g1, err := group.Execute(context.Background(), gargs)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := group.Execute(context.Background(), gargs)
	if err != nil {
		t.Fatal(err)
	}
	if g1 != g2 {
		t.Fatalf("group retry changed result")
	}
	var groups int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='group'`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if groups != 1 {
		t.Fatalf("created %d groups", groups)
	}
}

func TestTeamCancelledRunHasNoSideEffects(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := migrateTeams(s.store.db); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("owner", "coordinate", "model")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.store.CreateBot("peer", "research", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, r := teamTestRun(t, s, b)
	g, err := s.store.CreateGroup("cancelled discussion", []string{b.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "cancelled", "test"); err != nil || !ok {
		t.Fatal(err)
	}
	tool := teamTestTool(t, s, c, r, "create_bot")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"never","instructions":"noop"}`)); err == nil {
		t.Fatal("cancelled run created a bot")
	}
	var n int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE name='never'`).Scan(&n)
	if n != 0 {
		t.Fatal("cancelled run side effect persisted")
	}
	send := teamTestTool(t, s, c, r, "send_group_message")
	if _, err = send.Execute(context.Background(), json.RawMessage(`{"group_id":"`+g.ID+`","bot_id":"`+peer.ID+`","message":"never"}`)); err == nil {
		t.Fatal("cancelled run dispatched team work")
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=?`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("cancelled team dispatch persisted")
	}
}

func TestTeamSendRetriesAndForwardsMemberResultsToOrigin(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := migrateTeams(s.store.db); err != nil {
		t.Fatal(err)
	}
	a, _ := s.store.CreateBot("coordinator", "coordinate", "model")
	b, _ := s.store.CreateBot("researcher", "research", "model")
	origin, parent := teamTestRun(t, s, a)
	g, err := s.store.CreateGroup("discussion", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	send := teamTestTool(t, s, origin, parent, "send_group_message")
	args := json.RawMessage(`{"group_id":"` + g.ID + `","message":"Compare the evidence and state one conclusion."}`)
	out, err := send.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := send.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if out != retry {
		t.Fatal("send retry changed result")
	}
	var runs []Run
	runs, err = s.store.Runs(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one coordinator run, got %d", len(runs))
	}
	for _, child := range runs {
		if ok, e := s.store.SetRunStatus(child.ID, "running", ""); e != nil || !ok {
			t.Fatal(e)
		}
		if _, done, e := s.store.FinishRun(child.ID, g.ID, child.BotID, "evidence from "+child.BotID); e != nil || !done {
			t.Fatalf("finish child: %v %v", done, e)
		}
	}
	msgs, _, err := s.store.Messages(origin.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Kind == "forward_result" && strings.Contains(m.Content, "evidence") {
			found = true
		}
	}
	if !found {
		t.Fatal("member result did not reach origin DM")
	}
}

func TestTeamCreateGroupInvalidMemberRollsBackWithoutOrphan(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := s.store.CreateBot("owner", "coordinate", "model")
	c, r := teamTestRun(t, s, owner)
	group := teamTestTool(t, s, c, r, "create_group")
	if _, err = group.Execute(context.Background(), json.RawMessage(`{"name":"broken","bot_ids":["missing"]}`)); err == nil {
		t.Fatal("group with missing member succeeded")
	}
	var groups, operations int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='group'`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM team_operations WHERE run_id=?`, r.ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if groups != 0 || operations != 0 {
		t.Fatalf("failed group left groups=%d operations=%d", groups, operations)
	}
}

func TestTeamCreateGroupEventFailureRollsBackMutation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := s.store.CreateBot("owner", "coordinate", "model")
	other, _ := s.store.CreateBot("other", "research", "model")
	c, r := teamTestRun(t, s, owner)
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_team_conversation_event BEFORE INSERT ON events WHEN NEW.type='conversation' BEGIN SELECT RAISE(ABORT,'event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	group := teamTestTool(t, s, c, r, "create_group")
	args := json.RawMessage(`{"name":"rollback","bot_ids":["` + other.ID + `"]}`)
	if _, err = group.Execute(context.Background(), args); err == nil {
		t.Fatal("group succeeded despite event failure")
	}
	var groups, operations int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='group'`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM team_operations WHERE run_id=?`, r.ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if groups != 0 || operations != 0 {
		t.Fatalf("event failure left groups=%d operations=%d", groups, operations)
	}
}

func TestTeamFamilyLimitsIncludeAncestorAndDescendantRuns(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := s.store.CreateBot("owner", "coordinate", "model")
	peer, _ := s.store.CreateBot("peer", "research", "model")
	c, root := teamTestRun(t, s, owner)
	runs := []Run{root}
	for i := 1; i < maxTeamBotsPerRun+1; i++ {
		child, e := s.store.AddRun(c.ID, owner.ID, runs[len(runs)-1].ID)
		if e != nil {
			t.Fatal(e)
		}
		if ok, e := s.store.SetRunStatus(child.ID, "running", ""); e != nil || !ok {
			t.Fatalf("activate descendant: %v %v", ok, e)
		}
		child.Status = "running"
		runs = append(runs, child)
	}
	for i := 0; i < maxTeamBotsPerRun; i++ {
		create := teamTestTool(t, s, c, runs[i], "create_bot")
		args := json.RawMessage(fmt.Sprintf(`{"name":"specialist-%d","instructions":"task %d","model":"model"}`, i, i))
		if _, err = create.Execute(context.Background(), args); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	create := teamTestTool(t, s, c, runs[maxTeamBotsPerRun], "create_bot")
	if _, err = create.Execute(context.Background(), json.RawMessage(`{"name":"too-many","instructions":"blocked","model":"model"}`)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("descendant escaped family cap: %v", err)
	}
	for i := 0; i < maxTeamGroups; i++ {
		createGroup := teamTestTool(t, s, c, runs[i], "create_group")
		args := json.RawMessage(fmt.Sprintf(`{"name":"group-%d","bot_ids":["%s"]}`, i, peer.ID))
		if _, err = createGroup.Execute(context.Background(), args); err != nil {
			t.Fatalf("create group %d: %v", i, err)
		}
	}
	createGroup := teamTestTool(t, s, c, runs[maxTeamGroups], "create_group")
	if _, err = createGroup.Execute(context.Background(), json.RawMessage(`{"name":"too-many-groups","bot_ids":["`+peer.ID+`"]}`)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("descendant escaped group family cap: %v", err)
	}
}

func TestTeamCreateBotAllowsExistingBotPopulationButKeepsPerRunLimit(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := s.store.CreateBot("owner", "coordinate", "model")
	for i := 0; i < 64; i++ {
		if _, err := s.store.CreateBot(fmt.Sprintf("existing-%d", i), "existing", "model"); err != nil {
			t.Fatal(err)
		}
	}
	c, r := teamTestRun(t, s, owner)
	create := teamTestTool(t, s, c, r, "create_bot")
	for i := 0; i < maxTeamBotsPerRun; i++ {
		if _, err := create.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":"new-%d","instructions":"specialist","model":"model"}`, i))); err != nil {
			t.Fatalf("creation %d unexpectedly blocked above prior population: %v", i, err)
		}
	}
	if _, err := create.Execute(context.Background(), json.RawMessage(`{"name":"new-over-limit","instructions":"specialist","model":"model"}`)); err == nil || !strings.Contains(err.Error(), "team bot creation limit") {
		t.Fatalf("per-run creation limit was removed: %v", err)
	}
}

func TestTeamSequentialDispatchCapAndNestedOrigin(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("coordinator", "coordinate", "model")
	b, _ := s.store.CreateBot("researcher", "research", "model")
	origin, current := teamTestRun(t, s, a)
	g1, err := s.store.CreateGroup("discussion one", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.store.CreateGroup("discussion two", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	currentConversation := origin
	var requesters []Run
	for i := 0; i < maxTeamDispatches; i++ {
		requesters = append(requesters, current)
		target := b.ID
		if current.BotID == b.ID {
			target = a.ID
		}
		targetGroup := g1
		if currentConversation.ID == g1.ID {
			targetGroup = g2
		}
		send := teamTestTool(t, s, currentConversation, current, "send_group_message")
		args := json.RawMessage(fmt.Sprintf(`{"group_id":"%s","bot_id":"%s","message":"round %d"}`, targetGroup.ID, target, i+1))
		out, e := send.Execute(context.Background(), args)
		if e != nil {
			t.Fatalf("round %d: %v", i+1, e)
		}
		var payload struct {
			Runs []Run `json:"runs"`
		}
		if e = json.Unmarshal([]byte(out), &payload); e != nil || len(payload.Runs) != 1 {
			t.Fatalf("round %d payload=%s err=%v", i+1, out, e)
		}
		if payload.Runs[0].OriginConversationID != origin.ID {
			t.Fatalf("round %d origin=%q want %q", i+1, payload.Runs[0].OriginConversationID, origin.ID)
		}
		if ok, e := s.store.SetRunStatus(current.ID, "done", ""); e != nil || !ok {
			t.Fatalf("finish parent round %d: %v %v", i+1, ok, e)
		}
		current = payload.Runs[0]
		if ok, e := s.store.SetRunStatus(current.ID, "running", ""); e != nil || !ok {
			t.Fatalf("activate round %d: %v %v", i+1, ok, e)
		}
		current.Status = "running"
		currentConversation = targetGroup
	}
	targetGroup := g1
	if currentConversation.ID == g1.ID {
		targetGroup = g2
	}
	send := teamTestTool(t, s, currentConversation, current, "send_group_message")
	if _, err = send.Execute(context.Background(), json.RawMessage(`{"group_id":"`+targetGroup.ID+`","bot_id":"`+a.ID+`","message":"round nine"}`)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("ninth family dispatch was accepted: %v", err)
	}
	if _, done, err := s.store.FinishRun(current.ID, currentConversation.ID, current.BotID, "final team conclusion"); err != nil || !done {
		t.Fatalf("finish final child: %v %v", done, err)
	}
	// Keep the inherited origin as provenance, not a shortcut past all the
	// intermediate requesters. Each one must receive and integrate its result.
	for i := len(requesters) - 1; i >= 0; i-- {
		returned, ok, err := s.store.DirectMessageFollowupForRun(current.ID)
		if err != nil || !ok || returned.BotID != requesters[i].BotID || returned.ConversationID != requesters[i].ConversationID {
			t.Fatalf("return hop %d=%+v ok=%v err=%v", i, returned, ok, err)
		}
		if i > 0 {
			var premature int
			if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='forward_result'`, origin.ID).Scan(&premature); err != nil || premature != 0 {
				t.Fatalf("root received result before upstream integration: %d %v", premature, err)
			}
		}
		if _, err := s.store.SetRunStatus(returned.ID, "running", ""); err != nil {
			t.Fatal(err)
		}
		if _, done, err := s.store.FinishRun(returned.ID, returned.ConversationID, returned.BotID, "final team conclusion"); err != nil || !done {
			t.Fatalf("finish return hop %d: %v %v", i, done, err)
		}
		current = returned
	}
	if _, ok, err := s.store.DirectMessageFollowupForRun(current.ID); err != nil || ok {
		t.Fatalf("root return looped: %v %v", ok, err)
	}
	messages, _, err := s.store.Messages(origin.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		found = found || message.Kind == "forward_result" && message.Content == "final team conclusion"
	}
	if !found {
		t.Fatal("nested team result did not return to source DM")
	}
}

func TestTeamSendRejectsCurrentGroupBeforeConsumingHandoffOrBudget(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("coordinator", "coordinate", "model")
	b, _ := s.store.CreateBot("reviewer", "review", "model")
	g, err := s.store.CreateGroup("discussion", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.store.AddRun(g.ID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, e := s.store.SetRunStatus(r.ID, "running", ""); e != nil || !ok {
		t.Fatalf("activate run: %v %v", ok, e)
	}
	r.Status = "running"
	send := teamTestTool(t, s, g, r, "send_group_message")
	args := json.RawMessage(`{"group_id":"` + g.ID + `","bot_id":"` + b.ID + `","message":"review this"}`)
	if _, err = send.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "use handoff") {
		t.Fatalf("current-group dispatch was not rejected clearly: %v", err)
	}
	var handoffs, operations int
	if err = s.store.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, r.ID).Scan(&handoffs); err != nil {
		t.Fatal(err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM team_operations WHERE run_id=?`, r.ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if handoffs != 0 || operations != 0 {
		t.Fatalf("rejected dispatch consumed handoffs=%d operations=%d", handoffs, operations)
	}
	if _, child, e := s.store.AddHandoff(g.ID, a.ID, b.ID, r.ID, "review this"); e != nil || child.BotID != b.ID {
		t.Fatalf("legitimate handoff failed after rejection: child=%#v err=%v", child, e)
	}
}

func TestTeamConcurrentRetryCreatesExactlyOneGroup(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := s.store.CreateBot("owner", "coordinate", "model")
	other, _ := s.store.CreateBot("other", "research", "model")
	c, r := teamTestRun(t, s, owner)
	group := teamTestTool(t, s, c, r, "create_group")
	args := json.RawMessage(`{"name":"concurrent","bot_ids":["` + other.ID + `"]}`)
	const callers = 12
	results := make(chan string, callers)
	errors := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := group.Execute(context.Background(), args)
			results <- out
			errors <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	first := ""
	for result := range results {
		if first == "" {
			first = result
		} else if result != first {
			t.Fatalf("concurrent retry changed result")
		}
	}
	var groups, operations int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='group'`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM team_operations WHERE run_id=?`, r.ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if groups != 1 || operations != 1 {
		t.Fatalf("concurrent calls created groups=%d operations=%d", groups, operations)
	}
}

func TestTeamDefaultModelNeverBecomesUpstreamModelName(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "codex-gpt-5.6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.store.CreateBot("owner", "coordinate", "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	c, r := teamTestRun(t, s, owner)
	tool := teamTestTool(t, s, c, r, "create_bot")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"worker","instructions":"task","model":"default"}`))
	if err != nil {
		t.Fatal(err)
	}
	var b Bot
	if err = json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	if b.Model != "" {
		t.Fatalf("model alias leaked into upstream configuration: %q", b.Model)
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"bad","instructions":"task","model":"invented-model"}`)); err == nil {
		t.Fatal("invented upstream model accepted")
	}
}
