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
func triageSystemPrompt(members []Bot, tasks []triageTask) string {
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
	var open strings.Builder
	if len(tasks) > 0 {
		open.WriteString("Unfinished tasks of the group, with the member that holds each:\n")
		for _, task := range tasks {
			fmt.Fprintf(&open, "- %s [%s] held by %s (%s)\n", task.Title, task.Status, task.HolderName, task.HolderID)
		}
	}
	return "You route one group message to one member. Read the member list and the recent conversation. " +
		"Choose the member whose role description best fits the work in the latest message. " +
		"If the latest message continues a topic that one member already handles with the person, choose that member. " +
		"If the latest message continues an unfinished task, choose the member that holds it. " +
		"If no role fits, choose the primary assistant when the list has one, otherwise the member with the most general role. " +
		"Never answer the message. Never choose a name or id that is not in the list. " +
		"Reply with the exact id of the chosen member, or JSON {\"bot_id\":\"...\",\"brief\":\"...\"}. " +
		"The brief is optional: one sentence in the language of the latest message that restates what the person wants, with every reference in the latest message resolved from the recent conversation. Leave it out when the message already stands alone.\nMembers:\n" + roster.String() + open.String()
}

const (
	maxTriageBriefRunes = 200
	maxTriageTasks      = 12
	maxTriageTaskTitles = 80
)

// triageTask is one unfinished task of the group as the routing worker reads it.
type triageTask struct{ Title, Status, HolderID, HolderName string }

// openTasksForTriage lists the group's unfinished tasks, newest first, bounded
// in count and in the length of each title.
func (s *Store) openTasksForTriage(conversationID string) []triageTask {
	rows, err := s.db.Query(`SELECT w.title,w.status,w.bot_id,b.name FROM work_items w JOIN bots b ON b.id=w.bot_id
 WHERE w.conversation_id=? AND w.kind='task' AND w.status IN (`+openTaskStatuses+`) AND b.archived=0 ORDER BY w.updated_at DESC,w.id LIMIT ?`, conversationID, maxTriageTasks)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []triageTask
	for rows.Next() {
		var t triageTask
		if rows.Scan(&t.Title, &t.Status, &t.HolderID, &t.HolderName) == nil {
			t.Title = trimRunes(strings.Join(strings.Fields(t.Title), " "), maxTriageTaskTitles)
			out = append(out, t)
		}
	}
	return out
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

// storeTriageBrief keeps the routing worker's one-line brief as the title of
// the task the chosen member holds. The person's own words stay in the task
// body and in the conversation history the member reads.
func (s *Store) storeTriageBrief(runID, brief string) {
	if brief == "" {
		return
	}
	_, _ = s.db.Exec(`UPDATE work_items SET title=? WHERE assignee_run_id=? AND assigner_kind='user' AND kind='task'`, firstLine(brief, 120), runID)
}
