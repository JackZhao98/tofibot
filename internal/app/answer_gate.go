package app

import (
	"slices"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The answer gate sends a reply back once when the Bot labels it a gap (it does
// not know, could not find it, asks the person for information, or declines for
// lack of information) before the run has tried any lookup route. The decision
// reads two things only: the label the model filled in on its say(answer) call
// and the run's tool record. It never reads the draft text.

const (
	resolutionResolved = "resolved"
	resolutionGap      = "gap"
)

// sayResolutionDescription states the label as a principle.
const sayResolutionDescription = "With purpose answer, label the reply honestly. resolved: it settles what was asked, for this person. gap: you do not know, could not find it, are asking the person for information, are declining for lack of information, or are answering in general terms where the request depends on what this person has told you or has on record. Use resolved for any other purpose."

// answerGateReminder is the private instruction a gap answer gets once. It
// carries no sample wording.
const answerGateReminder = `Before this reply becomes final: you labelled it a gap, and this run has not yet tried any lookup route. A gap is acceptable only after the routes that could plausibly hold the answer have been tried: this conversation's history, saved memory, attachments and files, connected tools, and the web.
Try the routes that could plausibly hold it, then reply with what you found. If they are truly exhausted, the reply says what was checked. Keep this review private. The draft immediately before this reminder is replaced by your final reply and is not shown to the person, so the final reply must contain the complete substantive response.`

// answerGateLookupTools are the tools whose use counts as a lookup attempt: they
// read history, memory, the web, attachments or the computer. A read-only call
// (identity risk Observation, e.g. a connected MCP server) to a tool that is not
// bookkeeping or talking to the person (noCompletionReviewTools) counts too.
var answerGateLookupTools = []string{
	"search_history", "inspect_recent_runs", "list_memory", "web_search", "web_fetch",
	"read_attachment", "computer_browser", "computer_desktop", "computer_terminal", "computer_action",
}

// answerGateLookupSQL is the constant SQL list of answerGateLookupTools.
var answerGateLookupSQL = "'" + strings.Join(answerGateLookupTools, "','") + "'"

// lookupAttempt reports whether a tool call is a lookup attempt.
func lookupAttempt(name, risk string) bool {
	return slices.Contains(answerGateLookupTools, name) || (completionReviewTool(name) && risk == tooloutcome.Observation)
}

// answerGateDue is the whole gate decision: the setting is on, the answer is
// labelled a gap, the run has made no lookup attempt and the run's one repair
// budget is unspent.
func answerGateDue(enabled bool, resolution string, lookedUp, budgetSpent bool) bool {
	return enabled && resolution == resolutionGap && !lookedUp && !budgetSpent
}

// hasLookupAttempt restores the gate's tool record after a durable input
// continuation, from the persisted activity rows. Rows carry no identity risk,
// so a restored read-only call to an MCP tool is not recognised; the gate then
// errs toward a bounce, which costs one extra model call and never loses an answer.
func (s *Store) hasLookupAttempt(runID string) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM tool_activities
		WHERE run_id=? AND status IN ('running','completed','failed')
		AND name IN (`+answerGateLookupSQL+`)
	)`, runID).Scan(&found)
	return found != 0, err
}
