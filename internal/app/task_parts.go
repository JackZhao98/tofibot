package app

// delegate with parts (divide and conquer, line A): one call gives several
// different tasks to several members. One transaction makes the tasks and the
// one visible message of the group; the parts share one wait group (mode all)
// that the receipt names, so the giver ends its turn with a pending answer and
// wakes when the parts end or their deadlines pass.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// delegatePartInput is one entry of the parts array of delegate.
type delegatePartInput struct {
	BotID         string   `json:"bot_id"`
	Task          string   `json:"task"`
	Title         string   `json:"title"`
	Checklist     []string `json:"checklist"`
	DependsOn     []string `json:"depends_on"`
	AttachmentIDs []string `json:"attachment_ids"`
	ResultFormat  string   `json:"result_format"`
	DeadlineSecs  int      `json:"deadline_seconds"`
	Limits        string   `json:"limits"`
}

// checklistParamText is the description of the checklist parameter. It holds the
// rule for when a checklist is right; the parameter text is outside the cap of
// the tool description.
func checklistParamText(what string) string {
	return "The items of the " + what + "'s checklist, in order. Add items only for work that has separate parts; a small piece of work needs none."
}

func delegatePartsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": maxDelegateParts,
		"description": "Several different tasks in one call, each for one member. Use it in place of bot_id and task. The tasks start together, and the receipt holds one wait on all of them.",
		"items": objectSchema(map[string]any{
			"bot_id":           stringProp("Exact id of the member that holds this part."),
			"task":             stringProp(delegationBriefText),
			"title":            stringProp("A short name for the part. The group reads it in your message."),
			"checklist":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": checklistParamText("part")},
			"depends_on":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ids of tasks in the same tree that must end done before this part starts."},
			"attachment_ids":   attachmentIDsProp,
			"result_format":    stringProp("The form of the result that you need back."),
			"deadline_seconds": map[string]any{"type": "integer", "description": "How long the holder has, in seconds."},
			"limits":           stringProp("What the holder must not do or touch, and its budget."),
		}, []string{"bot_id", "task"})}
}

// delegateParts runs a delegate call that has parts.
func (s *Server) delegateParts(c Conversation, r Run, in []delegatePartInput) (string, error) {
	if c.Kind != "group" && c.Kind != "dm" {
		return "", errors.New("delegate requires a conversation")
	}
	if len(in) > maxDelegateParts {
		return "", taskRefusal("task_fanout_width", fmt.Sprintf("One call gives at most %d parts.", maxDelegateParts))
	}
	defaultDeadline := defaultPartDeadlineSeconds(s.store.db, r.ID)
	parts := make([]delegatePart, 0, len(in))
	var recipients []string
	for _, p := range in {
		p.BotID, p.Task = strings.TrimSpace(p.BotID), strings.TrimSpace(p.Task)
		if p.BotID == "" || p.Task == "" {
			return "", taskRefusal("part_invalid", "Each part needs a bot_id and a task.")
		}
		if len(p.Checklist) > maxChecklistItems {
			return "", taskRefusal("checklist_too_long", fmt.Sprintf("A checklist holds at most %d items.", maxChecklistItems))
		}
		for _, item := range p.Checklist {
			if t := strings.TrimSpace(item); t == "" || utf8.RuneCountInString(t) > maxChecklistTextRunes {
				return "", taskRefusal("checklist_item_invalid", fmt.Sprintf("Each item needs 1-%d characters.", maxChecklistTextRunes))
			}
		}
		contract, err := cleanTaskContract(taskContract{ResultFormat: p.ResultFormat, Limits: p.Limits, DeadlineSeconds: p.DeadlineSecs})
		if err != nil {
			return "", err
		}
		if contract.DeadlineSeconds == 0 {
			contract.DeadlineSeconds = defaultDeadline
		}
		attachments, err := s.store.checkDelegateAttachments(c.ID, p.AttachmentIDs)
		if err != nil {
			return "", err
		}
		parts = append(parts, delegatePart{Bot: p.BotID, Brief: p.Task, Title: p.Title, Checklist: p.Checklist, Deps: p.DependsOn, Attachments: attachments, Contract: contract})
		recipients = append(recipients, p.BotID)
	}
	receipts, err := s.store.AddTasks(c.ID, r.BotID, r.ID, parts, s.store.sameGroupRecipients(c.ID, recipients))
	if err != nil {
		return "", err
	}
	s.flushInbox()
	group := ""
	if len(receipts) > 0 {
		_ = s.store.db.QueryRow(`SELECT group_id FROM waits WHERE kind=? AND target=? AND bot_id=?`, waitTaskResult, receipts[0].TaskID, r.BotID).Scan(&group)
	}
	return receiptJSON(map[string]any{"tasks": receipts, "group_id": group, "note": "The parts start together. The receipt holds one wait on all of them: end your turn with a pending answer, and you wake when every part has ended or a deadline has passed."})
}

// insertPartsMessageTx posts the one visible message of a delegation with parts,
// as the giver's own message in the group. It triggers nothing: the tasks carry
// the work. The caller commits.
func insertPartsMessageTx(tx *sql.Tx, conv, sender, runID, content string, attachmentIDs []string) (string, error) {
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return "", err
	}
	t := now()
	m := Message{ID: uuid.NewString(), ConversationID: conv, Seq: seq, Role: "assistant", SenderBotID: sender, RunID: runID, Content: content, CreatedAt: t}
	if err := insertVisibleDelegationTx(tx, m); err != nil {
		return "", err
	}
	if err := bindDeliveredTx(tx, &m, attachmentIDs); err != nil {
		return "", err
	}
	if err := insertEventTx(tx, conv, "message", m, t); err != nil {
		return "", err
	}
	_, err := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv)
	return m.ID, err
}
