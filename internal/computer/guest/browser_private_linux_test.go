//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCDPPrivateInputDiscardsDiagnostics(t *testing.T) {
	old := runCDPCommand
	t.Cleanup(func() { runCDPCommand = old })
	for _, scenario := range []string{"success", "wrong_field", "exception", "invalid_json", "wrong_type", "transport"} {
		t.Run(scenario, func(t *testing.T) {
			runCDPCommand = func(_ context.Context, endpoint string, port int, method string, params map[string]any) (json.RawMessage, error) {
				if endpoint != "ws://127.0.0.1:9222/devtools/page/1" || port != 9222 || method != "Runtime.evaluate" || params["expression"] != "fake-private-value" || params["awaitPromise"] != false || params["returnByValue"] != true {
					t.Fatal("private operation changed target or synchronous contract")
				}
				switch scenario {
				case "wrong_field":
					return json.RawMessage(`{"result":{"type":"boolean","value":false}}`), nil
				case "exception":
					return json.RawMessage(`{"result":{"type":"boolean","value":true},"exceptionDetails":{"text":"fake-private-value"}}`), nil
				case "invalid_json":
					return json.RawMessage(`fake-private-value`), nil
				case "wrong_type":
					return json.RawMessage(`{"result":{"type":"string","value":"fake-private-value"}}`), nil
				case "transport":
					return nil, errors.New("fake-private-value")
				}
				return json.RawMessage(`{"result":{"type":"boolean","value":true}}`), nil
			}
			out, err := cdpTypePrivate(context.Background(), "ws://127.0.0.1:9222/devtools/page/1", 9222, "fake-private-value")
			if scenario == "success" {
				if err != nil || len(out) != 1 || out["ok"] != true {
					t.Fatal("valid result not acknowledged")
				}
			} else if err == nil || out != nil || strings.Contains(err.Error(), "fake-private-value") {
				t.Fatal("private diagnostic was accepted or leaked")
			}
		})
	}
}
