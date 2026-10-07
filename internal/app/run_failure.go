package app

import (
	"encoding/json"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
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
	if strings.HasPrefix(errorText, noFinalAnswerCode) {
		failure.Code = noFinalAnswerCode
		failure.Message = "The task ended without a final answer. Completed tool results are retained; retry to continue."
		return failure
	}
	if errorText == "approval_expired" {
		failure.Code = "approval_expired"
		failure.Message = "Approval expired. This workflow stopped; completed results are retained and uncertain effects require verification."
		return failure
	}
	// Model account failures name the account state; provider bodies stay in the audit error.
	if code, message := modelAccountFailure(errorText, r.Model); code != "" {
		failure.Code, failure.Message = code, message
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

var (
	modelUnconfiguredMarkers = []string{"codex is not connected", "provider is required", "unknown provider:", "model is not configured", "model provider is not configured"}
	modelAuthMarkers         = []string{"codex login expired", "codex access snapshot expired", "reconnect your chatgpt account", "api error (http 401)", "token_invalidated", "token_expired", "invalid_api_key", "refresh_token_reused", "invalid_grant", "authentication_error"}
	modelQuotaMarkers        = []string{"usage_limit_reached", "insufficient_quota", "credit balance is too low"}
	codexFailureMarkers      = []string{"codex", "chatgpt", "token_invalidated", "token_expired", "refresh_token_reused", "invalid_grant", "usage_limit_reached"}
)

// failureProvider names the provider a failure belongs to: the marker of an
// unconfigured provider, else the run's model, else Codex-specific text.
func failureProvider(errorText, model string) string {
	if _, rest, ok := strings.Cut(errorText, "model provider is not configured: "); ok {
		name, _, _ := strings.Cut(rest, " ")
		return strings.Trim(name, ".,;:")
	}
	if strings.TrimSpace(model) != "" {
		return runtime.ModelProvider(model)
	}
	for _, marker := range codexFailureMarkers {
		if strings.Contains(errorText, marker) {
			return providerCodex
		}
	}
	return ""
}

// modelAccountFailure recognizes only exact provider/account markers, so an
// unrelated tool error that merely mentions a status number stays generic.
// Messages name the provider when the failing model identifies it.
func modelAccountFailure(errorText, model string) (string, string) {
	has := func(markers []string) bool {
		for _, marker := range markers {
			if strings.Contains(errorText, marker) {
				return true
			}
		}
		return false
	}
	switch {
	case has(modelUnconfiguredMarkers):
		switch name := failureProvider(errorText, model); name {
		case providerCodex:
			return "model_unconfigured", "No AI provider is available for this model: the Codex account is not connected. Connect Codex under Settings, or choose a model from another connected provider, then retry."
		case providerOpenAI, providerAnthropic:
			return "model_unconfigured", "No AI provider is available for this model: no " + providerLabel(name) + " API key is configured. Add the key under Settings, or choose a model from another connected provider, then retry."
		}
		return "model_unconfigured", "No AI provider is available: no model provider is connected. Connect Codex or add an OpenAI or Claude API key under Settings, then retry."
	case has(modelAuthMarkers):
		switch name := failureProvider(errorText, model); name {
		case providerCodex:
			return "model_auth_invalid", "The model account sign-in is no longer valid, so the model rejected this request. Reconnect the Codex account under Settings, then retry."
		case providerOpenAI, providerAnthropic:
			return "model_auth_invalid", providerLabel(name) + " rejected the stored API key, so the model rejected this request. Replace the key under Settings, then retry."
		}
		return "model_auth_invalid", "The model provider rejected the stored credentials for this request. Reconnect Codex or replace the API key under Settings, then retry."
	case has(modelQuotaMarkers):
		if name := failureProvider(errorText, model); name == providerOpenAI || name == providerAnthropic {
			return "model_quota_exhausted", "The " + providerLabel(name) + " account has reached its usage or billing limit, so the model rejected this request. Retry after the limit resets or choose a model from another provider."
		}
		return "model_quota_exhausted", "The model account has reached its usage limit, so the model rejected this request. Retry after the limit resets or connect another account."
	}
	return "", ""
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
