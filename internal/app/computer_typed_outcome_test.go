package app

import (
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/JackZhao98/tofibot/internal/webtools"
)

func TestComputerPageCodeKeepsTypedOutcomes(t *testing.T) {
	rejected := tooloutcome.New(tooloutcome.Validation, "action_rejected", "not_executed", "refused", "repair_arguments").Err()
	gone := tooloutcome.New(tooloutcome.Transient, "computer_restarting", "not_executed", "restarting", "explain_blocker").Err()
	slow := tooloutcome.New(tooloutcome.Transient, tooloutcome.BrowserSlow, "unknown", "slow", "retry").Err()
	for _, tc := range []struct {
		err  error
		want webtools.Code
	}{
		{rejected, webtools.BadRequest},
		{gone, webtools.NotConfigured},
		{slow, webtools.Network},
		{errors.New("boom"), webtools.Network},
	} {
		if got := computerPageCode(tc.err); got != tc.want {
			t.Errorf("%v: got %s want %s", tc.err, got, tc.want)
		}
	}
}
