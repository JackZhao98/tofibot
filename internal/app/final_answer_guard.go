package app

import "errors"

// A run must never end "done" with no visible answer. When the engine returns
// no final content for a run that owes the user a reply, execute() publishes
// the retained demoted draft, else points at the run's latest progress
// report, else fails the run with this typed code.
const noFinalAnswerCode = "no_final_answer"

var errNoFinalAnswer = errors.New(noFinalAnswerCode + ": the run ended without a final answer")

// noFinalAnswerProgressNote closes a run whose only visible output is its
// progress reports. It points at them instead of repeating their text.
const noFinalAnswerProgressNote = "本次任务没有生成单独的最终总结；上方最近一条进度更新即为目前的结果。"

// finalAnswerRequired mirrors finishRunState's publication rule: a run with
// no handoff, outside group chat and without pending attachments publishes
// an assistant reply, so empty content would be an empty, silent answer.
func (s *Store) finalAnswerRequired(runID string) (bool, error) {
	var handoffs, pendingFiles int
	var kind string
	if err := s.db.QueryRow(`SELECT handoff_count,kind FROM runs WHERE id=?`, runID).Scan(&handoffs, &kind); err != nil {
		return false, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, runID).Scan(&pendingFiles); err != nil {
		return false, err
	}
	return handoffs == 0 && kind != runKindGroupChat && pendingFiles == 0, nil
}

// hasProgressReport reports whether the run published a non-empty progress
// turn the user can already see.
func (s *Store) hasProgressReport(runID string) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE run_id=? AND role='assistant' AND kind IN ('progress','segment') AND TRIM(content)<>'')`, runID).Scan(&found)
	return found != 0, err
}
