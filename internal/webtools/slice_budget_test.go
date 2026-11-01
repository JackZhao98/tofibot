package webtools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/resultcut"
)

// A part fits one model result once encoded, and the parts rebuild the page.
func TestSliceFitsModelBudgetForMultibyteAndEscapedText(t *testing.T) {
	for name, text := range map[string]string{
		"cjk":     strings.Repeat("日本語のテキスト。", 12000),
		"escaped": strings.Repeat("<a href=\"x\">&</a>\n", 8000),
	} {
		t.Run(name, func(t *testing.T) {
			full := Page{Source: "s", URL: "https://example.com/a", Title: "Title", Content: text}
			var got strings.Builder
			offset, parts := 0, 0
			for {
				if parts++; parts > 400 {
					t.Fatal("did not finish")
				}
				part := Slice(full, offset, DefaultMaxChars)
				enc, _ := json.Marshal(part)
				if len(enc) > resultcut.ModelBudget {
					t.Fatalf("part %d encodes to %d bytes", parts, len(enc))
				}
				if !utf8.ValidString(part.Content) || part.Content == "" {
					t.Fatal("empty or split part")
				}
				got.WriteString(part.Content)
				if part.NextOffset == 0 {
					break
				}
				if part.NextOffset != offset+utf8.RuneCountInString(part.Content) {
					t.Fatalf("next_offset %d does not follow the part", part.NextOffset)
				}
				offset = part.NextOffset
			}
			if got.String() != text {
				t.Fatalf("parts do not rebuild the page (%d parts)", parts)
			}
		})
	}
}
