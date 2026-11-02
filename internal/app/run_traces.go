package app

// The trace strip of a run: one stitched picture of its screen work, made when
// the run ends (B4). The per-capture pictures are in run_captures.

import (
	"database/sql"
	"errors"
)

func migrateRunTraces(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_traces(
	 run_id TEXT PRIMARY KEY, attachment_id TEXT, tiles INTEGER NOT NULL DEFAULT 0,
	 steps INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL,
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
	 FOREIGN KEY(attachment_id) REFERENCES attachments(id) ON DELETE SET NULL);
	CREATE INDEX IF NOT EXISTS run_traces_attachment ON run_traces(attachment_id);`)
	return err
}

// RunTrace is the folded strip shown on a run's activity.
type RunTrace struct {
	AttachmentID string `json:"attachment_id"`
	Tiles        int    `json:"tiles"`
	Steps        int    `json:"steps"`
	CreatedAt    string `json:"created_at"`
}

// runTraceStrip returns the strip of a run, or nil when the run has none.
func (s *Store) runTraceStrip(runID string) *RunTrace {
	t := RunTrace{}
	err := s.db.QueryRow(`SELECT COALESCE(attachment_id,''),tiles,steps,created_at FROM run_traces WHERE run_id=?`, runID).Scan(&t.AttachmentID, &t.Tiles, &t.Steps, &t.CreatedAt)
	if err != nil || t.AttachmentID == "" {
		return nil
	}
	return &t
}

// deleteRunTraceStrip removes the strip picture and its row.
func (s *Store) deleteRunTraceStrip(runID string) {
	var id sql.NullString
	if err := s.db.QueryRow(`SELECT attachment_id FROM run_traces WHERE run_id=?`, runID).Scan(&id); err != nil {
		return
	}
	if id.Valid && id.String != "" {
		if err := s.deleteUnboundAttachment(id.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
	}
	_, _ = s.db.Exec(`DELETE FROM run_traces WHERE run_id=?`, runID)
}
