package runtime

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/agent"
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
	// Timeout is the hard per-call deadline of active time. Waits for a person
	// or a queued shared resource (PauseToolDeadline) do not count. Zero uses
	// DefaultToolTimeout; longer values are capped at MaxToolTimeout. When it
	// passes, the call is cancelled and returns a typed tool_timeout outcome.
	Timeout time.Duration
	// ApprovalExpiryReadOnly is set only by a backend-owned executor whose
	// scoped observation is independently authorized. Remote metadata cannot set it.
	ApprovalExpiryReadOnly bool
	// Local marks a backend tool that validates and writes inside this process's
	// own store (typically one transaction). An unclassified error from it means
	// the call was refused before anything committed, so it is reported as a
	// definite not-executed failure rather than an uncertain external effect.
	Local bool
	// Strict asks providers that support it (OpenAI Responses) to hold the
	// call's arguments to Parameters. The schema must list every property as
	// required and forbid extras.
	Strict bool
	// ResolveDirectName lets a generic dispatcher tool (call_mcp_tool) claim a
	// call the model made under a concrete tool name instead of through the
	// dispatcher. It returns the canonical name to pass as the dispatcher's
	// "name" argument. It resolves exactly as the dispatcher itself would and
	// must fail closed (ok=false) for unknown or ambiguous names.
	ResolveDirectName func(ctx context.Context, name string) (string, bool)
	// Deferred keeps the tool executable but out of the declared tool list
	// until another tool of the run calls ActivateTools for it. Use it for
	// tools that matter rarely, so every turn does not carry their schemas.
	Deferred bool
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

// ContextBreakdown is an input-token estimate split into system prompt plus
// tool definitions, message content, and replayed reasoning.
type ContextBreakdown struct{ System, Messages, Reasoning int }

// ToolCallDelta is one streamed fragment of a provider tool call: the first
// fragment of a call carries its ID and Name, later ones argument text.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type Request struct {
	ApprovalExpiryRecovery bool
	// StructuredOutput runs the say protocol: plain assistant text is private
	// and only the say tool shows the person anything.
	StructuredOutput bool
	// ForceToolUse requires a tool call on every ordinary model request of a
	// structured run (see agent.Config.ForceToolUse).
	ForceToolUse bool
	// OnToolCallDelta receives every streamed tool-call fragment while the
	// model is still writing it, in order. It is called from the provider's
	// stream goroutine and must not block.
	OnToolCallDelta func(ToolCallDelta)
	// EndOnEmptyFinal is asked about a model response with no text and no tool
	// calls; true ends the run with an empty result instead of a nudge to
	// continue (the caller owes no answer, as after a handoff).
	EndOnEmptyFinal func() bool
	BotID           string
	RunID           string
	System          string
	Model           string
	ReasoningEffort string
	Messages        []Message
	Tools           []Tool
	OnDelta         func(string)
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
	// WebSearchMaxUses, when positive, gives the model the provider's own
	// server-side web search (Anthropic models; searches per model request
	// capped at this). Each search is reported through OnToolEvent as a
	// web_search call whose result lists the sources, and the billed number
	// of searches through OnWebSearchBilled.
	WebSearchMaxUses  int
	OnWebSearchBilled func(searches int64)
	OnCompact         func(originalTokens, compactedTokens int)
	// OnThinking receives provider reasoning-summary deltas. They are never
	// part of the answer and must not be mixed into OnDelta output.
	OnThinking func(delta string)
	// OnContextBreakdown reports the same estimate split by what fills it.
	OnContextBreakdown func(ContextBreakdown)
	// OnReasoningReplayRejected fires once when the provider rejects replayed
	// reasoning and replay is switched off for the rest of the run.
	OnReasoningReplayRejected func()
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
	// Attempt is the run's current claim generation; it is stamped into every
	// checkpoint so a stale writer can be told apart from the live one.
	Attempt int
	// OnCheckpoint durably stores an opaque boundary checkpoint (kind "turn" or
	// "tool"). An error fails the run before it dispatches anything further. An
	// empty checkpoint means this boundary could not be snapshotted: the store
	// must discard its older checkpoint instead of keeping a stale one.
	OnCheckpoint func(kind string, checkpoint json.RawMessage) error
	// ResumeToolResults settles, by call ID, calls of a boundary Continuation
	// that were running or queued when the service stopped. The backend has
	// already recorded their activity rows, so no tool events are re-emitted.
	ResumeToolResults map[string]agent.ResumeToolResult
}

type Result struct {
	// TurnEnded reports that a tool ended the turn with EndTurn: the loop
	// stopped after that tool's result on purpose, with no final text.
	TurnEnded       bool
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
	// Resolve, when set, routes every model request by its model ID
	// (ModelProvider) and supplies that provider's credential. Provider,
	// APIKey, BaseURL and Credential are then unused.
	Resolve func(ctx context.Context, providerName string) (string, error)
	// Endpoint optionally overrides a routed provider's base URL.
	Endpoint func(providerName string) string
	// Retries, when set, is read for every routed model request and sets how
	// many times a transient provider error is retried (0 disables retries),
	// so a settings change applies to the next request without a restart.
	Retries func() int
}

type MethodReadiness string

const (
	MethodReady         MethodReadiness = "ready"
	MethodNotConfigured MethodReadiness = "not_configured"
	MethodAuthRequired  MethodReadiness = "auth_required"
	MethodUnavailable   MethodReadiness = "unavailable"
	MethodUnknown       MethodReadiness = "unknown"
)
