package webtools

import (
	"strings"
	"testing"
)

func TestSliceReadsALongPageInParts(t *testing.T) {
	text := strings.Repeat("a", 2500) + strings.Repeat("é", 2500) + strings.Repeat("c", 1000)
	full := Page{Source: "builtin", URL: "https://x.example/", Content: text}
	var got strings.Builder
	offset, parts := 0, 0
	for {
		p := Slice(full, offset, 2000)
		parts++
		got.WriteString(p.Content)
		if p.TotalChars != 6000 || p.Offset != offset {
			t.Fatalf("part %d: %+v", parts, p)
		}
		if p.NextOffset == 0 {
			if p.Truncated || p.Note != "" {
				t.Fatalf("last part flagged: %+v", p)
			}
			break
		}
		if !p.Truncated || p.NextOffset != offset+2000 || !strings.Contains(p.Note, "offset") {
			t.Fatalf("part %d: %+v", parts, p)
		}
		offset = p.NextOffset
	}
	if got.String() != text || parts != 3 {
		t.Fatalf("parts=%d, joined text differs=%v", parts, got.String() != text)
	}
	past := Slice(full, 6000, 2000)
	if past.Content != "" || past.TotalChars != 6000 || !strings.Contains(past.Note, "past the end") {
		t.Fatalf("%+v", past)
	}
}

func TestFetchStillCapsAtMaxChars(t *testing.T) {
	long := strings.Repeat("word ", 20000)
	p := Slice(Page{Content: long}, 0, 0)
	if len([]rune(p.Content)) != DefaultMaxChars || p.NextOffset != DefaultMaxChars || p.TotalChars != len([]rune(long)) {
		t.Fatalf("%d %d %d", len([]rune(p.Content)), p.NextOffset, p.TotalChars)
	}
}
