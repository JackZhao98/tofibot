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
//
// A real span is short and has no line break. A U+E200 that is never closed (a
// model that echoes the character, a truncated stream) is not a marker: the
// span is abandoned at a line break or after maxMarkerSpanRunes, the start
// character is removed and the text after it is kept.
const (
	markerStart = ''
	markerEnd   = ''

	maxMarkerSpanRunes = 200
)

// MarkerFilter removes marker spans from text that arrives in pieces. A span
// that is not closed yet stays hidden (at most maxMarkerSpanRunes characters),
// so a half marker never reaches a draft. Spaces that the removal would leave
// doubled are collapsed.
type MarkerFilter struct {
	inSpan    bool
	held      []rune // the characters of the open span after its start
	lastSpace bool   // the last character written was a space
	skip      bool   // a span just closed after a space: drop spaces that follow
	carry     string
}

// Feed returns the visible part of the next piece.
func (f *MarkerFilter) Feed(piece string) string {
	piece = f.carry + piece
	f.carry = ""
	piece = f.holdPartial(piece)
	var b strings.Builder
	for _, r := range piece {
		f.step(&b, r)
	}
	return b.String()
}

func (f *MarkerFilter) step(b *strings.Builder, r rune) {
	if f.inSpan {
		switch {
		case r == markerEnd:
			f.inSpan = false
			f.held = f.held[:0]
			f.skip = f.lastSpace
			return
		case r == markerStart:
			// A second start: the first was not a marker.
			f.abandon(b)
			f.inSpan = true
			return
		case r == '\n':
			f.abandon(b)
		case len(f.held) < maxMarkerSpanRunes:
			f.held = append(f.held, r)
			return
		default:
			f.abandon(b)
		}
	}
	switch r {
	case markerStart:
		f.inSpan = true
	case markerEnd:
	default:
		f.write(b, r)
	}
}

// abandon ends an open span that is not a marker: its start character is gone,
// its text stays.
func (f *MarkerFilter) abandon(b *strings.Builder) {
	f.inSpan = false
	for _, h := range f.held {
		f.write(b, h)
	}
	f.held = f.held[:0]
}

func (f *MarkerFilter) write(b *strings.Builder, r rune) {
	if f.skip && r == ' ' {
		return
	}
	f.skip = false
	b.WriteRune(r)
	f.lastSpace = r == ' '
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

// Flush ends the text and returns what is still held: a span that never closed
// is not a marker, so its text comes out without the start character. A
// cut-off character stays dropped.
func (f *MarkerFilter) Flush() string {
	f.carry = ""
	if !f.inSpan {
		return ""
	}
	var b strings.Builder
	f.abandon(&b)
	return b.String()
}

// StripMarkers returns text without marker spans.
func StripMarkers(text string) string {
	if !strings.ContainsRune(text, markerStart) && !strings.ContainsRune(text, markerEnd) {
		return text
	}
	var f MarkerFilter
	return f.Feed(text) + f.Flush()
}
