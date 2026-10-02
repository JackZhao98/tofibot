package app

import "strings"

// A tool-backed task gets one bounded chance to compare its draft against the
// user's request before it becomes final. The agent owns the single repair
// budget; this callback only supplies the review instruction and never treats
// a draft as proof that work completed.
const taskCompletionFinalReminder = `Before sending the final answer, compare the user's requested deliverable, fields, and categories with the actual completed work and tool results in this conversation.
If any requested item remains unresolved, continue the remaining authorized work with relevant available and permitted tools while capacity remains. Give partial results only when the user asked for them or a concrete blocker prevents completion; otherwise do not stop after one lead or category. Do not describe a category, source, or action as searched, checked, completed, unavailable, or verified unless the conversation contains evidence for that statement. Distinguish an attempted or failed route from an inspected result.
Do not broaden the request or replay an action with an uncertain outcome; inspect its current state first. Keep this review private. The draft immediately before this reminder may appear only as a progress entry, so the final answer must still contain the substantive response to the user rather than assuming they saw that draft. Preserve the tone and form the user asked for, including playful or interactive replies. Do not replace the answer with an audit-style statement such as "I checked everything" or "nothing was missed" unless the user asked for an audit.`

func completionReviewTool(name string) bool {
	switch name {
	case "get_context_usage", "inspect_recent_runs", "search_history", "ask_user_question", "request_approval", "computer_help", "read_workflow_guide":
		return false
	default:
		return name != ""
	}
}

// hasCompletionReviewToolWork restores the review gate after a durable input
// continuation. The database is authoritative because the in-memory event
// observer is intentionally recreated for each runtime invocation.
func (s *Store) hasCompletionReviewToolWork(runID string) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM tool_activities
		WHERE run_id=? AND status IN ('running','completed','failed')
		AND name NOT IN ('get_context_usage','inspect_recent_runs','search_history','ask_user_question','request_approval','computer_help','read_workflow_guide')
	)`, runID).Scan(&found)
	return found != 0, err
}

// taskCompletionFinalReview avoids an extra model turn for conversational
// replies that performed no substantive tool work. Collecting an ordinary
// answer in a QuestionCard alone does not turn a conversational reply into a
// second, audit-style response; subsequent substantive tools still do.
func taskCompletionFinalReview(observedToolWork func() bool) func(string) (string, error) {
	if observedToolWork == nil {
		return nil
	}
	return func(content string) (string, error) {
		if !observedToolWork() || strings.TrimSpace(content) == "" {
			return "", nil
		}
		return taskCompletionFinalReminder, nil
	}
}
