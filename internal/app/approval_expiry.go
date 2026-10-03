package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

func migrateApprovalExpiry(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS approval_expiry_recoveries(
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
 checkpoint_json TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('ready','claimed','finished')),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 summary_message_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(run_id,question_id));`)
	return err
}

// Expiry and its queue entry commit together. The immutable checkpoint and
// approval/effect bindings survive terminal cleanup in this recovery record.
func enqueueApprovalExpiryTx(tx *sql.Tx, q Question) error {
	if q.Type != questionApproval || q.Status != questionExpired {
		return nil
	}
	var checkpoint, runStatus, runID, waitState string
	err := tx.QueryRow(`SELECT w.checkpoint_json,r.status,w.run_id,w.state FROM run_input_waits w JOIN runs r ON r.id=w.run_id WHERE w.question_id=?`, q.ID).Scan(&checkpoint, &runStatus, &runID, &waitState)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if runStatus != "waiting" && runStatus != "queued" {
		return nil
	}
	t := now()
	result, err := tx.Exec(`INSERT INTO approval_expiry_recoveries(run_id,question_id,checkpoint_json,state,created_at,updated_at) VALUES(?,?,?,'ready',?,?) ON CONFLICT DO NOTHING`, runID, q.ID, checkpoint, t, t)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	if _, err = tx.Exec(`UPDATE runs SET status='queued',error='approval_expiry_recovery',updated_at=? WHERE id=? AND status='waiting'`, t, runID); err != nil {
		return err
	}
	r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
	if err != nil {
		return err
	}
	if err = settleExpiryActivitiesTx(tx, r, q, checkpoint); err != nil {
		return err
	}
	if waitState == "claimed" {
		return settleApprovalExpiryTx(tx, runID, "")
	}
	return insertRecoveryEvent(tx, r.ConversationID, "run", r, t)
}

func (s *Store) expireApproval(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return err
	}
	if q.Status == questionPending || q.Status == questionAnswered {
		// A dispatch claim is evidence of a possible effect, never permission to replay.
		q.Status, q.UpdatedAt = questionExpired, now()
		if _, err = tx.Exec(`UPDATE questions SET status='expired',updated_at=? WHERE id=?`, q.UpdatedAt, id); err != nil {
			return err
		}
		if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), q.UpdatedAt); err != nil {
			return err
		}
	}
	if err = enqueueApprovalExpiryTx(tx, q); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) hasApprovalExpiry(runID string) bool {
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM approval_expiry_recoveries WHERE run_id=? AND state='ready')`, runID).Scan(&found)
	return found
}

func (s *Store) claimApprovalExpiry(ctx context.Context, runID string) (Question, json.RawMessage, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Question{}, nil, false, err
	}
	defer tx.Rollback()
	var id, data string
	err = tx.QueryRow(`SELECT e.question_id,e.checkpoint_json FROM approval_expiry_recoveries e JOIN runs r ON r.id=e.run_id WHERE e.run_id=? AND e.state='ready' AND r.status='queued'`, runID).Scan(&id, &data)
	if err == sql.ErrNoRows {
		return Question{}, nil, false, nil
	}
	if err != nil {
		return Question{}, nil, false, err
	}
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return q, nil, false, err
	}
	t := now()
	result, err := tx.Exec(`UPDATE approval_expiry_recoveries SET state='claimed',updated_at=? WHERE run_id=? AND question_id=? AND state='ready'`, t, runID, id)
	if err != nil {
		return q, nil, false, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return q, nil, false, nil
	}
	if _, err = tx.Exec(`UPDATE runs SET status='running',error='approval_expiry_recovery',updated_at=? WHERE id=? AND status='queued'`, t, runID); err != nil {
		return q, nil, false, err
	}
	r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
	if err != nil {
		return q, nil, false, err
	}
	if err = insertRecoveryEvent(tx, r.ConversationID, "run", r, t); err != nil {
		return q, nil, false, err
	}
	return q, json.RawMessage(data), true, tx.Commit()
}

func (s *Store) settleApprovalExpiry(runID, content string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = settleApprovalExpiryTx(tx, runID, content); err != nil {
		return err
	}
	return tx.Commit()
}

func settleApprovalExpiryTx(tx *sql.Tx, runID, content string) error {
	var id, data, state string
	err := tx.QueryRow(`SELECT question_id,checkpoint_json,state FROM approval_expiry_recoveries WHERE run_id=? ORDER BY created_at LIMIT 1`, runID).Scan(&id, &data, &state)
	if err == sql.ErrNoRows || state == "finished" {
		return nil
	}
	if err != nil {
		return err
	}
	r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
	if err != nil {
		return err
	}
	if r.Status != "running" && r.Status != "queued" && r.Status != "waiting" {
		return nil
	}
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return err
	}
	t := now()
	validationErr := settleExpiryActivitiesTx(tx, r, q, data)
	if validationErr != nil {
		return validationErr
	}
	_, checkpointErr := runtime.ApprovalExpiryCheckpoint(json.RawMessage(data), r.ID, r.BotID, q.ID, r.Model)
	var claimed bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE run_id=? AND claimed_at<>'')`, runID).Scan(&claimed); err != nil {
		return err
	}
	var completed, notExecuted, uncertain int
	if err = tx.QueryRow(`SELECT COALESCE(SUM(status='completed'),0),COALESCE(SUM(json_extract(NULLIF(outcome_json,''),'$.execution_certainty')='not_executed'),0),COALESCE(SUM(json_extract(NULLIF(outcome_json,''),'$.execution_certainty')='unknown'),0) FROM tool_activities WHERE run_id=?`, runID).Scan(&completed, &notExecuted, &uncertain); err != nil {
		return err
	}
	conclusion := fmt.Sprintf("审批已过期，本次工作已停止。记录保留：%d 个工具步骤已完成，%d 个步骤未执行。", completed, notExecuted)
	if uncertain > 0 || claimed || checkpointErr != nil || q.RunID != r.ID || q.BotID != r.BotID || q.ConversationID != r.ConversationID {
		conclusion += "存在结果不确定或无法验证的步骤；再次操作前需要核实结果。"
	} else {
		conclusion += "需要审批的操作未执行。"
	}
	conclusion += "未完成的工作不会自动重试。"
	if strings.TrimSpace(content) != "" {
		conclusion += "\n\n" + strings.TrimSpace(content)
	}
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, r.ConversationID, r.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return err
	}
	m := Message{ID: uuid.NewString(), ConversationID: r.ConversationID, Seq: seq, Role: "assistant", SenderBotID: r.BotID, RunID: r.ID, Content: conclusion, CreatedAt: t}
	// The deterministic conclusion is explicitly system-backed, including when
	// the model is missing, returns empty, or the claimed recovery crashed.
	if content == "" {
		m.Kind = "notice"
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,sender_bot_id,run_id,content,created_at,kind) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.SenderBotID, m.RunID, m.Content, t, m.Kind); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE approval_expiry_recoveries SET state='finished',summary_message_id=?,updated_at=? WHERE run_id=? AND question_id=? AND state<>'finished'`, m.ID, t, runID, id); err != nil {
		return err
	}
	r.Status, r.Error, r.UpdatedAt = "failed", "approval_expired", t
	if _, err = tx.Exec(`UPDATE runs SET status='failed',error='approval_expired',updated_at=? WHERE id=?`, t, runID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE stream_drafts SET status='cancelled',updated_at=? WHERE run_id=? AND status='active'`, t, runID); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, r.ConversationID, "message", m, t); err != nil {
		return err
	}
	return insertRecoveryEvent(tx, r.ConversationID, "run", r, t)
}

// Only ordinary startup calls this. Maintenance opens migrate schemas only.
// A claimed recovery is terminalized without invoking any model or tool.
func (s *Store) recoverClaimedApprovalExpiry() error {
	rows, err := s.db.Query(`SELECT DISTINCT run_id FROM approval_expiry_recoveries WHERE state='claimed'`)
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
		if err = s.settleApprovalExpiry(id, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) executeApprovalExpiry(c Conversation, r Run) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.mu.Lock()
	if s.closing || s.runs[r.ID] != nil {
		s.mu.Unlock()
		return
	}
	s.runs[r.ID] = cancel
	engine := s.engine
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.runs, r.ID); s.mu.Unlock(); s.clearRunSecrets(r.ID) }()
	q, checkpoint, ok, err := s.store.claimApprovalExpiry(ctx, r.ID)
	if err != nil || !ok {
		return
	}
	content := ""
	_, validationErr := runtime.ApprovalExpiryCheckpoint(checkpoint, r.ID, r.BotID, q.ID, r.Model)
	var unsafe bool
	if err = s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE run_id=? AND claimed_at<>'')`, r.ID).Scan(&unsafe); err != nil {
		unsafe = true
	}
	if engine != nil && validationErr == nil && !unsafe && q.Type == questionApproval && q.Status == questionExpired && q.RunID == r.ID && q.BotID == r.BotID && q.ConversationID == c.ID {
		b, e := s.store.GetBot(r.BotID)
		if e == nil && !b.Archived && !c.Archived {
			_, system := s.buildContextParts(c, r, b)
			// These local conversation-scoped observations cannot dispatch an external
			// effect. Do not prepare extensions or expose discovery/provider wrappers.
			var tools []Tool
			for _, t := range s.tools(c, r) {
				if t.ApprovalExpiryReadOnly {
					tools = append(tools, t)
				}
			}
			res, e := engine.Run(ctx, Request{BotID: r.BotID, RunID: r.ID, Model: r.Model, ReasoningEffort: b.ReasoningEffort, System: system, Continuation: checkpoint, ApprovalExpiryRecovery: true, Tools: tools, OnToolEvent: func(ev runtime.ToolEvent) error { return s.store.RecordToolEvent(c.ID, r.BotID, r.ID, ev) }, OnUsage: func(input, output int64) { _ = s.store.recordModelUsage(r.ID, input, output) }})
			if e == nil {
				content = cleanBotOutput(res.Content, b)
			}
		}
	}
	// Shutdown/cancellation still concludes this claimed workflow; no replay.
	if err = s.store.settleApprovalExpiry(r.ID, content); err != nil {
		log.Printf("[expiry] conclude run %s: %v", r.ID, err)
	}
}

func (s *Store) reconcileLegacyApprovalExpiry() error {
	rows, err := s.db.Query(`SELECT q.id,q.status,COALESCE(q.expires_at,'') FROM questions q JOIN run_input_waits w ON w.question_id=q.id JOIN runs r ON r.id=w.run_id WHERE q.type='approval' AND r.status IN ('waiting','queued')`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id, status, expires string
		if err = rows.Scan(&id, &status, &expires); err != nil {
			rows.Close()
			return err
		}
		if status == questionExpired || status == questionPending && expiredTaskDeadline(expires) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.expireApproval(id); err != nil {
			return err
		}
	}
	return nil
}

func settleExpiryActivitiesTx(tx *sql.Tx, r Run, q Question, data string) error {
	runID := r.ID
	waiting, validationErr := runtime.ApprovalExpiryCheckpoint(json.RawMessage(data), r.ID, r.BotID, q.ID, r.Model)
	var claimed bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE run_id=? AND claimed_at<>'')`, runID).Scan(&claimed); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? AND status IN ('queued','running')`, runID)
	if err != nil {
		return err
	}
	var activities []ToolActivity
	for rows.Next() {
		a, e := scanToolActivity(rows)
		if e != nil {
			rows.Close()
			return e
		}
		activities = append(activities, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	t := now()
	for _, a := range activities {
		o := tooloutcome.New(tooloutcome.Uncertain, "expiry_interrupted_effect", "unknown", "Execution was interrupted. Verify the recorded target state before any new workflow attempts this effect.", "verify_effect")
		if a.Status == "queued" {
			o = tooloutcome.New(tooloutcome.Permanent, "batch_skipped", "not_executed", "Queued call was not executed after approval expiry.", "finish_summary")
		}
		if a.CallID == waiting && validationErr == nil && !claimed {
			o = tooloutcome.New(tooloutcome.Expired, "approval_window_expired", "not_executed", "Approval expired; this call was not executed.", "finish_summary")
		}
		a.Status, a.Result, a.Outcome, a.UpdatedAt = "failed", o.JSON(), &o, t
		if _, err = tx.Exec(`UPDATE tool_activities SET status='failed',result=?,outcome_json=?,updated_at=? WHERE run_id=? AND call_id=?`, a.Result, o.JSON(), t, runID, a.CallID); err != nil {
			return err
		}
		if err = insertRecoveryEvent(tx, r.ConversationID, "tool", a, t); err != nil {
			return err
		}
	}
	return nil
}
