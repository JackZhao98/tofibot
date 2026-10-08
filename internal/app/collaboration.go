package app

// Collaboration routing and durable per-conversation execution queues live in
// this file so the HTTP/storage boundary remains small. The queue is an
// in-process dispatcher over durable queued rows: a restart can safely pick up
// queued work while running work is still marked interrupted by OpenStore.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// HasSteeringSuccessor is the durable human-input boundary. Scheduled runs
// also use role=user trigger messages, but they never create run_steering
// edges and therefore cannot interrupt an active run.
func (s *Store) HasSteeringSuccessor(oldRunID string) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_steering link JOIN runs successor ON successor.id=link.new_run_id WHERE link.old_run_id=? AND successor.status IN ('queued','running'))`, oldRunID).Scan(&found)
	return found != 0, err
}

const (
	runKindGroupChat = "group_chat"
	runKindTriage    = "triage"
	runKindTeam      = "team"
	runKindGroupTask = "group_task"
	runKindFollowup  = "group_followup"
	runKindMessage   = "message"
	runKindSchedule  = "schedule"
	messageKindUser  = "user_message"
	// bot_result is a private context anchor used to resume the Bot that sent
	// a message without leaking the recipient's reply into a user DM.
	messageKindBotResult = "bot_result"
	maxHandoffRounds     = 8
)

type conversationQueue struct {
	wake chan struct{}
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func newConversationQueue() *conversationQueue {
	return &conversationQueue{wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
}
func (q *conversationQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *conversationQueue) stopNow() { q.once.Do(func() { close(q.stop) }) }
func (q *conversationQueue) close()   { q.stopNow(); <-q.done }

type runSpec struct {
	BotID, Model, Kind, ParentRunID, OriginConversationID string
}

func (s *Store) GetMessage(id string) (Message, error) {
	var m Message
	var kind, sender, run, noticeData sql.NullString
	err := s.db.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE id=?`, id).Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &kind, &sender, &run, &m.Content, &noticeData, &m.CreatedAt)
	m.Kind, m.SenderBotID, m.RunID = kind.String, sender.String, run.String
	if noticeData.Valid {
		_ = json.Unmarshal([]byte(noticeData.String), &m.Notice)
	}
	if err == nil {
		items := []Message{m}
		err = s.hydrateDeletedSenders(items)
		m = items[0]
	}
	return m, err
}

// AddUserRuns writes one user message and one or more target runs in one
// transaction. Explicit targets and unaddressed group rounds share an immutable
// trigger anchor; group snapshots are selected after the idempotency check.
func (s *Store) AddUserRuns(conv, content, client string, specs []runSpec, interrupt ...bool) (Message, []Run, bool, error) {
	return s.addUserRuns(conv, content, client, specs, nil, interrupt...)
}

// AddUserRunsWithAttachments atomically persists the initiating message, its
// runs, and attachment bindings. Invalid bindings roll back the whole ingress.
func (s *Store) AddUserRunsWithAttachments(conv, content, client string, specs []runSpec, attachmentIDs []string, interrupt ...bool) (Message, []Run, bool, error) {
	return s.addUserRuns(conv, content, client, specs, attachmentIDs, interrupt...)
}

func (s *Store) addUserRuns(conv, content, client string, specs []runSpec, attachmentIDs []string, interrupt ...bool) (Message, []Run, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, nil, false, err
	}
	defer tx.Rollback()
	if client != "" {
		var m Message
		var kind, sb, ru, noticeData sql.NullString
		err = tx.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE conversation_id=? AND client_message_id=?`, conv, client).Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &kind, &sb, &ru, &m.Content, &noticeData, &m.CreatedAt)
		if err == nil {
			m.Kind, m.SenderBotID, m.RunID = kind.String, sb.String, ru.String
			if noticeData.Valid {
				_ = json.Unmarshal([]byte(noticeData.String), &m.Notice)
			}
			old := make([]Run, 0, len(specs))
			rows, qerr := tx.Query(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE trigger_message_id=? ORDER BY queue_seq,id`, m.ID)
			if qerr == nil {
				defer rows.Close()
				for rows.Next() {
					x, xerr := scanRun(rows)
					if xerr != nil {
						return Message{}, nil, false, xerr
					}
					old = append(old, x)
				}
				qerr = rows.Err()
				rows.Close()
			}
			// Rows created before trigger_message_id was introduced still carry
			// the initiating run on the message. Preserve idempotent retries for
			// those durable records during the migration window.
			if len(old) == 0 && m.RunID != "" {
				if x, xerr := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, m.RunID)); xerr == nil {
					old = append(old, x)
				}
			}
			return m, old, true, qerr
		}
		if err != sql.ErrNoRows {
			return Message{}, nil, false, err
		}
	}
	if len(specs) == 0 {
		var kind string
		if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, conv).Scan(&kind); err != nil {
			return Message{}, nil, false, err
		}
		if kind != "group" {
			return Message{}, nil, false, errors.New("at least one run target is required")
		}
		var selected runSpec
		selected, err = s.defaultGroupRunSpecTx(tx, conv)
		if err != nil {
			return Message{}, nil, false, err
		}
		selected.Kind = runKindGroupChat
		specs = []runSpec{selected}
	}
	// A human request supersedes stale queued work in an active conversation;
	// independent schedule runs are excluded by interruptInTransaction.
	if err = interruptInTransaction(tx, conv); err != nil {
		return Message{}, nil, false, err
	}
	// The HTTP layer resolves mentions before opening this transaction. A
	// concurrent group maintenance request may have replaced those members in
	// the meantime, so authorize every target again at the commit boundary.
	for _, spec := range specs {
		if err = requireCurrentMemberTx(tx, conv, spec.BotID); err != nil {
			return Message{}, nil, false, err
		}
	}
	t := now()
	var seq, nextQueue int64
	if err = tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, nil, false, err
	}
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0) FROM runs WHERE conversation_id=?`, conv).Scan(&nextQueue); err != nil {
		return Message{}, nil, false, err
	}
	m := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "user", Content: content, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,run_id,content,created_at,client_message_id) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, conv, seq, m.Role, m.Kind, nil, content, t, nullString(client)); err != nil {
		return Message{}, nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, m.ID, t); err != nil {
		return Message{}, nil, false, err
	}
	runs := make([]Run, 0, len(specs))
	for _, spec := range specs {
		if spec.BotID == "" {
			return Message{}, nil, false, errors.New("run bot is required")
		}
		nextQueue++
		origin := spec.OriginConversationID
		if origin == "" {
			origin = conv
		}
		r := Run{ID: newID(), ConversationID: conv, BotID: spec.BotID, Status: "queued", Kind: spec.Kind, Model: spec.Model, ParentRunID: spec.ParentRunID, OriginConversationID: origin, TriggerMessageID: m.ID, QueueSeq: nextQueue, CreatedAt: t, UpdatedAt: t}
		if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.ConversationID, r.BotID, r.Status, nil, nullString(r.ParentRunID), 0, r.Model, r.Kind, r.OriginConversationID, r.TriggerMessageID, r.QueueSeq, t, t); err != nil {
			return Message{}, nil, false, err
		}
		runs = append(runs, r)
	}
	// Link this user request to work that was already running in the same
	// conversation. The relation scopes carryover evidence to this request and
	// prevents old steering results leaking into later retries or runs.
	for _, r := range runs {
		if _, err = tx.Exec(`WITH RECURSIVE prior(id) AS (
			SELECT id FROM runs WHERE conversation_id=? AND id<>? AND (status='running' OR
				(status IN ('waiting','queued') AND EXISTS(SELECT 1 FROM run_input_waits w WHERE w.run_id=runs.id AND w.state='waiting')))
			UNION SELECT link.old_run_id FROM run_steering link JOIN prior p ON link.new_run_id=p.id
		) INSERT OR IGNORE INTO run_steering(new_run_id,old_run_id)
			SELECT ?,id FROM prior`, conv, r.ID, r.ID); err != nil {
			return Message{}, nil, false, err
		}
	}
	if err = supersedeInputWaitsTx(tx, conv); err != nil {
		return Message{}, nil, false, err
	}
	if _, err = tx.Exec(`UPDATE messages SET run_id=? WHERE id=?`, runs[0].ID, m.ID); err != nil {
		return Message{}, nil, false, err
	}
	m.RunID = runs[0].ID
	if len(attachmentIDs) > 0 {
		if err = bindAttachmentsTx(tx, conv, m.ID, attachmentIDs); err != nil {
			return Message{}, nil, false, err
		}
	}
	m.Attachments, err = attachmentMetadata(tx, m.ID)
	if err != nil {
		return Message{}, nil, false, err
	}
	for _, v := range []struct {
		typ   string
		value any
	}{{"message", m}} {
		b, _ := json.Marshal(v.value)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conv, v.typ, string(b), t); err != nil {
			return Message{}, nil, false, err
		}
	}
	for _, r := range runs {
		b, _ := json.Marshal(r)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conv, "run", string(b), t); err != nil {
			return Message{}, nil, false, err
		}
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv); err != nil {
		return Message{}, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, nil, false, err
	}
	return m, runs, false, nil
}

// defaultGroupRunSpecTx chooses one random active member inside the ingress
// transaction, after idempotency is checked. The saved run makes retries stable.
func (s *Store) defaultGroupRunSpecTx(tx *sql.Tx, conversationID string) (runSpec, error) {
	var selected runSpec
	err := tx.QueryRow(`SELECT b.id,b.model FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=? AND b.archived=0 ORDER BY RANDOM() LIMIT 1`, conversationID).Scan(&selected.BotID, &selected.Model)
	if err == sql.ErrNoRows {
		return runSpec{}, ErrNoActiveMembers
	}
	return selected, err
}

// AddForwardHandoff crosses a DM boundary without making the target Bot read
// the source Bot's private conversation. The source receives a durable notice;
// the explicit task is stored in the target canonical DM as the child anchor.
func (s *Store) AddForwardHandoff(origin, sender, bot, parent, task string) (Message, Run, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, Run{}, err
	}
	defer tx.Rollback()
	var parentConv string
	if err = tx.QueryRow(`SELECT conversation_id FROM runs WHERE id=? AND status='running' AND handoff_count=0`, parent).Scan(&parentConv); err != nil {
		return Message{}, Run{}, err
	}
	if parentConv != origin {
		return Message{}, Run{}, errors.New("handoff origin does not match parent run")
	}
	if err = requireConversationActiveTx(tx, origin); err != nil {
		return Message{}, Run{}, err
	}
	if err = requireActiveBotTx(tx, bot); err != nil {
		return Message{}, Run{}, err
	}
	var targetConv, model string
	var targetName, senderName string
	if err = tx.QueryRow(`SELECT dm_conversation_id,model,name FROM bots WHERE id=?`, bot).Scan(&targetConv, &model, &targetName); err != nil {
		return Message{}, Run{}, err
	}
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, sender).Scan(&senderName)
	if senderName == "" {
		senderName = sender
	}
	if targetName == "" {
		targetName = bot
	}
	res, updateErr := tx.Exec(`UPDATE runs SET handoff_count=handoff_count+1,updated_at=? WHERE id=? AND status='running' AND handoff_count=0`, now(), parent)
	if updateErr != nil {
		return Message{}, Run{}, updateErr
	}
	if changed, _ := res.RowsAffected(); changed != 1 {
		return Message{}, Run{}, errors.New("handoff parent is no longer active")
	}
	t := now()
	childID := newID()
	var targetSeq int64
	if err = tx.QueryRow(nextMessageSeqSQL, targetConv, targetConv, streamDraftActive).Scan(&targetSeq); err != nil {
		return Message{}, Run{}, err
	}
	taskMsg := Message{ID: newID(), ConversationID: targetConv, Seq: targetSeq, Role: "assistant", Kind: "notice", SenderBotID: sender, RunID: childID, Content: task, CreatedAt: t}
	notice := &HandoffNotice{Type: "forward", FromBotID: sender, ToBotID: bot, TargetConversationID: targetConv, TargetRunID: childID}
	taskMsg.Notice = notice
	noticeData, _ := json.Marshal(notice)
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, taskMsg.ID, targetConv, targetSeq, taskMsg.Role, taskMsg.Kind, sender, childID, task, string(noticeData), t); err != nil {
		return Message{}, Run{}, err
	}
	var originSeq int64
	if err = tx.QueryRow(nextMessageSeqSQL, origin, origin, streamDraftActive).Scan(&originSeq); err != nil {
		return Message{}, Run{}, err
	}
	noticeMsg := Message{ID: newID(), ConversationID: origin, Seq: originSeq, Role: "assistant", Kind: "notice", SenderBotID: sender, RunID: childID, Content: fmt.Sprintf("%s 已将任务转交给 %s。", senderName, targetName), Notice: notice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, noticeMsg.ID, origin, originSeq, noticeMsg.Role, noticeMsg.Kind, sender, childID, noticeMsg.Content, string(noticeData), t); err != nil {
		return Message{}, Run{}, err
	}
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, targetConv).Scan(&queueSeq); err != nil {
		return Message{}, Run{}, err
	}
	child := Run{ID: childID, ConversationID: targetConv, BotID: bot, Status: "queued", ParentRunID: parent, Model: model, OriginConversationID: origin, TriggerMessageID: taskMsg.ID, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, child.ID, targetConv, bot, child.Status, nil, parent, 0, child.Model, "", origin, taskMsg.ID, queueSeq, t, t); err != nil {
		return Message{}, Run{}, err
	}
	for _, item := range []struct {
		conv, typ string
		value     any
	}{{origin, "message", noticeMsg}, {origin, "run", child}, {targetConv, "message", taskMsg}, {targetConv, "run", child}} {
		b, _ := json.Marshal(item.value)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, item.conv, item.typ, string(b), t); err != nil {
			return Message{}, Run{}, err
		}
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id IN (?,?)`, t, origin, targetConv); err != nil {
		return Message{}, Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, Run{}, err
	}
	return noticeMsg, child, nil
}

// AddBotMessage creates a one-time Bot-to-Bot conversation. The actual
// exchange lives only in the hidden conversation; the two user-visible
// conversations receive lightweight reference capsules. This is deliberately
// separate from handoff: it does not consume the parent handoff budget and it
// never publishes the recipient's result back into the source conversation.
func (s *Store) AddBotMessage(origin, sender, bot, parent, content string) (Message, Run, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, Run{}, errors.New("message content is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, Run{}, err
	}
	defer tx.Rollback()
	var parentConv, parentStatus string
	if err = tx.QueryRow(`SELECT conversation_id,status FROM runs WHERE id=?`, parent).Scan(&parentConv, &parentStatus); err != nil {
		return Message{}, Run{}, err
	}
	if parentConv != origin || parentStatus != "running" {
		return Message{}, Run{}, errors.New("message parent is not active in this conversation")
	}
	if sender == bot {
		return Message{}, Run{}, errors.New("cannot message the current bot")
	}
	if err = requireConversationActiveTx(tx, origin); err != nil {
		return Message{}, Run{}, err
	}
	if err = requireActiveBotTx(tx, bot); err != nil {
		return Message{}, Run{}, err
	}
	var targetConv, targetName, senderName, model string
	if err = tx.QueryRow(`SELECT dm_conversation_id,name,model FROM bots WHERE id=?`, bot).Scan(&targetConv, &targetName, &model); err != nil {
		return Message{}, Run{}, err
	}
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, sender).Scan(&senderName)
	if senderName == "" {
		senderName = sender
	}
	if targetName == "" {
		targetName = bot
	}
	traceID := newID()
	t := now()
	if _, err = tx.Exec(`INSERT INTO conversations(id,kind,name,updated_at,user_visible) VALUES(?,?,?,?,0)`, traceID, "group", "", t); err != nil {
		return Message{}, Run{}, err
	}
	for _, member := range []string{sender, bot} {
		if _, err = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, traceID, member); err != nil {
			return Message{}, Run{}, err
		}
	}
	childID := newID()
	notice := &HandoffNotice{Type: "message", FromBotID: sender, ToBotID: bot, TargetConversationID: traceID, TargetRunID: childID, TargetBotIDs: []string{bot}}
	noticeData, _ := json.Marshal(notice)
	var traceSeq int64
	if err = tx.QueryRow(nextMessageSeqSQL, traceID, traceID, streamDraftActive).Scan(&traceSeq); err != nil {
		return Message{}, Run{}, err
	}
	traceMessage := Message{ID: newID(), ConversationID: traceID, Seq: traceSeq, Role: "assistant", Kind: "notice", SenderBotID: sender, RunID: childID, Content: content, Notice: notice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, traceMessage.ID, traceID, traceSeq, traceMessage.Role, traceMessage.Kind, sender, childID, content, string(noticeData), t); err != nil {
		return Message{}, Run{}, err
	}
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, traceID).Scan(&queueSeq); err != nil {
		return Message{}, Run{}, err
	}
	child := Run{ID: childID, ConversationID: traceID, BotID: bot, Status: "queued", ParentRunID: parent, Model: model, Kind: runKindMessage, OriginConversationID: origin, TriggerMessageID: traceMessage.ID, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, child.ID, traceID, bot, child.Status, nil, parent, 0, model, runKindMessage, origin, traceMessage.ID, queueSeq, t, t); err != nil {
		return Message{}, Run{}, err
	}
	// Only these capsules are copied into user-visible conversations. Their
	// body is never the Bot's actual reply; the hidden trace is the source of
	// truth opened by View Only Chat.
	makeCapsule := func(conv, label string) (Message, error) {
		var seq int64
		if err := tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
			return Message{}, err
		}
		capsule := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "message_ref", SenderBotID: sender, RunID: childID, Content: label, Notice: notice, CreatedAt: t}
		if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, capsule.ID, conv, seq, capsule.Role, capsule.Kind, sender, childID, label, string(noticeData), t); err != nil {
			return Message{}, err
		}
		return capsule, nil
	}
	sourceCapsule, err := makeCapsule(origin, "Messaged "+targetName)
	if err != nil {
		return Message{}, Run{}, err
	}
	if _, err = makeCapsule(targetConv, "Message from "+senderName); err != nil {
		return Message{}, Run{}, err
	}
	for _, item := range []struct {
		conv, typ string
		value     any
	}{{traceID, "message", traceMessage}, {traceID, "run", child}, {origin, "message", sourceCapsule}} {
		b, _ := json.Marshal(item.value)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, item.conv, item.typ, string(b), t); err != nil {
			return Message{}, Run{}, err
		}
	}
	// The target capsule is already durable; emit its event by reading the
	// just-written row so SSE clients receive the same shape as history.
	var targetCapsule Message
	if err = scanMessageTx(tx, targetConv, childID, &targetCapsule); err != nil {
		return Message{}, Run{}, err
	}
	b, _ := json.Marshal(targetCapsule)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, targetConv, "message", string(b), t); err != nil {
		return Message{}, Run{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id IN (?,?,?)`, t, traceID, origin, targetConv); err != nil {
		return Message{}, Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, Run{}, err
	}
	return sourceCapsule, child, nil
}

// AddBotMessages creates one hidden Bot-only trace for one sender and one or
// more recipients. The trace has no user-facing name; its identity is carried
// by the message notice so clients can render “from ↔ to” directly.
func (s *Store) AddBotMessages(origin, sender string, targets []string, parent, content string) (Message, []Run, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, nil, errors.New("message content is required")
	}
	unique := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		unique = append(unique, target)
	}
	if len(unique) == 0 {
		return Message{}, nil, errors.New("at least one message recipient is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, nil, err
	}
	defer tx.Rollback()
	var parentConv, parentStatus string
	if err = tx.QueryRow(`SELECT conversation_id,status FROM runs WHERE id=?`, parent).Scan(&parentConv, &parentStatus); err != nil {
		return Message{}, nil, err
	}
	if parentConv != origin || parentStatus != "running" {
		return Message{}, nil, errors.New("message parent is not active in this conversation")
	}
	if err = requireConversationActiveTx(tx, origin); err != nil {
		return Message{}, nil, err
	}
	if sender == "" {
		return Message{}, nil, errors.New("message sender is required")
	}
	var senderName string
	if err = tx.QueryRow(`SELECT name FROM bots WHERE id=? AND archived=0`, sender).Scan(&senderName); err != nil {
		return Message{}, nil, err
	}
	type targetInfo struct{ id, conversation, name, model string }
	infos := make([]targetInfo, 0, len(unique))
	for _, target := range unique {
		if target == sender {
			return Message{}, nil, errors.New("cannot message the current bot")
		}
		if err = requireActiveBotTx(tx, target); err != nil {
			return Message{}, nil, err
		}
		var info targetInfo
		if err = tx.QueryRow(`SELECT id,dm_conversation_id,name,model FROM bots WHERE id=?`, target).Scan(&info.id, &info.conversation, &info.name, &info.model); err != nil {
			return Message{}, nil, err
		}
		infos = append(infos, info)
	}
	traceID := newID()
	t := now()
	// The hidden trace is identified by its notice and participants, never by
	// a synthetic conversation title that would leak into the read-only header.
	if _, err = tx.Exec(`INSERT INTO conversations(id,kind,name,updated_at,user_visible) VALUES(?,?,?,?,0)`, traceID, "group", "", t); err != nil {
		return Message{}, nil, err
	}
	for _, member := range append([]string{sender}, unique...) {
		if _, err = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, traceID, member); err != nil {
			return Message{}, nil, err
		}
	}
	childIDs := make([]string, len(infos))
	for i := range childIDs {
		childIDs[i] = newID()
	}
	toIDs := append([]string(nil), unique...)
	notice := func(runID string) *HandoffNotice {
		value := &HandoffNotice{Type: "message", FromBotID: sender, ToBotID: toIDs[0], ToBotIDs: toIDs, TargetConversationID: traceID, TargetRunID: runID, TargetBotIDs: toIDs}
		if len(childIDs) > 1 {
			value.TargetRunID = ""
			value.TargetRunIDs = append([]string(nil), childIDs...)
		}
		return value
	}
	traceNotice := notice("")
	noticeData, _ := json.Marshal(traceNotice)
	var traceSeq int64
	if err = tx.QueryRow(nextMessageSeqSQL, traceID, traceID, streamDraftActive).Scan(&traceSeq); err != nil {
		return Message{}, nil, err
	}
	traceMessage := Message{ID: newID(), ConversationID: traceID, Seq: traceSeq, Role: "assistant", Kind: "notice", SenderBotID: sender, RunID: childIDs[0], Content: content, Notice: traceNotice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, traceMessage.ID, traceID, traceSeq, traceMessage.Role, traceMessage.Kind, sender, childIDs[0], content, string(noticeData), t); err != nil {
		return Message{}, nil, err
	}
	runs := make([]Run, 0, len(infos))
	for i, info := range infos {
		var queueSeq int64
		if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, traceID).Scan(&queueSeq); err != nil {
			return Message{}, nil, err
		}
		run := Run{ID: childIDs[i], ConversationID: traceID, BotID: info.id, Status: "queued", ParentRunID: parent, Model: info.model, Kind: runKindMessage, OriginConversationID: origin, TriggerMessageID: traceMessage.ID, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
		if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, traceID, run.BotID, run.Status, nil, parent, 0, run.Model, run.Kind, origin, traceMessage.ID, queueSeq, t, t); err != nil {
			return Message{}, nil, err
		}
		runs = append(runs, run)
	}
	makeCapsule := func(conv, label string, runID string) (Message, error) {
		var seq int64
		if err := tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
			return Message{}, err
		}
		capsuleNotice := notice(runID)
		data, _ := json.Marshal(capsuleNotice)
		capsule := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "message_ref", SenderBotID: sender, RunID: runID, Content: label, Notice: capsuleNotice, CreatedAt: t}
		if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, capsule.ID, conv, seq, capsule.Role, capsule.Kind, sender, runID, label, string(data), t); err != nil {
			return Message{}, err
		}
		return capsule, nil
	}
	sourceLabel := fmt.Sprintf("Messaged %d Bots", len(infos))
	if len(infos) == 1 {
		sourceLabel = "Messaged " + infos[0].name
	}
	sourceCapsule, err := makeCapsule(origin, sourceLabel, childIDs[0])
	if err != nil {
		return Message{}, nil, err
	}
	for i, info := range infos {
		if _, err = makeCapsule(info.conversation, "Message from "+senderName, childIDs[i]); err != nil {
			return Message{}, nil, err
		}
	}
	emit := func(conversation, eventType string, value any) error {
		data, _ := json.Marshal(value)
		_, emitErr := tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conversation, eventType, string(data), t)
		return emitErr
	}
	if err = emit(traceID, "message", traceMessage); err != nil {
		return Message{}, nil, err
	}
	for _, run := range runs {
		if err = emit(traceID, "run", run); err != nil {
			return Message{}, nil, err
		}
	}
	if err = emit(origin, "message", sourceCapsule); err != nil {
		return Message{}, nil, err
	}
	for i, info := range infos {
		var capsule Message
		if err = scanMessageTx(tx, info.conversation, childIDs[i], &capsule); err != nil {
			return Message{}, nil, err
		}
		if err = emit(info.conversation, "message", capsule); err != nil {
			return Message{}, nil, err
		}
	}
	updated := map[string]bool{traceID: true, origin: true}
	for _, info := range infos {
		updated[info.conversation] = true
	}
	for conversation := range updated {
		if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conversation); err != nil {
			return Message{}, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Message{}, nil, err
	}
	return sourceCapsule, runs, nil
}

// botUserMessageEligible uses durable conversation visibility and membership,
// not request text or a caller's Run snapshot. Hidden assignments can resume
// as group tasks/followups (including retries) without becoming user chats.
// Check again in the publication transaction: tool discovery is not authority.
func botUserMessageEligible(q interface{ QueryRow(string, ...any) *sql.Row }, bot, runID, conversationID string) (bool, error) {
	var eligible bool
	err := q.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM runs r
		JOIN conversations c ON c.id=r.conversation_id
		JOIN bots b ON b.id=r.bot_id
		JOIN members m ON m.conversation_id=c.id AND m.bot_id=b.id
		WHERE r.id=? AND r.bot_id=? AND r.conversation_id=? AND r.status='running'
		AND r.kind IN (?,?,?) AND c.kind='group' AND c.user_visible=0
		AND c.archived=0 AND b.archived=0
	)`, runID, bot, conversationID, runKindMessage, runKindGroupTask, runKindFollowup).Scan(&eligible)
	return eligible, err
}

// AddBotUserMessage publishes an explicit message from a hidden Bot-to-Bot
// run into the sending Bot's canonical user DM. It is separate from
// FinishRun: the hidden run remains hidden while this explicit side effect is
// visible to the user.
func (s *Store) AddBotUserMessage(bot, runID, content string) (Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, errors.New("user message content is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var runConversation string
	if err = tx.QueryRow(`SELECT conversation_id FROM runs WHERE id=?`, runID).Scan(&runConversation); err != nil {
		return Message{}, err
	}
	eligible, err := botUserMessageEligible(tx, bot, runID, runConversation)
	if err != nil {
		return Message{}, err
	}
	if !eligible {
		return Message{}, errors.New("user message is only available to an active hidden Bot-to-Bot run")
	}
	var targetConversation string
	if err = tx.QueryRow(`SELECT c.id FROM bots b JOIN conversations c ON c.id=b.dm_conversation_id WHERE b.id=? AND c.kind='dm' AND c.bot_id=b.id AND c.user_visible=1`, bot).Scan(&targetConversation); err != nil {
		return Message{}, err
	}
	if err = requireCurrentMemberTx(tx, targetConversation, bot); err != nil {
		return Message{}, err
	}
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, targetConversation, targetConversation, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	t := now()
	m := Message{ID: uuid.NewString(), ConversationID: targetConversation, Seq: seq, Role: "assistant", Kind: messageKindUser, SenderBotID: bot, RunID: runID, Content: content, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, bot, runID, content, t); err != nil {
		return Message{}, err
	}
	data, _ := json.Marshal(m)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, targetConversation, "message", string(data), t); err != nil {
		return Message{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, targetConversation); err != nil {
		return Message{}, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, err
	}
	return m, nil
}

func scanMessageTx(tx *sql.Tx, conv, runID string, out *Message) error {
	var kind, sender, run, noticeData sql.NullString
	err := tx.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE conversation_id=? AND run_id=? ORDER BY seq DESC LIMIT 1`, conv, runID).Scan(&out.ID, &out.ConversationID, &out.Seq, &out.Role, &kind, &sender, &run, &out.Content, &noticeData, &out.CreatedAt)
	out.Kind, out.SenderBotID, out.RunID = kind.String, sender.String, run.String
	if noticeData.Valid {
		_ = json.Unmarshal([]byte(noticeData.String), &out.Notice)
	}
	return err
}

func appendForwardResult(tx *sql.Tx, origin, sender, runID, content string) (Message, error) {
	var err error
	t := now()
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, origin, origin, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	var targetConversation string
	_ = tx.QueryRow(`SELECT conversation_id FROM runs WHERE id=?`, runID).Scan(&targetConversation)
	var noticeData string
	if targetConversation != "" && targetConversation != origin {
		notice := &HandoffNotice{Type: "forward_result", FromBotID: sender, ToBotID: sender, TargetConversationID: targetConversation, TargetRunID: runID}
		mNotice, marshalErr := json.Marshal(notice)
		if marshalErr != nil {
			return Message{}, marshalErr
		}
		noticeData = string(mNotice)
	}
	m := Message{ID: newID(), ConversationID: origin, Seq: seq, Role: "assistant", Kind: "forward_result", SenderBotID: sender, RunID: runID, Content: content, CreatedAt: t}
	if noticeData != "" {
		_ = json.Unmarshal([]byte(noticeData), &m.Notice)
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, origin, seq, m.Role, m.Kind, sender, runID, content, nullString(noticeData), t); err != nil {
		return Message{}, err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id)
	 SELECT am.attachment_id,? FROM attachment_messages am JOIN messages source ON source.id=am.message_id
	 WHERE source.run_id=? AND source.role='assistant'`, m.ID, runID); err != nil {
		return Message{}, err
	}
	m.Attachments, err = attachmentMetadata(tx, m.ID)
	if err != nil {
		return Message{}, err
	}
	b, _ := json.Marshal(m)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, origin, "message", string(b), t); err != nil {
		return Message{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, origin); err != nil {
		return Message{}, err
	}
	return m, nil
}

// appendBotMessageResult stores the recipient's result at the sender's
// conversation boundary. A visible DM gets a private, non-event anchor so it
// can drive the sender's follow-up without showing up in the user's chat. A
// hidden trace gets a normal forward_result so the read-only Bot conversation
// remains inspectable.
func appendBotMessageResult(tx *sql.Tx, origin, sender, runID, content string) (Message, error) {
	t := now()
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, origin, origin, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	var userVisible int
	if err := tx.QueryRow(`SELECT user_visible FROM conversations WHERE id=?`, origin).Scan(&userVisible); err != nil {
		return Message{}, err
	}
	kind := messageKindBotResult
	if userVisible == 0 {
		kind = "forward_result"
	}
	m := Message{ID: newID(), ConversationID: origin, Seq: seq, Role: "assistant", Kind: kind, SenderBotID: sender, RunID: runID, Content: content, CreatedAt: t}
	if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, origin, seq, m.Role, m.Kind, sender, runID, content, t); err != nil {
		return Message{}, err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id)
	 SELECT am.attachment_id,? FROM attachment_messages am JOIN messages source ON source.id=am.message_id
	 WHERE source.run_id=? AND source.role='assistant'`, m.ID, runID); err != nil {
		return Message{}, err
	}
	var err error
	m.Attachments, err = attachmentMetadata(tx, m.ID)
	if err != nil {
		return Message{}, err
	}
	if userVisible == 0 {
		b, _ := json.Marshal(m)
		if _, err := tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, origin, "message", string(b), t); err != nil {
			return Message{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, origin); err != nil {
		return Message{}, err
	}
	return m, nil
}

func newID() string { return uuid.NewString() }

type mentionResult struct {
	IDs       []string
	Unknown   []string
	Ambiguous []string
}

func parseMentions(content string, members []Bot) mentionResult {
	byName := map[string][]string{}
	byID := map[string]string{}
	for _, b := range members {
		byName[strings.ToLower(b.Name)] = append(byName[strings.ToLower(b.Name)], b.ID)
		byID[strings.ToLower(b.ID)] = b.ID
	}
	seen := map[string]bool{}
	var out mentionResult
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len([]rune(names[i])) > len([]rune(names[j])) })
	runes := []rune(content)
	for i, ch := range runes {
		if ch != '@' || (i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1]) || runes[i-1] == '_')) {
			continue
		}
		matched := ""
		for _, name := range names {
			nr := []rune(name)
			if i+1+len(nr) > len(runes) || !strings.EqualFold(string(runes[i+1:i+1+len(nr)]), name) {
				continue
			}
			end := i + 1 + len(nr)
			if end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
				continue
			}
			// An address such as @ann@example.com is an email local-part,
			// not a mention of a member named "ann".
			if end < len(runes) && runes[end] == '@' {
				continue
			}
			matched = name
			break
		}
		key := matched
		if key == "" {
			end := i + 1
			for end < len(runes) && !unicode.IsSpace(runes[end]) && !strings.ContainsRune("()[]{}<>.,!?;:\"'，。！？；：、", runes[end]) {
				end++
			}
			if end == i+1 {
				continue
			}
			if strings.ContainsRune(string(runes[i+1:end]), '@') {
				continue
			}
			key = string(runes[i+1 : end])
		}
		ids := byName[key]
		if len(ids) == 0 {
			if id := byID[key]; id != "" {
				ids = []string{id}
			}
		}
		if len(ids) == 0 {
			out.Unknown = append(out.Unknown, key)
			continue
		}
		if len(ids) > 1 {
			out.Ambiguous = append(out.Ambiguous, key)
			continue
		}
		if !seen[ids[0]] {
			out.IDs = append(out.IDs, ids[0])
			seen[ids[0]] = true
		}
	}
	return out
}

func (s *Server) triageModelName() string {
	return s.backgroundModel(backgroundTriage)
}

func (s *Server) memberBots(c Conversation) ([]Bot, error) {
	out := make([]Bot, 0, len(c.BotIDs))
	for _, id := range c.BotIDs {
		b, err := s.store.GetBot(id)
		if err != nil {
			return nil, err
		}
		if b.Archived {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func strictTriageTarget(raw string, members []Bot) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("triage returned an empty target")
	}
	var obj struct {
		BotID string `json:"bot_id"`
	}
	if strings.HasPrefix(raw, "{") {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&obj) != nil || obj.BotID == "" {
			return "", errors.New("triage returned invalid JSON")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return "", errors.New("triage returned extra data")
		}
		raw = obj.BotID
	}
	for _, b := range members {
		if raw == b.ID || strings.EqualFold(raw, b.Name) {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("triage selected non-member %q", raw)
}

func leadingMentionTarget(content string, c Conversation, s *Server) (string, string) {
	members, err := s.memberBots(c)
	if err != nil {
		return "", ""
	}
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "@") {
		return "", ""
	}
	parsed := parseMentions(trimmed, members)
	if len(parsed.IDs) != 1 || len(parsed.Unknown) != 0 || len(parsed.Ambiguous) != 0 {
		return "", ""
	}
	// Only a leading mention dispatches. Remove the exact name and adjacent
	// punctuation so the child receives the actual assignment text.
	for _, member := range members {
		if member.ID != parsed.IDs[0] {
			continue
		}
		prefix := "@" + member.Name
		if len([]rune(trimmed)) < len([]rune(prefix)) || !strings.EqualFold(string([]rune(trimmed)[:len([]rune(prefix))]), prefix) {
			continue
		}
		task := strings.TrimSpace(trimmed[len(prefix):])
		task = strings.TrimLeft(task, " \t,，:：-—")
		return member.ID, task
	}
	return "", ""
}

func hasHandoff(s *Store, id string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, id).Scan(&n)
	return n > 0
}

// Workers serve visible conversations and durable queued work in hidden Bot
// traces. A restart must not strand hidden messages merely because those
// traces are intentionally absent from the sidebar.
func (s *Store) workerConversationIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT c.id FROM conversations c
		WHERE c.archived=0 AND (c.user_visible=1 OR EXISTS (
			SELECT 1 FROM runs r WHERE r.conversation_id=c.id AND (r.status='queued' OR (r.status='waiting' AND EXISTS(SELECT 1 FROM run_input_waits w WHERE w.run_id=r.id)) OR (r.status='running' AND EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=r.id AND e.state='claimed')))))
		ORDER BY c.id`)
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

func (s *Server) startConversationWorker(conv string) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	if s.queues == nil {
		s.queues = map[string]*conversationQueue{}
	}
	if s.queues[conv] != nil {
		q := s.queues[conv]
		s.mu.Unlock()
		q.signal()
		return
	}
	q := newConversationQueue()
	s.queues[conv] = q
	s.mu.Unlock()
	go s.runConversationWorker(conv, q)
	q.signal()
}

func (s *Server) stopConversationWorkers() {
	s.mu.Lock()
	s.closing = true
	queues := make([]*conversationQueue, 0, len(s.queues))
	for _, q := range s.queues {
		q.stopNow()
		queues = append(queues, q)
	}
	for _, cancel := range s.runs {
		cancel()
	}
	s.mu.Unlock()
	for _, q := range queues {
		<-q.done
	}
}
func (s *Server) wakeConversationWorkers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, q := range s.queues {
		q.signal()
	}
}

func (s *Server) runConversationWorker(conv string, q *conversationQueue) {
	defer close(q.done)
	for {
		select {
		case <-q.stop:
			return
		default:
		}
		// Expiry settlement is independent of provider availability. Never leave
		// a parked approval indefinitely waiting when the model disconnects.
		r, ok, err := s.store.nextQueuedRun(conv)
		s.mu.Lock()
		active := s.runs[r.ID] != nil
		s.mu.Unlock()
		if err == nil && ok && !active && (s.modelConfigured() || s.store.hasApprovalExpiry(r.ID)) {
			s.executeForQueue(r)
			if !s.store.hasApprovalExpirySettlement(conv) {
				continue
			}
		}
		// Pending input has a bounded expiry even without an HTTP answer. Poll
		// only conversations with parked input, and release the worker between
		// checks. This also covers answers committed by internal callers.
		var tick <-chan time.Time
		var timer *time.Timer
		if err != nil || s.store.hasInputWait(conv) || s.store.hasApprovalExpirySettlement(conv) {
			timer = time.NewTimer(time.Second)
			tick = timer.C
		}
		select {
		case <-q.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-q.wake:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
func (s *Server) executeForQueue(r Run) {
	c, err := s.store.GetConversation(r.ConversationID)
	if err != nil {
		_, _ = s.store.SetRunStatus(r.ID, "failed", err.Error())
		return
	}
	if r.Kind == runKindGroupChat {
		// An explicit assignment already solicited this member in this round.
		var count int
		_ = s.store.db.QueryRow(roundFamilySQL+` SELECT COUNT(*) FROM runs WHERE id IN (SELECT id FROM family) AND bot_id=? AND kind='group_task'`, r.ID, r.BotID).Scan(&count)
		if count > 0 {
			if changed, _ := s.store.SetRunStatus(r.ID, "done", ""); changed {
				done, _ := s.store.GetRun(r.ID)
				_, _ = s.store.Event(c.ID, "run", done)
			}
			return
		}
	}
	s.execute(c, r)
}
func (s *Store) nextQueuedRun(conv string) (Run, bool, error) {
	if err := s.refreshInputWaits(conv); err != nil {
		return Run{}, false, err
	}
	row := s.db.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE conversation_id=? AND (status='queued' OR (status='running' AND EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=runs.id AND e.state='claimed' AND e.retry_after<=?))) ORDER BY CASE WHEN EXISTS(SELECT 1 FROM approval_expiry_recoveries e WHERE e.run_id=runs.id AND e.state IN ('ready','claimed')) THEN -1 WHEN kind IN ('group_task','group_followup') THEN 0 ELSE 1 END,queue_seq,created_at,id LIMIT 1`, conv, now())
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return Run{}, false, nil
	}
	return r, err == nil, err
}

func interruptInTransaction(tx *sql.Tx, conv string) error {
	// A new explicit mention is durable steering input. Leave running work
	// alone until its stream/tool/model safe boundary; queued stale work can be
	// cancelled immediately and the new run will carry any completed evidence.
	rows, err := tx.Query(`WITH RECURSIVE roots(id,trigger_message_id,parent_run_id) AS (
		SELECT id,trigger_message_id,parent_run_id FROM runs WHERE conversation_id=? AND status='running'
		UNION SELECT parent.id,parent.trigger_message_id,parent.parent_run_id FROM runs parent JOIN roots child ON parent.id=child.parent_run_id
	), family(id) AS (
		SELECT id FROM roots
		UNION SELECT child.id FROM runs child JOIN family parent ON child.parent_run_id=parent.id
	), steering_successors(id) AS (
		SELECT new_run_id FROM run_steering WHERE old_run_id IN (SELECT id FROM family)
	)
	SELECT queued.id FROM runs queued
	WHERE queued.conversation_id=? AND queued.status='queued' AND queued.kind<>'schedule'
	AND (queued.trigger_message_id IN (SELECT trigger_message_id FROM roots WHERE trigger_message_id<>'')
		OR queued.parent_run_id IN (SELECT id FROM family)
		OR queued.id IN (SELECT id FROM steering_successors))`, conv, conv)
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
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE runs SET status='cancelled',error='interrupted by user mention',updated_at=? WHERE id=? AND status='queued'`, now(), id); err != nil {
			return err
		}
		r, scanErr := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, id))
		if scanErr != nil {
			return scanErr
		}
		data, _ := json.Marshal(r)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, r.ConversationID, "run", string(data), now()); err != nil {
			return err
		}
	}
	return nil
}
func cancelTreeInTransaction(tx *sql.Tx, seed string, args ...any) error {
	rows, err := tx.Query(`WITH RECURSIVE seeds(id) AS (`+seed+`), ancestors(id,parent_run_id,kind,trigger_message_id) AS (
 SELECT r.id,r.parent_run_id,r.kind,r.trigger_message_id FROM runs r JOIN seeds ON seeds.id=r.id
 UNION SELECT r.id,r.parent_run_id,r.kind,r.trigger_message_id FROM runs r JOIN ancestors a ON a.parent_run_id=r.id
 ), affected(id) AS (
 SELECT id FROM seeds
 UNION SELECT r.id FROM runs r JOIN ancestors a ON a.kind='group_chat' AND r.kind='group_chat' AND r.trigger_message_id=a.trigger_message_id
 UNION SELECT r.id FROM runs r JOIN affected a ON r.parent_run_id=a.id
 ) SELECT id FROM runs WHERE id IN (SELECT id FROM affected) AND status IN ('queued','running','waiting')`, args...)
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
	for _, id := range ids {
		var parked bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits WHERE run_id=? AND state='waiting')`, id).Scan(&parked); err != nil {
			return err
		}
		if parked {
			if err = interruptToolActivitiesTx(tx, id, now()); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE stream_drafts SET status='cancelled',updated_at=? WHERE run_id=? AND status='active'`, now(), id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`UPDATE runs SET status='cancelled',error='interrupted by user mention',updated_at=? WHERE id=?`, now(), id); err != nil {
			return err
		}
		r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, id))
		if err != nil {
			return err
		}
		data, _ := json.Marshal(r)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, r.ConversationID, "run", string(data), now()); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) cancelInactiveRuns() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		r, err := s.store.GetRun(id)
		if err == nil && r.Status == "cancelled" {
			s.mu.Lock()
			cancel := s.runs[id]
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
	s.clearInactiveRunSecrets()
	s.wakeTerminalCleanup()
}
func (s *Server) interruptConversation(conv string) error {
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = interruptInTransaction(tx, conv); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.cancelInactiveRuns()
	return nil
}

func (s *Store) CancelRunTree(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = cancelTreeInTransaction(tx, `SELECT id FROM runs WHERE id=?`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.cleanupRunTreeAttachments(id)
	return nil
}
