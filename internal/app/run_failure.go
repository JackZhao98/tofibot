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
	failure := &RunFailure{Code: "execution_failed", Source: "runtime", Message: "Task failed before completion."}
	errorText := strings.ToLower(r.Error)
	if strings.Contains(errorText, "stream read error") || strings.Contains(errorText, "connection reset") || strings.Contains(errorText, "broken pipe") || strings.Contains(errorText, "unexpected eof") {
		failure.Code = "connection_interrupted"
		failure.Message = "Connection interrupted. Task did not finish."
	}
	return failure
}

func (r Run) MarshalJSON() ([]byte, error) {
	type plain Run
	return json.Marshal(struct {
		plain
		Failure *RunFailure `json:"failure,omitempty"`
	}{plain: plain(r), Failure: r.failure()})
}
