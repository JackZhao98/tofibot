package provider

import (
	"strings"
	"unicode/utf8"
)

// Some hosted models write their own citation markup into output text: a span
// opened by U+E200 and closed by U+E201 (cite, filecite, navlist and similar
// kinds, with U+E202 between fields). The words in it are internal ids, never
// prose. The sources are already collected from url_citation annotations and
// search-call sources, so the span is dropped and nothing is invented.
const (
	markerStart = ''
	markerEnd   = ''
)

// MarkerFilter removes marker spans from text that arrives in pieces. A span
// that is not closed yet stays hidden, so a half marker never reaches a draft.
// Spaces that the removal would leave doubled are collapsed.
type MarkerFilter struct {
	inSpan    bool
	lastSpace bool // the last character written was a space
	skip      bool // a span just closed after a space: drop spaces that follow
	carry     string
}

// Feed returns the visible part of the next piece.
func (f *MarkerFilter) Feed(piece string) string {
	piece = f.carry + piece
	f.carry = ""
	piece = f.holdPartial(piece)
	var b strings.Builder
	for _, r := range piece {
		switch {
		case f.inSpan:
			if r == markerEnd {
				f.inSpan = false
				f.skip = f.lastSpace
			}
		case r == markerStart:
			f.inSpan = true
		case r == markerEnd:
		case f.skip && r == ' ':
		default:
			f.skip = false
			b.WriteRune(r)
			f.lastSpace = r == ' '
		}
	}
	return b.String()
}

// holdPartial keeps an incomplete UTF-8 tail for the next piece.
func (f *MarkerFilter) holdPartial(s string) string {
	for i := 1; i < utf8.UTFMax && i <= len(s); i++ {
		tail := s[len(s)-i:]
		if !utf8.RuneStart(tail[0]) {
			continue
		}
		if tail[0] >= utf8.RuneSelf && !utf8.FullRuneInString(tail) {
			f.carry = tail
			return s[:len(s)-i]
		}
		return s
	}
	return s
}

// Flush ends the text. An unclosed span and a cut-off character stay dropped.
func (f *MarkerFilter) Flush() string {
	f.carry = ""
	f.inSpan = false
	return ""
}

// StripMarkers returns text without marker spans.
func StripMarkers(text string) string {
	if !strings.ContainsRune(text, markerStart) && !strings.ContainsRune(text, markerEnd) {
		return text
	}
	var f MarkerFilter
	return f.Feed(text) + f.Flush()
}
