package app

// Making, showing and deleting the strip of a run (work trace, B4).

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// decorateRunTraces sets Run.Trace on the runs that have a strip.
func (s *Store) decorateRunTraces(runs []Run) {
	if len(runs) == 0 {
		return
	}
	index := map[string]int{}
	var ids []any
	for i, r := range runs {
		index[r.ID] = i
		ids = append(ids, r.ID)
	}
	for start := 0; start < len(ids); start += 400 {
		end := min(start+400, len(ids))
		chunk := ids[start:end]
		rows, err := s.db.Query(`SELECT run_id,COALESCE(attachment_id,''),tiles,steps,created_at FROM run_traces WHERE attachment_id IS NOT NULL AND run_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, chunk...)
		if err != nil {
			return
		}
		for rows.Next() {
			var id string
			t := RunTrace{}
			if rows.Scan(&id, &t.AttachmentID, &t.Tiles, &t.Steps, &t.CreatedAt) == nil && t.AttachmentID != "" {
				trace := t
				runs[index[id]].Trace = &trace
			}
		}
		rows.Close()
	}
}

// finalizeRunTrace runs when a run has ended. It clears what the run held in
// memory, and when the work trace is on and the run kept at least two
// captures, it stitches the strip the person sees on the run's activity.
func (s *Server) finalizeRunTrace(c Conversation, run Run) {
	if run.Status == "queued" || run.Status == "running" || run.Status == runWaiting {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			log.Printf("[trace] strip of run %s: %v", run.ID, v)
		}
	}()
	s.waitTraceCaptures(run.ID)
	s.forgetTraceState(run.ID)
	if !s.autoBool(autoWorkTrace) {
		return
	}
	rows, err := s.store.runCaptures(run.ID)
	if err != nil || len(rows) < 2 {
		return
	}
	if err := s.buildRunTraceStrip(context.Background(), run, rows); err != nil {
		log.Printf("[trace] could not make the strip of run %s: %v", run.ID, err)
		return
	}
	if fresh, err := s.store.GetRun(run.ID); err == nil {
		list := []Run{fresh}
		s.store.decorateRunTraces(list)
		_, _ = s.store.Event(c.ID, "run", list[0])
	}
}

// buildRunTraceStrip stitches the last twelve captures with the same montage
// code the model's desktop.trace uses. When there are more than twelve, the
// first cell says how many earlier steps are left out.
func (s *Server) buildRunTraceStrip(ctx context.Context, run Run, rows []RunCapture) error {
	shown, earlier := rows, 0
	if len(rows) > montagePersonTilesMax {
		shown = rows[len(rows)-(montagePersonTilesMax-1):]
		earlier = len(rows) - len(shown)
	}
	kept := 0
	for _, row := range shown {
		if row.AttachmentID != "" && !row.Redacted {
			kept++
		}
	}
	if kept == 0 {
		return nil
	}
	res, err := s.buildRunMontage(ctx, rows, shown, montageOptions{Columns: 3, Earlier: earlier})
	if err != nil {
		return err
	}
	s.store.deleteRunTraceStrip(run.ID)
	att, err := s.store.addAttachmentOpts(ctx, run.ConversationID, fmt.Sprintf("work-trace-%s.jpg", traceShortID(run.ID)), "image/jpeg", bytes.NewReader(res.JPEG), attachmentOpts{Origin: attachmentOriginTrace, BotID: run.BotID})
	if err != nil {
		return err
	}
	_, err = s.store.db.Exec(`INSERT OR REPLACE INTO run_traces(run_id,attachment_id,tiles,steps,created_at) VALUES(?,?,?,?,?)`, run.ID, att.ID, len(shown), len(rows), now())
	return err
}

// runTraceRetention deletes the pictures older than the period; 0 days keeps
// everything.
func (s *Server) runTraceRetention(at time.Time) {
	days := s.autoInt(autoTraceRetentionDays)
	if days <= 0 {
		return
	}
	if _, err := s.store.cleanupRunTraces(at.Add(-time.Duration(days) * 24 * time.Hour)); err != nil {
		log.Printf("[trace] retention: %v", err)
	}
}

// cleanupRunTraces removes expired traces. A run with a strip ages by the
// strip: its pictures, rows and strip go together, so the cells of a strip
// never name a step that is gone. A run with no strip ages by each picture.
func (s *Store) cleanupRunTraces(cutoff time.Time) (int, error) {
	stamp := stampTime(cutoff)
	removed := 0
	const expired = `(EXISTS(SELECT 1 FROM run_traces t WHERE t.run_id=c.run_id AND t.created_at<?)
	 OR (NOT EXISTS(SELECT 1 FROM run_traces t WHERE t.run_id=c.run_id) AND c.taken_at<?))`
	rows, err := s.db.Query(`SELECT c.attachment_id FROM run_captures c WHERE c.attachment_id IS NOT NULL AND `+expired+` UNION SELECT attachment_id FROM run_traces WHERE attachment_id IS NOT NULL AND created_at<?`, stamp, stamp, stamp)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.deleteUnboundAttachment(id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return removed, err
		}
		removed++
	}
	if _, err := s.db.Exec(`DELETE FROM run_captures WHERE rowid IN (SELECT c.rowid FROM run_captures c WHERE `+expired+`)`, stamp, stamp); err != nil {
		return removed, err
	}
	if _, err := s.db.Exec(`DELETE FROM run_traces WHERE created_at<?`, stamp); err != nil {
		return removed, err
	}
	return removed, nil
}
