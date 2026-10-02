package app

import (
	"strings"
	"testing"
)

func TestBuildContextChildHandoffBoundaryAndParticipantRoles(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	parent, err := s.CreateBot("整理员", "coordinate the group", "model")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateBot("校对员", "check assigned work", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("工作组", []string{parent.ID, child.ID})
	if err != nil {
		t.Fatal(err)
	}
	parentRun, err := s.AddRun(c.ID, parent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parentRun.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AddMessage(c.ID, "user", "", parentRun.ID, "原始请求：整理全部材料并交给校对员", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAssistant(c.ID, parent.ID, parentRun.ID, "父任务总结：材料已整理，校对员只需核对引用"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAssistant(c.ID, child.ID, parentRun.ID, "校对员此前的自身回合"); err != nil {
		t.Fatal(err)
	}
	_, childRun, err := s.AddHandoff(c.ID, parent.ID, child.ID, parentRun.ID, "只核对引用并报告发现的问题")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.SaveSummary(c.ID, 2, "父任务总结：材料已整理"); err != nil {
		t.Fatal(err)
	}

	msgs, system := (&Server{store: s}).buildContext(c, childRun, child)
	if !strings.Contains(system, child.Name) || !strings.Contains(system, child.ID) {
		t.Fatalf("child identity missing from system prompt: %s", system)
	}
	var handoff, parentTurn, childTurn, summary bool
	for i, m := range msgs {
		switch {
		case strings.Contains(m.Content, "[current handoff task]"):
			handoff = m.Role == "user" && strings.Contains(m.Content, "只核对引用并报告发现的问题")
			if i != len(msgs)-1 {
				t.Fatalf("handoff must be the final context input: index=%d len=%d", i, len(msgs))
			}
		case strings.Contains(m.Content, "[sender 整理员"):
			parentTurn = m.Role == "user"
		case strings.Contains(m.Content, "校对员此前的自身回合"):
			childTurn = m.Role == "assistant"
		case strings.Contains(m.Content, "父任务总结：材料已整理"):
			summary = true
		}
	}
	if !handoff || !parentTurn || !childTurn || !summary {
		t.Fatalf("context shape handoff=%v parentTurn=%v childTurn=%v summary=%v msgs=%#v", handoff, parentTurn, childTurn, summary, msgs)
	}
	used := 0
	for _, m := range msgs {
		used += len([]rune(m.Content))
	}
	if used > maxHistoryRunes {
		t.Fatalf("context exceeds history budget: %d > %d", used, maxHistoryRunes)
	}
}

func TestBuildContextRetryChildRetainsOriginalHandoff(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parent, err := s.CreateBot("parent", "coordinate", "model")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateBot("child", "check", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("g", []string{parent.ID, child.ID})
	if err != nil {
		t.Fatal(err)
	}
	parentRun, err := s.AddRun(c.ID, parent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parentRun.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, childRun, err := s.AddHandoff(c.ID, parent.ID, child.ID, parentRun.ID, "核对引用，不要重新分派原始请求")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(childRun.ID, "failed", "test failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parentRun.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryRun(childRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := (&Server{store: s}).buildContext(c, retry, child)
	var found bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "[current handoff task]") {
			found = m.Role == "user" && strings.Contains(m.Content, "核对引用，不要重新分派原始请求")
		}
	}
	if !found {
		t.Fatalf("retry lost original handoff boundary: %#v", msgs)
	}
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "[current handoff task]") {
		t.Fatalf("retry handoff must remain the final context input: %#v", msgs)
	}
	used := 0
	for _, m := range msgs {
		used += len([]rune(m.Content))
	}
	if used > maxHistoryRunes {
		t.Fatalf("retry context exceeds history budget: %d > %d", used, maxHistoryRunes)
	}
}
