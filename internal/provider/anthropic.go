package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Anthropic Messages API adapter.
//
// Every request streams (Chat aggregates the stream), so large max_tokens
// never hits a non-streaming timeout and both paths share one parser.
//
// Reasoning effort → thinking controls (see anthropicThinkingFor):
//
//	Adaptive models (Opus 4.6+, Sonnet 4.6+, Sonnet/Opus 5.x, Fable, Mythos,
//	unknown future models): thinking {type: adaptive}, output_config.effort =
//	low|medium|high|xhigh|max as given ("none"/"minimal" → low, since current
//	models cannot disable thinking; "" → the model's default effort). Opus and
//	Sonnet 4.6 have no xhigh, so xhigh → high there. display: "summarized"
//	(4.7+ default to omitted) so reasoning streams to the UI.
//	Budget models (Haiku 4.5, Sonnet/Opus 4.5 and older 4.x, 3.7):
//	thinking {type: enabled, budget_tokens}: low 1024, medium 4096,
//	high 16384, xhigh 32768, max 49152; "" / "none" → no thinking.
//
// Thinking blocks (with signatures) and redacted_thinking come back on the
// response as one ReasoningItem{Provider: "anthropic"} holding the turn's
// content blocks, and are replayed verbatim on the next request.
const (
	anthropicVersion         = "2023-06-01"
	anthropicBindingBeta     = "thinking-binding-controls-2026-08-01"
	anthropicInterleavedBeta = "interleaved-thinking-2025-05-14"
	anthropicReasoningSource = "anthropic"
	anthropicWebSearchType   = "web_search_20250305"
	// The basic fetch variant, like the basic search one, runs on every
	// Messages API model without code-execution round trips; the page text is
	// billed as ordinary input tokens and the fetch itself carries no fee.
	anthropicWebFetchType     = "web_fetch_20250910"
	anthropicDefaultMaxTokens = 32000
	anthropicHighMaxTokens    = 64000
)

type anthropicProvider struct {
	apiKey  string
	baseURL string
	headers map[string]string
}

func newAnthropic(apiKey string, cfg *providerConfig) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required")
	}
	baseURL := "https://api.anthropic.com"
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}
	headers := cfg.ExtraHeaders
	// A key not scoped to a workspace travels as "key\x00wrkspc_…"; every
	// request then names the workspace (anthropic-workspace-id).
	if key, workspace, ok := strings.Cut(apiKey, "\x00"); ok {
		apiKey = key
		merged := map[string]string{"anthropic-workspace-id": workspace}
		for k, v := range headers {
			merged[k] = v
		}
		headers = merged
	}
	return &anthropicProvider{apiKey: apiKey, baseURL: baseURL, headers: headers}, nil
}

// Chat aggregates a streamed response.
func (a *anthropicProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return a.ChatStream(ctx, req, nil)
}

// ChatStream streams one Messages API response. A rejection of replayed
// thinking blocks gets one retry without them; the response reports it so
// the caller drops replay for the rest of the run.
func (a *anthropicProvider) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	forwarded := false
	forward := func(delta StreamDelta) {
		forwarded = true
		if onDelta != nil {
			onDelta(delta)
		}
	}
	if req.WebSearch != nil && a.webSearchRefused() {
		plain := *req
		plain.WebSearch = nil
		req = &plain
	}
	if req.WebFetch != nil && a.webFetchRefused() {
		plain := *req
		plain.WebFetch = nil
		req = &plain
	}
	resp, err := a.stream(ctx, req, forward)
	if err != nil && !forwarded && isAnthropicServerToolPairingError(err) {
		// A replayed server tool block lost its pair somewhere upstream: send
		// the history once more with every server tool block and the citations
		// that point at them removed, instead of failing the turn.
		log.Printf("[anthropic] unpaired server tool block in history (%s); retrying once without replayed server tool blocks", strings.TrimSpace(err.Error()))
		stripped := *req
		stripped.Messages = stripServerToolBlocks(req.Messages)
		req = &stripped
		resp, err = a.stream(ctx, req, forward)
	}
	// An account that has not enabled a server tool (an organization setting)
	// or a model that refuses it gets the request again without that tool,
	// and the refusal is remembered for this credential: a turn is never lost
	// to an optional capability. A rejection naming both drops both.
	for attempt := 0; attempt < 2 && err != nil && !forwarded; attempt++ {
		dropSearch := req.WebSearch != nil && isAnthropicWebSearchRejection(err)
		dropFetch := req.WebFetch != nil && isAnthropicWebFetchRejection(err)
		if !dropSearch && !dropFetch {
			break
		}
		plain := *req
		if dropSearch {
			anthropicWebSearchRefused.Store(a.apiKey, true)
			plain.WebSearch = nil
			log.Printf("[anthropic] web search refused (%s); continuing without it for this credential until restart", strings.TrimSpace(err.Error()))
		}
		if dropFetch {
			anthropicWebFetchRefused.Store(a.apiKey, true)
			plain.WebFetch = nil
			log.Printf("[anthropic] web fetch refused (%s); continuing without it for this credential until restart", strings.TrimSpace(err.Error()))
		}
		req = &plain
		resp, err = a.stream(ctx, req, forward)
	}
	if err != nil && !forwarded && req.ToolChoice == ToolChoiceAny && !anthropicAnyRejected(req.Model) && isAnthropicToolChoiceRejection(err) {
		// The model does not take a forced tool call (some thinking
		// configurations refuse it): remember that and let it choose.
		anthropicAnyRefused.Store(req.Model, true)
		log.Printf("[anthropic] %s refused tool_choice any (%s); not requiring a tool call for this model until restart", req.Model, strings.TrimSpace(err.Error()))
		retry := *req
		retry.ToolChoice = ""
		resp, err = a.stream(ctx, &retry, onDelta)
	}
	if err != nil && !forwarded && !req.OmitReasoningReplay && hasAnthropicReplay(req.Messages) && isAnthropicReplayRejection(err) {
		retry := *req
		retry.OmitReasoningReplay = true
		resp, err = a.stream(ctx, &retry, onDelta)
		if err == nil {
			resp.ReasoningReplayRejected = true
		}
	}
	return resp, err
}

func (a *anthropicProvider) stream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	payload, betas := a.buildPayload(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	build := func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("x-api-key", a.apiKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)
		if len(betas) > 0 {
			httpReq.Header.Set("anthropic-beta", strings.Join(betas, ","))
		}
		for key, value := range a.headers {
			httpReq.Header.Set(key, value)
		}
		return httpReq, nil
	}
	resp, err := streamWithWatchdogs(ctx, "anthropic", build, func(r io.Reader) (*ChatResponse, error) {
		return parseAnthropicStream(r, onDelta)
	})
	return resp, codeRequestRejection(err)
}

// streamWithWatchdogs runs one streaming POST under the shared idle and wall
// watchdogs (streamIdleTimeout covers the header wait too) and maps their
// aborts to StreamIdleError / StreamWallCapError.
func streamWithWatchdogs(ctx context.Context, providerName string, build func(context.Context) (*http.Request, error), parse func(io.Reader) (*ChatResponse, error)) (*ChatResponse, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle, wallCap := streamIdleTimeout, streamWallCap
	var idleFired, wallFired atomic.Bool
	timer := time.AfterFunc(idle, func() { idleFired.Store(true); cancel() })
	defer timer.Stop()
	watchdogErr := func(err error) error {
		if ctx.Err() != nil {
			return err
		}
		if wallFired.Load() {
			return &StreamWallCapError{Cap: wallCap}
		}
		if idleFired.Load() {
			return &StreamIdleError{Idle: idle}
		}
		return err
	}

	httpReq, err := build(streamCtx)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// No client timeout: the watchdogs bound header wait and body separately.
	resp, err := (&http.Client{}).Do(httpReq)
	if err != nil {
		return nil, watchdogErr(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, NewAPIErrorFor(providerName, resp, string(respBody))
	}

	timer.Reset(idle)
	wall := time.AfterFunc(wallCap, func() { wallFired.Store(true); cancel() })
	defer wall.Stop()
	result, err := parse(&idleReader{r: resp.Body, timer: timer, idle: idle})
	if err == nil && (idleFired.Load() || wallFired.Load()) {
		err = context.Canceled // never return a stream cut short as complete
	}
	if err != nil {
		return nil, watchdogErr(err)
	}
	return result, nil
}

// ── Request ──

type anthropicThinkingMode int

const (
	anthropicNoThinking anthropicThinkingMode = iota
	anthropicBudgetThinking
	anthropicAdaptive46 // adaptive; no xhigh; display defaults to summarized
	anthropicAdaptive
)

func anthropicThinkingModeFor(model string) anthropicThinkingMode {
	m := strings.ToLower(model)
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(m, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("claude-3-7"):
		return anthropicBudgetThinking
	case strings.HasPrefix(m, "claude-3") || strings.HasPrefix(m, "claude-2") || strings.HasPrefix(m, "claude-instant"):
		return anthropicNoThinking
	case has("opus-4-6", "sonnet-4-6"):
		return anthropicAdaptive46
	case has("haiku-4", "sonnet-4-5", "opus-4-5", "opus-4-1", "opus-4-0", "sonnet-4-0", "opus-4-2025", "sonnet-4-2025"):
		return anthropicBudgetThinking
	default:
		return anthropicAdaptive
	}
}

// anthropicThinking is the per-request thinking configuration.
type anthropicThinking struct {
	mode   anthropicThinkingMode
	effort string // output_config.effort (adaptive modes); "" = model default
	budget int    // budget_tokens (budget mode); 0 = thinking off
}

func (t anthropicThinking) enabled() bool {
	return t.mode == anthropicAdaptive || t.mode == anthropicAdaptive46 || t.budget > 0
}

func anthropicThinkingFor(model, effort string) anthropicThinking {
	t := anthropicThinking{mode: anthropicThinkingModeFor(model)}
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch t.mode {
	case anthropicAdaptive, anthropicAdaptive46:
		switch effort {
		case "none", "minimal":
			t.effort = "low"
		case "low", "medium", "high", "xhigh", "max":
			t.effort = effort
		}
		if t.mode == anthropicAdaptive46 && t.effort == "xhigh" {
			t.effort = "high"
		}
	case anthropicBudgetThinking:
		t.budget = map[string]int{"low": 1024, "medium": 4096, "high": 16384, "xhigh": 32768, "max": 49152}[effort]
	}
	return t
}

// anthropicMaxTokens picks a generous output cap: 32k by default, 64k for
// high-effort work, never above the model's limit.
func anthropicMaxTokens(model string, thinking anthropicThinking) int {
	limit := 0
	if info, ok := GetModelInfo(model); ok && info.Provider == "anthropic" {
		limit = info.MaxOutputTokens
	}
	if limit == 0 {
		switch thinking.mode {
		case anthropicNoThinking:
			limit = 8192
		case anthropicBudgetThinking:
			limit = 32000
		default:
			limit = 128000
		}
	}
	want := anthropicDefaultMaxTokens
	switch thinking.effort {
	case "high", "xhigh", "max":
		want = anthropicHighMaxTokens
	}
	if thinking.budget > 0 {
		want = max(want, thinking.budget+16000)
	}
	return min(want, limit)
}

// AnthropicOutputReserve is the max_tokens a request for model at effort asks
// for. Callers use it to keep input + reserve within the context window.
func AnthropicOutputReserve(model, effort string) int {
	return anthropicMaxTokens(model, anthropicThinkingFor(model, effort))
}

func cacheControl() map[string]any { return map[string]any{"type": "ephemeral"} }

// buildPayload constructs the Messages API body and the beta headers it needs.
func (a *anthropicProvider) buildPayload(req *ChatRequest) (map[string]any, []string) {
	thinking := anthropicThinkingFor(req.Model, req.ReasoningEffort)
	maxTokens := anthropicMaxTokens(req.Model, thinking)
	if thinking.budget >= maxTokens {
		thinking.budget = maxTokens / 2
	}
	replay := thinking.enabled() && !req.OmitReasoningReplay
	messages, replayed := convertAnthropicMessages(req.Messages, replay)

	// Manual-budget thinking must open the trailing tool round with the
	// thinking block it produced; without one, run this request unthought.
	if thinking.mode == anthropicBudgetThinking && thinking.budget > 0 && trailingToolTurnLacksThinking(messages) {
		thinking.budget = 0
	}

	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   messages,
	}
	var betas []string
	switch thinking.mode {
	case anthropicAdaptive, anthropicAdaptive46:
		config := map[string]any{"type": "adaptive"}
		if thinking.mode == anthropicAdaptive {
			config["display"] = "summarized"
		}
		if replayed {
			// History edits (compaction, trimming) invalidate later thinking
			// blocks; drop them server-side instead of failing the request.
			config["block_binding"] = map[string]any{"prefix_mismatch_behavior": "drop_block"}
			betas = append(betas, anthropicBindingBeta)
		}
		payload["thinking"] = config
		if thinking.effort != "" {
			payload["output_config"] = map[string]any{"effort": thinking.effort}
		}
	case anthropicBudgetThinking:
		if thinking.budget > 0 {
			payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": thinking.budget}
			if len(req.Tools) > 0 {
				betas = append(betas, anthropicInterleavedBeta)
			}
		}
	}

	if system := strings.TrimSpace(req.System); system != "" {
		payload["system"] = []map[string]any{{"type": "text", "text": req.System, "cache_control": cacheControl()}}
	}
	if len(req.Tools) > 0 || req.WebSearch != nil || req.WebFetch != nil {
		tools := make([]map[string]any, 0, len(req.Tools)+2)
		if req.WebSearch != nil {
			// The basic variant runs on every Messages API model and adds no
			// code-execution round trips; the model decides when to search.
			search := map[string]any{"type": anthropicWebSearchType, "name": "web_search"}
			if req.WebSearch.MaxUses > 0 {
				search["max_uses"] = req.WebSearch.MaxUses
			}
			tools = append(tools, search)
		}
		if req.WebFetch != nil {
			// Listed right after search: the two lookups come before the
			// Bot's own tools in the request.
			fetch := map[string]any{"type": anthropicWebFetchType, "name": "web_fetch"}
			if req.WebFetch.MaxUses > 0 {
				fetch["max_uses"] = req.WebFetch.MaxUses
			}
			if req.WebFetch.MaxContentTokens > 0 {
				fetch["max_content_tokens"] = req.WebFetch.MaxContentTokens
			}
			tools = append(tools, fetch)
		}
		for _, t := range req.Tools {
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tool := map[string]any{"name": t.Name, "input_schema": schema}
			if t.Description != "" {
				tool["description"] = t.Description
			}
			tools = append(tools, tool)
		}
		// Tools render first; one breakpoint caches the whole tool set.
		tools[len(tools)-1]["cache_control"] = cacheControl()
		payload["tools"] = tools
		switch {
		case req.ToolChoice == ToolChoiceNone:
			payload["tool_choice"] = map[string]any{"type": "none"}
		case req.ToolChoice == ToolChoiceAny && !anthropicAnyRejected(req.Model):
			payload["tool_choice"] = map[string]any{"type": "any"}
		}
	}
	return payload, betas
}

// anthropicContinueTurn is the user turn that closes a request whose history
// ends with an assistant turn (see convertAnthropicMessages).
const anthropicContinueTurn = "Continue."

// anthropicMessage is one Messages API turn. Blocks are maps built here or
// json.RawMessage replayed verbatim.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

// convertAnthropicMessages maps the unified history to Messages API turns,
// merging consecutive same-role messages (tool results join one user turn).
// It places the rolling cache breakpoints: on the last turn, and on the turn
// that ended the previous request (the user turn before the latest assistant
// turn), so each agent-loop step reads the prefix the previous one wrote.
// replayed reports whether any thinking block was replayed.
func convertAnthropicMessages(msgs []Message, replay bool) (out []anthropicMessage, replayed bool) {
	appendBlocks := func(role string, blocks []any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			var blocks []any
			for _, url := range msg.ImageURLs {
				blocks = append(blocks, anthropicImageBlock(url))
			}
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
			}
			appendBlocks("user", blocks)
		case "assistant":
			blocks, usedReplay := anthropicAssistantBlocks(msg, replay)
			replayed = replayed || usedReplay
			appendBlocks("assistant", blocks)
		case "tool":
			result := map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID}
			if len(msg.ImageURLs) > 0 {
				var content []any
				if strings.TrimSpace(msg.Content) != "" {
					content = append(content, map[string]any{"type": "text", "text": msg.Content})
				}
				for _, url := range msg.ImageURLs {
					content = append(content, anthropicImageBlock(url))
				}
				result["content"] = content
			} else if msg.Content != "" {
				result["content"] = msg.Content
			}
			if msg.ToolFailed {
				result["is_error"] = true
			}
			// tool_result blocks must lead their user turn.
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				content := out[n-1].Content
				at := 0
				for at < len(content) && isToolResultBlock(content[at]) {
					at++
				}
				content = append(content[:at], append([]any{result}, content[at:]...)...)
				out[n-1].Content = content
				continue
			}
			out = append(out, anthropicMessage{Role: "user", Content: []any{result}})
		}
	}

	// Current Claude models reject a request whose last turn is the
	// assistant's (prefill is gone), and no caller here wants one: history can
	// end on the Bot's own reply, or on a user turn that rendered empty. Close
	// the request with a user turn instead.
	if n := len(out); n > 0 && out[n-1].Role == "assistant" {
		out = append(out, anthropicMessage{Role: "user", Content: []any{map[string]any{"type": "text", "text": anthropicContinueTurn}}})
	}
	if n := len(out); n > 0 {
		markCacheBreakpoint(out[n-1])
		for i := n - 1; i >= 0; i-- {
			if out[i].Role == "assistant" {
				if i > 0 {
					markCacheBreakpoint(out[i-1])
				}
				break
			}
		}
	}
	return out, replayed
}

// AnthropicTurnRoles reports the roles of the Messages API turns a history
// converts to. The last one is always "user" (a tool result or user text) for
// a history that has any turn.
func AnthropicTurnRoles(msgs []Message, replay bool) []string {
	turns, _ := convertAnthropicMessages(msgs, replay)
	roles := make([]string, len(turns))
	for i, turn := range turns {
		roles[i] = turn.Role
	}
	return roles
}

func isToolResultBlock(block any) bool {
	m, ok := block.(map[string]any)
	return ok && m["type"] == "tool_result"
}

// markCacheBreakpoint tags the turn's last block. Replayed (raw) blocks and
// thinking blocks cannot carry cache_control, so assistant turns are skipped.
func markCacheBreakpoint(msg anthropicMessage) {
	if msg.Role != "user" || len(msg.Content) == 0 {
		return
	}
	if block, ok := msg.Content[len(msg.Content)-1].(map[string]any); ok {
		block["cache_control"] = cacheControl()
	}
}

// anthropicImageBlock maps a data: URL to a base64 source and any other URL
// to a url source.
func anthropicImageBlock(url string) map[string]any {
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		if meta, data, found := strings.Cut(rest, ","); found && strings.HasSuffix(meta, ";base64") {
			return map[string]any{"type": "image", "source": map[string]any{
				"type":       "base64",
				"media_type": strings.TrimSuffix(meta, ";base64"),
				"data":       data,
			}}
		}
	}
	return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": url}}
}

// toolInputRaw keeps a valid JSON object's original bytes so a replayed
// tool_use matches what the model produced; anything else becomes {}.
func toolInputRaw(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return map[string]any{}
}

func toolInput(arguments string) any {
	var input map[string]any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil || input == nil {
		return map[string]any{}
	}
	return input
}

// anthropicAssistantBlocks rebuilds an assistant turn. With replay, the
// captured content blocks go back verbatim when they still match the
// message's text and tool calls; otherwise the captured thinking blocks lead
// the rebuilt text and tool_use blocks.
func anthropicAssistantBlocks(msg Message, replay bool) ([]any, bool) {
	var blocks []any
	usedReplay := false
	if replay {
		if raw := pairServerToolBlocks(anthropicReplayBlocks(msg.ReasoningItems)); len(raw) > 0 {
			if replayMatches(raw, msg) {
				out := make([]any, len(raw))
				for i, b := range raw {
					out[i] = b
				}
				return out, true
			}
			for _, b := range raw {
				if t := blockType(b); t == "thinking" || t == "redacted_thinking" {
					blocks = append(blocks, b)
					usedReplay = true
				}
			}
		}
	}
	if strings.TrimSpace(msg.Content) != "" { // whitespace-only text is rejected
		blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": toolInputRaw(tc.Arguments)})
	}
	return blocks, usedReplay
}

func anthropicReplayBlocks(items []ReasoningItem) []json.RawMessage {
	for _, item := range items {
		if item.Provider != anthropicReasoningSource || len(item.Content) == 0 {
			continue
		}
		var raw []json.RawMessage
		if json.Unmarshal(item.Content, &raw) == nil {
			return raw
		}
	}
	return nil
}

// HasAnthropicReplay reports whether msgs carry Anthropic thinking blocks that
// the next request would replay.
func HasAnthropicReplay(msgs []Message) bool { return hasAnthropicReplay(msgs) }

func hasAnthropicReplay(msgs []Message) bool {
	for _, msg := range msgs {
		if len(anthropicReplayBlocks(msg.ReasoningItems)) > 0 {
			return true
		}
	}
	return false
}

func blockType(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// replayMatches reports whether captured blocks still describe msg: same
// text and the same tool calls with equal inputs.
func replayMatches(raw []json.RawMessage, msg Message) bool {
	var text strings.Builder
	var calls []ToolCall
	for _, b := range raw {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(b, &block) != nil {
			return false
		}
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			calls = append(calls, ToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
		}
	}
	if strings.TrimSpace(text.String()) != strings.TrimSpace(msg.Content) || len(calls) != len(msg.ToolCalls) {
		return false
	}
	for i, call := range calls {
		want := msg.ToolCalls[i]
		if call.ID != want.ID || call.Name != want.Name {
			return false
		}
		a, _ := json.Marshal(toolInput(call.Arguments))
		b, _ := json.Marshal(toolInput(want.Arguments))
		if !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}

// trailingToolTurnLacksThinking reports a last assistant turn with tool_use
// that does not open with a thinking block.
func trailingToolTurnLacksThinking(messages []anthropicMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		hasToolUse := false
		for _, b := range messages[i].Content {
			if anyBlockType(b) == "tool_use" {
				hasToolUse = true
			}
		}
		if !hasToolUse {
			return false
		}
		first := anyBlockType(messages[i].Content[0])
		return first != "thinking" && first != "redacted_thinking"
	}
	return false
}

func anyBlockType(block any) string {
	switch b := block.(type) {
	case map[string]any:
		t, _ := b["type"].(string)
		return t
	case json.RawMessage:
		return blockType(b)
	}
	return ""
}

// anthropicWebSearchRefused remembers credentials whose requests the API
// rejected for declaring server-side web search.
var anthropicWebSearchRefused sync.Map

func (a *anthropicProvider) webSearchRefused() bool {
	_, refused := anthropicWebSearchRefused.Load(a.apiKey)
	return refused
}

// isAnthropicWebSearchRejection matches the API saying this credential,
// organization or model cannot use the web search tool. A 400 that merely
// names the tool because the request was malformed (a replayed history with an
// unpaired server tool block) is not a refusal and never disables search.
func isAnthropicWebSearchRejection(err error) bool {
	return isServerToolRefusal(err, "web_search", "web search")
}

// isServerToolRefusal is true for a 400/403 whose message both names the tool
// and says the capability is unavailable (not enabled, not permitted, not
// supported, disabled). A message about the shape of the request (a position
// such as "messages.7", a missing or unpaired block) is never a refusal.
func isServerToolRefusal(err error, names ...string) bool {
	apiErr, ok := AsAPIError(err)
	if !ok || (apiErr.StatusCode != 400 && apiErr.StatusCode != 403) {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	named := false
	for _, n := range names {
		if strings.Contains(body, n) {
			named = true
		}
	}
	if !named || isMalformedRequestBody(body) {
		return false
	}
	for _, phrase := range []string{"not enabled", "isn't enabled", "is not available", "not available", "not supported", "unsupported", "not permitted", "not allowed", "disabled", "does not support", "doesn't support", "has not been enabled", "permission"} {
		if strings.Contains(body, phrase) {
			return true
		}
	}
	return false
}

// isMalformedRequestBody recognises an error about the structure of the
// request: it points at a message position or at a block lacking its pair.
func isMalformedRequestBody(body string) bool {
	if strings.Contains(body, "messages.") || strings.Contains(body, "without a corresponding") ||
		strings.Contains(body, "tool_use_id") || strings.Contains(body, "tool_result") || strings.Contains(body, "tool_use ") {
		return true
	}
	return false
}

// isAnthropicServerToolPairingError matches the 400 for a replayed server tool
// use block with no matching result (or a result with no use).
func isAnthropicServerToolPairingError(err error) bool {
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	if !strings.Contains(body, "without a corresponding") && !strings.Contains(body, "without a matching") {
		return false
	}
	return strings.Contains(body, "web_search") || strings.Contains(body, "web_fetch") || strings.Contains(body, "server_tool_use") || strings.Contains(body, "_tool_result")
}

// anthropicWebFetchRefused remembers credentials whose requests the API
// rejected for declaring server-side web fetch.
var anthropicWebFetchRefused sync.Map

func (a *anthropicProvider) webFetchRefused() bool {
	_, refused := anthropicWebFetchRefused.Load(a.apiKey)
	return refused
}

// isAnthropicWebFetchRejection is isAnthropicWebSearchRejection for fetch.
func isAnthropicWebFetchRejection(err error) bool {
	return isServerToolRefusal(err, "web_fetch", "web fetch")
}

// anthropicAnyRefused remembers models whose API refused tool_choice any, so
// the request is not rejected and retried on every turn.
var anthropicAnyRefused sync.Map

func anthropicAnyRejected(model string) bool {
	_, refused := anthropicAnyRefused.Load(model)
	return refused
}

// isAnthropicToolChoiceRejection matches a 400 saying that a forced tool
// choice is not available (for example with thinking enabled), not any 400 that
// merely names the field.
func isAnthropicToolChoiceRejection(err error) bool {
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	if !strings.Contains(body, "tool_choice") {
		return false
	}
	for _, wording := range []string{"forces tool use", "may not be enabled", "not supported", "does not support", "only supports"} {
		if strings.Contains(body, wording) {
			return true
		}
	}
	return false
}

// isAnthropicReplayRejection matches a 400 caused by replayed thinking (bad
// or conversation-bound signature) or by the binding beta it needs.
func isAnthropicReplayRejection(err error) bool {
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return strings.Contains(body, "signature") || strings.Contains(body, "redacted_thinking") ||
		strings.Contains(body, "`thinking`") || strings.Contains(body, "thinking block") ||
		strings.Contains(body, "block_binding") || strings.Contains(body, "anthropic-beta")
}

// ── Response ──

// anthropicBlock accumulates one streamed content block.
type anthropicBlock struct {
	index     int
	kind      string
	start     json.RawMessage // content_block_start payload, kept for unknown kinds
	id, name  string
	data      string // redacted_thinking
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	input     strings.Builder
}

// anthropicAPIStatus maps a streamed error type to the HTTP status the API
// uses for it, so retry classification matches a non-streamed failure.
func anthropicAPIStatus(errorType string) int {
	switch errorType {
	case "invalid_request_error":
		return 400
	case "authentication_error":
		return 401
	case "billing_error":
		return 402
	case "permission_error":
		return 403
	case "not_found_error":
		return 404
	case "request_too_large":
		return 413
	case "rate_limit_error":
		return 429
	case "overloaded_error":
		return 529
	default:
		return 500
	}
}

func parseAnthropicStream(body io.Reader, onDelta func(StreamDelta)) (*ChatResponse, error) {
	emit := func(d StreamDelta) {
		if onDelta != nil {
			onDelta(d)
		}
	}
	result := &ChatResponse{}
	blocks := map[int]*anthropicBlock{}
	var usage struct {
		input, output, cacheRead, cacheWrite, webSearches, webFetches int64
	}
	readUsage := func(raw json.RawMessage) {
		var u struct {
			InputTokens              *int64 `json:"input_tokens"`
			OutputTokens             *int64 `json:"output_tokens"`
			CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
			ServerToolUse            *struct {
				WebSearchRequests int64 `json:"web_search_requests"`
				WebFetchRequests  int64 `json:"web_fetch_requests"`
			} `json:"server_tool_use"`
		}
		if len(raw) == 0 || json.Unmarshal(raw, &u) != nil {
			return
		}
		// message_delta usage is cumulative; a field it omits keeps its value.
		set := func(dst *int64, v *int64) {
			if v != nil && *v > 0 {
				*dst = *v
			}
		}
		set(&usage.input, u.InputTokens)
		set(&usage.output, u.OutputTokens)
		set(&usage.cacheRead, u.CacheReadInputTokens)
		set(&usage.cacheWrite, u.CacheCreationInputTokens)
		if u.ServerToolUse != nil && u.ServerToolUse.WebSearchRequests > 0 {
			usage.webSearches = u.ServerToolUse.WebSearchRequests
		}
		if u.ServerToolUse != nil && u.ServerToolUse.WebFetchRequests > 0 {
			usage.webFetches = u.ServerToolUse.WebFetchRequests
		}
	}
	var stopReason string
	var stopDetails struct {
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	}
	completed := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // event:, comments, blank separators
		}
		data = strings.TrimSpace(data)
		var ev struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			Message      json.RawMessage `json:"message"`
			ContentBlock json.RawMessage `json:"content_block"`
			Delta        json.RawMessage `json:"delta"`
			Usage        json.RawMessage `json:"usage"`
			Error        struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			var msg struct {
				Usage json.RawMessage `json:"usage"`
			}
			if json.Unmarshal(ev.Message, &msg) == nil {
				readUsage(msg.Usage)
			}

		case "content_block_start":
			var cb struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
				Data string `json:"data"`
			}
			if json.Unmarshal(ev.ContentBlock, &cb) != nil {
				continue
			}
			block := &anthropicBlock{index: ev.Index, kind: cb.Type, start: ev.ContentBlock, id: cb.ID, name: cb.Name, data: cb.Data}
			blocks[ev.Index] = block
			switch cb.Type {
			case "tool_use":
				emit(StreamDelta{ToolCalls: []ToolCallDelta{{Index: ev.Index, ID: cb.ID, Name: cb.Name}}})
			case "text":
				if cb.Text != "" {
					block.text.WriteString(cb.Text)
					emit(StreamDelta{Content: cb.Text})
				}
			}

		case "content_block_delta":
			block := blocks[ev.Index]
			if block == nil {
				continue
			}
			var d struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			}
			if json.Unmarshal(ev.Delta, &d) != nil {
				continue
			}
			switch d.Type {
			case "text_delta":
				if d.Text != "" {
					block.text.WriteString(d.Text)
					emit(StreamDelta{Content: d.Text})
				}
			case "thinking_delta":
				if d.Thinking != "" {
					block.thinking.WriteString(d.Thinking)
					emit(StreamDelta{Reasoning: d.Thinking})
				}
			case "signature_delta":
				block.signature.WriteString(d.Signature)
			case "input_json_delta":
				if d.PartialJSON != "" {
					block.input.WriteString(d.PartialJSON)
					emit(StreamDelta{ToolCalls: []ToolCallDelta{{Index: ev.Index, Arguments: d.PartialJSON}}})
				}
			}

		case "message_delta":
			var d struct {
				StopReason  string          `json:"stop_reason"`
				StopDetails json.RawMessage `json:"stop_details"`
			}
			if json.Unmarshal(ev.Delta, &d) == nil {
				if d.StopReason != "" {
					stopReason = d.StopReason
				}
				if len(d.StopDetails) > 0 && string(d.StopDetails) != "null" {
					_ = json.Unmarshal(d.StopDetails, &stopDetails)
				}
			}
			readUsage(ev.Usage)

		case "message_stop":
			completed = true

		case "error":
			return nil, NewAPIError("anthropic", anthropicAPIStatus(ev.Error.Type), data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}
	if !completed {
		// A clean EOF without message_stop is a cut-off response.
		return nil, ErrStreamIncomplete
	}

	switch stopReason {
	case "max_tokens", "model_context_window_exceeded":
		return nil, &IncompleteResponseError{Provider: "anthropic", Reason: stopReason}
	case "refusal":
		return nil, &RefusalError{Provider: "anthropic", Category: stopDetails.Category, Explanation: stopDetails.Explanation}
	}

	result.Usage = Usage{
		InputTokens:      usage.input + usage.cacheRead + usage.cacheWrite,
		OutputTokens:     usage.output,
		CacheReadTokens:  usage.cacheRead,
		CacheWriteTokens: usage.cacheWrite,
	}

	ordered := make([]*anthropicBlock, 0, len(blocks))
	for _, b := range blocks {
		ordered = append(ordered, b)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })

	var content, reasoning strings.Builder
	var replay []json.RawMessage
	var searches []WebSearch
	var fetches []WebFetch
	hasThinking := false
	for _, b := range ordered {
		var out any
		switch b.kind {
		case "text":
			text := b.text.String()
			content.WriteString(text)
			if strings.TrimSpace(text) != "" {
				out = map[string]any{"type": "text", "text": text}
			}
		case "thinking":
			hasThinking = true
			if reasoning.Len() > 0 && b.thinking.Len() > 0 {
				reasoning.WriteString("\n\n")
			}
			reasoning.WriteString(b.thinking.String())
			out = map[string]any{"type": "thinking", "thinking": b.thinking.String(), "signature": b.signature.String()}
		case "redacted_thinking":
			hasThinking = true
			out = map[string]any{"type": "redacted_thinking", "data": b.data}
		case "tool_use":
			args := strings.TrimSpace(b.input.String())
			if args == "" {
				args = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: b.id, Name: b.name, Arguments: args})
			out = map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": toolInputRaw(args)}
		case "server_tool_use":
			// The streamed start carries an empty input; the query arrives as
			// input_json_delta, and a replayed block must hold it.
			args := strings.TrimSpace(b.input.String())
			if args == "" {
				args = startInput(b.start)
			}
			out = map[string]any{"type": "server_tool_use", "id": b.id, "name": b.name, "input": toolInputRaw(args)}
			if b.name == "web_search" {
				var in struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal([]byte(args), &in)
				searches = append(searches, WebSearch{ID: b.id, Query: in.Query})
			}
			if b.name == "web_fetch" {
				var in struct {
					URL string `json:"url"`
				}
				_ = json.Unmarshal([]byte(args), &in)
				fetches = append(fetches, WebFetch{ID: b.id, URL: in.URL})
			}
		case "web_search_tool_result":
			if len(b.start) > 0 {
				out = b.start
			}
			var rb struct {
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if json.Unmarshal(b.start, &rb) == nil {
				attachWebSearchResult(searches, rb.ToolUseID, rb.Content)
			}
		case "web_fetch_tool_result":
			if len(b.start) > 0 {
				out = b.start
			}
			var rb struct {
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if json.Unmarshal(b.start, &rb) == nil {
				attachWebFetchResult(fetches, rb.ToolUseID, rb.Content)
			}
		default:
			if len(b.start) > 0 {
				out = b.start
			}
		}
		if out == nil {
			continue
		}
		raw, err := json.Marshal(out)
		if err != nil {
			continue
		}
		replay = append(replay, raw)
	}
	result.Content = content.String()
	result.Reasoning = reasoning.String()
	result.WebSearches = searches
	result.WebSearchRequests = max(usage.webSearches, int64(len(searches)))
	result.WebFetches = fetches
	result.WebFetchRequests = max(usage.webFetches, int64(len(fetches)))
	if hasThinking {
		if raw, err := json.Marshal(replay); err == nil {
			result.ReasoningItems = []ReasoningItem{{Provider: anthropicReasoningSource, Content: raw}}
		}
	}
	return result, nil
}

// attachWebSearchResult fills the search with the given tool_use_id from a
// web_search_tool_result content: a list of results on success, one error
// object (HTTP 200) on failure.
func attachWebSearchResult(searches []WebSearch, toolUseID string, content json.RawMessage) {
	at := -1
	for i := range searches {
		if searches[i].ID == toolUseID {
			at = i
		}
	}
	if at < 0 {
		return
	}
	var list []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		PageAge string `json:"page_age"`
	}
	if json.Unmarshal(content, &list) == nil {
		for _, r := range list {
			searches[at].Sources = append(searches[at].Sources, WebSearchSource{Title: r.Title, URL: r.URL, PageAge: r.PageAge})
		}
		return
	}
	var failure struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(content, &failure) == nil && failure.ErrorCode != "" {
		searches[at].Error = failure.ErrorCode
	}
}

// attachWebFetchResult fills the fetch with the given tool_use_id from a
// web_fetch_tool_result content: a web_fetch_result holding the page on
// success, one web_fetch_tool_error object (HTTP 200) on failure. The page
// text stays in the replayed block; only its address, title and time are kept.
func attachWebFetchResult(fetches []WebFetch, toolUseID string, content json.RawMessage) {
	at := -1
	for i := range fetches {
		if fetches[i].ID == toolUseID {
			at = i
		}
	}
	if at < 0 {
		return
	}
	var res struct {
		Type        string `json:"type"`
		URL         string `json:"url"`
		RetrievedAt string `json:"retrieved_at"`
		ErrorCode   string `json:"error_code"`
		Content     struct {
			Title string `json:"title"`
		} `json:"content"`
	}
	if json.Unmarshal(content, &res) != nil {
		return
	}
	if res.ErrorCode != "" {
		fetches[at].Error = res.ErrorCode
		return
	}
	if res.URL != "" {
		fetches[at].URL = res.URL
	}
	fetches[at].Title = res.Content.Title
	fetches[at].RetrievedAt = res.RetrievedAt
}

// startInput is the input a content_block_start carried whole (a block whose
// arguments arrive in one piece has no input_json_delta), or an empty object.
func startInput(start json.RawMessage) string {
	var cb struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(start, &cb) == nil && len(cb.Input) > 0 && string(cb.Input) != "null" {
		return string(cb.Input)
	}
	return "{}"
}

// serverToolResultType is true for the result block of a server-side tool
// (web_search_tool_result, web_fetch_tool_result, code execution results...),
// never for the client tool_result.
func serverToolResultType(t string) bool {
	return t != "tool_result" && strings.HasSuffix(t, "_tool_result")
}

// pairServerToolBlocks makes the server tool blocks of one replayed assistant
// turn travel together: a server_tool_use is kept only with its result, and a
// result only with its use. When a block is dropped, the citations on the
// turn's text are dropped too, since they point into those results. A turn
// whose server blocks are all paired comes back untouched.
func pairServerToolBlocks(raw []json.RawMessage) []json.RawMessage {
	uses, results := map[string]bool{}, map[string]bool{}
	type head struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		ToolUseID string `json:"tool_use_id"`
	}
	heads := make([]head, len(raw))
	for i, b := range raw {
		if json.Unmarshal(b, &heads[i]) != nil {
			continue
		}
		switch {
		case heads[i].Type == "server_tool_use":
			uses[heads[i].ID] = true
		case serverToolResultType(heads[i].Type):
			results[heads[i].ToolUseID] = true
		}
	}
	dropped := false
	keep := make([]bool, len(raw))
	for i, h := range heads {
		keep[i] = true
		switch {
		case h.Type == "server_tool_use" && !results[h.ID]:
			keep[i], dropped = false, true
		case serverToolResultType(h.Type) && !uses[h.ToolUseID]:
			keep[i], dropped = false, true
		}
	}
	if !dropped {
		return raw
	}
	out := make([]json.RawMessage, 0, len(raw))
	for i, b := range raw {
		if keep[i] {
			out = append(out, stripCitations(b))
		}
	}
	return out
}

// stripCitations removes the citations of a text block; other blocks pass.
func stripCitations(b json.RawMessage) json.RawMessage {
	if blockType(b) != "text" {
		return b
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return b
	}
	if _, ok := m["citations"]; !ok {
		return b
	}
	delete(m, "citations")
	if out, err := json.Marshal(m); err == nil {
		return out
	}
	return b
}

// stripServerToolBlocks returns messages whose replayed Anthropic turns carry
// no server tool blocks at all (and no citations), keeping the thinking, text
// and client tool_use blocks.
func stripServerToolBlocks(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		items := make([]ReasoningItem, len(out[i].ReasoningItems))
		copy(items, out[i].ReasoningItems)
		for j, item := range items {
			if item.Provider != anthropicReasoningSource || len(item.Content) == 0 {
				continue
			}
			var raw []json.RawMessage
			if json.Unmarshal(item.Content, &raw) != nil {
				continue
			}
			kept, changed := make([]json.RawMessage, 0, len(raw)), false
			for _, b := range raw {
				if t := blockType(b); t == "server_tool_use" || serverToolResultType(t) {
					changed = true
					continue
				}
				kept = append(kept, b)
			}
			if !changed {
				continue
			}
			for k := range kept {
				kept[k] = stripCitations(kept[k])
			}
			if content, err := json.Marshal(kept); err == nil {
				items[j].Content = content
			}
		}
		out[i].ReasoningItems = items
	}
	return out
}
