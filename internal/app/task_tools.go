package app

// The Bot's tools for tasks and waits (docs/decisions/0059-tasks-and-waits.md):
// delegate gives a task, wait_for starts a wait and returns a receipt, cancel_wait
// ends one, task_update changes a task and task_ask asks the member that gave
// it. handoff and message stay one version as aliases of delegate.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	delegateToolDescription = "Give a task to one or more members. Each gets a task record: your brief, a version number and an optional checklist. The call returns a receipt at once and starts the work. The result returns later as an event that wakes you, so do not poll. Write the brief so that it stands alone: the goal, the form of the result, the tools and sources to use, and the limits. Add a checklist only for work with separate parts. Make one call per turn."

	waitForToolDescription = "Start a wait and get a receipt at once. The wait ends when its event happens or its deadline passes; then you are woken with the result, and a timeout is a result too. After the receipt, tell the person what they need and end your turn. To wait for several events together, repeat the call with the group id from the receipt: mode all ends when every member has ended, mode any at the first fulfilled member. A wait that would close a cycle is refused."

	cancelWaitToolDescription = "End a wait you no longer need, for example when its event came another way or the work changed. A wait you end does not wake you."

	taskUpdateToolDescription = "Change a task you gave or hold. The member that gave the task may change its request (the version goes up and the holder is told), add or remove checklist items, answer a question about the task, accept it as done, or cancel it. The member that holds the task may report its status, and set the status and the reason of each checklist item; it may not add or remove items. Work on one item at a time, and end each item as done, skipped or blocked with a reason."

	taskAskToolDescription = "Ask the member that gave you a task a question about it. The task becomes input_required, and the question wakes that member after your turn ends. End your turn after you ask; you wake when the answer arrives. When the person gave the task, ask the person with the question tool."

	answerChecklistOpenReminder = "Your answer says the work is done, but checklist items of your task are still open. End each open item as done, skipped or blocked with a reason, using task_update, then answer. Keep this check private; the person has not seen the draft."
)

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func (s *Server) taskWaitTools(c Conversation, r Run) []Tool {
	if (c.Kind != "group" && c.Kind != "dm") || r.Kind == runKindTriage {
		return nil
	}
	return []Tool{s.delegateTool(c, r), s.waitForTool(c, r), s.cancelWaitTool(r), s.taskUpdateTool(r), s.taskAskTool(r)}
}

func receiptJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func (s *Server) delegateTool(c Conversation, r Run) Tool {
	return Tool{Name: "delegate", Description: delegateToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"bot_id":    stringProp("Exact id of the member."),
			"bot_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact ids of the members, when more than one."},
			"task":      stringProp(visibleDelegationText),
			"title":     stringProp("A short name for the task."),
			"checklist": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The items of the task's checklist, in order."},
		}, []string{"task"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				BotID     string   `json:"bot_id"`
				BotIDs    []string `json:"bot_ids"`
				Task      string   `json:"task"`
				Title     string   `json:"title"`
				Checklist []string `json:"checklist"`
			}
			if json.Unmarshal(raw, &in) != nil {
				return "", errors.New("invalid delegate arguments")
			}
			in.Task = strings.TrimSpace(in.Task)
			var recipients []string
			seen := map[string]bool{}
			for _, id := range append([]string{in.BotID}, in.BotIDs...) {
				if id = strings.TrimSpace(id); id != "" && !seen[id] {
					seen[id] = true
					recipients = append(recipients, id)
				}
			}
			if len(recipients) == 0 || in.Task == "" {
				return "", errors.New("bot_id or bot_ids and task are required")
			}
			if len(in.Checklist) > maxChecklistItems {
				return "", taskRefusal("checklist_too_long", fmt.Sprintf("A checklist holds at most %d items.", maxChecklistItems))
			}
			for _, item := range in.Checklist {
				if t := strings.TrimSpace(item); t == "" || utf8.RuneCountInString(t) > maxChecklistTextRunes {
					return "", taskRefusal("checklist_item_invalid", fmt.Sprintf("Each item needs 1-%d characters.", maxChecklistTextRunes))
				}
			}
			var out string
			var runs []Run
			var err error
			if len(recipients) == 1 {
				args, _ := json.Marshal(map[string]string{"bot_id": recipients[0], "task": in.Task})
				out, runs, err = s.execHandoff(ctx, c, r, args)
			} else {
				args, _ := json.Marshal(map[string]any{"bot_ids": recipients, "content": in.Task})
				out, runs, err = s.execMessage(ctx, c, r, args)
			}
			if err != nil {
				return "", err
			}
			tasks, err := s.store.shapeDelegatedTasks(runs, in.Title, in.Checklist)
			if err != nil {
				return "", err
			}
			receipt := map[string]any{"tasks": tasks}
			if len(runs) == 1 && out != runs[0].ID {
				receipt["note"] = out
			}
			return receiptJSON(receipt)
		}}
}

type delegatedTaskReceipt struct {
	TaskID  string `json:"task_id"`
	BotID   string `json:"bot_id"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	RunID   string `json:"run_id"`
}

// shapeDelegatedTasks gives the tasks the delegation made their name and
// checklist, and reports them as receipts.
func (s *Store) shapeDelegatedTasks(runs []Run, title string, checklist []string) ([]delegatedTaskReceipt, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > 240 {
		title = string([]rune(title)[:240])
	}
	receipts := []delegatedTaskReceipt{}
	for _, run := range runs {
		var id string
		if err = tx.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, run.ID).Scan(&id); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return nil, err
		}
		if title != "" {
			if _, err = tx.Exec(`UPDATE work_items SET title=? WHERE id=?`, title, id); err != nil {
				return nil, err
			}
		}
		if len(checklist) > 0 {
			if err = addChecklistItemsTx(tx, id, checklist); err != nil {
				return nil, err
			}
		}
		if err = taskEventTx(tx, id); err != nil {
			return nil, err
		}
		item, err := getTaskTx(tx, id)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, delegatedTaskReceipt{TaskID: id, BotID: item.BotID, Status: item.Status, Version: item.SpecVersion, RunID: run.ID})
	}
	return receipts, tx.Commit()
}

func (s *Server) waitForTool(c Conversation, r Run) Tool {
	return Tool{Name: "wait_for", Description: waitForToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"kind":    map[string]any{"type": "string", "enum": []string{waitBotPost, waitTaskResult, waitTimer}, "description": "What the wait is on: the next post of a member, the end of a task, or a set time."},
			"target":  stringProp("The member id for bot_post; the task id for task_result."),
			"seconds": map[string]any{"type": "integer", "description": "For a timer, how long to wait. For other kinds, an optional limit in place of the default."},
			"group":   stringProp("The group id of an earlier receipt, to add this wait to that group."),
			"mode":    map[string]any{"type": "string", "enum": []string{waitModeAny, waitModeAll}, "description": "The mode of a new group; it is fixed by its first wait."},
			"note":    stringProp("Your own label for the wait."),
		}, []string{"kind"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				Kind    string `json:"kind"`
				Target  string `json:"target"`
				Seconds int    `json:"seconds"`
				Group   string `json:"group"`
				Mode    string `json:"mode"`
				Note    string `json:"note"`
			}
			if json.Unmarshal(raw, &in) != nil {
				return "", errors.New("invalid wait_for arguments")
			}
			spec := WaitSpec{BotID: r.BotID, RunID: r.ID, ConversationID: c.ID, Kind: in.Kind, Target: strings.TrimSpace(in.Target), GroupID: strings.TrimSpace(in.Group), Mode: in.Mode, Note: in.Note}
			switch in.Kind {
			case waitBotPost:
				if c.Kind != "group" {
					return "", waitRefusal("wait_kind_unavailable", "A wait on a post from a member is for a group conversation.")
				}
				member := false
				for _, id := range c.BotIDs {
					member = member || id == spec.Target
				}
				if !member || spec.Target == r.BotID {
					return "", waitRefusal("wait_target_invalid", "A wait on a post needs the id of another member of this group.")
				}
				spec.Lineage = lineageKey(s.store.db, r.ID)
			case waitTaskResult:
				item, err := getTaskTx(s.store.db, spec.Target)
				if err != nil {
					return "", waitRefusal("task_not_found", "No task has that id.")
				}
				if item.AssignerBotID != r.BotID && item.ConversationID != c.ID && item.WorkConversationID != c.ID {
					return "", waitRefusal("task_not_visible", "You can wait only on a task you gave or one in this conversation.")
				}
				if isClosedWorkStatus(item.Status) {
					return "", waitRefusal("task_already_ended", "That task has ended with status "+item.Status+". Read its result instead of waiting.")
				}
			case waitTimer:
				if in.Seconds < minTimerSeconds || in.Seconds > maxTimerSeconds {
					return "", waitRefusal("wait_timer_range", fmt.Sprintf("A timer waits %d seconds to 7 days.", minTimerSeconds))
				}
			case waitCommandExit:
				return "", waitRefusal("wait_kind_unavailable", "A wait on a command's end is not available yet.")
			default:
				return "", waitRefusal("wait_kind_invalid", "A wait is on a post from a member, a task's result or a timer.")
			}
			if in.Seconds > 0 {
				if in.Seconds > maxTimerSeconds {
					in.Seconds = maxTimerSeconds
				}
				spec.Timeout = time.Duration(in.Seconds) * time.Second
			} else if s.waitTimeout > 0 && in.Kind == waitBotPost {
				spec.Timeout = s.waitTimeout
			}
			w, err := s.store.registerWait(spec)
			if err != nil {
				return "", err
			}
			return receiptJSON(map[string]any{"wait_id": w.ID, "group_id": w.GroupID, "mode": w.Mode, "kind": w.Kind, "target": w.Target, "status": w.Status, "ends_at": w.DeadlineAt})
		}}
}

func (s *Server) cancelWaitTool(r Run) Tool {
	return Tool{Name: "cancel_wait", Description: cancelWaitToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{"wait_id": stringProp("The id of the wait.")}, []string{"wait_id"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				WaitID string `json:"wait_id"`
			}
			if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.WaitID) == "" {
				return "", errors.New("wait_id is required")
			}
			w, err := s.store.cancelWait(r.BotID, strings.TrimSpace(in.WaitID))
			if err != nil {
				return "", err
			}
			s.flushInbox()
			return receiptJSON(map[string]any{"wait_id": w.ID, "status": w.Status})
		}}
}

func (s *Server) taskUpdateTool(r Run) Tool {
	return Tool{Name: "task_update", Description: taskUpdateToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"task_id":      stringProp("The id of the task."),
			"status":       map[string]any{"type": "string", "enum": []string{"in_progress", "blocked", "done", "cancelled"}, "description": "The holder reports in_progress or blocked. The giver accepts the task as done or cancels it."},
			"reason":       stringProp("The reason for a blocked status."),
			"brief":        stringProp("The giver's new request. The version goes up."),
			"answer":       stringProp("The giver's answer to a question about the task."),
			"add_items":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The giver adds these checklist items."},
			"remove_items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The giver removes the checklist items with these ids."},
			"items": map[string]any{"type": "array", "description": "The holder sets the status and reason of checklist items.", "items": objectSchema(map[string]any{
				"id":     map[string]any{"type": "string"},
				"status": map[string]any{"type": "string", "enum": []string{checklistOpen, checklistDone, checklistSkipped, checklistBlocked}},
				"reason": map[string]any{"type": "string"},
			}, []string{"id", "status"})},
		}, []string{"task_id"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				TaskID      string            `json:"task_id"`
				Status      string            `json:"status"`
				Reason      string            `json:"reason"`
				Brief       *string           `json:"brief"`
				Answer      string            `json:"answer"`
				AddItems    []string          `json:"add_items"`
				RemoveItems []string          `json:"remove_items"`
				Items       []ChecklistChange `json:"items"`
			}
			if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.TaskID) == "" {
				return "", errors.New("task_id is required")
			}
			item, err := s.store.updateTask(r.BotID, TaskUpdate{TaskID: strings.TrimSpace(in.TaskID), Status: in.Status, Brief: in.Brief, AddItems: in.AddItems, RemoveItems: in.RemoveItems, Items: in.Items, Reason: in.Reason, Answer: strings.TrimSpace(in.Answer)})
			if err != nil {
				return "", err
			}
			s.flushInbox()
			return receiptJSON(map[string]any{"task_id": item.ID, "status": item.Status, "version": item.SpecVersion, "checklist": item.Checklist})
		}}
}

func (s *Server) taskAskTool(r Run) Tool {
	return Tool{Name: "task_ask", Description: taskAskToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"task_id":  stringProp("The id of the task."),
			"question": stringProp("The question."),
		}, []string{"task_id", "question"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				TaskID   string `json:"task_id"`
				Question string `json:"question"`
			}
			if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.TaskID) == "" {
				return "", errors.New("task_id and question are required")
			}
			item, err := s.store.askTask(r.BotID, r.ID, strings.TrimSpace(in.TaskID), in.Question)
			if err != nil {
				return "", err
			}
			return receiptJSON(map[string]any{"task_id": item.ID, "status": item.Status, "note": "The question is stored. End your turn; you wake when the answer arrives."})
		}}
}

// openChecklistForSubmission counts the checklist items that are still open on
// the task an answer would complete: the task named by task_id, or the task the
// run works on.
func (s *Store) openChecklistForSubmission(runID, botID, taskID string) int {
	if s == nil || s.db == nil {
		return 0
	}
	if taskID == "" {
		item, ok, err := taskForRunTx(s.db, runID)
		if err != nil || !ok {
			return 0
		}
		return openChecklistCount(s.db, item.ID)
	}
	item, err := getTaskTx(s.db, taskID)
	if err != nil || item.BotID != botID {
		return 0
	}
	return openChecklistCount(s.db, item.ID)
}

// submitTaskForRun binds a run's answer to a task as that task's result.
func (s *Store) submitTaskForRun(runID, botID, taskID string) error {
	item, err := getTaskTx(s.db, taskID)
	if err == sql.ErrNoRows {
		return taskRefusal("task_not_found", "No task has that id.")
	}
	if err != nil {
		return err
	}
	if item.BotID != botID {
		return taskRefusal("task_not_held", "You can submit a result only for a task you hold.")
	}
	if isClosedWorkStatus(item.Status) {
		return taskRefusal("task_closed", "The task is closed.")
	}
	_, err = s.db.Exec(`UPDATE work_items SET submit_run_id=? WHERE id=?`, runID, item.ID)
	return err
}

func (s *Store) noteSayOutcome(runID, outcome string) {
	if outcome == "" {
		return
	}
	if _, err := s.db.Exec(`UPDATE run_say_answers SET outcome=? WHERE run_id=?`, outcome, runID); err != nil {
		logTaskError("say outcome", err)
	}
}

// botHoldsOpenTask reports whether the Bot holds a task from another member or
// the person; only then does its answer offer task_id.
func (s *Store) botHoldsOpenTask(botID string) bool {
	if s == nil || s.db == nil {
		return false
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM work_items WHERE bot_id=? AND assigner_kind IN ('bot','user') AND kind='task' AND status IN (`+openTaskStatuses+`)`, botID).Scan(&n)
	return n > 0
}
