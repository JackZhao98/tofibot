//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
)

func (s *Service) browserTypePrivate(ctx context.Context, botID string, input privateBrowserInput) (map[string]any, error) {
	expression, err := privateBrowserInputExpression(input)
	if err != nil {
		return nil, err
	}
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, errors.New("private input requires an active browser")
	}
	pages, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, errors.New("could not inspect private input destination")
	}
	current, source := chooseCurrentPage(pages)
	if current == nil || source != "focused" {
		return nil, errors.New("focus the intended password field first")
	}
	// The exact tab is fixed here. The synchronous function rechecks its real
	// origin and focus, so a navigation/tab switch cannot redirect the value.
	return cdpTypePrivate(ctx, current.WebSocketDebuggerURL, d.remotePort, expression)
}

func cdpTypePrivate(ctx context.Context, endpoint string, port int, expression string) (map[string]any, error) {
	raw, err := runCDPCommand(ctx, endpoint, port, "Runtime.evaluate", map[string]any{
		"expression": expression, "returnByValue": true, "awaitPromise": false,
	})
	if err != nil {
		return nil, errors.New("private input could not be verified; inspect the password field before retrying")
	}
	var result struct {
		Result struct {
			Type  string `json:"type"`
			Value bool   `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if json.Unmarshal(raw, &result) != nil || (len(result.Exception) > 0 && string(result.Exception) != "null") || result.Result.Type != "boolean" || !result.Result.Value {
		return nil, errors.New("private input rejected: focus a visible password field on the approved website")
	}
	return map[string]any{"ok": true}, nil
}
