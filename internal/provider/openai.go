package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// supportsReasoning returns true for OpenAI models with reasoning capability:
// the o-series (o1, o3, o4, ...) and gpt-5 onward (gpt-5.x, gpt-6*, ...),
// with or without the codex- slug prefix. Other Responses-API models like
// gpt-4o reject the reasoning + include payload fields.
func supportsReasoning(model string) bool {
	m := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "codex-")
	if rest, ok := strings.CutPrefix(m, "gpt-"); ok {
		return leadingNumber(rest) >= 5
	}
	if rest, ok := strings.CutPrefix(m, "o"); ok {
		return leadingNumber(rest) >= 1
	}
	return false
}

// leadingNumber parses the decimal digits at the start of s (-1 if none).
func leadingNumber(s string) int {
	n, digits := 0, 0
	for _, r := range s {
		if r < '0' || r > '9' || digits == 4 {
			break
		}
		n = n*10 + int(r-'0')
		digits++
	}
	if digits == 0 {
		return -1
	}
	return n
}

// openaiResponses implements Provider using the OpenAI Responses API.
// This is the primary API for all OpenAI native models.
// It includes an automatic fallback to Chat Completions API when the
// Responses API fails with tool-related errors (known API issue).
type openaiResponses struct {
	apiKey  string
	baseURL string
	legacy  *openaiChatCompletions // Chat Completions fallback for tool-related errors
	headers map[string]string
	noStore bool
}

func newOpenAIResponses(apiKey string, cfg *providerConfig) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	baseURL := "https://api.openai.com/v1"
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}
	// Create a Chat Completions fallback for when Responses API has tool issues
	legacy := &openaiChatCompletions{apiKey: apiKey, baseURL: baseURL}
	return &openaiResponses{apiKey: apiKey, baseURL: baseURL, legacy: legacy}, nil
}

// isToolCallError checks if the error is a Responses API tool-related error
// that can be retried via the Chat Completions API fallback.
func isToolCallError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "No tool output found for function call") ||
		strings.Contains(msg, "tool output") ||
		(strings.Contains(msg, "HTTP 400") && strings.Contains(msg, "function_call"))
}

// Chat sends a non-streaming request via the Responses API.
// Falls back to Chat Completions if the Responses API fails with tool errors.
func (o *openaiResponses) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	payload := o.buildPayload(req, false)

	body, err := o.doRequest(ctx, payload)
	rejected := false
	if err != nil && !req.OmitReasoningReplay && hasReasoningReplay(req.Messages) && isReasoningReplayRejection(err) {
		retry := *req
		retry.OmitReasoningReplay = true
		body, err = o.doRequest(ctx, o.buildPayload(&retry, false))
		rejected = err == nil
	}
	if err == nil && rejected {
		resp, parseErr := o.parseResponse(body)
		if parseErr == nil {
			resp.ReasoningReplayRejected = true
		}
		return resp, parseErr
	}
	if err != nil {
		// Fallback to Chat Completions API for tool-related errors
		if o.legacy != nil && isToolCallError(err) && len(req.Tools) > 0 {
			return o.legacy.Chat(ctx, req)
		}
		return nil, err
	}

	return o.parseResponse(body)
}

// ChatStream sends a streaming request via the Responses API.
// Falls back to Chat Completions if the Responses API fails with tool errors.
func (o *openaiResponses) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	// A rejection can arrive in-stream; never replay a forwarded prefix.
	forwarded := false
	forward := func(delta StreamDelta) {
		forwarded = true
		if onDelta != nil {
			onDelta(delta)
		}
	}
	resp, err := o.stream(ctx, req, forward)
	if err != nil && !forwarded && !req.OmitReasoningReplay && hasReasoningReplay(req.Messages) && isReasoningReplayRejection(err) {
		// Replayed reasoning is an optimization. A backend that rejects it gets
		// one request without it; the caller drops it for the rest of the run.
		retry := *req
		retry.OmitReasoningReplay = true
		resp, err = o.stream(ctx, &retry, onDelta)
		if err == nil {
			resp.ReasoningReplayRejected = true
			return resp, nil
		}
	}
	if err != nil && o.legacy != nil && len(req.Tools) > 0 && isToolCallError(err) {
		// Fallback to Chat Completions API for tool-related errors
		if _, httpErr := AsAPIError(err); httpErr {
			return o.legacy.ChatStream(ctx, req, onDelta)
		}
	}
	return resp, err
}

// Stream watchdogs. streamIdleTimeout aborts a stream that delivers no bytes
// for this long, including the wait for response headers; reasoning models
// can stay silent for minutes (Codex CLI uses 300 s). streamWallCap bounds one
// attempt after its headers arrive. Variables so tests can shorten them.
var (
	streamIdleTimeout = 300 * time.Second
	streamWallCap     = 600 * time.Second
)

type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	return n, err
}

func (o *openaiResponses) stream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	payload := o.buildPayload(req, true)

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle, wallCap := streamIdleTimeout, streamWallCap
	var idleFired, wallFired atomic.Bool
	timer := time.AfterFunc(idle, func() { idleFired.Store(true); cancel() })
	defer timer.Stop()
	idleErr := func(err error) error {
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

	httpReq, err := http.NewRequestWithContext(streamCtx, "POST", o.baseURL+"/responses", strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	for key, value := range o.headers {
		httpReq.Header.Set(key, value)
	}

	// No client timeout: the watchdogs bound header wait and body separately.
	resp, err := (&http.Client{}).Do(httpReq)
	if err != nil {
		return nil, idleErr(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, NewAPIError("openai", resp.StatusCode, string(respBody))
	}

	timer.Reset(idle)
	wall := time.AfterFunc(wallCap, func() { wallFired.Store(true); cancel() })
	defer wall.Stop()
	result, err := o.parseStream(&idleReader{r: resp.Body, timer: timer, idle: idle}, onDelta)
	if err == nil && (idleFired.Load() || wallFired.Load()) {
		err = context.Canceled // never return a stream cut short as complete
	}
	if err != nil {
		return nil, idleErr(err)
	}
	return result, nil
}

func hasReasoningReplay(messages []Message) bool {
	for _, msg := range messages {
		for _, item := range msg.ReasoningItems {
			if item.EncryptedContent != "" {
				return true
			}
		}
	}
	return false
}

// buildPayload constructs the Responses API request body.
func (o *openaiResponses) buildPayload(req *ChatRequest, stream bool) map[string]interface{} {
	payload := map[string]interface{}{
		"model": req.Model,
	}
	if o.noStore {
		payload["store"] = false
	}

	if req.System != "" {
		payload["instructions"] = req.System
	}

	if stream {
		payload["stream"] = true
	}
	if key := strings.TrimSpace(req.PromptCacheKey); key != "" {
		payload["prompt_cache_key"] = key
	}

	// Enable reasoning with summary ONLY for models that support it.
	// gpt-4o / gpt-4-turbo etc. reject the include + reasoning fields with
	// "Encrypted content is not supported with this model.". Reasoning is
	// the o-series (o1, o3, o4) and gpt-5.x family.
	if supportsReasoning(req.Model) && strings.TrimSpace(req.ReasoningEffort) != "none" {
		effort := strings.TrimSpace(req.ReasoningEffort)
		if effort == "" {
			effort = "medium"
		}
		payload["reasoning"] = map[string]interface{}{
			"effort":  effort,
			"summary": "auto",
		}
		payload["include"] = []string{"reasoning.encrypted_content"}
	}

	// Convert messages to Responses API input format
	// Replayed reasoning is only meaningful when the request asks for it.
	_, reasoning := payload["reasoning"]
	input := o.convertMessages(req.Messages, reasoning && !req.OmitReasoningReplay)
	if len(input) > 0 {
		payload["input"] = input
	}

	// Convert tools to Responses API format (flat, no function wrapper)
	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, t := range req.Tools {
			tool := map[string]interface{}{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
				"strict":      false, // Don't enforce strict mode for flexibility
			}
			tools = append(tools, tool)
		}
		payload["tools"] = tools
		if req.ToolChoice == ToolChoiceNone {
			payload["tool_choice"] = "none"
		}
	}

	return payload
}

// convertMessages converts unified Messages to Responses API input format.
// With replayReasoning, an assistant tool-call message's reasoning items are
// sent before its output, matching the order the model produced them.
func (o *openaiResponses) convertMessages(msgs []Message, replayReasoning bool) []interface{} {
	var input []interface{}

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if len(msg.ImageURLs) > 0 {
				content := make([]map[string]interface{}, 0, len(msg.ImageURLs)+1)
				if msg.Content != "" {
					content = append(content, map[string]interface{}{"type": "input_text", "text": msg.Content})
				}
				for _, imageURL := range msg.ImageURLs {
					content = append(content, map[string]interface{}{
						"type":      "input_image",
						"image_url": imageURL,
						"detail":    "auto",
					})
				}
				input = append(input, map[string]interface{}{
					"role":    "user",
					"content": content,
				})
				continue
			}
			input = append(input, map[string]interface{}{
				"role":    "user",
				"content": msg.Content,
			})

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if replayReasoning {
					input = append(input, o.reasoningInput(msg.ReasoningItems)...)
				}
				// Assistant message with tool calls becomes multiple output items
				// First, add text content if any
				if msg.Content != "" {
					input = append(input, map[string]interface{}{
						"type": "message",
						"role": "assistant",
						"content": []map[string]interface{}{
							{"type": "output_text", "text": msg.Content},
						},
					})
				}
				// Then add function_call items
				// Note: only set call_id (not id) — id is the item's unique identifier
				// which we don't preserve from the original response. Setting id to the
				// wrong value can confuse the Responses API validation.
				for _, tc := range msg.ToolCalls {
					input = append(input, map[string]interface{}{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
						"status":    "completed",
					})
				}
			} else if msg.Content != "" {
				input = append(input, map[string]interface{}{
					"type": "message",
					"role": "assistant",
					"content": []map[string]interface{}{
						{"type": "output_text", "text": msg.Content},
					},
				})
			}

		case "tool":
			// Tool result → function_call_output
			input = append(input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
				"status":  "completed",
			})
			if len(msg.ImageURLs) > 0 {
				content := []map[string]interface{}{
					{
						"type": "input_text",
						"text": "The web page images returned by the preceding tool are attached below. Inspect them when they are relevant to the task; use the URLs in the tool result as citations.",
					},
				}
				for _, imageURL := range msg.ImageURLs {
					content = append(content, map[string]interface{}{
						"type":      "input_image",
						"image_url": imageURL,
						"detail":    "auto",
					})
				}
				input = append(input, map[string]interface{}{
					"type":    "message",
					"role":    "user",
					"content": content,
				})
			}
		}
	}

	return input
}

// reasoningInput builds replayable reasoning input items. Items without
// encrypted content cannot be resolved with store=false and are skipped.
// Without storage an item id refers to nothing, so it is sent only when the
// backend stores responses (the Codex CLI also omits it).
func (o *openaiResponses) reasoningInput(items []ReasoningItem) []interface{} {
	var input []interface{}
	for _, item := range items {
		if item.EncryptedContent == "" {
			continue
		}
		summary := make([]map[string]interface{}, 0, len(item.Summary))
		for _, text := range item.Summary {
			summary = append(summary, map[string]interface{}{"type": "summary_text", "text": text})
		}
		entry := map[string]interface{}{
			"type":              "reasoning",
			"encrypted_content": item.EncryptedContent,
			"summary":           summary,
		}
		if !o.noStore && item.ID != "" {
			entry["id"] = item.ID
		}
		input = append(input, entry)
	}
	return input
}

// doRequest sends a non-streaming POST request.
func (o *openaiResponses) doRequest(ctx context.Context, payload map[string]interface{}) (string, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/responses", strings.NewReader(string(jsonData)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	for key, value := range o.headers {
		httpReq.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", NewAPIError("openai", resp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}

// parseResponse parses a non-streaming Responses API response.
func (o *openaiResponses) parseResponse(body string) (*ChatResponse, error) {
	result := &ChatResponse{}

	// Parse output items
	var resp struct {
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	result.Usage.InputTokens = resp.Usage.InputTokens
	result.Usage.OutputTokens = resp.Usage.OutputTokens

	for _, raw := range resp.Output {
		var item struct {
			Type             string `json:"type"`
			ID               string `json:"id"`
			CallID           string `json:"call_id"`
			Name             string `json:"name"`
			Args             string `json:"arguments"`
			EncryptedContent string `json:"encrypted_content"`
			Content          []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Text string `json:"text"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					result.Content += c.Text
				}
			}
		case "function_call":
			// call_id is the identifier required by the subsequent
			// function_call_output; item id is a distinct output item id.
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        callID,
				Name:      item.Name,
				Arguments: item.Args,
			})
		case "reasoning":
			// Extract reasoning summary if present
			for _, c := range item.Content {
				if c.Type == "summary_text" {
					result.Reasoning += c.Text
				}
			}
			if item.EncryptedContent != "" {
				captured := ReasoningItem{ID: item.ID, EncryptedContent: item.EncryptedContent}
				for _, s := range item.Summary {
					captured.Summary = append(captured.Summary, s.Text)
				}
				result.ReasoningItems = append(result.ReasoningItems, captured)
			}
		}
	}

	return result, nil
}

// streamCall accumulates one streamed function_call output item.
type streamCall struct {
	itemID   string
	callID   string
	name     string
	index    int
	hasIndex bool
	seq      int
	streamed strings.Builder // concatenated argument deltas
	final    string          // arguments from a .done / output_item.done event
	hasFinal bool
}

// streamCalls keys function calls by item_id (stable across all events) with
// output_index as a secondary key; either may be missing on a given event.
type streamCalls struct {
	byItem  map[string]*streamCall
	byIndex map[int]*streamCall
	all     []*streamCall
}

func (s *streamCalls) get(itemID string, index *int) *streamCall {
	var c *streamCall
	if itemID != "" {
		c = s.byItem[itemID]
	}
	if c == nil && index != nil {
		if found := s.byIndex[*index]; found != nil && (itemID == "" || found.itemID == "" || found.itemID == itemID) {
			c = found
		}
	}
	if c == nil {
		c = &streamCall{seq: len(s.all)}
		s.all = append(s.all, c)
	}
	if itemID != "" && c.itemID == "" {
		c.itemID = itemID
		s.byItem[itemID] = c
	}
	if index != nil && !c.hasIndex {
		c.index, c.hasIndex = *index, true
		s.byIndex[*index] = c
	}
	return c
}

// arguments prefers the complete arguments from a done event; streamed deltas
// are only a fallback when the backend sent no done arguments.
func (c *streamCall) callIDOrItem() string {
	if c.callID != "" {
		return c.callID
	}
	return c.itemID
}

func (c *streamCall) arguments() string {
	if c.hasFinal && (c.final != "" || c.streamed.Len() == 0) {
		return c.final
	}
	return c.streamed.String()
}

// parseStream parses the Responses API streaming events.
func (o *openaiResponses) parseStream(body io.Reader, onDelta func(StreamDelta)) (*ChatResponse, error) {
	result := &ChatResponse{}
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder

	calls := &streamCalls{byItem: map[string]*streamCall{}, byIndex: map[int]*streamCall{}}
	// setFinal records authoritative arguments and forwards any part that was
	// not streamed as deltas, so argument-size observers see the whole call.
	setFinal := func(c *streamCall, args string) {
		c.final, c.hasFinal = args, true
		if onDelta == nil || !c.hasIndex {
			return
		}
		streamed := c.streamed.String()
		if strings.HasPrefix(args, streamed) && len(args) > len(streamed) {
			rest := args[len(streamed):]
			c.streamed.WriteString(rest)
			onDelta(StreamDelta{ToolCalls: []ToolCallDelta{{Index: c.index, Arguments: rest}}})
		}
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	completed := false

	for scanner.Scan() {
		line := scanner.Text()

		// Track event type
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		// The data payload names its own type; prefer it so a stream without
		// (or with a stale) "event:" line is still routed correctly.
		eventType := currentEvent
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &head) == nil && head.Type != "" {
			eventType = head.Type
		}
		currentEvent = ""

		switch eventType {
		case "response.output_text.delta":
			var ev struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
				contentBuf.WriteString(ev.Delta)
				if onDelta != nil {
					onDelta(StreamDelta{Content: ev.Delta})
				}
			}

		case "response.reasoning_summary_text.delta":
			var ev struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
				reasoningBuf.WriteString(ev.Delta)
				if onDelta != nil {
					onDelta(StreamDelta{Reasoning: ev.Delta})
				}
			}

		case "response.output_item.added":
			// A new output item — could be function_call or reasoning
			var ev struct {
				OutputIndex *int `json:"output_index"`
				Item        struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Item.Type == "function_call" {
				c := calls.get(ev.Item.ID, ev.OutputIndex)
				if ev.Item.CallID != "" {
					c.callID = ev.Item.CallID
				}
				if ev.Item.Name != "" {
					c.name = ev.Item.Name
				}
				if onDelta != nil && c.hasIndex {
					onDelta(StreamDelta{
						ToolCalls: []ToolCallDelta{{
							Index: c.index,
							ID:    c.callIDOrItem(),
							Name:  c.name,
						}},
					})
				}
				if ev.Item.Arguments != "" {
					setFinal(c, ev.Item.Arguments)
				}
			}

		case "response.function_call_arguments.delta":
			var ev struct {
				OutputIndex *int   `json:"output_index"`
				ItemID      string `json:"item_id"`
				Delta       string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
				c := calls.get(ev.ItemID, ev.OutputIndex)
				c.streamed.WriteString(ev.Delta)
				if onDelta != nil && c.hasIndex {
					onDelta(StreamDelta{
						ToolCalls: []ToolCallDelta{{
							Index:     c.index,
							Arguments: ev.Delta,
						}},
					})
				}
			}

		case "response.function_call_arguments.done":
			var ev struct {
				OutputIndex *int    `json:"output_index"`
				ItemID      string  `json:"item_id"`
				Name        string  `json:"name"`
				Arguments   *string `json:"arguments"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Arguments != nil {
				c := calls.get(ev.ItemID, ev.OutputIndex)
				if ev.Name != "" && c.name == "" {
					c.name = ev.Name
				}
				setFinal(c, *ev.Arguments)
			}

		case "response.output_item.done":
			// Check for reasoning item with summary — only use as fallback
			// if we didn't already receive reasoning via streaming deltas.
			var ev struct {
				OutputIndex *int            `json:"output_index"`
				Item        json.RawMessage `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				var fc struct {
					Type      string  `json:"type"`
					ID        string  `json:"id"`
					CallID    string  `json:"call_id"`
					Name      string  `json:"name"`
					Arguments *string `json:"arguments"`
				}
				if json.Unmarshal(ev.Item, &fc) == nil && fc.Type == "function_call" {
					// The completed item is the authoritative record of the call.
					c := calls.get(fc.ID, ev.OutputIndex)
					if fc.CallID != "" {
						c.callID = fc.CallID
					}
					if fc.Name != "" {
						c.name = fc.Name
					}
					if fc.Arguments != nil {
						setFinal(c, *fc.Arguments)
					}
				}
				var item struct {
					Type             string `json:"type"`
					ID               string `json:"id"`
					EncryptedContent string `json:"encrypted_content"`
					Summary          []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"summary"`
				}
				if json.Unmarshal(ev.Item, &item) == nil && item.Type == "reasoning" {
					if item.EncryptedContent != "" {
						captured := ReasoningItem{ID: item.ID, EncryptedContent: item.EncryptedContent}
						for _, s := range item.Summary {
							captured.Summary = append(captured.Summary, s.Text)
						}
						result.ReasoningItems = append(result.ReasoningItems, captured)
					}
					// Only use summary from done event if no streaming deltas were received
					if reasoningBuf.Len() == 0 {
						for _, s := range item.Summary {
							if s.Text != "" {
								reasoningBuf.WriteString(s.Text)
								if onDelta != nil {
									onDelta(StreamDelta{Reasoning: s.Text})
								}
							}
						}
					}
				}
			}

		case "response.completed", "response.incomplete":
			// Terminal event with usage; incomplete still carries a usable output.
			completed = true
			var ev struct {
				Response struct {
					Usage struct {
						InputTokens  int64 `json:"input_tokens"`
						OutputTokens int64 `json:"output_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				result.Usage.InputTokens = ev.Response.Usage.InputTokens
				result.Usage.OutputTokens = ev.Response.Usage.OutputTokens
			}

		case "response.failed":
			var ev struct {
				Response struct {
					Error struct {
						Message string `json:"message"`
						Code    string `json:"code"`
					} `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				return nil, &ResponseFailedError{Code: ev.Response.Error.Code, Message: ev.Response.Error.Message}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}
	if !completed {
		// A clean EOF without a terminal event is a cut-off response.
		return nil, ErrStreamIncomplete
	}

	result.Content = contentBuf.String()
	result.Reasoning = reasoningBuf.String()

	// Assemble tool calls in output order; items seen without an
	// output_index keep their arrival order after indexed ones.
	ordered := append([]*streamCall(nil), calls.all...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.hasIndex != b.hasIndex {
			return a.hasIndex
		}
		if a.hasIndex && a.index != b.index {
			return a.index < b.index
		}
		return a.seq < b.seq
	})
	for _, c := range ordered {
		if c.name == "" && c.callID == "" {
			continue // argument events for an item never identified as a call
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        c.callIDOrItem(),
			Name:      c.name,
			Arguments: c.arguments(),
		})
	}

	return result, nil
}
