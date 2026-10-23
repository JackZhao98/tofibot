package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

// Group triage: a group message that names no one is routed by a short,
// tool-free run (runKindTriage) to the member whose role best fits it. The run
// is queued by the ingress transaction and executes on the background model;
// nothing in ingress waits for it. Every failure path hands the message to the
// member the older rule picks, so a message is never left without an answer.

// triageSystemPrompt is the routing worker's whole system prompt. It states
// principles only; the member list is the sole variable part.
func triageSystemPrompt(members []Bot) string {
	var roster strings.Builder
	for _, member := range members {
		role := trimRunes(strings.Join(strings.Fields(member.Instructions), " "), 600)
		if role == "" {
			role = "no role description"
		}
		mark := ""
		if member.IsPrimary {
			mark = " [primary assistant]"
		}
		fmt.Fprintf(&roster, "%s (%s)%s: %s\n", member.Name, member.ID, mark, role)
	}
	return "You route one group message to one member. Read the member list and the recent conversation. " +
		"Choose the member whose role description best fits the work in the latest message. " +
		"If the latest message continues a topic that one member already handles with the person, choose that member. " +
		"If no role fits, choose the primary assistant when the list has one, otherwise the member with the most general role. " +
		"Never answer the message. Never choose a name or id that is not in the list. " +
		"Reply with only the exact id of the chosen member, or JSON {\"bot_id\":\"...\"}.\nMembers:\n" + roster.String()
}

// routeAfterFailedTriage hands the message of a failed triage run to the
// member the older rule picks (the Primary Assistant, else a random active
// member). It reports whether the hand-off was made; false leaves the caller to
// fail the run.
func (s *Server) routeAfterFailedTriage(c Conversation, r Run, cause error) bool {
	if cause == nil || errors.Is(cause, context.Canceled) {
		return false
	}
	target, err := s.store.fallbackGroupMember(c.ID)
	if err != nil {
		return false
	}
	_, child, err := s.store.AddHandoff(c.ID, "system", target, r.ID, "")
	if err != nil {
		return false
	}
	log.Printf("[triage] fell back to the default member conv=%s run=%s", c.ID, r.ID)
	if changed, se := s.store.SetRunStatus(r.ID, "done", ""); se == nil && changed {
		if done, de := s.store.GetRun(r.ID); de == nil {
			_, _ = s.store.Event(c.ID, "run", done)
		}
	}
	s.enqueue(c, child)
	return true
}
