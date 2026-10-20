package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

const providerProbeTimeout = 12 * time.Second

// providerKeyOutOfQuota makes one minimal generation (16 output tokens at
// most) and reports true only when the provider answers that the account has
// no usage left. A model list proves the key is valid, not that it can
// generate; every other outcome (timeout, unsupported model, odd status) is
// treated as "no evidence" so a working key is never refused by a probe quirk.
func (s *Server) providerKeyOutOfQuota(ctx context.Context, name, key, model string) bool {
	if model == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, providerProbeTimeout)
	defer cancel()
	base := s.providerEndpoint(name)
	var req *http.Request
	var err error
	switch name {
	case providerOpenAI:
		body, _ := json.Marshal(map[string]any{"model": model, "input": "ok", "max_output_tokens": 16, "store": false})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/responses", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case providerAnthropic:
		body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ok"}}})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
		if err == nil {
			apiKey, workspace, _ := strings.Cut(key, "\x00")
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
			if workspace != "" {
				req.Header.Set("anthropic-workspace-id", workspace)
			}
		}
	default:
		return false
	}
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return false
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return provider.IsQuotaExhausted(string(data))
}
