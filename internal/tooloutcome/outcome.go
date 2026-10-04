// Package tooloutcome carries backend-owned recovery decisions across the
// executor, model transcript and durable UI activity. Remote prose is never
// used to authorize retries or classify execution certainty.
package tooloutcome

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	NeedApproval    = "need_approval"
	NeedInformation = "need_information"
	Validation      = "validation_error"
	Transient       = "transient_failure"
	Permanent       = "permanent_failure"
	Denied          = "denied"
	Expired         = "approval_expired"
	Uncertain       = "uncertain_effect"
)

type Outcome struct {
	Readiness   string `json:"readiness,omitempty"`
	Version     int    `json:"version"`
	Status      string `json:"status"`
	Code        string `json:"code"`
	Certainty   string `json:"execution_certainty"`
	Message     string `json:"message"`
	NextAction  string `json:"next_action"`
	Attempts    int    `json:"attempts,omitempty"`
	RetryLimit  int    `json:"retry_limit,omitempty"`
	RepairLimit int    `json:"repair_limit,omitempty"`
}

func New(status, code, certainty, message, next string) Outcome {
	return Outcome{Version: 1, Status: status, Code: code, Certainty: certainty, Message: message, NextAction: next}
}

func (o Outcome) JSON() string { b, _ := json.Marshal(o); return string(b) }

type Error struct{ Outcome Outcome }

func (e *Error) Error() string { return e.Outcome.Message }
func (o Outcome) Err() error   { return &Error{Outcome: o} }

func FromError(err error) (Outcome, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return Outcome{}, false
	}
	return e.Outcome, true
}

func Parse(text string) *Outcome {
	text = strings.TrimPrefix(strings.TrimSpace(text), "Tool error: ")
	var o Outcome
	if json.Unmarshal([]byte(text), &o) != nil || o.Version != 1 || o.Status == "" || o.Certainty == "" || o.NextAction == "" {
		return nil
	}
	return &o
}

// ModelResult preserves typed errors rather than flattening their recovery
// instructions into unclassified prose. Ordinary errors keep legacy wording.
func ModelResult(err error) string {
	if o, ok := FromError(err); ok {
		return o.JSON()
	}
	return "Tool error: " + err.Error()
}
