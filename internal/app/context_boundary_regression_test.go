package app

import (
	"strings"
	"testing"
)

func TestBuildContextDoesNotUseSummaryCreatedAfterTriggerBoundary(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.CreateBot("bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	message, run, _, err := s.AddUserRun(c.ID, b.ID, "trigger task", "boundary-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSummary(c.ID, message.Seq, "summary before trigger"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AddMessage(c.ID, "user", "", "", "later user message", "later-client"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSummary(c.ID, message.Seq+1, "late summary must not leak"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := (&Server{store: s}).buildContext(c, run, b)
	var sawOld, sawLate bool
	for _, msg := range msgs {
		sawOld = sawOld || strings.Contains(msg.Content, "summary before trigger")
		sawLate = sawLate || strings.Contains(msg.Content, "late summary must not leak")
	}
	if !sawOld || sawLate {
		t.Fatalf("summary boundary old=%v late=%v messages=%#v", sawOld, sawLate, msgs)
	}
}

func TestBuildContextHandoffIncludesParentFinalExcludesFutureAndKeepsOwnVoice(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parent, err := s.CreateBot("parent", "coordinate", "model")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateBot("child", "execute assigned task", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("team", []string{parent.ID, child.ID})
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
	if _, _, err = s.AddMessage(c.ID, "user", "", parentRun.ID, "original user task", ""); err != nil {
		t.Fatal(err)
	}
	_, childRun, err := s.AddHandoff(c.ID, parent.ID, child.ID, parentRun.ID, "specific child assignment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAssistant(c.ID, child.ID, childRun.ID, "own assistant answer"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAssistant(c.ID, parent.ID, parentRun.ID, "parent final answer"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AddMessage(c.ID, "user", "", "", "future unrelated message", "future-client"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := (&Server{store: s}).buildContext(c, childRun, child)
	var handoff, parentFinal, future, ownPrefix bool
	for _, msg := range msgs {
		handoff = handoff || strings.Contains(msg.Content, "[current handoff task]") && strings.Contains(msg.Content, "specific child assignment")
		parentFinal = parentFinal || strings.Contains(msg.Content, "parent final answer")
		future = future || strings.Contains(msg.Content, "future unrelated message")
		ownPrefix = ownPrefix || strings.Contains(msg.Content, "[sender "+child.Name+" id="+child.ID+"]")
	}
	if !handoff || !parentFinal || future || ownPrefix {
		t.Fatalf("handoff=%v parentFinal=%v future=%v ownPrefix=%v messages=%#v", handoff, parentFinal, future, ownPrefix, msgs)
	}
}
