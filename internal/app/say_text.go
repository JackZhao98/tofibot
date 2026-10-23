package app

// A model sometimes writes the arguments of the say tool as its plain final
// text instead of calling the tool (seen on the OpenAI Responses path,
// 2026-10-22). Plain text is private under the say protocol, so without this
// the host's implicit-final rule would publish the raw JSON. Detection is by
// shape and by the tool's own input schema, never by wording.
//
// A GPT model (2026-10-23, codex-gpt-5.6-terra) sometimes leaks its internal
// tool-call header as that text: a literal functions.<name> marker and
// tokenizer garbage, then the real JSON. Only the say function is ever
// recovered and executed; a marker for any other function, or one from which
// no schema-valid say call can be read, is sent back privately and never
// published, so leaked tool-call text is never a path to arbitrary execution.

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

// sayTextDefaultOutcome returns raw with outcome defaulted to done when the
// object carries purpose and text but no outcome. rc.39 made outcome a
// required field, so a clean leak that predates it (or omits the field) would
// otherwise fail the schema; the default restores its recovery. An outcome the
// model did supply is never overwritten, and an object without purpose and
// text is left untouched for the schema to reject.
func sayTextDefaultOutcome(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	if _, ok := obj["purpose"]; !ok {
		return raw
	}
	if _, ok := obj["text"]; !ok {
		return raw
	}
	if _, ok := obj["outcome"]; ok {
		return raw
	}
	obj["outcome"] = json.RawMessage(`"` + sayOutcomeDone + `"`)
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return b
}

// sayTextFunctionMarkerPrefix is the literal that precedes a function name in a
// leaked tool-call header, in each tolerated form (functions.say, to=functions.say).
const sayTextFunctionMarkerPrefix = "functions."

// firstFunctionMarker finds the first functions.<name> tool-call marker and
// returns the function name and the byte index in content just past it. The
// prefix match is case-sensitive; the name is one or more identifier bytes.
// The bare substring "functions" with no following identifier is not a marker.
func firstFunctionMarker(content string) (name string, after int, ok bool) {
	base := 0
	for {
		i := strings.Index(content[base:], sayTextFunctionMarkerPrefix)
		if i < 0 {
			return "", 0, false
		}
		start := base + i + len(sayTextFunctionMarkerPrefix)
		end := start
		for end < len(content) && isFuncNameByte(content[end]) {
			end++
		}
		if end > start {
			return content[start:end], end, true
		}
		base = start
	}
}

func isFuncNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// sayTextObjectAfter decodes exactly one JSON object starting at the first '{'
// in s. json.Decoder stops at the object's end, so garbage or markers after
// the object are ignored. Shape and schema are checked by the caller.
func sayTextObjectAfter(s string) (json.RawMessage, bool) {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s[i:]))
	var obj json.RawMessage
	if dec.Decode(&obj) != nil {
		return nil, false
	}
	return obj, true
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
	raw = sayTextDefaultOutcome(raw)
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
//
// A final that is not a bare say object but leaks a functions.<name> tool-call
// header is handled by shape: only the say function is recovered (one object
// read from after its marker and run as that call), and any marker that yields
// no schema-valid say call — including one naming any other function — is sent
// back privately rather than published, so the leaked text never reaches the
// person as-is and never triggers execution of a tool other than say. A final
// with neither shape is left to inner, unchanged.
func sayTextFinalReview(ctx context.Context, say Tool, runID string, recovered *atomic.Bool, inner func(string) (string, error)) func(string) (string, error) {
	// exec runs a schema-valid say object as that call. A refusal from the tool
	// goes back through the one repair; a cancellation surfaces the ctx error.
	exec := func(raw json.RawMessage, purpose string) (string, error) {
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
	return func(content string) (string, error) {
		// The whole final is one say object (optionally fenced).
		if raw, ok := sayTextObject(content); ok {
			raw = sayTextDefaultOutcome(raw)
			purpose, valid := sayTextArgs(say, raw)
			if !valid {
				return sayTextInvalidReminder, nil
			}
			return exec(raw, purpose)
		}
		// A leaked tool-call header. Recover only the say function; never
		// execute any other, and never publish a marker we cannot recover from.
		if name, after, ok := firstFunctionMarker(content); ok {
			if name == "say" {
				if raw, ok := sayTextObjectAfter(content[after:]); ok {
					raw = sayTextDefaultOutcome(raw)
					if purpose, valid := sayTextArgs(say, raw); valid {
						return exec(raw, purpose)
					}
				}
			}
			return sayTextInvalidReminder, nil
		}
		if inner == nil {
			return "", nil
		}
		return inner(content)
	}
}
