package provider

import (
	"strings"
	"testing"
)

const markedText = "Hotels fill up early. citeturn784583view0turn784583view1 Book soon navlistx fileciteffor a deal, `code` 日本語 ok."
const markedWant = "Hotels fill up early. Book soon for a deal, `code` 日本語 ok."

func TestStripMarkersEveryByteSplit(t *testing.T) {
	if got := StripMarkers(markedText); got != markedWant {
		t.Fatalf("whole = %q", got)
	}
	for i := 0; i <= len(markedText); i++ {
		var f MarkerFilter
		got := f.Feed(markedText[:i]) + f.Feed(markedText[i:]) + f.Flush()
		if got != markedWant {
			t.Fatalf("split %d = %q", i, got)
		}
	}
	if got := StripMarkers("plain  text"); got != "plain  text" {
		t.Fatalf("other private-use text changed: %q", got)
	}
}

func TestResponsesStreamDropsMarkersAndKeepsSources(t *testing.T) {
	runes := []rune(markedText)
	for i := 0; i <= len(runes); i++ {
		o := &openaiResponses{}
		var live strings.Builder
		stream := sseStream([]sseEvent{
			{"response.output_item.done", map[string]any{"output_index": 0, "item": map[string]any{"type": "web_search_call", "id": "ws_1", "status": "completed", "action": map[string]any{"type": "search", "query": "q"}}}},
			{"response.output_text.delta", map[string]any{"delta": string(runes[:i])}},
			{"response.output_text.delta", map[string]any{"delta": string(runes[i:])}},
			{"response.output_text.annotation.added", map[string]any{"annotation": map[string]any{"type": "url_citation", "url": "https://example.com/a?utm_source=openai", "title": "A"}}},
			completedSSE,
		}, true)
		got, err := o.parseStream(strings.NewReader(stream), func(d StreamDelta) { live.WriteString(d.Content) })
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != markedWant || live.String() != markedWant {
			t.Fatalf("split %d: content %q live %q", i, got.Content, live.String())
		}
		if strings.ContainsAny(got.Content+live.String(), "") {
			t.Fatalf("split %d: marker kept", i)
		}
		if len(got.WebSearches) != 1 || len(got.WebSearches[0].Sources) != 1 || got.WebSearches[0].Sources[0].URL != "https://example.com/a" {
			t.Fatalf("split %d: sources = %+v", i, got.WebSearches)
		}
	}
}

func TestResponsesNonStreamDropsMarkers(t *testing.T) {
	complete := `{"output":[{"type":"message","content":[{"type":"output_text","text":"Yes citeturn1view0 it is."}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	got, err := (&openaiResponses{}).parseResponse(complete)
	if err != nil || got.Content != "Yes it is." {
		t.Fatalf("got %+v err=%v", got, err)
	}
}
