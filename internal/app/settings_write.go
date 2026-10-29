package app

import (
	"context"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// settingWriter applies one change through the store method that owns it.
type settingWriter func(ctx context.Context, sr settingRun, t Target, value any) (string, error)

func (sr settingRun) set(ctx context.Context, in settingsToolInput) (string, error) {
	return "", tooloutcome.New(tooloutcome.Denied, "settings_change_unavailable", "not_executed", "Changing settings through this tool is not available yet.", "explain_blocker").Err()
}

func (sr settingRun) revert(ctx context.Context, in settingsToolInput) (string, error) {
	return "", tooloutcome.New(tooloutcome.Denied, "settings_change_unavailable", "not_executed", "Changing settings through this tool is not available yet.", "explain_blocker").Err()
}
