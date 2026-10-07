package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// ApprovalExpiryCheckpoint verifies the original rendezvous and protocol state.
// Callers may conclude corrupt work, but must never recover tools from it.
func ApprovalExpiryCheckpoint(raw json.RawMessage, runID, botID, questionID, model string) (string, error) {
	c, err := decodeContinuation(raw, Request{RunID: runID, BotID: botID}, model)
	if err != nil {
		return "", err
	}
	if c == nil || c.QuestionID != questionID {
		return "", errors.New("expiry checkpoint question mismatch")
	}
	return c.WaitingToolCallID, nil
}

const expiryConclusionPrompt = "The approval expired. Finish this workflow now. You may use only supplied independently authorized read-only observations to answer the original request. Never repeat the expired call, change tools/providers to perform its effect, request new approval, or claim incomplete/uncertain work succeeded. Summarize completed and incomplete work accurately. The next request, if needed, is summary-only."

// This narrow continuation has no agent repair loop, discovery, compaction,
// reserved budget or provider retry/fallback. Its second request has no tools.
func (e *engine) finishApprovalExpiry(ctx context.Context, req Request, model string, c *agent.Continuation) (Result, error) {
	if c == nil {
		return Result{}, errors.New("expiry checkpoint required")
	}
	if c.BudgetWrapUp {
		return Result{}, errors.New("original recovery budget exhausted")
	}
	requests := min(2, agent.MaxStepsWithProgressReports-c.Step)
	if requests <= 0 {
		return Result{}, errors.New("original step budget exhausted")
	}
	duration := e.config.MaxDuration
	if duration <= 0 {
		duration = defaultMaxDuration
	}
	remaining := duration - time.Duration(c.ActiveElapsedNanos)
	if remaining <= 0 {
		return Result{}, errors.New("original active budget exhausted")
	}
	ctx, cancel := context.WithTimeout(ctx, min(60*time.Second, remaining))
	defer cancel()
	messages, records, err := agent.ResumeApprovalExpiry(c)
	if err != nil {
		return Result{}, err
	}
	// Construct the same provider without the ordinary retry wrapper: every
	// request counts toward the two-request hard cap, including failures.
	key := e.config.APIKey
	if e.credential != nil {
		key, err = e.credential(ctx)
		if err != nil {
			return Result{}, err
		}
	}
	var opts []provider.Option
	if e.config.BaseURL != "" {
		opts = append(opts, provider.WithBaseURL(e.config.BaseURL))
	}
	p, err := provider.New(e.config.Provider, key, opts...)
	if err != nil {
		return Result{}, err
	}
	safe := map[string]Tool{}
	var schemas []provider.Tool
	for _, t := range req.Tools {
		if requests > 1 && t.ApprovalExpiryReadOnly && t.Execute != nil {
			safe[t.Name] = t
			schemas = append(schemas, provider.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
		}
	}
	request := &provider.ChatRequest{Model: model, ReasoningEffort: req.ReasoningEffort, System: req.System + "\n" + expiryConclusionPrompt, Messages: messages, Tools: schemas}
	var result Result
	seen := map[string]bool{}
	for turn := 0; turn < requests; turn++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if req.BeforeModelCall != nil {
			if err := req.BeforeModelCall(); err != nil {
				return result, err
			}
		}
		response, err := p.Chat(ctx, request)
		if err != nil {
			return result, err
		}
		if response == nil {
			return result, errors.New("empty expiry conclusion response")
		}
		result.InputTokens += response.Usage.InputTokens
		result.OutputTokens += response.Usage.OutputTokens
		if req.OnUsage != nil {
			req.OnUsage(response.Usage.InputTokens, response.Usage.OutputTokens)
		}
		if len(response.ToolCalls) == 0 {
			result.Content = strings.TrimSpace(response.Content)
			if result.Content == "" {
				return result, errors.New("empty expiry conclusion")
			}
			return result, nil
		}
		if turn == requests-1 {
			return result, errors.New("summary-only response requested tools")
		}
		if len(safe) == 0 || len(response.ToolCalls) > 32 {
			return result, errors.New("no compatible bounded recovery tool batch")
		}
		request.Messages = append(request.Messages, provider.Message{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls})
		for index, call := range response.ToolCalls {
			denied := tooloutcome.New(tooloutcome.Denied, "expiry_recovery_tool_denied", "not_executed", "This call is outside the expired workflow's bounded, independently authorized read-only recovery. No action was executed.", "finish_summary")
			output, outcome := denied.JSON(), &denied
			t, exists := safe[call.Name]
			if exists && index < 2 && call.ID != "" && !seen[call.ID] && call.ID != c.WaitingToolCallID && ctx.Err() == nil {
				seen[call.ID] = true
				var args map[string]any
				executeErr := json.Unmarshal([]byte(call.Arguments), &args)
				if executeErr == nil {
					executeErr = tooloutcome.ValidateArguments(t.Parameters, args)
				}
				identity := tooloutcome.DefaultIdentity(t.Name, json.RawMessage(call.Arguments))
				if t.Identity != nil {
					identity = t.Identity(json.RawMessage(call.Arguments))
				}
				if executeErr == nil && t.ResolveIdentity != nil {
					identity, executeErr = t.ResolveIdentity(ctx, json.RawMessage(call.Arguments))
				}
				check := func(i tooloutcome.Identity) *tooloutcome.Outcome {
					if i.Risk != tooloutcome.Observation {
						return &denied
					}
					return agent.ApprovalExpiryGuard(records, i, c.RecoveryEpoch)
				}
				toolCtx := tooloutcome.WithBoundary(ctx, check, nil)
				if executeErr == nil {
					executeErr = tooloutcome.CheckBoundary(toolCtx, identity)
				}
				if executeErr == nil {
					ev := ToolEvent{CallID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: "queued"}
					if req.OnToolEvent != nil {
						executeErr = req.OnToolEvent(ev)
						if executeErr == nil {
							ev.Status = "running"
							executeErr = req.OnToolEvent(ev)
						}
					}
					if executeErr == nil {
						output, executeErr = t.Execute(context.WithValue(tooloutcome.WithExecutionIdentity(toolCtx, identity), toolCallIDContextKey{}, call.ID), json.RawMessage(call.Arguments))
					}
					outcome = nil
					ev.Status, ev.Result = "completed", output
					if executeErr != nil {
						o, ok := tooloutcome.FromError(executeErr)
						if !ok {
							o = tooloutcome.New(tooloutcome.Permanent, "observation_failed", "no_side_effects", "Read-only observation failed; finish with retained evidence.", "finish_summary")
						}
						outcome = &o
						output = o.JSON()
						ev.Status, ev.Result, ev.Outcome = "failed", output, outcome
					}
					if req.OnToolEvent != nil {
						if err = req.OnToolEvent(ev); err != nil {
							return result, err
						}
					}
				} else {
					if o, ok := tooloutcome.FromError(executeErr); ok {
						output, outcome = o.JSON(), &o
					}
				}
			}
			request.Messages = append(request.Messages, provider.Message{Role: "tool", ToolCallID: call.ID, ToolName: call.Name, Content: output, ToolOutcome: outcome, ToolFailed: outcome != nil})
		}
		request.Tools = nil
		request.Messages = append(request.Messages, provider.Message{Role: "user", Content: "Now provide the final completed/incomplete summary. No tools are available. Approval expired; this workflow stops after this response."})
	}
	return result, errors.New("expiry conclusion request budget exhausted")
}
