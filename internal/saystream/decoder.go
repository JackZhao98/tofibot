// Package saystream decodes the streamed JSON arguments of a say call
// incrementally, so the text a model is still writing can reach the stream
// draft as it is written. The decoder only ever feeds a draft: what is
// published is parsed from the complete arguments, so a decoder bug shows up
// as a wrong draft, never as stored text.
package saystream

import (
	"strings"
	"unicode/utf16"
)

// jsonStringDecoder decodes the body of one JSON string a rune at a time,
// holding back an escape sequence cut by a chunk boundary.
type jsonStringDecoder struct {
	esc  bool
	inU  bool
	hex  []rune
	high rune // pending high surrogate
}

func (d *jsonStringDecoder) reset() { *d = jsonStringDecoder{} }

// feed consumes one rune of the string body. closed reports the closing quote.
func (d *jsonStringDecoder) feed(r rune) (out string, closed bool) {
	if d.inU {
		d.hex = append(d.hex, r)
		if len(d.hex) < 4 {
			return "", false
		}
		var code rune
		for _, h := range d.hex {
			code <<= 4
			switch {
			case h >= '0' && h <= '9':
				code |= h - '0'
			case h >= 'a' && h <= 'f':
				code |= h - 'a' + 10
			case h >= 'A' && h <= 'F':
				code |= h - 'A' + 10
			default:
				d.inU, d.hex, d.high = false, nil, 0
				return string(utf8Replacement), false
			}
		}
		d.inU, d.hex = false, nil
		switch {
		case d.high != 0 && utf16.IsSurrogate(code) && code >= 0xDC00:
			joined := utf16.DecodeRune(d.high, code)
			d.high = 0
			return string(joined), false
		case code >= 0xD800 && code < 0xDC00:
			d.high = code
			return "", false
		case d.high != 0:
			d.high = 0
			return string(utf8Replacement) + string(code), false
		}
		return string(code), false
	}
	if d.esc {
		d.esc = false
		switch r {
		case 'n':
			return "\n", false
		case 't':
			return "\t", false
		case 'r':
			return "\r", false
		case 'b':
			return "\b", false
		case 'f':
			return "\f", false
		case 'u':
			d.inU, d.hex = true, d.hex[:0]
			return "", false
		}
		return string(r), false
	}
	if d.high != 0 && r != '\\' {
		d.high = 0 // a lone high surrogate; nothing sensible to show
	}
	switch r {
	case '\\':
		d.esc = true
		return "", false
	case '"':
		return "", true
	}
	return string(r), false
}

const utf8Replacement = '�'

type state int

const (
	stStart      state = iota // before '{'
	stKeyOrEnd                // expecting a key's opening quote, ',' or '}'
	stInKey                   // inside a key
	stColon                   // after a key, expecting ':'
	stValue                   // expecting a value
	stInText                  // inside the "text" string
	stInPurpose               // inside the "purpose" string
	stInOutcome               // inside the "outcome" string
	stSkipString              // inside a string value we ignore
	stSkipNested              // inside an object or array we ignore
	stSkipScalar              // inside a number, true, false or null
	stAfterValue              // after a value, expecting ',' or '}'
	stDone
)

// Decoder pulls the purpose and the text out of the arguments JSON of
// a say call as it streams. Text decoded before the purpose is known is held
// and released with the purpose, so a model that writes text first still
// streams once the purpose arrives.
type Decoder struct {
	state    state
	key      strings.Builder
	cur      string
	str      jsonStringDecoder
	purpose  strings.Builder
	known    bool
	outcome  strings.Builder
	outKnown bool
	held     strings.Builder
	depth    int
	inStr    bool // inside a string within a skipped nested value
	nestEsc  bool
}

// Purpose returns the purpose once its string has closed.
func (d *Decoder) Purpose() (string, bool) { return d.purpose.String(), d.known }

// Outcome returns the outcome once its string has closed.
func (d *Decoder) Outcome() (string, bool) { return d.outcome.String(), d.outKnown }

// Feed consumes an arguments fragment and returns the text that is now
// decided and may be shown.
func (d *Decoder) Feed(chunk string) string {
	var out strings.Builder
	emit := func(s string) {
		if s == "" {
			return
		}
		if d.known {
			out.WriteString(s)
		} else {
			d.held.WriteString(s)
		}
	}
	for _, r := range chunk {
		switch d.state {
		case stStart:
			if r == '{' {
				d.state = stKeyOrEnd
			}
		case stKeyOrEnd:
			switch r {
			case '"':
				d.key.Reset()
				d.str.reset()
				d.state = stInKey
			case '}':
				d.state = stDone
			}
		case stInKey:
			s, closed := d.str.feed(r)
			if closed {
				d.cur = d.key.String()
				d.state = stColon
			} else {
				d.key.WriteString(s)
			}
		case stColon:
			if r == ':' {
				d.state = stValue
			}
		case stValue:
			switch {
			case r == ' ' || r == '\n' || r == '\t' || r == '\r':
			case r == '"' && d.cur == "text":
				d.str.reset()
				d.state = stInText
			case r == '"' && d.cur == "purpose":
				d.str.reset()
				d.purpose.Reset()
				d.state = stInPurpose
			case r == '"' && d.cur == "outcome":
				d.str.reset()
				d.outcome.Reset()
				d.outKnown = false
				d.state = stInOutcome
			case r == '"':
				d.str.reset()
				d.state = stSkipString
			case r == '{' || r == '[':
				d.depth, d.inStr, d.nestEsc = 1, false, false
				d.state = stSkipNested
			default:
				d.state = stSkipScalar
			}
		case stInText:
			s, closed := d.str.feed(r)
			if closed {
				d.state = stAfterValue
			} else {
				emit(s)
			}
		case stInPurpose:
			s, closed := d.str.feed(r)
			if closed {
				d.state = stAfterValue
				if !d.known {
					d.known = true
					out.WriteString(d.held.String())
					d.held.Reset()
				}
			} else {
				d.purpose.WriteString(s)
			}
		case stInOutcome:
			s, closed := d.str.feed(r)
			if closed {
				d.state = stAfterValue
				d.outKnown = true
			} else {
				d.outcome.WriteString(s)
			}
		case stSkipString:
			if _, closed := d.str.feed(r); closed {
				d.state = stAfterValue
			}
		case stSkipNested:
			switch {
			case d.inStr && d.nestEsc:
				d.nestEsc = false
			case d.inStr && r == '\\':
				d.nestEsc = true
			case d.inStr && r == '"':
				d.inStr = false
			case !d.inStr && r == '"':
				d.inStr = true
			case !d.inStr && (r == '{' || r == '['):
				d.depth++
			case !d.inStr && (r == '}' || r == ']'):
				if d.depth--; d.depth == 0 {
					d.state = stAfterValue
				}
			}
		case stSkipScalar:
			switch r {
			case ',':
				d.state = stKeyOrEnd
			case '}':
				d.state = stDone
			}
		case stAfterValue:
			switch r {
			case ',':
				d.state = stKeyOrEnd
			case '}':
				d.state = stDone
			}
		case stDone:
		}
	}
	return out.String()
}
