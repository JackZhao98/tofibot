package provider

import (
	"strings"
	"testing"
)

const (
	cS = ""
	cE = ""
)

// A U+E200 that is never closed is not a marker: the start character goes, the
// text stays. A span ends at U+E201, and is abandoned at a line break or after
// maxMarkerSpanRunes characters.
func TestUnclosedMarkerKeepsTheText(t *testing.T) {
	long := strings.Repeat("x", maxMarkerSpanRunes+50)
	cases := []struct{ name, in, want string }{
		{"end of text", "a " + cS + "citetur", "a citetur"},
		{"line break", "a " + cS + "cite\nnext line stays", "a cite\nnext line stays"},
		{"too long", "a " + cS + long + " tail", "a " + long + " tail"},
		{"closed", "a " + cS + "citeturn1" + cE + " b", "a b"},
		{"closed near the limit", "a " + cS + strings.Repeat("y", maxMarkerSpanRunes) + cE + "b", "a b"},
		{"second start", "a " + cS + "echo " + cS + "citeturn1" + cE + " b", "a echo b"},
		{"lone end", "a " + cE + "b", "a b"},
		{"lone start", cS, ""},
	}
	for _, tc := range cases {
		if got := StripMarkers(tc.in); got != tc.want {
			t.Errorf("%s: StripMarkers = %q, want %q", tc.name, got, tc.want)
		}
		// Streaming, every byte split: same words.
		for i := 0; i <= len(tc.in); i++ {
			var f MarkerFilter
			got := f.Feed(tc.in[:i]) + f.Feed(tc.in[i:]) + f.Flush()
			if got != tc.want {
				t.Errorf("%s: split %d = %q, want %q", tc.name, i, got, tc.want)
				break
			}
		}
		// Streaming one character at a time.
		var f MarkerFilter
		var b strings.Builder
		for _, r := range tc.in {
			b.WriteString(f.Feed(string(r)))
		}
		b.WriteString(f.Flush())
		if b.String() != tc.want {
			t.Errorf("%s: per rune = %q, want %q", tc.name, b.String(), tc.want)
		}
	}
}

func TestResponsesStreamKeepsTextAfterAnUnclosedMarker(t *testing.T) {
	o := &openaiResponses{}
	var live strings.Builder
	stream := sseStream([]sseEvent{
		{"response.output_text.delta", map[string]any{"delta": "Hello " + cS + "cite"}},
		{"response.output_text.delta", map[string]any{"delta": "turn1 and the rest."}},
		completedSSE,
	}, true)
	got, err := o.parseStream(strings.NewReader(stream), func(d StreamDelta) { live.WriteString(d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Hello citeturn1 and the rest." || live.String() != got.Content {
		t.Fatalf("content %q live %q", got.Content, live.String())
	}
}

// What a draft shows while a span is open is bounded: nothing leaks from a real
// span, and a long run is released once it is clearly not a marker.
func TestMarkerFilterHoldsAtMostTheBound(t *testing.T) {
	var f MarkerFilter
	if got := f.Feed("a " + cS + "citetur"); got != "a " {
		t.Fatalf("an open span is held: %q", got)
	}
	got := f.Feed(strings.Repeat("z", maxMarkerSpanRunes+5))
	if !strings.HasPrefix(got, "citetur") || len(got) < maxMarkerSpanRunes {
		t.Fatalf("a span over the bound is released: %d bytes", len(got))
	}
}
