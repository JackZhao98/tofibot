package app

import (
	"database/sql"
	"strings"
)

// runs.final_origin records how a run's visible final answer came to be, so
// the protocol that produces it can be measured rather than inferred.
//
//	say        the final came from a tool the model chose explicitly
//	implicit   the final is the model's plain final text
//	promoted   the host published a progress note as the answer
//	none       nothing was published (silent run, or failed without an answer)
//	quiet      a scheduled answer was recorded as the run's conclusion and not
//	           posted (say(answer, quiet=true), D52)
//	heuristic:<name>   a host heuristic changed what was published; it replaces
//	           the base value and several are comma-separated in firing order
const (
	finalOriginSay      = "say"
	finalOriginImplicit = "implicit"
	finalOriginPromoted = "promoted"
	finalOriginNone     = "none"
	finalOriginQuiet    = "quiet"

	heuristicRepeat         = "heuristic:repeat"          // H3 repeated reply
	heuristicNarrationFold  = "heuristic:narration_fold"  // H2 step narration
	heuristicDelegationFold = "heuristic:delegation_fold" // H4 note after a handoff
	heuristicSchedulePrep   = "heuristic:schedule_prepend"
	heuristicLeadingMention = "heuristic:leading_mention" // H6
	heuristicExactDup       = "heuristic:exact_dup"       // H8
)

type finalOriginDB interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// mergeFinalOrigin combines the stored value with a new one. A heuristic
// replaces a base value and is otherwise appended once; a base value is kept
// only if no value is stored yet or the stored one is also a base value.
func mergeFinalOrigin(current, value string) string {
	if value == "" {
		return current
	}
	heuristic := strings.HasPrefix(value, "heuristic:")
	stored := strings.HasPrefix(current, "heuristic:")
	switch {
	case current == "":
		return value
	case !heuristic && stored:
		return current
	case !heuristic:
		return value
	case !stored:
		return value
	}
	for _, p := range strings.Split(current, ",") {
		if p == value {
			return current
		}
	}
	return current + "," + value
}

// noteFinalOrigin records value for the run. Failures are not fatal: this is
// instrumentation and must never change a run's outcome.
func noteFinalOrigin(q finalOriginDB, runID, value string) {
	var current string
	if q.QueryRow(`SELECT final_origin FROM runs WHERE id=?`, runID).Scan(&current) != nil {
		return
	}
	if next := mergeFinalOrigin(current, value); next != current {
		_, _ = q.Exec(`UPDATE runs SET final_origin=? WHERE id=?`, next, runID)
	}
}

// noteFinalOriginDefault records value only when nothing is stored yet.
func noteFinalOriginDefault(q finalOriginDB, runID, value string) {
	_, _ = q.Exec(`UPDATE runs SET final_origin=? WHERE id=? AND final_origin=''`, value, runID)
}

func (s *Store) noteFinalOrigin(runID, value string) { noteFinalOrigin(s.db, runID, value) }

// rowScanner is the Scan half of *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(...any) error }

type withFinalOrigin struct {
	rowScanner
	origin *string
}

func (w withFinalOrigin) Scan(dest ...any) error {
	return w.rowScanner.Scan(append(dest, w.origin)...)
}

// scanRunWithOrigin scans the scanRun column list plus final_origin.
func scanRunWithOrigin(r rowScanner) (Run, error) {
	var origin string
	x, err := scanRun(withFinalOrigin{r, &origin})
	x.FinalOrigin = origin
	return x, err
}
