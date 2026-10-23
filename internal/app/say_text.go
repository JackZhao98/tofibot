package app

// A model sometimes writes the arguments of the say tool as its plain final
// text instead of calling the tool (seen on the OpenAI Responses path,
// 2026-10-22). Plain text is private under the say protocol, so without this
// the host's implicit-final rule would publish the raw JSON. Detection is by
// shape and by the tool's own input schema, never by wording.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// sayTextCallPrefix names the synthetic call a recovered say runs under; the
// run id makes a replay record the same answer.
const sayTextCallPrefix = "say-text:"

const sayTextInvalidReminder = "Your last message was the arguments of the say tool written as plain text, and plain text is private, so nobody has read it. Call the say tool itself with valid arguments for this run to deliver your reply."

// sayTextObject returns the content as one JSON object with purpose and text
// keys, optionally inside a single fenced block that holds only that object.
// Text that merely contains JSON, or holds more than one value, is not one.
func sayTextObject(content string) (json.RawMessage, bool) {
	t := strings.TrimSpace(content)
	if strings.HasPrefix(t, "```") {
		nl := strings.IndexByte(t, '\n')
		if nl < 0 {
			return nil, false
		}
		if lang := strings.ToLower(strings.TrimSpace(t[3:nl])); lang != "" && lang != "json" {
			return nil, false
		}
		body := strings.TrimSpace(t[nl+1:])
		if !strings.HasSuffix(body, "```") {
			return nil, false
		}
		t = strings.TrimSpace(strings.TrimSuffix(body, "```"))
		if strings.Contains(t, "```") {
			return nil, false
		}
	}
	if !strings.HasPrefix(t, "{") || !strings.HasSuffix(t, "}") {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(t))
	var obj map[string]json.RawMessage
	if dec.Decode(&obj) != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	if _, ok := obj["purpose"]; !ok {
		return nil, false
	}
	if _, ok := obj["text"]; !ok {
		return nil, false
	}
	return json.RawMessage(t), true
}

// sayTextArgs reports whether the say tool's own schema accepts raw, and the
// purpose it carries.
func sayTextArgs(say Tool, raw json.RawMessage) (purpose string, valid bool) {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || tooloutcome.ValidateArguments(say.Parameters, args) != nil {
		return "", false
	}
	purpose, _ = args["purpose"].(string)
	return purpose, true
}

// sayTextAnswer is the text of a valid say(answer) written as plain text.
func sayTextAnswer(say Tool, content string) (string, bool) {
	raw, ok := sayTextObject(content)
	if !ok {
		return "", false
	}
	if purpose, valid := sayTextArgs(say, raw); !valid || purpose != sayAnswer {
		return "", false
	}
	var in struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return "", false
	}
	return strings.TrimSpace(stripProgressTags(in.Text)), true
}

// sayTextFinalReview runs ahead of the other final reviews. A final that is
// one valid say call is executed as that call, so every rule of the real tool
// applies; a refusal goes back through the one final-response repair. A final
// shaped like say arguments that fails the schema is repaired the same way.
// Anything else is left to inner.
func sayTextFinalReview(ctx context.Context, say Tool, runID string, recovered *atomic.Bool, inner func(string) (string, error)) func(string) (string, error) {
	return func(content string) (string, error) {
		raw, ok := sayTextObject(content)
		if !ok {
			if inner == nil {
				return "", nil
			}
			return inner(content)
		}
		purpose, valid := sayTextArgs(say, raw)
		if !valid {
			return sayTextInvalidReminder, nil
		}
		_, err := say.Execute(runtime.WithToolCallID(ctx, sayTextCallPrefix+runID), raw)
		switch {
		case err == nil:
			if purpose == sayAnswer {
				recovered.Store(true)
			}
			return "", nil
		case ctx.Err() != nil:
			return "", ctx.Err()
		}
		return err.Error() + " " + sayTextInvalidReminder, nil
	}
}
