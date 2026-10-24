package app

// Waits: a Bot starts a call whose answer comes later. The call returns a
// receipt at once; the answer (or the deadline passing) comes back as an event
// in the Bot's inbox, which wakes it (docs/decisions/0059-tasks-and-waits.md).
//
// A wait belongs to a wait group. A group of mode "all" settles when every
// member has ended; a group of mode "any" settles at the first member that is
// fulfilled, or when none is left. A settled group is delivered once, as one
// inbox row. The tables survive a restart, and the sweeper (wait_sweeper.go)
// ends the waits whose deadline has passed, so no timer lives only in memory.

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	waitBotPost     = "bot_post"
	waitTaskResult  = "task_result"
	waitHumanAnswer = "human_answer"
	waitCommandExit = "command_exit"
	waitTimer       = "timer"

	waitPending   = "pending"
	waitFulfilled = "fulfilled"
	waitTimedOut  = "timed_out"
	waitCancelled = "cancelled"

	waitModeAny = "any"
	waitModeAll = "all"

	deliveryEngine = "engine"
	deliveryLegacy = "legacy"

	// maxPendingWaitsPerBot bounds what one Bot can have open at once.
	maxPendingWaitsPerBot = 16
	// maxWaitCycleSteps bounds the walk of the wait graph at registration.
	maxWaitCycleSteps = 64
	// maxWaitChain bounds how many times in a row a Bot may wake itself from
	// its own waits for one piece of work.
	maxWaitChain = 24
	// maxWaitNoteRunes bounds the Bot's own label on a wait.
	maxWaitNoteRunes = 160
	minTimerSeconds  = 5
	maxTimerSeconds  = 7 * 24 * 3600
)

// waitTimeoutFor is the default deadline of a kind of wait (owner decision Q19):
// a Bot's post 15 minutes, a task's result 2 hours, a person's answer 30
// minutes. A command has no deadline of its own, so it gets a day; the Bot is
// expected to pair it with a timer. A timer carries its own time.
func waitTimeoutFor(kind string) time.Duration {
	switch kind {
	case waitBotPost:
		return 15 * time.Minute
	case waitTaskResult:
		return 2 * time.Hour
	case waitHumanAnswer:
		return 30 * time.Minute
	case waitCommandExit:
		return 24 * time.Hour
	}
	return 15 * time.Minute
}

func validWaitKind(kind string) bool {
	switch kind {
	case waitBotPost, waitTaskResult, waitHumanAnswer, waitCommandExit, waitTimer:
		return true
	}
	return false
}

type WaitSpec struct {
	BotID, RunID, ConversationID, Kind, Target string
	GroupID, Mode, Note                        string
	RootTaskID, Lineage, Delivery              string
	Timeout                                    time.Duration
	// SkipCycle exempts waits that record a call already made (a delegation)
	// rather than ask the system to hold the Bot.
	SkipCycle bool
}

type Wait struct {
	ID, BotID, RunID, ConversationID, Kind, Target string
	GroupID, Mode, Status, Delivery, DeadlineAt    string
	RootTaskID, Lineage, Note, ResultRef           string
	CreatedAt, ResolvedAt, DeliveredAt             string
}

// WaitView is a wait as the board and the Bot's context show it.
type WaitView struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Target     string `json:"target,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	Mode       string `json:"mode"`
	GroupID    string `json:"group_id"`
	DeadlineAt string `json:"deadline_at"`
	Note       string `json:"note,omitempty"`
}

const waitColumns = `id,bot_id,run_id,conversation_id,kind,target,group_id,group_mode,status,delivery,deadline_at,root_task_id,lineage,note,result_ref,created_at,resolved_at,delivered_at`

func scanWait(row interface{ Scan(...any) error }) (Wait, error) {
	var w Wait
	err := row.Scan(&w.ID, &w.BotID, &w.RunID, &w.ConversationID, &w.Kind, &w.Target, &w.GroupID, &w.Mode, &w.Status, &w.Delivery, &w.DeadlineAt, &w.RootTaskID, &w.Lineage, &w.Note, &w.ResultRef, &w.CreatedAt, &w.ResolvedAt, &w.DeliveredAt)
	return w, err
}

func waitIDsTx(q queryer, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func waitRefusal(code, message string) error { return taskRefusal(code, message) }

// waitGraphNext is the Bot a pending wait holds its owner behind, if any.
func waitGraphNext(q queryer, kind, target string) string {
	switch kind {
	case waitBotPost:
		return target
	case waitTaskResult:
		var bot string
		_ = q.QueryRow(`SELECT bot_id FROM work_items WHERE id=?`, target).Scan(&bot)
		return bot
	}
	return ""
}

// waitCycleTx reports whether owner waiting on (kind, target) would close a
// cycle of waits: the Bot it waits for, through the waits that Bot and the
// others already hold, waits for owner. The walk is bounded; a graph too large
// to walk is refused, because a cycle cannot be ruled out.
func waitCycleTx(tx *sql.Tx, owner, kind, target string) (bool, error) {
	first := waitGraphNext(tx, kind, target)
	if first == "" {
		return false, nil
	}
	if first == owner {
		return true, nil
	}
	seen := map[string]bool{first: true}
	queue := []string{first}
	for steps := 0; len(queue) > 0; steps++ {
		if steps >= maxWaitCycleSteps {
			return true, nil
		}
		bot := queue[0]
		queue = queue[1:]
		rows, err := tx.Query(`SELECT kind,target FROM waits WHERE bot_id=? AND status='pending' AND kind IN ('bot_post','task_result')`, bot)
		if err != nil {
			return false, err
		}
		type edge struct{ kind, target string }
		var edges []edge
		for rows.Next() {
			var e edge
			if err := rows.Scan(&e.kind, &e.target); err != nil {
				rows.Close()
				return false, err
			}
			edges = append(edges, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		for _, e := range edges {
			next := waitGraphNext(tx, e.kind, e.target)
			if next == "" || seen[next] {
				continue
			}
			if next == owner {
				return true, nil
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}

// recordWaitTx registers one wait. The cycle check and the insert share the
// transaction, so two registrations cannot each pass the check and then close
// the cycle together.
func recordWaitTx(tx *sql.Tx, spec WaitSpec) (Wait, error) {
	if !validWaitKind(spec.Kind) {
		return Wait{}, waitRefusal("wait_kind_invalid", "A wait is on a post from a member, a task's result, a person's answer, a command's end or a timer.")
	}
	if spec.BotID == "" || spec.ConversationID == "" {
		return Wait{}, waitRefusal("wait_owner_required", "A wait needs the Bot that holds it and the conversation it returns to.")
	}
	if spec.Mode == "" {
		spec.Mode = waitModeAll
	}
	if spec.Mode != waitModeAny && spec.Mode != waitModeAll {
		return Wait{}, waitRefusal("wait_mode_invalid", "A wait group settles on any member or on all members.")
	}
	if spec.Delivery == "" {
		spec.Delivery = deliveryEngine
	}
	spec.Note = strings.TrimSpace(spec.Note)
	if utf8.RuneCountInString(spec.Note) > maxWaitNoteRunes {
		spec.Note = string([]rune(spec.Note)[:maxWaitNoteRunes])
	}
	if spec.Timeout <= 0 {
		spec.Timeout = waitTimeoutFor(spec.Kind)
	}
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND status='pending'`, spec.BotID).Scan(&pending); err != nil {
		return Wait{}, err
	}
	if pending >= maxPendingWaitsPerBot && spec.Delivery == deliveryEngine {
		return Wait{}, waitRefusal("wait_limit", fmt.Sprintf("You already hold %d open waits. Cancel one, or let one end, before you add another.", maxPendingWaitsPerBot))
	}
	id := newID()
	group := id
	if spec.GroupID != "" {
		var owner, mode string
		var delivered string
		err := tx.QueryRow(`SELECT bot_id,group_mode,MAX(delivered_at) FROM waits WHERE group_id=? GROUP BY group_id`, spec.GroupID).Scan(&owner, &mode, &delivered)
		if err == sql.ErrNoRows || (err == nil && owner != spec.BotID) {
			return Wait{}, waitRefusal("wait_group_unknown", "No open wait group of yours has that id.")
		}
		if err != nil {
			return Wait{}, err
		}
		if delivered != "" {
			return Wait{}, waitRefusal("wait_group_closed", "That wait group has already ended. Start a new group.")
		}
		group, spec.Mode = spec.GroupID, mode
	}
	if spec.RunID != "" && spec.Delivery == deliveryEngine && inboxWakeDepthTx(tx, spec.RunID) >= maxWaitChain {
		return Wait{}, waitRefusal("wait_chain_limit", fmt.Sprintf("You have woken yourself from your own waits %d times in a row for this work. Report where it stands to the person, or finish it, instead of waiting again.", maxWaitChain))
	}
	if !spec.SkipCycle {
		cycle, err := waitCycleTx(tx, spec.BotID, spec.Kind, spec.Target)
		if err != nil {
			return Wait{}, err
		}
		if cycle {
			return Wait{}, waitRefusal("wait_cycle", "This wait would close a cycle: the member you would wait for is already waiting, directly or through others, for you. Resolve that wait first, or do the work yourself.")
		}
	}
	t := time.Now()
	deadline := stampTime(t.Add(spec.Timeout))
	if _, err := tx.Exec(`INSERT INTO waits(`+waitColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, spec.BotID, spec.RunID, spec.ConversationID, spec.Kind, spec.Target, group, spec.Mode, waitPending, spec.Delivery, deadline, spec.RootTaskID, spec.Lineage, spec.Note, "", stampTime(t), "", ""); err != nil {
		return Wait{}, err
	}
	return scanWait(tx.QueryRow(`SELECT `+waitColumns+` FROM waits WHERE id=?`, id))
}

// registerWait is the store entry point of the tools: its own short transaction.
func (s *Store) registerWait(spec WaitSpec) (Wait, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Wait{}, err
	}
	defer tx.Rollback()
	if spec.RootTaskID == "" && spec.RunID != "" {
		if item, ok, e := taskForRunTx(tx, spec.RunID); e == nil && ok {
			spec.RootTaskID = item.RootTaskID
		}
	}
	w, err := recordWaitTx(tx, spec)
	if err != nil {
		return Wait{}, err
	}
	return w, tx.Commit()
}

// resolveWaitTx ends a pending wait with a status and settles its group. It
// reports whether this call ended it.
func resolveWaitTx(tx *sql.Tx, id, status, resultRef string) (bool, error) {
	res, err := tx.Exec(`UPDATE waits SET status=?,result_ref=?,resolved_at=? WHERE id=? AND status='pending'`, status, resultRef, now(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false, nil
	}
	var group string
	if err = tx.QueryRow(`SELECT group_id FROM waits WHERE id=?`, id).Scan(&group); err != nil {
		return true, err
	}
	return true, settleGroupTx(tx, group)
}

// settleGroupTx closes a wait group when its mode says it is complete and
// delivers it once. Delivery is an inbox row; a group whose every member was
// cancelled delivers nothing, and a group whose members were all fulfilled by a
// path that has its own return (delivery "legacy") settles in silence.
func settleGroupTx(tx *sql.Tx, group string) error {
	rows, err := tx.Query(`SELECT `+waitColumns+` FROM waits WHERE group_id=? ORDER BY created_at,id`, group)
	if err != nil {
		return err
	}
	var members []Wait
	for rows.Next() {
		w, err := scanWait(rows)
		if err != nil {
			rows.Close()
			return err
		}
		members = append(members, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(members) == 0 {
		return err
	}
	pending, fulfilled := 0, 0
	for _, w := range members {
		if w.DeliveredAt != "" {
			return nil
		}
		switch w.Status {
		case waitPending:
			pending++
		case waitFulfilled:
			fulfilled++
		}
	}
	mode := members[0].Mode
	settled := pending == 0 || (mode == waitModeAny && fulfilled > 0)
	if !settled {
		return nil
	}
	t := now()
	if pending > 0 {
		// An "any" group ends at its first fulfilled member; the rest stand down.
		if _, err = tx.Exec(`UPDATE waits SET status='cancelled',resolved_at=? WHERE group_id=? AND status='pending'`, t, group); err != nil {
			return err
		}
		for i := range members {
			if members[i].Status == waitPending {
				members[i].Status = waitCancelled
			}
		}
	}
	if _, err = tx.Exec(`UPDATE waits SET delivered_at=? WHERE group_id=?`, t, group); err != nil {
		return err
	}
	live, silent := 0, true
	for _, w := range members {
		if w.Status == waitCancelled {
			continue
		}
		live++
		// A wait that mirrors a call with its own return (a delegation's result, a
		// question to the person) delivers nothing when it ends well; a question the
		// person left unanswered is settled by the question flow, never twice.
		own := w.Delivery == deliveryLegacy && (w.Status == waitFulfilled || w.Kind == waitHumanAnswer)
		if !own {
			silent = false
		}
	}
	if live == 0 || silent {
		return nil
	}
	return deliverGroupTx(tx, group, mode, members)
}

// deliverGroupTx turns a settled group into the inbox row that wakes its owner.
func deliverGroupTx(tx *sql.Tx, group, mode string, members []Wait) error {
	owner := members[0]
	item := InboxItem{BotID: owner.BotID, ConversationID: owner.ConversationID, Kind: inboxWaitResult, RootTaskID: owner.RootTaskID, WaitGroup: group, Wakes: true, ParentRun: owner.RunID}
	// One member on a Bot's post, in a group, keeps the wake a colleague post
	// has always made: the woken Bot reads the post it waited for.
	if len(members) == 1 && owner.Kind == waitBotPost && owner.Delivery == deliveryEngine && owner.Status != waitCancelled {
		var kind string
		if err := tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, owner.ConversationID).Scan(&kind); err == nil && kind == "group" {
			item.Via, item.Anchor = "mention", owner.ResultRef
		}
	}
	var b strings.Builder
	if len(members) == 1 && item.Via == "mention" {
		if owner.Status == waitFulfilled {
			name := owner.Target
			_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, owner.Target).Scan(&name)
			b.WriteString(name + " posted the content you were waiting for (see the message above). Continue your part with it.")
		} else {
			b.WriteString(waitTimeoutRequest)
		}
		item.Body = b.String()
		return addInboxTx(tx, item)
	}
	fmt.Fprintf(&b, "Your wait ended (group %s, mode %s).", group, mode)
	for _, w := range members {
		b.WriteString("\n- " + describeWaitTx(tx, w))
	}
	item.Body = trimRunes(b.String(), 3000)
	return addInboxTx(tx, item)
}

// describeWaitTx is one line of a wait result.
func describeWaitTx(q queryer, w Wait) string {
	name := w.Target
	switch w.Kind {
	case waitBotPost:
		_ = q.QueryRow(`SELECT name FROM bots WHERE id=?`, w.Target).Scan(&name)
	case waitTaskResult:
		_ = q.QueryRow(`SELECT title FROM work_items WHERE id=?`, w.Target).Scan(&name)
	}
	note := ""
	if w.Note != "" {
		note = " (" + w.Note + ")"
	}
	switch w.Kind {
	case waitTimer:
		if w.Status == waitFulfilled {
			return "timer ended" + note + ": the time you set has come."
		}
	case waitBotPost:
		switch w.Status {
		case waitFulfilled:
			text := ""
			if w.ResultRef != "" {
				_ = q.QueryRow(`SELECT content FROM messages WHERE id=?`, w.ResultRef).Scan(&text)
			}
			return fmt.Sprintf("%s posted%s: %s", name, note, trimRunes(text, 1200))
		case waitTimedOut:
			return fmt.Sprintf("timed out waiting for a post from %s%s: it has not posted.", name, note)
		}
	case waitTaskResult:
		var status, summary, version string
		var spec, based int
		_ = q.QueryRow(`SELECT status,result_summary,spec_version,result_based_on_version FROM work_items WHERE id=?`, w.Target).Scan(&status, &summary, &spec, &based)
		switch w.Status {
		case waitFulfilled:
			stale := ""
			if based > 0 && based < spec {
				stale = fmt.Sprintf(" The result is based on version %d of the request; the current version is %d.", based, spec)
			}
			version = ""
			return fmt.Sprintf("task %q (%s) ended with status %s%s.%s %s", name, w.Target, status, note, stale+version, trimRunes(summary, 400))
		case waitTimedOut:
			return fmt.Sprintf("timed out waiting for the result of task %q (%s)%s: it is still %s.", name, w.Target, note, status)
		}
	case waitHumanAnswer:
		if w.Status == waitFulfilled {
			return "the person answered your question" + note + "."
		}
		return "the question to the person" + note + " went unanswered."
	case waitCommandExit:
		if w.Status == waitFulfilled {
			return "the command ended" + note + "."
		}
	}
	if w.Status == waitTimedOut {
		return fmt.Sprintf("timed out waiting on %s %s%s.", w.Kind, name, note)
	}
	return fmt.Sprintf("%s %s ended with status %s%s.", w.Kind, name, w.Status, note)
}

const waitTimeoutRequest = "The wait timed out: the Bot you were waiting for has not posted. Say plainly what is still missing, or do your part with what is available."

// cancelWait ends a pending wait its owner no longer needs. Cancelling a member
// of a group lets the group settle on the others.
func (s *Store) cancelWait(botID, waitID string) (Wait, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Wait{}, err
	}
	defer tx.Rollback()
	w, err := scanWait(tx.QueryRow(`SELECT `+waitColumns+` FROM waits WHERE id=?`, waitID))
	if err == sql.ErrNoRows || (err == nil && w.BotID != botID) {
		return Wait{}, waitRefusal("wait_not_found", "You hold no wait with that id.")
	}
	if err != nil {
		return Wait{}, err
	}
	if w.Status != waitPending {
		return Wait{}, waitRefusal("wait_not_pending", "That wait has already ended ("+w.Status+").")
	}
	if _, err = resolveWaitTx(tx, waitID, waitCancelled, ""); err != nil {
		return Wait{}, err
	}
	w, err = scanWait(tx.QueryRow(`SELECT `+waitColumns+` FROM waits WHERE id=?`, waitID))
	if err != nil {
		return Wait{}, err
	}
	return w, tx.Commit()
}

// runHasPendingWait reports whether a run holds a wait that is still open. A
// run that ends with a promise of later work is honest only when this is true.
func (s *Store) runHasPendingWait(runID string) bool {
	if s == nil || s.db == nil {
		return false
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending'`, runID).Scan(&n)
	return n > 0
}

func pendingWaitViews(q queryer, botID, rootTaskID string, limit int) ([]WaitView, error) {
	query := `SELECT id,kind,target,group_mode,group_id,deadline_at,note FROM waits WHERE bot_id=? AND status='pending'`
	args := []any{botID}
	if rootTaskID != "" {
		query += ` AND root_task_id=?`
		args = append(args, rootTaskID)
	}
	query += ` ORDER BY deadline_at,id LIMIT ?`
	args = append(args, limit)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var views []WaitView
	for rows.Next() {
		var v WaitView
		if err := rows.Scan(&v.ID, &v.Kind, &v.Target, &v.Mode, &v.GroupID, &v.DeadlineAt, &v.Note); err != nil {
			rows.Close()
			return nil, err
		}
		views = append(views, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range views {
		switch views[i].Kind {
		case waitBotPost:
			_ = q.QueryRow(`SELECT name FROM bots WHERE id=?`, views[i].Target).Scan(&views[i].TargetName)
		case waitTaskResult:
			_ = q.QueryRow(`SELECT title FROM work_items WHERE id=?`, views[i].Target).Scan(&views[i].TargetName)
		}
	}
	return views, nil
}

const waitingOnContextRunes = 1600

// waitingOnContext is the block that tells a Bot what it is waiting on: each
// open wait, what it is on and when it ends. It is bounded; a Bot with more
// waits than fit sees how many were left out.
func (s *Store) waitingOnContext(botID string) string {
	views, err := pendingWaitViews(s.db, botID, "", 64)
	if err != nil || len(views) == 0 {
		return ""
	}
	head := "[waiting on]\nWaits you hold. Each ends when its event arrives or its deadline passes, and you are woken with the result either way. End one you no longer need with cancel_wait; it loads on demand when you name it in workspace_capabilities.\n"
	tail := "[/waiting on]"
	var b strings.Builder
	b.WriteString(head)
	used := utf8.RuneCountInString(head) + utf8.RuneCountInString(tail) + 40
	omitted := 0
	for _, v := range views {
		on := v.Target
		if v.TargetName != "" {
			on = fmt.Sprintf("%s (%s)", v.TargetName, v.Target)
		}
		line := fmt.Sprintf("- wait_id=%s kind=%s on=%s group=%s mode=%s ends=%s", v.ID, v.Kind, on, v.GroupID, v.Mode, v.DeadlineAt)
		if v.Note != "" {
			line += " note=" + v.Note
		}
		line += "\n"
		if used+utf8.RuneCountInString(line) > waitingOnContextRunes {
			omitted++
			continue
		}
		used += utf8.RuneCountInString(line)
		b.WriteString(line)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d more waits not listed)\n", omitted)
	}
	b.WriteString(tail)
	return b.String()
}

// expireDueWaits ends every pending wait whose deadline is at or before at. A
// timer that reaches its time is fulfilled; any other wait timed out. It
// returns how many it ended.
func (s *Store) expireDueWaits(at time.Time) (int, error) {
	ids, err := waitIDsTx(s.db, `SELECT id FROM waits WHERE status='pending' AND deadline_at<=? ORDER BY deadline_at,id LIMIT 500`, stampTime(at))
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, id := range ids {
		tx, err := s.db.Begin()
		if err != nil {
			return ended, err
		}
		var kind string
		if err = tx.QueryRow(`SELECT kind FROM waits WHERE id=?`, id).Scan(&kind); err != nil {
			tx.Rollback()
			continue
		}
		status := waitTimedOut
		if kind == waitTimer {
			status = waitFulfilled
		}
		changed, err := resolveWaitTx(tx, id, status, "")
		if err != nil {
			// One wait that cannot end must not keep the later ones from timing out.
			tx.Rollback()
			log.Printf("expire wait %s: %v", id, err)
			continue
		}
		if err = tx.Commit(); err != nil {
			log.Printf("expire wait %s: %v", id, err)
			continue
		}
		if changed {
			ended++
		}
	}
	return ended, nil
}

// reconcileHumanAnswerWaits ends the waits that mirror a question to a person
// once the question is no longer pending. The question flow resumes the run on
// its own; the wait only records that the call was open.
func (s *Store) reconcileHumanAnswerWaits() error {
	rows, err := s.db.Query(`SELECT w.id,COALESCE(q.status,'') FROM waits w LEFT JOIN questions q ON q.id=w.target WHERE w.kind='human_answer' AND w.status='pending' AND COALESCE(q.status,'')<>?`, questionPending)
	if err != nil {
		return err
	}
	type pair struct{ id, status string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.status); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		status := waitCancelled
		switch p.status {
		case questionAnswered:
			status = waitFulfilled
		case questionExpired:
			status = waitTimedOut
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err = resolveWaitTx(tx, p.id, status, ""); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// mirrorHumanAnswerWait records that a run asked a person, so the question is
// one of the run's open waits. The question flow keeps its own resume.
func (s *Store) mirrorHumanAnswerWait(q Question) {
	if q.Status != questionPending {
		return
	}
	timeout := waitTimeoutFor(waitHumanAnswer)
	if q.ExpiresAt != "" {
		if at, err := time.Parse(time.RFC3339Nano, q.ExpiresAt); err == nil {
			timeout = time.Until(at)
			if timeout < time.Second {
				timeout = time.Second
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		logTaskError("human answer wait", err)
		return
	}
	defer tx.Rollback()
	root := ""
	if item, ok, e := taskForRunTx(tx, q.RunID); e == nil && ok {
		root = item.RootTaskID
	}
	if _, err = recordWaitTx(tx, WaitSpec{BotID: q.BotID, RunID: q.RunID, ConversationID: q.ConversationID, Kind: waitHumanAnswer, Target: q.ID, Timeout: timeout,
		RootTaskID: root, Delivery: deliveryLegacy, SkipCycle: true}); err != nil {
		logTaskError("human answer wait", err)
		return
	}
	if err = tx.Commit(); err != nil {
		logTaskError("human answer wait", err)
	}
}

// fulfillBotPostWaits ends the waits that a post from botID, in the lineage of
// the run that posted it, was awaited by.
func (s *Store) fulfillBotPostWaits(conversationID, botID, lineage, messageID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := waitIDsTx(tx, `SELECT id FROM waits WHERE conversation_id=? AND kind='bot_post' AND target=? AND status='pending' AND bot_id<>? AND lineage=?`, conversationID, botID, botID, lineage)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = resolveWaitTx(tx, id, waitFulfilled, messageID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var errWaitStore = errors.New("wait store is required")

// inboxWakeDepthTx counts the wake runs, each started from inbox rows, that lead
// to the run through its parents. A Bot that keeps waiting and waking reaches
// the bound.
func inboxWakeDepthTx(tx *sql.Tx, runID string) int {
	depth := 0
	for id, steps := runID, 0; id != "" && steps < 4*maxWaitChain; steps++ {
		var source, parent sql.NullString
		if err := tx.QueryRow(`SELECT (SELECT source FROM run_wakes w WHERE w.run_id=r.id),parent_run_id FROM runs r WHERE r.id=?`, id).Scan(&source, &parent); err != nil {
			break
		}
		if source.String == wakeSourceInbox {
			depth++
		}
		id = parent.String
	}
	return depth
}

// reconcileTaskRuns closes the tasks whose working run ended some way that did
// not reach the task hooks (a recovery, an expiry): the task follows the run.
func (s *Store) reconcileTaskRuns() error {
	rows, err := s.db.Query(`SELECT w.assignee_run_id,r.status FROM work_items w JOIN runs r ON r.id=w.assignee_run_id
 WHERE w.assigner_kind='bot' AND w.status IN (` + openTaskStatuses + `) AND r.status IN ('failed','cancelled')`)
	if err != nil {
		return err
	}
	type pair struct{ run, status string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.run, &p.status); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if err = taskRunEndedTx(tx, p.run, p.status); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
