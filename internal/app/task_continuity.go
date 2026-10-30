package app

// Task continuity (docs/design/task-continuity.md). A run is evidence; a task is
// what was asked and what is left. When a newer person message takes the Bot
// away from running work (the run yields at its next boundary), the work is
// not lost: the task goes to the Bot's own list for the conversation as paused
// (`todo` with interrupted_at), with a bounded note of what was done. When the
// run that interrupted has ended and nothing of a person's waits, the Bot starts
// the paused task again by itself, at most task_auto_resume_limit times per
// task, and says which task it resumes. A person's queued message always goes
// first. A paused task of a schedule that has a newer occurrence is dropped.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	taskSourceUser     = "user_request"
	taskSourceSchedule = "schedule_occurrence"

	interruptSteered   = "steered"
	interruptEarlyStop = "early_stop"

	// taskAutoResumeLimit caps only work the Bot itself stopped early: a Bot that keeps stopping the same task must not
	// restart it forever. Work a person's message interrupted has no cap; it always resumes once the current work ends.
	// Either way the task stays on the list, so the Bot always knows what it left unfinished.
	taskAutoResumeLimit = 2

	resumeNoticeTask = "task"

	maxResumeStateBytes = 4096
	maxResumeProgress   = 8
	maxResumeTools      = 16
)

// resumeTool is one tool call of the interrupted work, bounded.
type resumeTool struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Args   string `json:"args,omitempty"`
	Result string `json:"result,omitempty"`
}

// resumeState is the progress note of a paused task. The harness writes it from
// records it already has, because the interrupted run has no turn left.
type resumeState struct {
	Goal      string       `json:"goal"`
	Progress  []string     `json:"progress,omitempty"`
	LastTools []resumeTool `json:"last_tools,omitempty"`
	Remaining string       `json:"remaining,omitempty"`
}

func (st resumeState) encode() string {
	for {
		raw, _ := json.Marshal(st)
		if len(raw) <= maxResumeStateBytes {
			return string(raw)
		}
		switch {
		case len(st.LastTools) > 4:
			st.LastTools = st.LastTools[1:]
		case len(st.Progress) > 2:
			st.Progress = st.Progress[1:]
		case len([]rune(st.Goal)) > 400:
			st.Goal = trimRunes(st.Goal, 400)
		case len(st.LastTools) > 0:
			st.LastTools = st.LastTools[1:]
		default:
			st.Progress, st.Remaining = nil, trimRunes(st.Remaining, 200)
			if raw, _ = json.Marshal(st); len(raw) > maxResumeStateBytes {
				st.Goal = trimRunes(st.Goal, 120)
			}
		}
	}
}

// isPersonSteerSuccessorTx is true when the run that took over from old is a
// person's new message run of the same Bot: the interruption case. A delegation
// or wake successor is a hand-over, and a continuation run (it read the messages
// old left unread) carries the work on itself.
func isPersonSteerSuccessorTx(tx *sql.Tx, oldID, successorID string) (bool, error) {
	var bot, kind, parent, trigger, oldBot string
	var role sql.NullString
	err := tx.QueryRow(`SELECT n.bot_id,COALESCE(n.kind,''),COALESCE(n.parent_run_id,''),COALESCE(n.trigger_message_id,''),(SELECT bot_id FROM runs WHERE id=?),
		(SELECT m.role FROM messages m WHERE m.id=n.trigger_message_id) FROM runs n WHERE n.id=?`, oldID, successorID).Scan(&bot, &kind, &parent, &trigger, &oldBot, &role)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bot != oldBot || parent != "" || trigger == "" || role.String != "user" || (kind != "" && kind != runKindGroupChat) {
		return false, nil
	}
	var continued int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM bot_inbox WHERE kind='user_message' AND parent_run=? AND consumed_by_run=?`, oldID, successorID).Scan(&continued); err != nil {
		return false, err
	}
	return continued == 0, nil
}

// pauseForPersonSteerTx pauses the work of a run that yields to a person's new
// message, when every run it yields to is such a message run. A hand-over to
// another member or a continuation of the same message is not an interruption.
func pauseForPersonSteerTx(tx *sql.Tx, oldID string) error {
	rows, err := tx.Query(`SELECT link.new_run_id FROM run_steering link JOIN runs n ON n.id=link.new_run_id WHERE link.old_run_id=? AND n.status IN ('queued','running')`, oldID)
	if err != nil {
		return err
	}
	var successors []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		successors = append(successors, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(successors) == 0 {
		return err
	}
	for _, id := range successors {
		person, err := isPersonSteerSuccessorTx(tx, oldID, id)
		if err != nil || !person {
			return err
		}
	}
	_, err = pauseTaskForRunTx(tx, oldID, interruptSteered)
	return err
}

// pauseTaskForRunTx records the work of a run that was interrupted as a paused
// task of its Bot and returns the task id ("" when the run is not work of this
// kind). It runs in the transaction that ends the run.
func pauseTaskForRunTx(tx *sql.Tx, runID, reason string) (string, error) {
	return holdTaskForRunTx(tx, runID, reason, "todo")
}

// holdTaskForRunTx is pauseTaskForRunTx for either way a run leaves work
// unfinished: "todo" pauses it (the Bot starts it again), "blocked" records the
// obstacle the Bot reported and waits for the person.
func holdTaskForRunTx(tx *sql.Tx, runID, reason, status string) (string, error) {
	var conv, bot, kind, origin, trigger, parent string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id,COALESCE(kind,''),COALESCE(origin_conversation_id,''),COALESCE(trigger_message_id,''),COALESCE(parent_run_id,'') FROM runs WHERE id=?`, runID).Scan(&conv, &bot, &kind, &origin, &trigger, &parent); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	source := ""
	switch {
	case kind == runKindSchedule:
		source = taskSourceSchedule
	case (kind == "" || kind == runKindGroupChat) && (origin == "" || origin == conv):
		source = taskSourceUser
	default:
		return "", nil
	}
	if parent != "" {
		var parentBot string
		if tx.QueryRow(`SELECT bot_id FROM runs WHERE id=?`, parent).Scan(&parentBot) == nil && parentBot != bot {
			return "", nil
		}
	}
	var goal string
	if trigger != "" {
		_ = tx.QueryRow(`SELECT content FROM messages WHERE id=?`, trigger).Scan(&goal)
	}
	t := now()
	var taskID, assigner, prior string
	err := tx.QueryRow(`SELECT id,assigner_kind,resume_state FROM work_items WHERE assignee_run_id=? AND status NOT IN ('done','cancelled','failed') ORDER BY created_at LIMIT 1`, runID).Scan(&taskID, &assigner, &prior)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil && assigner == "bot" {
		return "", nil
	}
	state := resumeState{Goal: trimRunes(goal, 1500)}
	if prior != "" {
		var old resumeState
		if json.Unmarshal([]byte(prior), &old) == nil {
			state.Progress = old.Progress
			if old.Goal != "" {
				state.Goal = old.Goal
			}
		}
	}
	if err = fillResumeState(tx, runID, &state); err != nil {
		return "", err
	}
	scheduleID := ""
	if source == taskSourceSchedule {
		_ = tx.QueryRow(`SELECT schedule_id FROM schedule_occurrences WHERE run_id=?`, runID).Scan(&scheduleID)
	}
	if taskID == "" {
		title := firstLine(goal, 80)
		if source == taskSourceSchedule {
			var occurrenceTitle string
			if tx.QueryRow(`SELECT title FROM schedule_occurrences WHERE run_id=?`, runID).Scan(&occurrenceTitle) == nil && strings.TrimSpace(occurrenceTitle) != "" {
				title = firstLine(occurrenceTitle, 80)
			}
		}
		if title == "" {
			title = "Task"
		}
		taskID = newID()
		if _, err = tx.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,parent_task_id,origin_run_id,assignee_run_id,work_conversation_id,spec_version)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, taskID, conv, bot, "task", title, trimRunes(goal, maxTaskBriefRunes), "todo", nil, t, t, "",
			"user", "", taskID, "", runID, "", conv, 1); err != nil {
			return "", err
		}
		if _, err = tx.Exec(`INSERT INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, taskID, 1, "person", "", trimRunes(goal, maxTaskBriefRunes), t); err != nil {
			return "", err
		}
	}
	interrupted := t
	if status != "todo" {
		interrupted = ""
	}
	if _, err = tx.Exec(`UPDATE work_items SET status=?,assignee_run_id=CASE WHEN ?='todo' THEN '' ELSE assignee_run_id END,hidden=0,completed_at='',closed_at='',interrupted_at=?,interrupt_reason=?,
 source=?,source_run_id=?,schedule_id=CASE WHEN ?<>'' THEN ? ELSE schedule_id END,resume_state=?,updated_at=? WHERE id=?`,
		status, status, interrupted, reason, source, runID, scheduleID, scheduleID, state.encode(), t, taskID); err != nil {
		return "", err
	}
	return taskID, taskEventTx(tx, taskID)
}

// captureEarlyStopTx records the work of a run that ended its turn with a report
// that it is not finished. A report of pending work, with no wait that will wake
// the Bot, pauses the task so the Bot carries it on; a blocker waits for the
// person. Only a person's request counts: a scheduled run reports through its
// receipt.
func captureEarlyStopTx(tx *sql.Tx, runID, outcome string) error {
	var kind, origin, conv string
	if err := tx.QueryRow(`SELECT COALESCE(kind,''),COALESCE(origin_conversation_id,''),conversation_id FROM runs WHERE id=?`, runID).Scan(&kind, &origin, &conv); err != nil {
		return err
	}
	if kind == runKindSchedule {
		return nil
	}
	switch outcome {
	case sayOutcomePending:
		var waits int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending'`, runID).Scan(&waits); err != nil || waits > 0 {
			return err
		}
		_, err := holdTaskForRunTx(tx, runID, interruptEarlyStop, "todo")
		return err
	case sayOutcomeBlocked:
		_, err := holdTaskForRunTx(tx, runID, interruptEarlyStop, "blocked")
		return err
	}
	return nil
}

// fillResumeState adds what the run did to the note: its own progress messages
// (kept, with the older ones of earlier attempts, to eight), the last tool
// calls, and the person's message it never read.
func fillResumeState(tx *sql.Tx, runID string, st *resumeState) error {
	rows, err := tx.Query(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND kind IN ('','progress') AND TRIM(content)<>'' ORDER BY seq`, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		st.Progress = append(st.Progress, trimRunes(strings.TrimSpace(c), 300))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if n := len(st.Progress); n > maxResumeProgress {
		st.Progress = st.Progress[n-maxResumeProgress:]
	}
	rows, err = tx.Query(`SELECT name,status,arguments,result FROM tool_activities WHERE run_id=? ORDER BY updated_at DESC,call_id DESC LIMIT ?`, runID, maxResumeTools)
	if err != nil {
		return err
	}
	var tools []resumeTool
	for rows.Next() {
		var x resumeTool
		var args, result sql.NullString
		if err = rows.Scan(&x.Name, &x.Status, &args, &result); err != nil {
			rows.Close()
			return err
		}
		x.Args, x.Result = trimRunesMarker(args.String, 120, "…"), trimRunesMarker(result.String, 240, "…")
		tools = append(tools, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := len(tools) - 1; i >= 0; i-- {
		st.LastTools = append(st.LastTools, tools[i])
	}
	var unread string
	if err = tx.QueryRow(`SELECT COALESCE(body,'') FROM bot_inbox WHERE kind='user_message' AND parent_run=? AND consumed_at='' ORDER BY created_at DESC,id DESC LIMIT 1`, runID).Scan(&unread); err != nil && err != sql.ErrNoRows {
		return err
	}
	st.Remaining = trimRunes(unread, 300)
	return nil
}

// resumePausedTasksTx runs in the transaction that ends a run of a Bot. When the
// Bot has nothing else queued or running in the conversation it starts the
// oldest paused task again as a run of its own and returns that run.
func resumePausedTasksTx(tx *sql.Tx, endedID string) (Run, bool, error) {
	var conv, bot string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id FROM runs WHERE id=?`, endedID).Scan(&conv, &bot); err != nil {
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	var busy int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM runs WHERE conversation_id=? AND bot_id=? AND id<>? AND kind<>'triage' AND status IN ('queued','running'))
		+(SELECT COUNT(*) FROM bot_inbox WHERE kind='user_message' AND bot_id=? AND conversation_id=? AND consumed_at='')`, conv, bot, endedID, bot, conv).Scan(&busy); err != nil {
		return Run{}, false, err
	}
	if busy > 0 {
		return Run{}, false, nil
	}
	for {
		var id, source string
		err := tx.QueryRow(`SELECT id,source FROM work_items WHERE bot_id=? AND conversation_id=? AND status='todo' AND interrupted_at<>'' AND (interrupt_reason=? OR resume_count<?) ORDER BY interrupted_at,id LIMIT 1`, bot, conv, interruptSteered, taskAutoResumeLimit).Scan(&id, &source)
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		if err != nil {
			return Run{}, false, err
		}
		if superseded, err := supersededOccurrenceTx(tx, id); err != nil {
			return Run{}, false, err
		} else if superseded {
			t := now()
			if _, err = tx.Exec(`UPDATE work_items SET status='cancelled',interrupted_at='',result_summary='superseded_by_next_occurrence',completed_at=?,closed_at=?,updated_at=? WHERE id=?`, t, t, t, id); err != nil {
				return Run{}, false, err
			}
			if err = taskEventTx(tx, id); err != nil {
				return Run{}, false, err
			}
			continue
		}
		run, err := createResumeRunTx(tx, id, true)
		return run, err == nil, err
	}
}

// supersededOccurrenceTx is true when the paused task belongs to a schedule that
// has claimed a newer occurrence since: two occurrences of one schedule never run
// for the same stretch of time.
func supersededOccurrenceTx(tx *sql.Tx, taskID string) (bool, error) {
	var source, sourceRun, scheduleID string
	if err := tx.QueryRow(`SELECT source,source_run_id,schedule_id FROM work_items WHERE id=?`, taskID).Scan(&source, &sourceRun, &scheduleID); err != nil {
		return false, err
	}
	if source != taskSourceSchedule || scheduleID == "" {
		return false, nil
	}
	var newer int
	err := tx.QueryRow(`WITH RECURSIVE anc(id) AS (SELECT ? UNION SELECT r.parent_run_id FROM runs r JOIN anc a ON r.id=a.id WHERE COALESCE(r.parent_run_id,'')<>'')
		SELECT COUNT(*) FROM schedule_occurrences n WHERE n.schedule_id=? AND n.scheduled_for_utc>(SELECT MAX(o.scheduled_for_utc) FROM schedule_occurrences o WHERE o.schedule_id=? AND o.run_id IN (SELECT id FROM anc))`, sourceRun, scheduleID, scheduleID).Scan(&newer)
	return newer > 0, err
}

// createResumeRunTx makes the queued run that carries a paused task on. The run
// is a child of the interrupted one, so a scheduled occurrence reports through
// its family, and it is announced with the task title. counted says the start is
// an automatic one that uses up the task's allowance.
func createResumeRunTx(tx *sql.Tx, taskID string, counted bool) (Run, error) {
	var conv, bot, source, sourceRun, state string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id,source,source_run_id,resume_state FROM work_items WHERE id=? AND status='todo' AND interrupted_at<>''`, taskID).Scan(&conv, &bot, &source, &sourceRun, &state); err != nil {
		return Run{}, err
	}
	var kind, trigger, model string
	if err := tx.QueryRow(`SELECT COALESCE(kind,''),COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, sourceRun).Scan(&kind, &trigger); err != nil {
		return Run{}, err
	}
	if err := tx.QueryRow(`SELECT model FROM bots WHERE id=?`, bot).Scan(&model); err != nil {
		return Run{}, err
	}
	// A person's request is answered with the whole conversation in view, the
	// messages that came in between included; a scheduled occurrence keeps its
	// own instruction as the request.
	if source == taskSourceUser {
		var latest string
		if tx.QueryRow(`SELECT id FROM messages WHERE conversation_id=? AND role='user' AND kind='' ORDER BY seq DESC LIMIT 1`, conv).Scan(&latest) == nil && latest != "" {
			trigger = latest
		}
	}
	var queueSeq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, conv).Scan(&queueSeq); err != nil {
		return Run{}, err
	}
	t := now()
	run := Run{ID: newID(), ConversationID: conv, BotID: bot, Status: "queued", ParentRunID: sourceRun, Model: model, Kind: kind, OriginConversationID: conv, TriggerMessageID: trigger, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err := tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,resume_notice,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.ConversationID, run.BotID, run.Status, nullString(run.ParentRunID), 0, run.Model, run.Kind, run.OriginConversationID, nullString(run.TriggerMessageID), run.QueueSeq, resumeNoticeTask, t, t); err != nil {
		return Run{}, err
	}
	count := 0
	if counted {
		count = 1
	}
	if _, err := tx.Exec(`UPDATE work_items SET assignee_run_id=?,interrupted_at='',resume_count=resume_count+?,updated_at=? WHERE id=?`, run.ID, count, t, taskID); err != nil {
		return Run{}, err
	}
	if err := taskEventTx(tx, taskID); err != nil {
		return Run{}, err
	}
	if err := insertEventTx(tx, conv, "run", run, t); err != nil {
		return Run{}, err
	}
	return run, nil
}

// ResumePausedTask starts a paused task again because the person asked for it.
func (s *Store) ResumePausedTask(taskID string) (Run, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := createResumeRunTx(tx, taskID, false)
	if err != nil {
		return Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, err
	}
	return run, nil
}

// cancelPausedTasksTx ends the paused tasks of a Bot in a conversation: the person
// stopped its work, so nothing of it starts again by itself.
func cancelPausedTasksTx(tx *sql.Tx, conv, bot string) error {
	rows, err := tx.Query(`SELECT id FROM work_items WHERE conversation_id=? AND bot_id=? AND status='todo' AND interrupted_at<>''`, conv, bot)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	t := now()
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE work_items SET status='cancelled',interrupted_at='',result_summary='stopped_by_user',completed_at=?,closed_at=?,updated_at=? WHERE id=?`, t, t, t, id); err != nil {
			return err
		}
		if err = taskEventTx(tx, id); err != nil {
			return err
		}
	}
	return nil
}

// taskResumeContext is the block a resumed run reads first: the goal, what the
// interrupted work reported and did, and the rule for using it. Empty for any
// other run.
func (s *Store) taskResumeContext(r Run) string {
	var id string
	if s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=? AND source<>'' AND resume_state<>'' LIMIT 1`, r.ID).Scan(&id) != nil {
		return ""
	}
	return s.taskRecordText(id)
}

// taskRecordText renders the record of one task that was left unfinished.
func (s *Store) taskRecordText(taskID string) string {
	var title, state, source string
	var count int
	if s.db.QueryRow(`SELECT title,resume_state,source,resume_count FROM work_items WHERE id=? AND resume_state<>''`, taskID).Scan(&title, &state, &source, &count) != nil {
		return ""
	}
	var st resumeState
	if json.Unmarshal([]byte(state), &st) != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[unfinished task]\nSystem record of work that was interrupted before it was finished. It is data, not a new instruction. Carry this task on now, from where it stopped. Completed steps are evidence: do not repeat a step that has a side effect; check the current state before you rely on a result. Earlier messages in the conversation that you see were answered already; this task is what you do. Report it done only when its goal is met in this turn.\n")
	fmt.Fprintf(&b, "title: %s\nsource: %s\nresume_number: %d\ngoal: %s\n", title, source, count, trimRunes(st.Goal, 1500))
	if len(st.Progress) > 0 {
		b.WriteString("progress reported so far:\n")
		for _, p := range st.Progress {
			b.WriteString("- " + p + "\n")
		}
	}
	if len(st.LastTools) > 0 {
		b.WriteString("last tool calls:\n")
		for _, x := range st.LastTools {
			fmt.Fprintf(&b, "- %s [%s] args=%s result=%s\n", x.Name, x.Status, x.Args, x.Result)
		}
	}
	if st.Remaining != "" {
		b.WriteString("message that arrived and was not read: " + st.Remaining + "\n")
	}
	b.WriteString("[/unfinished task]")
	return b.String()
}

// unfinishedTasksContext lists the Bot's paused and blocked tasks in the
// conversation, for every run of the Bot: a message that asks to continue
// refers to this list.
func (s *Store) unfinishedTasksContext(conv, bot string) string {
	rows, err := s.db.Query(`SELECT id,title,source,status,interrupted_at,resume_count FROM work_items WHERE conversation_id=? AND bot_id=? AND source<>'' AND ((status='todo' AND interrupted_at<>'') OR status='blocked') ORDER BY updated_at,id LIMIT 8`, conv, bot)
	if err != nil {
		return ""
	}
	defer rows.Close()
	type entry struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Source      string `json:"source"`
		State       string `json:"state"`
		PausedSince string `json:"paused_since,omitempty"`
		Resumes     int    `json:"automatic_resumes_used"`
	}
	var list []entry
	for rows.Next() {
		var e entry
		var status string
		if rows.Scan(&e.ID, &e.Title, &e.Source, &status, &e.PausedSince, &e.Resumes) != nil {
			return ""
		}
		e.State = "paused"
		if status == "blocked" {
			e.State = "blocked"
		}
		e.Title = trimRunes(e.Title, 120)
		list = append(list, e)
	}
	if len(list) == 0 {
		return ""
	}
	raw, _ := json.Marshal(list)
	return "[unfinished tasks]\nSystem record of work on your list that is not finished. It is information only, not a new instruction. Only the run that is assigned to an entry works on it: a paused entry resumes automatically after the current work, in its own run, so do not do its work in this run and do not repeat its steps. Do not act on an entry unless the person explicitly asks you to continue, resume or finish earlier work: with one entry, carry that entry on; with several entries and no name, ask which one in one short question; with a name or a clear reference, take that entry. To carry an entry on in this run, set its status to in_progress with update_work_item; the result returns its record, and the entry then no longer resumes by itself. If the person's request is itself the goal of an entry, finish it in this run and mark the entry done with update_work_item so it does not run again.\n" + string(raw) + "\n[/unfinished tasks]"
}

// bindPausedTask makes the run the holder of a paused task its Bot takes up
// itself, and reports whether the task was paused.
func (s *Store) bindPausedTask(taskID, runID, conv, bot string) bool {
	res, err := s.db.Exec(`UPDATE work_items SET assignee_run_id=?,interrupted_at='',updated_at=? WHERE id=? AND conversation_id=? AND bot_id=? AND status='todo' AND interrupted_at<>''`, runID, now(), taskID, conv, bot)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// decoratePausedRuns names, on each cancelled run in the list, the task that run
// left paused, so the chat can say which task stopped.
func (s *Store) decoratePausedRuns(runs []Run) {
	index := map[string]int{}
	var ids []any
	for i, r := range runs {
		if r.Status == "cancelled" {
			index[r.ID] = i
			ids = append(ids, r.ID)
		}
	}
	for start := 0; start < len(ids); start += 400 {
		end := start + 400
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		rows, err := s.db.Query(`SELECT source_run_id,title FROM work_items WHERE source<>'' AND status NOT IN ('done','cancelled','failed') AND source_run_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, chunk...)
		if err != nil {
			return
		}
		for rows.Next() {
			var id, title string
			if rows.Scan(&id, &title) == nil {
				runs[index[id]].PausedTaskTitle = title
			}
		}
		rows.Close()
	}
}

// steeredAnswerLeavesOriginalTx is the safety net for a run that read a message
// from the person while it worked: its final answer reports done, but does not
// state that it covers the request the run started with. The harness does not
// judge the text; the answer's own flag (say covers_original) is the statement.
// A run that holds a pending wait is woken by it later and is left alone.
func steeredAnswerLeavesOriginalTx(tx *sql.Tx, runID, outcome string) (bool, error) {
	if outcome != "" && outcome != sayOutcomeDone {
		return false, nil
	}
	var kind string
	if err := tx.QueryRow(`SELECT COALESCE(kind,'') FROM runs WHERE id=?`, runID).Scan(&kind); err != nil {
		return false, err
	}
	if kind != "" && kind != runKindGroupChat {
		return false, nil
	}
	var covers, quiet int
	if err := tx.QueryRow(`SELECT covers_original,quiet FROM run_say_answers WHERE run_id=?`, runID).Scan(&covers, &quiet); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if covers != 0 || quiet != 0 {
		return false, nil
	}
	var read, waits int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM bot_inbox WHERE kind='user_message' AND consumed_by_run=? AND drained=1),(SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending')`, runID, runID).Scan(&read, &waits); err != nil {
		return false, err
	}
	return read > 0 && waits == 0, nil
}
