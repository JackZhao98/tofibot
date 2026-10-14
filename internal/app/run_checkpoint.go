package app

// Boundary checkpoints let a run that was running when the process died resume
// from its last model-response or tool-result boundary. They are a second
// durable rendezvous next to run_input_waits (human input), and the claim in
// claimRun is the single queued->running transition for both.
//
// What a crash can lose is exactly the model call in flight: its partial text
// is cancelled with the stream draft and the turn is regenerated. A tool that
// was running is never re-run on faith: it is settled uncertain (or retryable
// when read-only) and the model is told so before it continues.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// maxAutomaticResumes is owner decision D2: a run is resumed automatically at
// most this many times; the next interruption fails it.
const maxAutomaticResumes = 3

// resumedRepeatedlyCode prefixes the error of a run stopped by that cap.
const resumedRepeatedlyCode = "resumed_repeatedly"

var errStaleCheckpoint = errors.New("run checkpoint written by a stale attempt")

func migrateRunCheckpoints(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_checkpoints(
		run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
		attempt INTEGER NOT NULL,
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL CHECK(kind IN ('turn','tool')),
		checkpoint_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TRIGGER IF NOT EXISTS run_checkpoint_terminal_cleanup AFTER UPDATE OF status ON runs
	WHEN NEW.status NOT IN ('queued','running')
	BEGIN
		DELETE FROM run_checkpoints WHERE run_id=NEW.id;
	END;`); err != nil {
		return err
	}
	for _, col := range []struct{ name, ddl string }{
		{"attempt", `ALTER TABLE runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`},
		{"resumes", `ALTER TABLE runs ADD COLUMN resumes INTEGER NOT NULL DEFAULT 0`},
		{"resume_notice", `ALTER TABLE runs ADD COLUMN resume_notice TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(db, "runs", col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

// SaveRunCheckpoint stores the boundary checkpoint for the claim generation
// that is still running the run. A writer from another generation, or for a
// run that is no longer running, is refused. An empty checkpoint (the boundary
// could not be snapshotted) or one over the size limit removes the stored one
// instead: the run then falls back to the interrupted-on-restart behavior
// rather than resuming from a stale boundary.
func (s *Store) SaveRunCheckpoint(ctx context.Context, runID string, attempt int, kind string, checkpoint json.RawMessage) error {
	if kind != "turn" && kind != "tool" {
		return fmt.Errorf("invalid checkpoint kind %q", kind)
	}
	if len(checkpoint) > 0 && !json.Valid(checkpoint) {
		return errors.New("invalid run checkpoint")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT status,attempt FROM runs WHERE id=?`, runID).Scan(&status, &current); err != nil {
		return err
	}
	if status != "running" {
		return context.Canceled
	}
	if current != attempt {
		return errStaleCheckpoint
	}
	if len(checkpoint) == 0 || len(checkpoint) > maxInputCheckpointBytes {
		if len(checkpoint) > 0 {
			log.Printf("[run] checkpoint for %s is %d bytes, over the %d limit; the run will not be resumable", runID, len(checkpoint), maxInputCheckpointBytes)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM run_checkpoints WHERE run_id=?`, runID); err != nil {
			return err
		}
		return tx.Commit()
	}
	t := now()
	res, err := tx.ExecContext(ctx, `INSERT INTO run_checkpoints(run_id,attempt,seq,kind,checkpoint_json,created_at,updated_at) VALUES(?,?,1,?,?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET
			seq=CASE WHEN run_checkpoints.attempt=excluded.attempt THEN run_checkpoints.seq+1 ELSE 1 END,
			attempt=excluded.attempt, kind=excluded.kind, checkpoint_json=excluded.checkpoint_json, updated_at=excluded.updated_at
		WHERE run_checkpoints.attempt<=excluded.attempt`, runID, attempt, kind, string(checkpoint), t, t)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errStaleCheckpoint
	}
	return tx.Commit()
}

// runClaim is the result of the queued->running transition.
type runClaim struct {
	// Continuation is the opaque checkpoint to resume from: a human-input
	// wait's, else a boundary checkpoint's, else empty for a fresh start.
	Continuation json.RawMessage
	Question     Question
	Boundary     bool
	Attempt      int
	// ResumeNotice (e.g. "restart") was recorded by recovery and is consumed
	// here; the caller shows it once.
	ResumeNotice string
}

// claimRun is the only queued->running transition. The status update comes
// first and must touch exactly one row, so concurrent claimers cannot both
// win; attempt increases by one per claim. A human-input wait is claimed
// before any boundary checkpoint, and discards the older checkpoint it
// supersedes. A boundary checkpoint must have been written by an earlier
// attempt than the one being claimed.
func (s *Store) claimRun(ctx context.Context, runID string) (runClaim, bool, error) {
	var claim runClaim
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return claim, false, err
	}
	defer tx.Rollback()
	var prior string
	if err = tx.QueryRowContext(ctx, `SELECT resume_notice FROM runs WHERE id=? AND status='queued'`, runID).Scan(&prior); err != nil {
		if err == sql.ErrNoRows {
			return claim, false, nil
		}
		return claim, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status='running',error=NULL,attempt=attempt+1,resume_notice='',updated_at=? WHERE id=? AND status='queued'`, now(), runID)
	if err != nil {
		return claim, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return claim, false, nil
	}
	if err = tx.QueryRowContext(ctx, `SELECT attempt FROM runs WHERE id=?`, runID).Scan(&claim.Attempt); err != nil {
		return claim, false, err
	}
	claim.ResumeNotice = prior

	var data, questionID, state string
	err = tx.QueryRowContext(ctx, `SELECT checkpoint_json,question_id,state FROM run_input_waits WHERE run_id=?`, runID).Scan(&data, &questionID, &state)
	if err != nil && err != sql.ErrNoRows {
		return claim, false, err
	}
	if err == nil {
		if state != "waiting" {
			return claim, false, errors.New("input continuation already claimed")
		}
		q, qe := scanQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id=?`, questionID))
		if qe != nil {
			return claim, false, qe
		}
		if q.RunID != runID || (q.Type == questionApproval && q.Status == questionExpired) || (q.Status != questionAnswered && q.Status != questionCancelled && q.Status != questionExpired) {
			return claim, false, errors.New("input continuation is not ready")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE run_input_waits SET state='claimed' WHERE run_id=? AND state='waiting'`, runID); err != nil {
			return claim, false, err
		}
		// The wait is newer than any boundary snapshot; resuming that one after
		// a crash would replay the human question.
		if _, err = tx.ExecContext(ctx, `DELETE FROM run_checkpoints WHERE run_id=?`, runID); err != nil {
			return claim, false, err
		}
		claim.Continuation, claim.Question = json.RawMessage(data), q
	} else {
		var cpAttempt int
		err = tx.QueryRowContext(ctx, `SELECT attempt,checkpoint_json FROM run_checkpoints WHERE run_id=?`, runID).Scan(&cpAttempt, &data)
		if err != nil && err != sql.ErrNoRows {
			return claim, false, err
		}
		if err == nil {
			if cpAttempt >= claim.Attempt {
				return claim, false, fmt.Errorf("run checkpoint attempt %d is not older than claim attempt %d", cpAttempt, claim.Attempt)
			}
			claim.Continuation, claim.Boundary = json.RawMessage(data), true
		}
	}
	if err = markScheduleStartedTx(tx, runID); err != nil {
		return claim, false, err
	}
	if err = tx.Commit(); err != nil {
		return claim, false, err
	}
	return claim, true, nil
}

// boundarySettlements tells the resumed model what happened to each call of
// the checkpoint's last turn that has no result in the transcript. Recovery
// has already closed their activity rows, so this only reads them back: a
// call that finished after the checkpoint (crash before the next one was
// written) returns its recorded result, an interrupted or skipped call its
// recorded outcome, and a call with no row is resumed as not executed.
func (s *Store) boundarySettlements(runID, botID string, checkpoint json.RawMessage) (map[string]agent.ResumeToolResult, error) {
	c, err := runtime.BoundaryCheckpoint(checkpoint, runID, botID)
	if err != nil {
		return nil, err
	}
	settled := map[string]agent.ResumeToolResult{}
	for _, call := range c.SkippedToolCalls {
		row := s.db.QueryRow(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? AND call_id=?`, runID, call.ID)
		a, err := scanToolActivity(row)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		switch a.Status {
		case "completed":
			settled[call.ID] = agent.ResumeToolResult{Content: a.Result, Outcome: a.Outcome}
		case "failed":
			content := a.Result
			if content == "" && a.Outcome != nil {
				content = a.Outcome.JSON()
			}
			settled[call.ID] = agent.ResumeToolResult{Content: content, Outcome: a.Outcome, Failed: true}
		case "running", "interrupted":
			o := agent.RestartInterruptedOutcome(restartObservation(a))
			settled[call.ID] = agent.ResumeToolResult{Content: o.JSON(), Outcome: &o, Failed: true}
		}
	}
	return settled, nil
}

// restartObservation reports whether an interrupted call is read-only by its
// static identity. Anything not known to be an observation is treated as an
// effect that must be verified.
func restartObservation(a ToolActivity) bool {
	return tooloutcome.DefaultIdentity(a.Name, json.RawMessage(a.Arguments)).Risk == tooloutcome.Observation
}

// settleRestartedActivitiesTx closes the activity rows of a run that was
// running at a restart: the running call becomes failed with the outcome the
// resumed model will read, and queued calls of its batch are not executed.
func settleRestartedActivitiesTx(tx *sql.Tx, runID, t string) error {
	rows, err := tx.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? AND status IN ('queued','running')`, runID)
	if err != nil {
		return err
	}
	activities, err := scanToolActivities(rows)
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range activities {
		o := agent.RestartInterruptedOutcome(restartObservation(a))
		if a.Status == "queued" {
			o = agent.RestartSkippedOutcome()
		}
		a.Status, a.Result, a.Outcome, a.UpdatedAt = "failed", o.JSON(), &o, t
		if _, err = tx.Exec(`UPDATE tool_activities SET status='failed',result=?,outcome_json=?,updated_at=? WHERE run_id=? AND call_id=?`, a.Result, o.JSON(), t, runID, a.CallID); err != nil {
			return err
		}
		if err = insertRecoveryEvent(tx, a.ConversationID, "tool", a, t); err != nil {
			return err
		}
	}
	return nil
}

// runHadObservableWork reports whether a run did anything a restart must not
// repeat or hide: a tool call, a published turn, or a visible message.
func runHadObservableWork(tx *sql.Tx, runID string) (bool, error) {
	var found bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tool_activities WHERE run_id=?)
		OR EXISTS(SELECT 1 FROM stream_assistant_turns WHERE run_id=?)
		OR EXISTS(SELECT 1 FROM messages WHERE run_id=? AND role='assistant' AND kind<>'notice')`, runID, runID, runID).Scan(&found)
	return found, err
}

// recoverRunningRunTx decides the fate of one run found running at startup and
// writes its state. The caller records the run event and then calls
// finishRecoveredActivitiesTx, so subscribers see the run before its tools.
func recoverRunningRunTx(tx *sql.Tx, run Run, t string) (Run, bool, error) {
	var checkpoint string
	hasCheckpoint := true
	if err := tx.QueryRow(`SELECT checkpoint_json FROM run_checkpoints WHERE run_id=?`, run.ID).Scan(&checkpoint); err != nil {
		if err != sql.ErrNoRows {
			return run, false, err
		}
		hasCheckpoint = false
	}
	if hasCheckpoint {
		if _, err := runtime.BoundaryCheckpoint(json.RawMessage(checkpoint), run.ID, run.BotID); err != nil {
			// A snapshot this build cannot read (for example written by a newer
			// build before a rollback) is not a boundary to resume from.
			log.Printf("[run] checkpoint for %s is unusable, interrupting instead of resuming: %v", run.ID, err)
			hasCheckpoint = false
		}
	}
	hadWork, err := runHadObservableWork(tx, run.ID)
	if err != nil {
		return run, false, err
	}
	// A scheduled occurrence that stopped without a checkpoint stays
	// interrupted even if nothing visible happened: the scheduler's contract is
	// that an uncertain attempt is never replayed on its own.
	if !hasCheckpoint && (hadWork || run.Kind == runKindSchedule) {
		run.Status, run.Error, run.UpdatedAt = "interrupted", "service restarted after claim", t
		if _, err = tx.Exec(`UPDATE runs SET status='interrupted',error=?,updated_at=? WHERE id=? AND status='running'`, run.Error, t, run.ID); err != nil {
			return run, false, err
		}
		_, err = tx.Exec(`UPDATE questions SET status='run_done',updated_at=? WHERE run_id=? AND status='pending'`, t, run.ID)
		return run, false, err
	}

	var resumes int
	if err = tx.QueryRow(`SELECT resumes FROM runs WHERE id=?`, run.ID).Scan(&resumes); err != nil {
		return run, false, err
	}
	resumes++
	if resumes > maxAutomaticResumes {
		run.Status, run.Error, run.UpdatedAt = "failed", fmt.Sprintf("%s:%d", resumedRepeatedlyCode, resumes), t
		_, err = tx.Exec(`UPDATE runs SET status='failed',error=?,resumes=?,updated_at=? WHERE id=? AND status='running'`, run.Error, resumes, t, run.ID)
		return run, true, err
	}
	// Nothing visible happened before the restart: re-run silently. After a
	// boundary, tell the user the run is continuing.
	notice := ""
	if hasCheckpoint {
		notice = "restart"
	}
	run.Status, run.Error, run.UpdatedAt = "queued", "", t
	_, err = tx.Exec(`UPDATE runs SET status='queued',error=NULL,resumes=?,resume_notice=?,updated_at=? WHERE id=? AND status='running'`, resumes, notice, t, run.ID)
	return run, true, err
}

// finishRecoveredActivitiesTx closes the run's in-flight tool rows: settled
// for a run that continues or is stopped for repeated interruption,
// interrupted for one that is not resumable.
func finishRecoveredActivitiesTx(tx *sql.Tx, run Run, settle bool, t string) error {
	if settle {
		return settleRestartedActivitiesTx(tx, run.ID, t)
	}
	return interruptToolActivitiesTx(tx, run.ID, t)
}

// AddResumeNotice records the one-line chat notice that a run is continuing.
func (s *Store) AddResumeNotice(r Run, reason string) (Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	t := now()
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, r.ConversationID, r.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	notice := &HandoffNotice{Type: "resume", Reason: reason, FromBotID: r.BotID, ToBotID: r.BotID, TargetConversationID: r.ConversationID, TargetRunID: r.ID}
	noticeData, _ := json.Marshal(notice)
	m := Message{ID: newID(), ConversationID: r.ConversationID, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: r.BotID, RunID: r.ID, Content: resumeNoticeText(reason), Notice: notice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, m.RunID, m.Content, string(noticeData), t); err != nil {
		return Message{}, err
	}
	if err = insertRecoveryEvent(tx, m.ConversationID, "message", m, t); err != nil {
		return Message{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, m.ConversationID); err != nil {
		return Message{}, err
	}
	return m, tx.Commit()
}

// resumeNoticeText is the stored fallback; the Web UI renders the localized
// text from notice.reason.
func resumeNoticeText(reason string) string {
	switch strings.TrimSpace(reason) {
	case "restart":
		return "Service restarted. Resuming from the last checkpoint."
	}
	return "Resuming from the last checkpoint."
}
