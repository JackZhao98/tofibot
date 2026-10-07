package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	defaultToolCallsBetweenReports = 15
	defaultMaxDuration             = 3 * time.Minute
	continuationVersion            = 1
)

// SystemPromptOverheadRunes is the fixed agent suffix added by this runtime.
// App runs use ExtraTools and lazy extension discovery, not SkillTools/preloads.
// Keep the provider-request regression test in sync if that configuration changes.
func SystemPromptOverheadRunes() int {
	return len([]rune(agent.ProgressReportPrompt(defaultToolCallsBetweenReports)))
}

type continuationEnvelope struct {
	Version int                 `json:"version"`
	RunID   string              `json:"run_id"`
	BotID   string              `json:"bot_id"`
	Model   string              `json:"model"`
	Agent   *agent.Continuation `json:"agent"`
}

func decodeContinuation(raw json.RawMessage, req Request, model string) (*agent.Continuation, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var checkpoint continuationEnvelope
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode runtime continuation: %w", err)
	}
	if checkpoint.Version != continuationVersion {
		return nil, fmt.Errorf("unsupported runtime continuation version %d", checkpoint.Version)
	}
	if strings.TrimSpace(req.RunID) == "" || checkpoint.RunID != req.RunID {
		return nil, errors.New("runtime continuation run identity does not match request")
	}
	if checkpoint.BotID != req.BotID {
		return nil, errors.New("runtime continuation bot identity does not match request")
	}
	if checkpoint.Model != model {
		return nil, errors.New("runtime continuation model does not match request")
	}
	if err := agent.ValidateContinuation(checkpoint.Agent); err != nil {
		return nil, fmt.Errorf("invalid runtime continuation: %w", err)
	}
	return checkpoint.Agent, nil
}

func encodeContinuation(req Request, model string, continuation *agent.Continuation) (json.RawMessage, error) {
	if err := agent.ValidateContinuation(continuation); err != nil {
		return nil, fmt.Errorf("invalid suspension continuation: %w", err)
	}
	checkpoint, err := json.Marshal(continuationEnvelope{
		Version: continuationVersion,
		RunID:   req.RunID,
		BotID:   req.BotID,
		Model:   model,
		Agent:   continuation,
	})
	if err != nil {
		return nil, fmt.Errorf("encode runtime continuation: %w", err)
	}
	return checkpoint, nil
}

// RenewContinuationQuestion changes only the backend-owned rendezvous ID.
// The provider transcript and tool call remain unchanged; no action is replayed.
func RenewContinuationQuestion(raw json.RawMessage, oldID, newID string) (json.RawMessage, error) {
	var c continuationEnvelope
	if json.Unmarshal(raw, &c) != nil || c.Version != continuationVersion || c.Agent == nil || c.Agent.QuestionID != oldID || strings.TrimSpace(newID) == "" {
		return nil, errors.New("invalid approval renewal checkpoint")
	}
	if err := agent.ValidateContinuation(c.Agent); err != nil {
		return nil, err
	}
	c.Agent.QuestionID = newID
	return json.Marshal(c)
}

type engine struct {
	provider   provider.Provider
	config     Config
	model      string
	credential func(context.Context) (string, error)
}

type toolCallIDContextKey struct{}

// ToolCallID identifies the provider call currently executing a built-in tool.
func ToolCallID(ctx context.Context) string {
	id, _ := ctx.Value(toolCallIDContextKey{}).(string)
	return id
}

// New creates the production agent-loop adapter. It never creates a fake or
// echo provider. Local providers are allowed to omit a key only when the
// provider/base URL explicitly identifies a local endpoint.
func New(cfg Config) (Engine, error) {
	providerName := strings.TrimSpace(cfg.Provider)
	if providerName == "" {
		return nil, errors.New("model provider is not configured")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("model is not configured")
	}
	if cfg.APIKey == "" && cfg.Credential == nil && !isExplicitLocalProvider(providerName, cfg.BaseURL) {
		return nil, fmt.Errorf("API key is required for remote provider %q", providerName)
	}

	var p provider.Provider
	if cfg.Credential == nil {
		var err error
		p, err = newProvider(cfg, cfg.APIKey)
		if err != nil {
			return nil, fmt.Errorf("create model provider: %w", err)
		}
	}
	return &engine{provider: p, config: cfg, model: model, credential: cfg.Credential}, nil
}

func newProvider(cfg Config, credential string) (provider.Provider, error) {
	var opts []provider.Option
	if cfg.BaseURL != "" {
		opts = append(opts, provider.WithBaseURL(cfg.BaseURL))
	}
	// Keep the runtime adapter aligned with the original server wiring:
	// every provider gets the shared retry policy, including ChatStream.
	opts = append(opts, provider.WithDefaultRetry())
	return provider.New(strings.TrimSpace(cfg.Provider), credential, opts...)
}

type credentialProvider struct {
	config     Config
	credential func(context.Context) (string, error)
}

func (p credentialProvider) provider(ctx context.Context) (provider.Provider, error) {
	credential, err := p.credential(ctx)
	if err != nil {
		return nil, err
	}
	return newProvider(p.config, credential)
}

func (p credentialProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	model, err := p.provider(ctx)
	if err != nil {
		return nil, err
	}
	return model.Chat(ctx, req)
}

func (p credentialProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, onDelta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	model, err := p.provider(ctx)
	if err != nil {
		return nil, err
	}
	return model.ChatStream(ctx, req, onDelta)
}

func isExplicitLocalProvider(name, baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ollama", "lmstudio", "lm-studio", "local":
		return strings.TrimSpace(baseURL) != "" && local
	}
	return local
}

func (e *engine) Run(ctx context.Context, req Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("run context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = e.model
	}
	if model == "" {
		return Result{}, errors.New("model is not configured")
	}
	continuation, err := decodeContinuation(req.Continuation, req, model)
	if err != nil {
		return Result{}, err
	}
	if req.ApprovalExpiryRecovery {
		return e.finishApprovalExpiry(ctx, req, model, continuation)
	}

	messages := make([]provider.Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = provider.Message{Role: msg.Role, Content: msg.Content, ImageURLs: append([]string(nil), msg.ImageURLs...)}
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	runCtx, userWait := withUserWaitBudget(runCtx)
	runCtx = withSuspensionControl(runCtx, req.OnSuspend != nil)
	extraTools := make([]agent.ExtraBuiltinTool, 0, len(req.Tools))
	tracker := newToolEventTracker(func(ev ToolEvent) error {
		if req.OnToolEvent == nil {
			return nil
		}
		err := req.OnToolEvent(ev)
		if err != nil {
			cancelRun()
		}
		return err
	})
	resolveIdentity := func(name, args string) tooloutcome.Identity {
		for _, t := range req.Tools {
			if t.Name == name && t.Identity != nil {
				return t.Identity(json.RawMessage(args))
			}
		}
		return tooloutcome.DefaultIdentity(name, json.RawMessage(args))
	}
	tracker.risk = func(name, args string) string { return resolveIdentity(name, args).Risk }
	if req.OnRetry != nil {
		runCtx = provider.WithRetryObserver(runCtx, func(attempt int, _ error, wait time.Duration) { req.OnRetry(attempt, wait) })
	}
	if continuation != nil {
		if err := tracker.restoreContinuation(continuation); err != nil {
			return Result{}, fmt.Errorf("restore tool events: %w", err)
		}
	}
	for _, tool := range req.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return Result{}, errors.New("runtime tool name is required")
		}
		if tool.Execute == nil {
			return Result{}, fmt.Errorf("runtime tool %q has no executor", tool.Name)
		}
		params := tool.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		rawTool := tool
		extraTools = append(extraTools, agent.ExtraBuiltinTool{
			Schema: provider.Tool{
				Name:        rawTool.Name,
				Description: rawTool.Description,
				Parameters:  params,
			},
			HandlerCtx: func(toolCtx context.Context, args map[string]interface{}) (string, error) {
				if err := tracker.Err(); err != nil {
					return "", err
				}
				callID, err := tracker.start(rawTool.Name)
				if err != nil {
					return "", err
				}
				encoded, err := json.Marshal(args)
				if err != nil {
					return "", fmt.Errorf("encode arguments for %s: %w", rawTool.Name, err)
				}
				if err := tooloutcome.ValidateArguments(params, args); err != nil {
					return "", err
				}
				identity := tooloutcome.DefaultIdentity(rawTool.Name, encoded)
				if rawTool.Identity != nil {
					identity = rawTool.Identity(encoded)
				}
				if rawTool.ResolveIdentity != nil {
					identity, err = rawTool.ResolveIdentity(toolCtx, encoded)
					if err != nil {
						if _, typed := tooloutcome.FromError(err); typed {
							return "", err
						}
						return "", tooloutcome.New(tooloutcome.Permanent, "identity_resolution_failed", "not_executed", "The backend target could not be verified before dispatch. Inspect the target or backend status before proposing this operation again.", "verify_target").Err()
					}
				}
				if err := tooloutcome.CheckBoundary(toolCtx, identity); err != nil {
					return "", err
				}
				executionCtx := tooloutcome.WithExecutionIdentity(toolCtx, identity)
				result, executeErr := rawTool.Execute(context.WithValue(executionCtx, toolCallIDContextKey{}, callID), encoded)
				var suspension *userInputSuspensionError
				if executeErr != nil && !errors.As(executeErr, &suspension) && toolCtx.Err() == nil {
					if _, classified := tooloutcome.FromError(executeErr); !classified {
						status, code, next := tooloutcome.Uncertain, "unclassified_tool_failure", "verify_effect"
						if errors.Is(executeErr, errors.ErrUnsupported) {
							status, code, next = tooloutcome.Permanent, "unsupported_operation", "explain_blocker"
						}
						certainty, explanation := "unknown", executeErr.Error()+" Verify the target state before repeating this call."
						if identity.Risk == tooloutcome.Observation {
							status, code, next, certainty = tooloutcome.Permanent, "observation_failed", "explain_blocker", "no_side_effects"
							explanation = executeErr.Error() + " This observation failed without side effects. Fix its precondition (for example, start what it reads) before retrying it, inspect another target, or explain the blocker."
						}
						executeErr = tooloutcome.New(status, code, certainty, explanation, next).Err()
					}
				}
				// A later path lookup cannot establish what this dispatch mutated.
				// Keep its original boundary evidence; unknown creates stay opaque.
				return result, executeErr
			},
		})
	}

	execCtx := models.NewExecutionContext(req.RunID, req.BotID, "")
	defer execCtx.Cancel()
	modelProvider := e.provider
	if e.credential != nil {
		modelProvider = credentialProvider{config: e.config, credential: e.credential}
	}
	var onStream func(string, string)
	if req.OnDelta != nil {
		onStream = func(_ string, delta string) { req.OnDelta(delta) }
	}
	var onThinking func(string, string)
	if req.OnThinking != nil {
		onThinking = func(_ string, delta string) { req.OnThinking(delta) }
	}
	var onReviewDraft func(int, string)
	if req.OnReviewDraft != nil {
		onReviewDraft = func(turnIndex int, content string) {
			if err := req.OnReviewDraft(turnIndex, content); err != nil {
				tracker.setErr(err)
				cancelRun()
			}
		}
	}
	duration := e.config.MaxDuration
	if duration <= 0 {
		duration = defaultMaxDuration
	}
	result, err := agent.RunAgentLoop(agent.AgentConfig{
		Ctx:                        runCtx,
		Provider:                   modelProvider,
		Model:                      model,
		ReasoningEffort:            req.ReasoningEffort,
		System:                     req.System,
		Messages:                   messages,
		ExtraTools:                 extraTools,
		SessionID:                  req.RunID,
		ToolsOnly:                  true,
		Continuation:               continuation,
		ResumeResult:               req.ResumeResult,
		ResumeOutcome:              req.ResumeOutcome,
		ResolveToolIdentity:        resolveIdentity,
		PromptCacheKey:             req.RunID,
		OnThinkingChunk:            onThinking,
		OnStreamReset:              req.OnStreamReset,
		OnFinalDraftDemoted:        onReviewDraft,
		MaxToolCallsBetweenReports: defaultToolCallsBetweenReports,
		MaxRunDuration:             duration,
		UserWaitDuration:           userWait.duration,
		OnContextEstimate:          req.OnContextEstimate,
		OnUsage:                    req.OnUsage,
		OnCompact:                  req.OnCompact,
		OnStreamChunk:              onStream,
		BeforeFinalResponse:        req.BeforeFinalResponse,
		FinalResponseRepairTools:   append([]string(nil), req.FinalResponseRepairTools...),
		BeforeModelCall: func() error {
			if req.BeforeModelCall == nil {
				return nil
			}
			if err := req.BeforeModelCall(); err != nil {
				cancelRun()
				return err
			}
			return nil
		},
		OnAssistantTurn: func(turnIndex int, content string) {
			if req.OnAssistantTurn == nil {
				return
			}
			if err := req.OnAssistantTurn(turnIndex, content); err != nil {
				tracker.setErr(err)
				cancelRun()
			}
		},
		OnMessage: func(msg provider.Message) {
			if len(msg.ToolCalls) > 0 {
				tracker.queue(msg.ToolCalls)
				if tracker.Err() != nil {
					cancelRun()
				}
				return
			}
			if msg.Role == "tool" {
				tracker.finish(msg)
				if tracker.Err() != nil {
					cancelRun()
				}
			}
		},
	}, execCtx)
	if callbackErr := tracker.Err(); callbackErr != nil {
		return Result{}, callbackErr
	}
	if err != nil {
		return Result{}, err
	}
	if result.Suspended {
		if result.Continuation == nil || strings.TrimSpace(result.QuestionID) == "" || result.QuestionID != result.Continuation.QuestionID {
			return Result{}, errors.New("agent returned invalid suspension state")
		}
		if req.OnSuspend == nil {
			return Result{}, errors.New("agent suspended without a durable suspension callback")
		}
		// Recheck the caller-owned safe boundary immediately before persisting a
		// waiting run. This prevents a just-arrived cancellation or steering
		// decision from being buried beneath a checkpoint.
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if req.BeforeModelCall != nil {
			if err := req.BeforeModelCall(); err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				return Result{}, fmt.Errorf("before suspend: %w", err)
			}
		}
		checkpoint, err := encodeContinuation(req, model, result.Continuation)
		if err != nil {
			return Result{}, err
		}
		if err := req.OnSuspend(result.QuestionID, checkpoint); err != nil {
			return Result{}, err
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{
			InputTokens:  result.TotalUsage.InputTokens,
			OutputTokens: result.TotalUsage.OutputTokens,
			Suspended:    true,
		}, nil
	}
	return Result{
		Content:         result.Content,
		BudgetExhausted: result.BudgetExhausted, BudgetReason: result.BudgetReason,
		InputTokens:  result.TotalUsage.InputTokens,
		OutputTokens: result.TotalUsage.OutputTokens,
	}, nil
}
