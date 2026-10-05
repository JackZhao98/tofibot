package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type autoReviewSettings struct {
	Mode     string `json:"mode"`
	Revision int64  `json:"revision"`
}

// Recover a missing singleton in off mode without reusing the epoch of any
// surviving decision. Purge removes those decisions too, so a fresh store is 0.
const seedAutoReviewSettings = `INSERT OR IGNORE INTO auto_review_settings(id,mode,revision) SELECT 1,'off',COALESCE(MAX(settings_revision)+1,0) FROM mcp_auto_reviews`

func migrateAutoReview(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS auto_review_settings(id INTEGER PRIMARY KEY CHECK(id=1),mode TEXT NOT NULL CHECK(mode IN ('off','shadow','auto')),revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS mcp_auto_reviews(
question_id TEXT PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,
account_id TEXT NOT NULL,conversation_id TEXT NOT NULL,run_id TEXT NOT NULL,
action_hash TEXT NOT NULL,server TEXT NOT NULL,tool TEXT NOT NULL,config_fingerprint TEXT NOT NULL,
arguments_digest TEXT NOT NULL,policy_version TEXT NOT NULL,context_digest TEXT NOT NULL,
provenance TEXT NOT NULL,mode TEXT NOT NULL,settings_revision INTEGER NOT NULL,
status TEXT NOT NULL,decision TEXT NOT NULL,reason TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS mcp_auto_reviews_run_action ON mcp_auto_reviews(run_id,action_hash);
` + seedAutoReviewSettings + `;
CREATE TABLE IF NOT EXISTS mcp_call_execution_claims(run_id TEXT NOT NULL,action_hash TEXT NOT NULL,question_id TEXT NOT NULL,claimed_at TEXT NOT NULL,PRIMARY KEY(run_id,action_hash));
INSERT OR IGNORE INTO mcp_call_execution_claims SELECT run_id,action_hash,question_id,claimed_at FROM mcp_call_approvals WHERE claimed_at<>'';
UPDATE questions SET status=CASE WHEN status='pending' AND id IN (SELECT question_id FROM mcp_auto_reviews WHERE mode='auto') THEN 'cancelled' ELSE status END,approval_json=json_set(approval_json,'$.review.status',CASE WHEN id IN (SELECT question_id FROM mcp_auto_reviews WHERE mode='shadow') THEN 'shadow_unavailable' ELSE 'unavailable' END,'$.review.reason','Review was interrupted. No policy judgment or automatic execution permission was established.'),updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id IN (SELECT question_id FROM mcp_auto_reviews WHERE status='reviewing') AND approval_json<>'';
UPDATE mcp_auto_reviews SET status=CASE WHEN mode='shadow' THEN 'shadow_unavailable' ELSE 'unavailable' END,reason='Review was interrupted. No policy judgment or automatic execution permission was established.' WHERE status='reviewing';`)
	if err != nil {
		return err
	}
	if err = ensureColumn(db, "mcp_auto_reviews", "schema_digest", `ALTER TABLE mcp_auto_reviews ADD COLUMN schema_digest TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	for _, col := range []struct{ name, ddl string }{
		{"risk_level", `ALTER TABLE mcp_auto_reviews ADD COLUMN risk_level TEXT NOT NULL DEFAULT ''`},
		{"confirmation_required", `ALTER TABLE mcp_auto_reviews ADD COLUMN confirmation_required INTEGER NOT NULL DEFAULT -1`},
		{"context_snapshot", `ALTER TABLE mcp_auto_reviews ADD COLUMN context_snapshot TEXT NOT NULL DEFAULT ''`},
	} {
		if err = ensureColumn(db, "mcp_auto_reviews", col.name, col.ddl); err != nil {
			return err
		}
	}
	// Policy versions cannot inherit execution permission across upgrades.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE questions SET status=CASE WHEN status='answered' THEN CASE WHEN julianday(expires_at)>julianday(?) THEN 'pending' ELSE 'expired' END ELSE status END,answer_json=NULL,answered_by=NULL,approval_json=json_set(approval_json,'$.review.status','invalidated','$.review.reason','The review policy changed. This automatic decision cannot authorize execution.'),updated_at=? WHERE answered_by=? AND id IN (SELECT v.question_id FROM mcp_auto_reviews v JOIN mcp_call_approvals a ON a.question_id=v.question_id WHERE v.policy_version<>? AND a.claimed_at='')`, now(), now(), autoReviewActor, autoReviewPolicyVersion); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status='invalidated' WHERE policy_version<>? AND status='approved' AND question_id IN (SELECT question_id FROM mcp_call_approvals WHERE claimed_at='')`, autoReviewPolicyVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func readAutoReviewSettings(db reviewQuerier) (autoReviewSettings, error) {
	var x autoReviewSettings
	err := db.QueryRow(`SELECT mode,revision FROM auto_review_settings WHERE id=1`).Scan(&x.Mode, &x.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return autoReviewSettings{Mode: "off"}, nil
	}
	return x, err
}
func (s *Store) getAutoReviewSettings() (autoReviewSettings, error) {
	return readAutoReviewSettings(s.db)
}

// Every mode change or singleton recovery advances the epoch and invalidates
// unclaimed decisions atomically. Switching back to auto cannot resurrect them.
func (s *Store) putAutoReviewMode(mode string) error {
	if mode != "off" && mode != "shadow" && mode != "auto" {
		return errors.New("invalid AutoReview mode")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seed, err := tx.Exec(seedAutoReviewSettings)
	if err != nil {
		return err
	}
	inserted, err := seed.RowsAffected()
	if err != nil {
		return err
	}
	x, err := readAutoReviewSettings(tx)
	if err != nil {
		return err
	}
	if x.Mode == mode && inserted == 0 {
		return tx.Commit()
	}
	if x.Mode != mode {
		if _, err = tx.Exec(`UPDATE auto_review_settings SET mode=?,revision=revision+1 WHERE id=1`, mode); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT ` + prefixedQuestionColumns("q") + ` FROM questions q JOIN mcp_auto_reviews v ON v.question_id=q.id JOIN mcp_call_approvals a ON a.question_id=q.id WHERE a.claimed_at='' AND v.status IN ('approved','reviewing')`)
	if err != nil {
		return err
	}
	var qs []Question
	for rows.Next() {
		q, e := scanQuestion(rows)
		if e != nil {
			rows.Close()
			return e
		}
		qs = append(qs, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, q := range qs {
		if q.AnsweredBy == autoReviewActor {
			if q.Status == questionAnswered {
				q.Status = questionPending
				deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt)
				if e != nil || !time.Now().Before(deadline) {
					q.Status = questionExpired
				}
			}
			q.Answer = nil
			q.AnsweredBy = ""
		}
		q.UpdatedAt = now()
		display := MCPReviewDisplay{autoReviewActor, "invalidated", "AutoReview mode changed. The previous automatic decision cannot authorize execution.", "codex-auto-review", "", false, autoReviewPolicyVersion, nil}
		if q.Status == questionExpired || q.Status == questionCancelled || q.Status == questionRunDone {
			display.Status, display.Reason = "terminal", "This proposal has ended and remains non-executable."
		}
		if q.Approval.Review != nil && strings.HasPrefix(q.Approval.Review.Status, "shadow_") {
			display.Status = "shadow_invalidated"
		}
		q.Approval.Review = &display
		raw, _ := json.Marshal(q.Approval)
		if _, err = tx.Exec(`UPDATE questions SET status=?,answer_json=?,answered_by=?,approval_json=?,updated_at=? WHERE id=?`, q.Status, nullString(string(q.Answer)), nullString(q.AnsweredBy), string(raw), q.UpdatedAt, q.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status='invalidated' WHERE question_id=?`, q.ID); err != nil {
			return err
		}
		if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), q.UpdatedAt); err != nil {
			return err
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) autoReviewSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		x, err := s.store.getAutoReviewSettings()
		if err != nil {
			writeErr(w, 500, "storage", "Could not read AutoReview settings")
			return
		}
		writeJSON(w, 200, map[string]any{"mode": x.Mode, "revision": x.Revision, "review_scope": "all_external_tools"})
	case http.MethodPut:
		var x struct {
			Mode string `json:"mode"`
		}
		if decode(r, &x) != nil || s.store.putAutoReviewMode(x.Mode) != nil {
			writeErr(w, 400, "invalid_request", "AutoReview mode must be off, shadow or auto")
			return
		}
		current, err := s.store.getAutoReviewSettings()
		if err != nil {
			writeErr(w, 500, "storage", "Could not read AutoReview settings")
			return
		}
		writeJSON(w, 200, current)
	default:
		writeErr(w, 405, "method_not_allowed", "Use GET or PUT")
	}
}

func prefixedQuestionColumns(alias string) string {
	return alias + "." + strings.ReplaceAll(questionColumns, ",", ","+alias+".")
}
