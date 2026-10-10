package app

import "strings"

// The Bot marks transient narration with <progress>...</progress> in its normal
// text. Everything outside the tag is an answer. One incremental scanner backs
// both the stored message split and the live stream filter, so a draft and the
// message it becomes always agree, and a tag never reaches the user.
//
// Rules: "<progress>" outside a note opens one; "</progress>" inside closes it.
// A stray close tag or a nested open tag is dropped. An unclosed note runs to
// the end of the text. A tag split across chunks is held back until decidable.
//
// Markdown code is literal: inside an inline code span (`...`, closed by a run
// of the same length, ending at the latest at the line end) or a fenced block
// (``` or ~~~ at a line start, closed by a fence of at least that length at a
// line start) a tag is text, so an answer can discuss the HTML <progress>
// element. A backtick run cut by a chunk boundary is held back like a tag.
const (
	progressOpenTag  = "<progress>"
	progressCloseTag = "</progress>"
)

type textPart struct {
	Text     string
	Progress bool
}

type progressScanner struct {
	inside   bool
	pending  string // trailing bytes that may still become a tag or a longer backtick run
	code     int    // >0: inside code opened by a run of this many codeChar bytes
	codeChar byte   // '`' or '~'
	fence    bool   // the open code is a fenced block (closes only at a line start)
	midLine  bool   // a non-blank byte was seen on the current line
}

// feed consumes a chunk and returns the parts that are now decided.
func (p *progressScanner) feed(chunk string) []textPart {
	in := p.pending + chunk
	p.pending = ""
	var out []textPart
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, textPart{Text: buf.String(), Progress: p.inside})
			buf.Reset()
		}
	}
	for i := 0; i < len(in); {
		ch := in[i]
		switch {
		case ch == '`' || ch == '~':
			j := i + 1
			for j < len(in) && in[j] == ch {
				j++
			}
			if j == len(in) {
				p.pending = in[i:] // the run may continue in the next chunk
				i = j
				continue
			}
			n := j - i
			switch {
			case p.code == 0 && n >= 3 && !p.midLine:
				p.code, p.codeChar, p.fence = n, ch, true
			case p.code == 0 && ch == '`':
				p.code, p.codeChar, p.fence = n, ch, false
			case p.code > 0 && ch == p.codeChar && p.fence && n >= p.code && !p.midLine:
				p.code = 0
			case p.code > 0 && ch == p.codeChar && !p.fence && n == p.code:
				p.code = 0
			}
			buf.WriteString(in[i:j])
			p.midLine = true
			i = j
		case ch == '\n':
			buf.WriteByte(ch)
			p.midLine = false
			if !p.fence {
				p.code = 0 // an inline span never crosses a line end
			}
			i++
		case ch == '<' && p.code == 0:
			rest := in[i:]
			switch {
			case hasPrefixFold(rest, progressOpenTag):
				flush()
				p.inside = true // a nested open tag inside a note is simply dropped
				i += len(progressOpenTag)
			case hasPrefixFold(rest, progressCloseTag):
				flush()
				p.inside = false // a stray close tag outside a note is simply dropped
				i += len(progressCloseTag)
			case len(rest) < len(progressCloseTag) && (hasPrefixFold(progressOpenTag, rest) || hasPrefixFold(progressCloseTag, rest)):
				p.pending = rest // possibly a tag cut by the chunk boundary
				i = len(in)
			default:
				buf.WriteByte('<')
				p.midLine = true
				i++
			}
		default:
			buf.WriteByte(ch)
			if ch != ' ' && ch != '\t' && ch != '\r' {
				p.midLine = true
			}
			i++
		}
	}
	flush()
	return out
}

// finish releases a held-back tail as literal text and resets the scanner.
func (p *progressScanner) finish() []textPart {
	var out []textPart
	if p.pending != "" {
		out = []textPart{{Text: p.pending, Progress: p.inside}}
	}
	*p = progressScanner{}
	return out
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// splitProgress splits a complete turn, in order, into note and answer parts.
// Adjacent parts of one kind merge and whitespace-only parts are dropped.
func splitProgress(text string) []textPart {
	var sc progressScanner
	raw := append(sc.feed(text), sc.finish()...)
	var parts []textPart
	for _, r := range raw {
		if n := len(parts); n > 0 && parts[n-1].Progress == r.Progress {
			parts[n-1].Text += r.Text
		} else {
			parts = append(parts, r)
		}
	}
	kept := parts[:0]
	for _, p := range parts {
		if p.Text = strings.TrimSpace(p.Text); p.Text != "" {
			kept = append(kept, p)
		}
	}
	return kept
}

// stripProgressTags returns the text with every tag removed and nothing else
// changed; notes keep their words.
func stripProgressTags(text string) string {
	if !strings.Contains(text, "<") {
		return text
	}
	var sc progressScanner
	var b strings.Builder
	for _, p := range append(sc.feed(text), sc.finish()...) {
		b.WriteString(p.Text)
	}
	return b.String()
}

// finalSplit divides the last turn of a run (no tool calls) into notes that
// precede the answer and the answer itself. The final turn always yields a
// visible answer: when it is all notes, the notes joined (tags stripped) are
// the answer. A note after the last answer part moves in front of it, so the
// answer stays the run's last message.
func finalSplit(text string) (notes []textPart, answer string) {
	parts := splitProgress(text)
	last := -1
	for i, p := range parts {
		if !p.Progress {
			last = i
		}
	}
	if last < 0 {
		var all []string
		for _, p := range parts {
			all = append(all, p.Text)
		}
		return nil, strings.Join(all, "\n\n")
	}
	for i, p := range parts {
		if i != last {
			notes = append(notes, p)
		}
	}
	return notes, parts[last].Text
}
