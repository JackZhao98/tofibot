package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const sayAnswerText = "Revenue grew four percent, and costs stayed flat."

// sayFragments cuts a say call's arguments so the text arrives in pieces.
func sayArgFragments() []string {
	b, _ := json.Marshal(map[string]string{"purpose": "answer", "outcome": "done", "text": sayAnswerText})
	runes := []rune(string(b))
	var out []string
	for len(runes) > 14 {
		out = append(out, string(runes[:14]))
		runes = runes[14:]
	}
	return append(out, string(runes))
}

func writeSSE(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	w.(http.Flusher).Flush()
}

// providerSayServer serves one say(answer) call as the real wire format of a
// provider, pausing between fragments so the draft is observable mid-stream.
// Requests without tools (the post-run summary) get a plain short reply.
func providerSayServer(t *testing.T, anthropic bool) *httptest.Server {
	t.Helper()
	pause := 70 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		hasSay := strings.Contains(string(raw), `"name":"say"`)
		switch {
		case anthropic && hasSay:
			writeSSE(w, "message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`)
			writeSSE(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_say","name":"say","input":{}}}`)
			for _, f := range sayArgFragments() {
				b, _ := json.Marshal(f)
				writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(b)+`}}`)
				time.Sleep(pause)
			}
			writeSSE(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
			writeSSE(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
			writeSSE(w, "message_stop", `{"type":"message_stop"}`)
		case anthropic:
			writeSSE(w, "message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`)
			writeSSE(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)
			writeSSE(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
			writeSSE(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
			writeSSE(w, "message_stop", `{"type":"message_stop"}`)
		case hasSay:
			item := `{"type":"function_call","id":"fc_say","call_id":"call_say","name":"say"}`
			writeSSE(w, "response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":`+item+`}`)
			whole := strings.Join(sayArgFragments(), "")
			for _, f := range sayArgFragments() {
				b, _ := json.Marshal(f)
				writeSSE(w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_say","delta":`+string(b)+`}`)
				time.Sleep(pause)
			}
			wb, _ := json.Marshal(whole)
			writeSSE(w, "response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_say","arguments":`+string(wb)+`}`)
			writeSSE(w, "response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_say","call_id":"call_say","name":"say","arguments":`+string(wb)+`,"status":"completed"}}`)
			writeSSE(w, "response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":20}}}`)
		default:
			writeSSE(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"ok"}`)
			writeSSE(w, "response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2}}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sayStreamingThroughProvider(t *testing.T, providerName, model string, anthropic bool) {
	srv := providerSayServer(t, anthropic)
	engine, err := runtime.New(runtime.Config{Provider: providerName, APIKey: "synthetic", BaseURL: srv.URL, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	s, run := dmRun(t, engine)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	rows := assistantRows(t, s.store, run.ConversationID)
	if !equalStrings(kindsAndText(rows), []string{"|" + sayAnswerText}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
	// The draft showed the answer while it was written, in several steps, and
	// the final message is that same bubble.
	deltas := deltaTexts(t, s.store, run.ConversationID)
	if strings.Join(deltas, "") != sayAnswerText || len(deltas) < 4 {
		t.Fatalf("draft deltas = %q, want several fragments of the answer", deltas)
	}
	events := eventsOfType(t, s.store, run.ConversationID, "delta")
	if events[len(events)-1]["message_id"] != rows[0].ID {
		t.Fatalf("draft id %v != message id %s", events[len(events)-1]["message_id"], rows[0].ID)
	}
	for _, ev := range events {
		if strings.Contains(fmt.Sprint(ev["content"]), `"purpose"`) {
			t.Fatalf("argument JSON leaked into the draft: %v", ev["content"])
		}
	}
}

func TestSayAnswerStreamsIntoTheDraftOverTheAnthropicWire(t *testing.T) {
	sayStreamingThroughProvider(t, "anthropic", "claude-haiku-5-5", true)
}

func TestSayAnswerStreamsIntoTheDraftOverTheOpenAIResponsesWire(t *testing.T) {
	sayStreamingThroughProvider(t, "openai", "gpt-5.5", false)
}
