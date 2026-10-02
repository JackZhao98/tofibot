package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestParseMentionsExactOrderNamesAndEmailBoundary(t *testing.T) {
	members := []Bot{{ID: "a", Name: "整理员"}, {ID: "b", Name: "校对员"}, {ID: "c", Name: "ann"}}
	got := parseMentions("请 @校对员，随后 @整理员。mail ann@example.com", members)
	if strings.Join(got.IDs, ",") != "b,a" || len(got.Unknown) != 0 {
		t.Fatalf("mentions=%#v", got)
	}
	got = parseMentions("@ann", members)
	if len(got.IDs) != 1 || got.IDs[0] != "c" {
		t.Fatalf("exact mention=%#v", got)
	}
	got = parseMentions("mail @ann@example.com", members)
	if len(got.IDs) != 0 || len(got.Unknown) != 0 {
		t.Fatalf("email treated as mention=%#v", got)
	}
}

func TestAddUserRunsDurableFanoutAndIdempotency(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("a", "", "m1")
	b, _ := s.CreateBot("b", "", "m2")
	c, _ := s.CreateGroup("g", []string{a.ID, b.ID})
	m, runs, dup, err := s.AddUserRuns(c.ID, "@a @b work", "client", []runSpec{{BotID: a.ID, Model: a.Model}, {BotID: b.ID, Model: b.Model}})
	if err != nil || dup || len(runs) != 2 || m.RunID != runs[0].ID {
		t.Fatalf("message=%#v runs=%#v dup=%v err=%v", m, runs, dup, err)
	}
	if runs[0].TriggerMessageID != m.ID || runs[1].TriggerMessageID != m.ID || runs[0].QueueSeq >= runs[1].QueueSeq {
		t.Fatalf("anchors/order=%#v", runs)
	}
	_, again, dup, err := s.AddUserRuns(c.ID, "ignored", "client", []runSpec{{BotID: a.ID}})
	if err != nil || !dup || len(again) != 2 || again[0].ID != runs[0].ID || again[1].ID != runs[1].ID {
		t.Fatalf("duplicate runs=%#v dup=%v err=%v", again, dup, err)
	}
}

func TestDMForwardUsesTargetCanonicalDMAndDurableNotice(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	parent, _ := s.AddRun(a.DMConversationID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	notice, child, err := s.AddForwardHandoff(a.DMConversationID, a.ID, b.ID, parent.ID, "只检查引用")
	if err != nil {
		t.Fatal(err)
	}
	if child.ConversationID != b.DMConversationID || child.OriginConversationID != a.DMConversationID || notice.Kind != "notice" || notice.Notice == nil {
		t.Fatalf("notice=%#v child=%#v", notice, child)
	}
	if notice.Notice.TargetRunID != child.ID || notice.Notice.TargetConversationID != b.DMConversationID {
		t.Fatalf("notice payload=%#v", notice.Notice)
	}
	msgs, _, _ := s.Messages(b.DMConversationID, 0, 20)
	if len(msgs) != 1 || msgs[0].Content != "只检查引用" {
		t.Fatalf("target messages=%#v", msgs)
	}
}

func TestDMHandoffToolAcceptsAnotherBotAndUsesCanonicalDM(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	c, _ := s.GetConversation(a.DMConversationID)
	parent, _ := s.AddRun(c.ID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, runs: map[string]context.CancelFunc{}, queues: map[string]*conversationQueue{}}
	tools := server.tools(c, parent)
	childID, err := tools[2].Execute(context.Background(), json.RawMessage(`{"bot_id":"`+b.ID+`","task":"check"}`))
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.GetRun(childID)
	if err != nil || child.ConversationID != b.DMConversationID {
		t.Fatalf("child=%#v err=%v", child, err)
	}
}

func TestBotMessageUsesHiddenTraceAndCapsulesOnly(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	parent, _ := s.AddRun(a.DMConversationID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	capsule, child, err := s.AddBotMessage(a.DMConversationID, a.ID, b.ID, parent.ID, "请只回复这一条，不要修改任何持久配置")
	if err != nil {
		t.Fatal(err)
	}
	if capsule.Kind != "message_ref" || capsule.Notice == nil || capsule.Notice.Type != "message" {
		t.Fatalf("capsule=%#v", capsule)
	}
	if child.ConversationID == b.DMConversationID || child.OriginConversationID != a.DMConversationID || child.Kind != runKindMessage {
		t.Fatalf("child=%#v", child)
	}
	trace, err := s.GetConversation(child.ConversationID)
	if err != nil || trace.UserVisible {
		t.Fatalf("trace=%#v err=%v", trace, err)
	}
	visible, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range visible {
		if item.ID == trace.ID {
			t.Fatalf("hidden trace leaked into sidebar: %#v", visible)
		}
	}
	if _, err = s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	visible, err = s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	var targetDM Conversation
	for _, conversation := range visible {
		if conversation.ID == b.DMConversationID {
			targetDM = conversation
		}
	}
	if !slicesContains(targetDM.WorkingBotIDs, b.ID) {
		t.Fatalf("target DM did not expose hidden work: %#v", targetDM)
	}
	if _, _, err = s.FinishRun(child.ID, trace.ID, b.ID, "只回复当前消息"); err != nil {
		t.Fatal(err)
	}
	followup, ok, err := s.DirectMessageFollowupForRun(child.ID)
	if err != nil || !ok || followup.BotID != a.ID || followup.ConversationID != a.DMConversationID || followup.TriggerMessageID == "" {
		t.Fatalf("missing sender followup=%#v ok=%v err=%v", followup, ok, err)
	}
	anchor, err := s.GetMessage(followup.TriggerMessageID)
	if err != nil || anchor.Kind != messageKindBotResult || anchor.Content != "只回复当前消息" {
		t.Fatalf("followup anchor=%#v err=%v", anchor, err)
	}
	sourceMessages, _, err := s.Messages(a.DMConversationID, 0, 20)
	if err != nil || len(sourceMessages) != 1 || sourceMessages[0].Kind != "message_ref" {
		t.Fatalf("source messages=%#v err=%v", sourceMessages, err)
	}
	if strings.Contains(sourceMessages[0].Content, "只回复当前消息") {
		t.Fatalf("source capsule leaked reply: %#v", sourceMessages[0])
	}
	traceMessages, _, err := s.Messages(trace.ID, 0, 20)
	if err != nil || len(traceMessages) != 2 || traceMessages[1].Content != "只回复当前消息" {
		t.Fatalf("trace messages=%#v err=%v", traceMessages, err)
	}
	targetMessages, _, err := s.Messages(b.DMConversationID, 0, 20)
	if err != nil || len(targetMessages) != 1 || targetMessages[0].Kind != "message_ref" {
		t.Fatalf("target messages=%#v err=%v", targetMessages, err)
	}
}

func TestNestedBotMessageReturnsToTheSenderInsideHiddenTrace(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	c, _ := s.CreateBot("C", "", "mc")
	parent, _ := s.AddRun(a.DMConversationID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, bRun, err := s.AddBotMessage(a.DMConversationID, a.ID, b.ID, parent.ID, "请让 C 检查这一项")
	if err != nil {
		t.Fatal(err)
	}
	trace, err := s.GetConversation(bRun.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(bRun.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, cRun, err := s.AddBotMessage(trace.ID, b.ID, c.ID, bRun.ID, "请检查这一项")
	if err != nil {
		t.Fatal(err)
	}
	cTrace, err := s.GetConversation(cRun.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(cRun.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.FinishRun(cRun.ID, cTrace.ID, c.ID, "C 已检查完成"); err != nil {
		t.Fatal(err)
	}
	followup, ok, err := s.DirectMessageFollowupForRun(cRun.ID)
	if err != nil || !ok || followup.BotID != b.ID || followup.ConversationID != trace.ID {
		t.Fatalf("missing hidden sender followup=%#v ok=%v err=%v", followup, ok, err)
	}
	traceMessages, _, err := s.Messages(trace.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range traceMessages {
		if message.Kind == "forward_result" && message.Content == "C 已检查完成" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hidden trace did not retain nested result: %#v", traceMessages)
	}
}

func TestBotMessagesUseOneUntitledTraceForMultipleRecipients(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	c, _ := s.CreateBot("C", "", "mc")
	parent, _ := s.AddRun(a.DMConversationID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	capsule, children, err := s.AddBotMessages(a.DMConversationID, a.ID, []string{b.ID, c.ID}, parent.ID, "一起看这一条")
	if err != nil || len(children) != 2 {
		t.Fatalf("capsule=%#v children=%#v err=%v", capsule, children, err)
	}
	if children[0].ConversationID != children[1].ConversationID || capsule.Kind != "message_ref" || capsule.Notice == nil {
		t.Fatalf("capsule=%#v children=%#v", capsule, children)
	}
	trace, err := s.GetConversation(children[0].ConversationID)
	if err != nil || trace.UserVisible || trace.Name != "" {
		t.Fatalf("trace=%#v err=%v", trace, err)
	}
	if len(capsule.Notice.TargetBotIDs) != 2 || len(capsule.Notice.TargetRunIDs) != 2 {
		t.Fatalf("notice=%#v", capsule.Notice)
	}
	sourceMessages, _, err := s.Messages(a.DMConversationID, 0, 20)
	if err != nil || len(sourceMessages) != 1 || sourceMessages[0].Kind != "message_ref" {
		t.Fatalf("source messages=%#v err=%v", sourceMessages, err)
	}
	for _, target := range []Bot{b, c} {
		messages, _, err := s.Messages(target.DMConversationID, 0, 20)
		if err != nil || len(messages) != 1 || messages[0].Kind != "message_ref" {
			t.Fatalf("target=%s messages=%#v err=%v", target.ID, messages, err)
		}
		containsTarget := false
		for _, id := range messages[0].Notice.TargetBotIDs {
			containsTarget = containsTarget || id == target.ID
		}
		if messages[0].Content != "Message from A" || messages[0].Notice.FromBotID != a.ID || !containsTarget {
			t.Fatalf("target=%s capsule=%#v", target.ID, messages[0])
		}
	}
}

func TestHiddenBotRunCanExplicitlyMessageItsUser(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("A", "", "ma")
	b, _ := s.CreateBot("B", "", "mb")
	parent, _ := s.AddRun(a.DMConversationID, a.ID, "")
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, children, err := s.AddBotMessages(a.DMConversationID, a.ID, []string{b.ID}, parent.ID, "告诉用户今天是什么日子")
	if err != nil || len(children) != 1 {
		t.Fatalf("children=%#v err=%v", children, err)
	}
	child := children[0]
	if _, err = s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	m, err := s.AddBotUserMessage(b.ID, child.ID, "今天是星期六。")
	if err != nil || m.ConversationID != b.DMConversationID || m.Role != "assistant" || m.SenderBotID != b.ID {
		t.Fatalf("message=%#v err=%v", m, err)
	}
	messages, _, err := s.Messages(b.DMConversationID, 0, 20)
	if err != nil || len(messages) != 2 || messages[1].Content != "今天是星期六。" {
		t.Fatalf("user messages=%#v err=%v", messages, err)
	}
	if messages[1].Kind != messageKindUser {
		t.Fatalf("user message kind=%q", messages[1].Kind)
	}
	conversations, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range conversations {
		if conversation.ID == b.DMConversationID {
			if conversation.UnreadCount != 1 || conversation.LastMessage == nil || conversation.LastMessage.Internal || conversation.LastMessage.Kind != messageKindUser {
				t.Fatalf("user inbox preview=%+v", conversation)
			}
			return
		}
	}
	t.Fatalf("missing target conversation %s", b.DMConversationID)
}

type directResumeEngine struct {
	target string
}

func (e directResumeEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.target {
		return runtime.Result{Content: "target result"}, nil
	}
	for _, message := range req.Messages {
		if strings.Contains(message.Content, "[completed colleague task]") {
			return runtime.Result{Content: "primary resumed"}, nil
		}
	}
	for _, tool := range req.Tools {
		if tool.Name != "handoff" {
			continue
		}
		args, _ := json.Marshal(map[string]string{"bot_id": e.target, "task": "inspect this"})
		_, err := tool.Execute(ctx, args)
		return runtime.Result{}, err
	}
	return runtime.Result{}, context.Canceled
}

func TestDMHandoffReturnsToPrimaryConversationWithoutTargetNotification(t *testing.T) {
	a, err := NewServer(Config{DataDir: t.TempDir(), Provider: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	primary, _ := a.store.CreateBot("Primary", "", "primary-model")
	target, _ := a.store.CreateBot("Target", "", "target-model")
	a.mu.Lock()
	a.engine = directResumeEngine{target: target.ID}
	a.mu.Unlock()
	origin, _ := a.store.GetConversation(primary.DMConversationID)
	_, parent, _, err := a.store.AddUserRun(origin.ID, primary.ID, "delegate this", "dm-forward")
	if err != nil {
		t.Fatal(err)
	}
	a.enqueue(origin, parent)
	waitForRunStatus(t, a, parent.ID, "done")
	targetRuns, err := a.store.Runs(target.DMConversationID)
	if err != nil || len(targetRuns) != 1 {
		t.Fatalf("target runs=%+v err=%v", targetRuns, err)
	}
	targetRun := targetRuns[0]
	waitForRunStatus(t, a, targetRun.ID, "done")
	followup, ok, err := a.store.DirectMessageFollowupForRun(targetRun.ID)
	if err != nil || !ok {
		t.Fatalf("followup=%+v ok=%v err=%v", followup, ok, err)
	}
	waitForRunStatus(t, a, followup.ID, "done")

	allConversations, err := a.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	var targetConversation Conversation
	for _, conversation := range allConversations {
		if conversation.ID == target.DMConversationID {
			targetConversation = conversation
		}
	}
	if targetConversation.UnreadCount != 0 || targetConversation.LastMessage == nil || !targetConversation.LastMessage.Internal {
		t.Fatalf("target inbox preview=%+v", targetConversation)
	}
	messages, _, err := a.store.Messages(origin.ID, 0, 20)
	if err != nil || len(messages) != 4 || messages[2].Kind != "forward_result" || messages[2].Content != "target result" || messages[3].Content != "primary resumed" {
		t.Fatalf("origin messages=%+v err=%v", messages, err)
	}
}

type relayOutcomeEngine struct {
	target, childResult string
	childErr            error
	started, release    chan struct{}
}

func (e relayOutcomeEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.target {
		select {
		case e.started <- struct{}{}:
		default:
		}
		select {
		case <-e.release:
		case <-ctx.Done():
			return runtime.Result{}, ctx.Err()
		}
		return runtime.Result{Content: e.childResult}, e.childErr
	}
	for _, message := range req.Messages {
		if strings.Contains(message.Content, "[completed colleague task]") {
			return runtime.Result{Content: "A reports: " + e.childResult}, nil
		}
	}
	for _, tool := range req.Tools {
		if tool.Name == "handoff" {
			args, _ := json.Marshal(map[string]string{"bot_id": e.target, "task": "Check the isolated item"})
			_, err := tool.Execute(ctx, args)
			return runtime.Result{}, err
		}
	}
	return runtime.Result{}, errors.New("handoff unavailable")
}

func TestDMRelayOutcomesReturnOnlyToOrigin(t *testing.T) {
	for _, test := range []struct {
		name, result, want string
		err                error
		cancel             bool
	}{
		{name: "completed", result: "B checked the item", want: "completed"},
		{name: "refused", result: "B cannot complete this request", want: "completed"},
		{name: "failed", err: errors.New("private synthetic failure"), want: "failed"},
		{name: "interrupted", result: "late result must not publish", want: "cancelled", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := store.CreateBot("A", "", "model")
			b, _ := store.CreateBot("B", "", "model")
			other, _ := store.CreateBot("unrelated", "", "model")
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			engine := relayOutcomeEngine{target: b.ID, childResult: test.result, childErr: test.err, started: make(chan struct{}, 1), release: make(chan struct{})}
			server, err := NewServer(Config{DataDir: dir, Engine: engine})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			origin, _ := server.store.GetConversation(a.DMConversationID)
			_, parent, _, err := server.store.AddUserRun(origin.ID, a.ID, "Delegate the isolated item", "relay-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			server.enqueue(origin, parent)
			waitForRunStatus(t, server, parent.ID, "done")
			select {
			case <-engine.started:
			case <-time.After(2 * time.Second):
				t.Fatal("B did not start")
			}
			runs, err := server.store.Runs(b.DMConversationID)
			if err != nil || len(runs) != 1 || runs[0].ParentRunID != parent.ID || runs[0].OriginConversationID != origin.ID {
				t.Fatalf("B assignment=%+v err=%v", runs, err)
			}
			child := runs[0]
			if state := relayTaskState(t, server.store, origin.ID); state == nil || state.Status != "executing" {
				t.Fatalf("origin before B result=%+v", state)
			}
			if test.cancel {
				if err := server.store.CancelRunTree(child.ID); err != nil {
					t.Fatal(err)
				}
			}
			close(engine.release)
			childStatus := "done"
			if test.err != nil {
				childStatus = "failed"
			}
			if test.cancel {
				childStatus = "cancelled"
			}
			waitForRunStatus(t, server, child.ID, childStatus)
			if test.want == "completed" {
				followup, found, err := server.store.DirectMessageFollowupForRun(child.ID)
				if err != nil || !found {
					t.Fatalf("A continuation=%+v found=%v err=%v", followup, found, err)
				}
				waitForRunStatus(t, server, followup.ID, "done")
			}
			state := relayTaskState(t, server.store, origin.ID)
			if state == nil || state.Status != test.want {
				t.Fatalf("origin outcome=%+v want=%s", state, test.want)
			}
			messages, _, err := server.store.Messages(origin.ID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			var returns, final int
			for _, message := range messages {
				if message.Kind == "forward_result" && message.RunID == child.ID && message.Content == test.result {
					returns++
				}
				if strings.HasPrefix(message.Content, "A reports: ") {
					final++
				}
			}
			if (test.want == "completed" && (returns != 1 || final != 1)) || (test.want != "completed" && (returns != 0 || final != 0)) {
				t.Fatalf("origin delivery: returns=%d final=%d", returns, final)
			}
			if state := relayTaskState(t, server.store, other.DMConversationID); state != nil {
				t.Fatalf("unrelated conversation received relay state: %+v", state)
			}
		})
	}
}

func relayTaskState(t *testing.T, store *Store, conversationID string) *ConversationTaskState {
	t.Helper()
	items, err := store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == conversationID {
			return item.TaskState
		}
	}
	t.Fatal("conversation missing")
	return nil
}

type collaborationCaptureEngine struct{ requests chan runtime.Request }

func (e collaborationCaptureEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	e.requests <- req
	return runtime.Result{Content: "done"}, nil
}

func TestQueueAcceptsMessagesAndKeepsRootAnchor(t *testing.T) {
	e := collaborationCaptureEngine{requests: make(chan runtime.Request, 4)}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, runs, _, err := s.store.AddUserRuns(c.ID, "first", "one", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	_, runs2, _, err := s.store.AddUserRuns(c.ID, "second", "two", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, runs[0])
	s.enqueue(c, runs2[0])
	select {
	case req := <-e.requests:
		if req.RunID != runs[0].ID {
			t.Fatalf("first run=%s", req.RunID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first run not executed")
	}
	select {
	case req := <-e.requests:
		if req.RunID != runs2[0].ID {
			t.Fatalf("second run=%s", req.RunID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second run not executed")
	}
}

func TestStrictTriageTargetRejectsNonMemberAndAcceptsOnlyExactJSON(t *testing.T) {
	members := []Bot{{ID: "a", Name: "A"}}
	if _, err := strictTriageTarget(`{"bot_id":"missing"}`, members); err == nil {
		t.Fatal("accepted non-member")
	}
	id, err := strictTriageTarget(`{"bot_id":"a"}`, members)
	if err != nil || id != "a" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	var n HandoffNotice
	if json.Unmarshal([]byte(`{"type":"forward"}`), &n) != nil {
		t.Fatal("notice shape invalid")
	}
}
