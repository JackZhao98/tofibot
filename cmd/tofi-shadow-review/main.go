// Explicit one-shot synthetic availability probe. Never started by TOFI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/codexauth"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/shadowreview"
)

type observation struct {
	Model             string               `json:"model"`
	Fixture           string               `json:"fixture"`
	Availability      string               `json:"availability"`
	Attempts          int                  `json:"attempts"`
	HTTPStatus        int                  `json:"http_status,omitempty"`
	APIErrorCode      string               `json:"api_error_code,omitempty"`
	APIErrorType      string               `json:"api_error_type,omitempty"`
	Blocker           string               `json:"blocker,omitempty"`
	ContentCharacters int                  `json:"content_characters,omitempty"`
	ToolCalls         int                  `json:"tool_calls"`
	Usage             provider.Usage       `json:"usage"`
	Report            *shadowreview.Report `json:"shadow_report,omitempty"`
	ExecutionApproved bool                 `json:"execution_approved"`
}

type observedProvider struct {
	provider.Provider
	calls    int
	response *provider.ChatResponse
	err      error
}

func (p *observedProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	p.response, p.err = p.Provider.Chat(ctx, req)
	return p.response, p.err
}

func main() {
	dir := flag.String("data-dir", "", "existing TOFI auth data directory (never created or modified)")
	fixture := flag.String("fixture", "public_search", "fixed synthetic fixture")
	limit := flag.Duration("timeout", 30*time.Second, "request deadline, 1s to 60s; no retry")
	flag.Parse()
	result, code := probe(*dir, *fixture, *limit)
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(code)
}

func probe(dir, fixture string, limit time.Duration) (observation, int) {
	o := observation{Model: shadowreview.ModelID, Fixture: fixture, Availability: "not_tested"}
	if limit < time.Second || limit > time.Minute {
		o.Blocker = "invalid_timeout"
		return o, 2
	}
	// Validate the fixed input before touching any auth state.
	if fixture != "public_search" && fixture != "uncertain_publish" && fixture != "expired_approval" {
		o.Blocker = "unknown_synthetic_fixture"
		return o, 2
	}
	m, err := codexauth.OpenReadOnly(dir)
	if err != nil {
		o.Blocker = "existing_auth_directory_required"
		return o, 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	credential, err := m.CredentialReadOnly(ctx)
	if err != nil {
		o.Blocker = "current_unexpired_auth_snapshot_required"
		if strings.Contains(err.Error(), "snapshot expired") {
			o.Blocker = "existing_auth_snapshot_expired"
		}
		return o, 2
	}
	// The existing adapter owns its exact endpoint and OAuth headers. No
	// custom URL, fallback, token refresh, retry policy or tool executor.
	base, err := provider.New("openai_codex", credential)
	if err != nil {
		o.Blocker = "provider_initialization_failed"
		return o, 2
	}
	p := &observedProvider{Provider: base}
	report, err := shadowreview.EvaluateSynthetic(ctx, p, fixture)
	o.Attempts = p.calls
	if p.response != nil {
		o.Availability = "response_received"
		o.ContentCharacters = len([]rune(p.response.Content))
		o.ToolCalls = len(p.response.ToolCalls)
		o.Usage = p.response.Usage
	}
	if err == nil {
		o.Availability = "available"
		o.Report = &report
		return o, 0
	}
	if api, ok := provider.AsAPIError(p.err); ok {
		o.Availability = "request_rejected"
		o.HTTPStatus = api.StatusCode
		o.Blocker = "provider_rejected_request"
		// Capture error schema tokens, never raw body, headers, credential,
		// account identity, request/response text or provider error strings.
		var body struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(api.Body), &body) == nil {
			o.APIErrorCode = schemaToken(body.Error.Code)
			o.APIErrorType = schemaToken(body.Error.Type)
		}
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		o.Blocker = "request_deadline_exceeded"
	} else if p.err != nil {
		o.Blocker = "transport_or_stream_failure"
	} else {
		o.Blocker = "shadow_response_schema_or_tool_call_rejected"
	}
	return o, 2
}

func schemaToken(value string) string {
	if len(value) > 64 || value == "" {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return ""
		}
	}
	return strings.TrimSpace(value)
}
