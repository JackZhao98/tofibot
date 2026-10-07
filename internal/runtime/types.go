package runtime

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"time"
)

// Message is the backend-owned conversation message passed to the agent loop.
// Tool call history is intentionally not part of Alpha's public adapter
// contract; the backend owns durable context and supplies the bounded view.
type Message struct {
	Role    string
	Content string
	// ImageURLs are provider-facing visual inputs. They must be validated by
	// the product boundary before entering the runtime.
	ImageURLs []string
}

// Tool is the complete tool surface for one run. Runtime never discovers or
// registers additional tools; the backend must explicitly supply each tool.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(context.Context, json.RawMessage) (string, error)
	Identity    func(json.RawMessage) tooloutcome.Identity
	// ResolveIdentity may make a bounded, read-only backend lookup. A failure
	// here is known to precede execution, and must not become uncertain effect.
	ResolveIdentity func(context.Context, json.RawMessage) (tooloutcome.Identity, error)
	// CheckReadiness is a bounded, read-only method check. Backends invoke it
	// before asking for approval and again immediately before dispatch.
	CheckReadiness func(context.Context) (MethodReadiness, error)
	// ApprovalExpiryReadOnly is set only by a backend-owned executor whose
	// scoped observation is independently authorized. Remote metadata cannot set it.
	ApprovalExpiryReadOnly bool
}

// ToolEvent describes one provider tool call and its lifecycle. Arguments and
// Result are bounded by the runtime before they leave this package. Result is
// taken from the tool's full output; the agent loop separately bounds the
// copy it passes to the model.
type ToolEvent struct {
	CallID    string               `json:"call_id"`
	Name      string               `json:"name"`
	Arguments string               `json:"arguments"`
	Result    string               `json:"result"`
	Status    string               `json:"status"`
	Truncated bool                 `json:"truncated"`
	Outcome   *tooloutcome.Outcome `json:"outcome,omitempty"`
	// Risk is the call's identity risk class (tooloutcome.Observation, ...),
	// resolved by the backend from the tool's Identity, never from results.
	Risk string `json:"risk,omitempty"`
}

type Request struct {
	ApprovalExpiryRecovery bool
	BotID                  string
	RunID                  string
	System                 string
	Model                  string
	ReasoningEffort        string
	Messages               []Message
	Tools                  []Tool
	OnDelta                func(string)
	// ConversationID keys the provider prompt cache across runs of one
	// conversation; RunID is the fallback.
	ConversationID string
	// OnAssistantTurn is called for completed non-final assistant turns with
	// non-empty public content immediately before their tool calls are queued or
	// executed. It is also called for a budget wrap-up turn whose tool calls are
	// discarded before the next model request, and for a final draft promoted
	// to a non-final turn by BeforeFinalResponse.
	OnAssistantTurn   func(turnIndex int, content string) error
	OnToolEvent       func(ToolEvent) error
	OnContextEstimate func(estimatedInput int)
	OnUsage           func(inputTokens, outputTokens int64)
	OnCompact         func(originalTokens, compactedTokens int)
	// OnThinking receives provider reasoning-summary deltas. They are never
	// part of the answer and must not be mixed into OnDelta output.
	OnThinking func(delta string)
	// OnRetry is called before the provider retries a failed model request
	// after a backoff of wait.
	OnRetry func(attempt int, wait time.Duration)
	// OnStreamReset discards OnDelta output already streamed by a model call
	// that was aborted and is about to be retried.
	OnStreamReset func()
	// OnReviewDraft, when set, receives a final draft that BeforeFinalResponse
	// sent back for review; it replaces the OnAssistantTurn publication of that
	// draft, which the reviewed final answer supersedes.
	OnReviewDraft func(turnIndex int, content string) error
	// BeforeModelCall runs at the safe boundary immediately before each model
	// request, after prior streaming and tool work has settled.
	BeforeModelCall func() error
	// BeforeFinalResponse reviews cleaned, non-empty text-only final content.
	// An empty reminder accepts; an error aborts; a non-empty reminder requests
	// at most one repair using existing history and the remaining run budget.
	// The draft is published through OnReviewDraft (or OnAssistantTurn when
	// unset), but the internal reminder is not. It is skipped after repair or
	// on cancellation.
	BeforeFinalResponse func(content string) (reminder string, err error)
	// FinalResponseRepairTools permits one reserved repair after an exhausted
	// run budget. The repair request exposes and executes only these named
	// tools, so callers may use it for an internal receipt without replaying
	// the task's original external action. Empty keeps ordinary budget rules.
	FinalResponseRepairTools []string
	// OnSuspend durably stores an opaque checkpoint after a ToolsOnly tool asks
	// for human input. It must persist atomically with the run's waiting state.
	// A callback error aborts the run; a successful callback is followed by a
	// Result with Suspended true and no model-visible tool error.
	OnSuspend func(questionID string, checkpoint json.RawMessage) error
	// Continuation is a checkpoint returned to OnSuspend. When present, Run
	// resumes its exact provider transcript and inserts ResumeResult for the
	// suspended tool rather than replaying any old tool execution.
	Continuation  json.RawMessage
	ResumeResult  string
	ResumeOutcome *tooloutcome.Outcome
}

type Result struct {
	Content         string
	InputTokens     int64
	OutputTokens    int64
	Suspended       bool
	BudgetExhausted bool
	BudgetReason    string
}

type Engine interface {
	Run(context.Context, Request) (Result, error)
}

type Config struct {
	// MaxDuration is the active budget between agent-loop calls, including
	// tools but excluding explicit bounded human-input waits. Zero retains the
	// runtime default; caller deadlines and cancellation remain immediate.
	MaxDuration time.Duration
	Provider    string
	APIKey      string
	BaseURL     string
	Model       string
	Credential  func(context.Context) (string, error)
}

type MethodReadiness string

const (
	MethodReady         MethodReadiness = "ready"
	MethodNotConfigured MethodReadiness = "not_configured"
	MethodAuthRequired  MethodReadiness = "auth_required"
	MethodUnavailable   MethodReadiness = "unavailable"
	MethodUnknown       MethodReadiness = "unknown"
)
