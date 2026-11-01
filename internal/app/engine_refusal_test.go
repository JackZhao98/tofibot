package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestEngineErrorsCarryTheirReasonAndAreRepairable(t *testing.T) {
	for name, unwrap := range map[string]func(string) (string, error){"browser": browserEngineResult, "desktop": desktopEngineResult} {
		_, err := unwrap(`{"tool":"x","text":"Element is outside the viewport","is_error":true}`)
		o, ok := tooloutcome.FromError(err)
		if !ok || o.Status != tooloutcome.Permanent || o.NextAction != "repair_arguments" || o.Status == tooloutcome.Uncertain {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(o.Message, "Element is outside the viewport") {
			t.Fatalf("%s: reason lost: %q", name, o.Message)
		}
	}
}
