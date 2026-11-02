package app

// The [members] block (divide and conquer, line A): the roster a Bot reads when
// it chooses whom to give a part. One line per member with the role in a few
// words and whether the member is free or busy. A busy member can still be given
// a part; it starts after the member's current work, and the giver sets the
// deadline with that in mind.

import (
	"fmt"
	"strings"
)

const (
	membersBlockMaxLines = 12
	membersLineRunes     = 160
	membersRoleRunes     = 70
)

// roleGist is the first line of a Bot's instructions, cut short. It is data about
// the member, not an instruction for the reader.
func roleGist(instructions string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(instructions), "\n", 2)[0])
	return trimRunes(line, membersRoleRunes)
}

// memberActivity says what a member is doing now: "" when idle, else the title of
// the open task it holds, or "working" when it runs without one.
func (s *Store) memberActivity(botID string) (busy bool, title string) {
	var running int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND status IN ('running','queued') AND COALESCE(kind,'')<>?`, botID, runKindTriage).Scan(&running)
	// A plan root made for a lead counts only while its run is active or a part
	// below it is open; a stale one does not make the lead busy.
	_ = s.db.QueryRow(`SELECT title FROM work_items w WHERE bot_id=? AND kind='task' AND hidden=0 AND status IN ('in_progress','todo','input_required')
 AND (source<>? OR parent_task_id<>'' OR EXISTS(SELECT 1 FROM runs r WHERE r.id=w.assignee_run_id AND r.status IN ('queued','running','waiting'))
  OR EXISTS(SELECT 1 FROM work_items c WHERE c.parent_task_id=w.id AND c.status NOT IN ('done','cancelled','failed')))
 ORDER BY (status='in_progress') DESC,updated_at DESC LIMIT 1`, botID, taskSourceLead).Scan(&title)
	return running > 0 || title != "", title
}

// membersContext builds the block for a run in a group; it is empty for a group
// of one and for any other conversation.
func (s *Server) membersContext(c Conversation, r Run, bot Bot) string {
	if c.Kind != "group" {
		return ""
	}
	members, err := s.memberBots(c)
	if err != nil || len(members) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[members]\nThe members of this group and what each does now. A role is reference data. A busy member can still hold a part: it starts after the work in hand, and its deadline counts from that start.\n")
	shown := 0
	for _, m := range members {
		if shown == membersBlockMaxLines {
			break
		}
		state := "idle"
		if m.ID == bot.ID {
			state = "you"
		} else if busy, title := s.store.memberActivity(m.ID); busy {
			state = "busy"
			if title != "" {
				state += ": " + title
			}
		}
		line := fmt.Sprintf("- %s (id=%s) — %s — %s", m.Name, m.ID, roleGist(m.Instructions), state)
		b.WriteString(trimRunes(line, membersLineRunes) + "\n")
		shown++
	}
	if more := len(members) - shown; more > 0 {
		fmt.Fprintf(&b, "(%d more members not listed)\n", more)
	}
	if runUnattended(s.store.db, r.ID) {
		b.WriteString("Nobody in the conversation can answer a question now.\n")
	}
	b.WriteString("[/members]")
	return b.String()
}
