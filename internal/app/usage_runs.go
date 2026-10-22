package app

import (
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	usageRunsDefaultDays = 7
	usageRunsMaxDays     = 30
	usageRunsTopFailures = 5
)

// UsageRunFailure is one named failure reason and how many runs ended with it.
// Codes come from Run.failure(), never from upstream provider text.
type UsageRunFailure struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// UsageRunHealth is the read-only run-health summary behind
// GET /api/usage/runs: runs by status, the most common failure reasons and
// how many tool calls hit their time limit.
type UsageRunHealth struct {
	Days         int               `json:"days"`
	Since        string            `json:"since"`
	Runs         int               `json:"runs"`
	ByStatus     map[string]int    `json:"by_status"`
	Failures     []UsageRunFailure `json:"failures"`
	ToolCalls    int               `json:"tool_calls"`
	ToolTimeouts int               `json:"tool_timeouts"`
	ModelCalls   int               `json:"model_calls"`
}

// toolTimeoutPredicate matches a failed shell-style tool call that timed out
// (the "[Timed out after" prefix or timed_out flag) or was killed (exit 137).
const toolTimeoutPredicate = `status='failed' AND (result LIKE '[Timed out after%' OR result LIKE '%"timed_out":true%' OR result LIKE '%"timed_out": true%' OR result LIKE '%[Exit 137:%' OR result LIKE '%[Exit code: 137]%' OR result LIKE '%"exit_code":137%' OR result LIKE '%"exit_code": 137%')`

func (s *Store) usageRunHealth(days int, at time.Time) (UsageRunHealth, error) {
	since := stampTime(at.Add(-time.Duration(days) * 24 * time.Hour))
	out := UsageRunHealth{Days: days, Since: since, ByStatus: map[string]int{}, Failures: []UsageRunFailure{}}
	rows, err := s.db.Query(`SELECT status,COUNT(*) FROM runs WHERE updated_at>=? GROUP BY status`, since)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.ByStatus[status] = n
		out.Runs += n
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	failed, err := s.db.Query(`SELECT COALESCE(error,''),model FROM runs WHERE status='failed' AND updated_at>=?`, since)
	if err != nil {
		return out, err
	}
	counts := map[string]int{}
	for failed.Next() {
		run := Run{Status: "failed"}
		if err = failed.Scan(&run.Error, &run.Model); err != nil {
			failed.Close()
			return out, err
		}
		counts[run.failure().Code]++
	}
	if err = failed.Close(); err != nil {
		return out, err
	}
	for code, n := range counts {
		out.Failures = append(out.Failures, UsageRunFailure{Code: code, Count: n})
	}
	sort.Slice(out.Failures, func(i, j int) bool {
		if out.Failures[i].Count != out.Failures[j].Count {
			return out.Failures[i].Count > out.Failures[j].Count
		}
		return out.Failures[i].Code < out.Failures[j].Code
	})
	if len(out.Failures) > usageRunsTopFailures {
		out.Failures = out.Failures[:usageRunsTopFailures]
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE started_at>=? AND status IN ('completed','failed')`, since).Scan(&out.ToolCalls); err != nil {
		return out, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE started_at>=? AND `+toolTimeoutPredicate, since).Scan(&out.ToolTimeouts); err != nil {
		return out, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM usage_calls WHERE occurred_at>=? AND kind='chat'`, since).Scan(&out.ModelCalls)
	return out, err
}

func (s *Server) routeUsageRuns(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "usage/runs" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	days := usageRunsDefaultDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > usageRunsMaxDays {
			writeErr(w, http.StatusBadRequest, "invalid_days", "days must be between 1 and 30")
			return true
		}
		days = n
	}
	health, err := s.store.usageRunHealth(days, time.Now())
	if err != nil {
		writeErr(w, 500, "storage", "run health unavailable")
		return true
	}
	writeJSON(w, 200, health)
	return true
}
