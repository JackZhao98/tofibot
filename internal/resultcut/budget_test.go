package resultcut

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFitJSONMatchesTheEncoderAndKeepsCharactersWhole(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("<&>\"\\\n", 5000),
		strings.Repeat("日本語", 9000),
		strings.Repeat("a \x01é", 4000),
		strings.Repeat("ok\xff", 3000),
	} {
		var rebuilt strings.Builder
		rest := s
		for rest != "" {
			n := FitJSON(rest, 5000)
			if n <= 0 || !utf8.ValidString(rest[:n]) && utf8.ValidString(rest) {
				t.Fatalf("bad cut %d", n)
			}
			enc, _ := json.Marshal(rest[:n])
			if len(enc)-2 > 5000 || JSONLen(rest[:n]) != len(enc)-2 {
				t.Fatalf("estimate %d, encoded %d", JSONLen(rest[:n]), len(enc)-2)
			}
			rebuilt.WriteString(rest[:n])
			rest = rest[n:]
		}
		if rebuilt.String() != s {
			t.Fatal("pieces do not rebuild the text")
		}
	}
	if n := FitJSON("<", 1); n != 1 {
		t.Fatalf("one character must move forward, got %d", n)
	}
}
