package app

import (
	"encoding/json"
	"strings"
)

// RunFailure is runtime feedback, never an assistant/model contribution.
// Deriving it from durable status/error also covers legacy records on reload.
type RunFailure struct {
	Code    string `json:"code"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

func (r Run) failure() *RunFailure {
	if r.Status != "failed" {
		return nil
	}
	failure := &RunFailure{Code: "execution_failed", Source: "runtime", Message: "Task failed before completion. Completed tool results are retained; verify any uncertain effects before retrying."}
	errorText := strings.ToLower(r.Error)
	if errorText == "approval_expired" {
		failure.Code = "approval_expired"
		failure.Message = "Approval expired. This workflow stopped; completed results are retained and uncertain effects require verification."
		return failure
	}
	if strings.Contains(errorText, "stream read error") || strings.Contains(errorText, "connection reset") || strings.Contains(errorText, "broken pipe") || strings.Contains(errorText, "unexpected eof") || strings.Contains(errorText, "internal_error") {
		failure.Code = "connection_interrupted"
		failure.Message = "Connection interrupted. Task did not finish. Completed tool results are retained; verify any uncertain effects before retrying."
	}
	if strings.Contains(errorText, "budget") || strings.Contains(errorText, "maximum agent steps") || strings.Contains(errorText, "empty responses") {
		failure.Code = "budget_exhausted"
		failure.Message = "The execution or repair budget was exhausted. Task did not finish; completed tool results are retained. Review the blocker before continuing."
	}
	return failure
}

func (r Run) MarshalJSON() ([]byte, error) {
	type plain Run
	return json.Marshal(struct {
		plain
		Failure         *RunFailure `json:"failure,omitempty"`
		StopReason      string      `json:"stop_reason,omitempty"`
		FinishingReason string      `json:"finishing_reason,omitempty"`
	}{plain: plain(r), Failure: r.failure(), StopReason: func() string {
		if r.Error == "approval_expired" {
			return "approval_expired"
		}
		return ""
	}(), FinishingReason: func() string {
		if r.Error == "approval_expiry_recovery" && (r.Status == "queued" || r.Status == "running") {
			return "approval_expired"
		}
		return ""
	}()})
}
