package saystream

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// feedAll feeds chunks in order and returns the text released after each one.
func feedAll(chunks []string) (purpose string, pieces []string) {
	var d Decoder
	for _, c := range chunks {
		pieces = append(pieces, d.Feed(c))
	}
	purpose, _ = d.Purpose()
	return purpose, pieces
}

func args(purpose, text string) string {
	b, _ := json.Marshal(map[string]string{"purpose": purpose, "text": text})
	return string(b)
}

// Every split of the arguments at a rune boundary, in two or in single runes,
// decodes to exactly what encoding/json reads from the whole.
func TestDecoderMatchesJSONAtEverySplit(t *testing.T) {
	texts := []string{
		"plain words",
		"line one\nline two\ttabbed \"quoted\" back\\slash / slash",
		"unicode: 你好, héllo, 😀 and  ",
		`{"looks":"like json"} and </progress>`,
		"",
	}
	for _, text := range texts {
		whole := args("answer", text)
		var want struct{ Text string }
		if err := json.Unmarshal([]byte(whole), &want); err != nil {
			t.Fatal(err)
		}
		runes := []rune(whole)
		for cut := 0; cut <= len(runes); cut++ {
			purpose, pieces := feedAll([]string{string(runes[:cut]), string(runes[cut:])})
			if got := strings.Join(pieces, ""); got != text || purpose != "answer" {
				t.Fatalf("split %d of %q: purpose=%q text=%q", cut, whole, purpose, got)
			}
		}
		var one []string
		for _, r := range runes {
			one = append(one, string(r))
		}
		if purpose, pieces := feedAll(one); strings.Join(pieces, "") != text || purpose != "answer" {
			t.Fatalf("rune by rune %q: purpose=%q text=%q", whole, purpose, strings.Join(pieces, ""))
		}
	}
}

func TestDecoderEscapesAndSurrogatesAcrossChunks(t *testing.T) {
	// Surrogate pair for one emoji, an accented letter and a newline, all
	// written as JSON escapes; cut at every position.
	raw := []rune(`{"purpose":"note","text":"a\ud83d\ude00b \u00e9\n"}`)
	for cut := 1; cut < len(raw); cut++ {
		_, pieces := feedAll([]string{string(raw[:cut]), string(raw[cut:])})
		if got := strings.Join(pieces, ""); got != "a\U0001F600b \u00e9\n" {
			t.Fatalf("cut %d: %q", cut, got)
		}
	}
}

// Text written before the purpose is held until the purpose is known.
func TestDecoderHoldsTextUntilPurposeIsKnown(t *testing.T) {
	var d Decoder
	if got := d.Feed(`{"text":"early words","pur`); got != "" {
		t.Fatalf("released before purpose: %q", got)
	}
	if _, known := d.Purpose(); known {
		t.Fatal("purpose known too soon")
	}
	if got := d.Feed(`pose":"message"}`); got != "early words" {
		t.Fatalf("held text = %q", got)
	}
	if p, known := d.Purpose(); !known || p != "message" {
		t.Fatalf("purpose = %q %v", p, known)
	}
}

func TestDecoderSkipsUnknownFieldsOfEveryShape(t *testing.T) {
	raw := `{"extra":{"a":[1,2,{"b":"} \" ]"}],"c":null},"n":12.5e3,"flag":true,"purpose":"answer","list":["x","y"],"s":"str","text":"kept"}`
	for cut := 0; cut <= len(raw); cut++ {
		purpose, pieces := feedAll([]string{raw[:cut], raw[cut:]})
		if got := strings.Join(pieces, ""); got != "kept" || purpose != "answer" {
			t.Fatalf("cut %d: purpose=%q text=%q", cut, purpose, got)
		}
	}
}

// Text arrives while it is being written, not at the end.
func TestDecoderReleasesTextProgressively(t *testing.T) {
	chunks := []string{`{"purpose":"answer",`, `"text":"First `, `part, second `, `part."`, `}`}
	_, pieces := feedAll(chunks)
	want := []string{"", "First ", "part, second ", "part.", ""}
	for i := range want {
		if pieces[i] != want[i] {
			t.Fatalf("pieces = %q, want %q", pieces, want)
		}
	}
}

// Malformed input never panics and never invents text after the object ends.
func TestDecoderToleratesMalformedInput(t *testing.T) {
	for _, raw := range []string{``, `not json`, `{`, `{"text"`, `{"text":`, `{"text":"unterminated`, `{"purpose":"answer","text":"x"} trailing "text":"y"`, `{"text":"a\u12`, "{\"text\":\"\xff\xfe\"}"} {
		var d Decoder
		for _, r := range raw {
			_ = d.Feed(string(r))
		}
		var d2 Decoder
		out := d2.Feed(raw)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 from %q", raw)
		}
	}
	var d Decoder
	got := d.Feed(`{"purpose":"answer","text":"x"} {"text":"after"}`)
	if got != "x" {
		t.Fatalf("text after the object: %q", got)
	}
}

func FuzzDecoderNeverPanics(f *testing.F) {
	f.Add(args("answer", "hello \"world\" \n 😀"), 5)
	f.Add(`{"purpose":"note"`, 3)
	f.Fuzz(func(t *testing.T, raw string, cut int) {
		if cut < 0 || cut > len(raw) {
			cut = 0
		}
		var d Decoder
		_ = d.Feed(raw[:cut])
		_ = d.Feed(raw[cut:])
	})
}

// With HoldForResolution an answer's text waits for the label; other purposes
// and an object that ends without a label release at once.
func TestDecoderHoldsAnswerTextForTheResolutionLabel(t *testing.T) {
	d := Decoder{HoldForResolution: true}
	if got := d.Feed(`{"purpose":"answer","text":"Early words.","resolution":"gap"`); got != "Early words." {
		t.Fatalf("released %q", got)
	}
	if v, ok := d.Resolution(); !ok || v != "gap" {
		t.Fatalf("resolution %q %v", v, ok)
	}
	d = Decoder{HoldForResolution: true}
	if got := d.Feed(`{"purpose":"answer","text":"Held words."`); got != "" || d.Ready() {
		t.Fatalf("released before the label: %q", got)
	}
	if got := d.Feed(`}`); got != "Held words." || !d.Ready() {
		t.Fatalf("not released at the end: %q", got)
	}
	d = Decoder{HoldForResolution: true}
	if got := d.Feed(`{"purpose":"note","text":"A note."`); got != "A note." {
		t.Fatalf("note held: %q", got)
	}
	d = Decoder{HoldForResolution: true}
	if got := d.Feed(`{"purpose":"answer","resolution":"resolved","text":"Streams at once."`); got != "Streams at once." {
		t.Fatalf("resolved held: %q", got)
	}
}
