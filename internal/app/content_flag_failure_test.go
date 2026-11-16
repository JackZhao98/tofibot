package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// A run the provider's content filter blocked has a named failure code, and
// the person-facing message never repeats the provider's words.
func TestContentFilterBlockIsANamedFailure(t *testing.T) {
	// The first error is the test machine's run 873bf1c2 (2026-11-16), verbatim.
	codex := "LLM call failed: response failed: [invalid_prompt] Invalid prompt: your prompt was flagged as potentially violating our usage policy. Please try again with a different prompt: https://platform.openai.com/docs/guides/reasoning#advice-on-prompting"
	for _, tc := range []struct {
		name, err string
		retried   bool
	}{
		{"codex in-stream flag", codex, false},
		{"flagged again after the automatic retry", codex + " (flagged again after one automatic retry)", true},
		{"openai http 400", `LLM call failed: openai API error (HTTP 400): {"error":{"message":"Invalid prompt: your prompt was flagged as potentially violating our usage policy.","type":"invalid_request_error","code":"invalid_prompt"}}`, false},
		{"anthropic refusal stop", "LLM call failed: anthropic model declined the request (cyber): synthetic explanation", false},
		{"anthropic output filter", `LLM call failed: anthropic API error (HTTP 400): {"type":"error","error":{"type":"invalid_request_error","message":"Output blocked by content filtering policy"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(Run{ID: "r", Status: "failed", Model: "codex-gpt-5.6-terra", Error: tc.err})
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Failure *RunFailure `json:"failure"`
				Error   string      `json:"error"`
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			f := payload.Failure
			if f == nil || f.Code != "model_content_flagged" || f.AutoRetried != tc.retried {
				t.Fatalf("failure=%+v", f)
			}
			if payload.Error != tc.err {
				t.Fatal("the run's error no longer carries the provider's words")
			}
			for _, upstream := range []string{"invalid_prompt", "usage policy", "synthetic explanation", "Output blocked", "platform.openai.com"} {
				if strings.Contains(f.Message, upstream) {
					t.Fatalf("message repeats provider text %q: %s", upstream, f.Message)
				}
			}
		})
	}
	// An unrelated failure that mentions a policy stays generic.
	if f := (Run{Status: "failed", Error: "tool refused: org policy denies this action"}).failure(); f.Code != "execution_failed" {
		t.Fatalf("unrelated failure classified as %s", f.Code)
	}
}
