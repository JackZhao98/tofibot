// Package resultcut shortens an over-long tool result so that it stays valid
// JSON when it was JSON, and says what was removed.
package resultcut

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

// Hint tells the reader how to get what was removed.
const Hint = "Ask the tool for the rest with its offset or cursor argument when it has one, or narrow the request."

const maxPasses = 64

// MaxInputFactor bounds the work JSON does: an output more than this many
// times max is not decoded, and the caller cuts it as text.
const MaxInputFactor = 16

// JSON returns output cut to at most max bytes when output is a JSON object or
// array. Arrays lose their middle elements and long strings lose their middle;
// each cut leaves a marker that names how much was removed. The second result
// is false when output is not JSON or cannot be brought under max, and the
// caller then falls back to a text cut.
func JSON(output string, max int) (string, bool) {
	if len(output) <= max {
		return output, true
	}
	if len(output) > MaxInputFactor*max {
		return "", false
	}
	trimmed := bytes.TrimSpace([]byte(output))
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil || dec.More() {
		return "", false
	}
	for pass := 0; pass < maxPasses; pass++ {
		enc, ok := encode(root)
		if !ok {
			return "", false
		}
		if len(enc) <= max {
			return enc, true
		}
		var shrunk bool
		root, shrunk = shrink(root, len(enc), max)
		if !shrunk {
			return "", false
		}
	}
	return "", false
}

func encode(v any) (string, bool) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return "", false
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), true
}

func size(v any) int {
	s, _ := encode(v)
	return len(s)
}

// shrink reduces the largest cuttable value under v. excess is how many bytes
// the whole result is over; each call removes about half of that or of the
// value, whichever is less, so the loop converges.
func shrink(v any, total, max int) (any, bool) {
	excess := total - max
	switch x := v.(type) {
	case []any:
		// Descend when one element holds most of the bulk.
		if i, sz := largest(x); sz > excess*2 && sz*2 > total {
			if nv, ok := shrink(x[i], sz, sz-excess); ok {
				out := append([]any(nil), x...)
				out[i] = nv
				return out, true
			}
		}
		return cutArray(x, excess), len(x) > 2
	case map[string]any:
		keys := make([]string, 0, len(x))
		sizes := make(map[string]int, len(x))
		for k, c := range x {
			keys = append(keys, k)
			sizes[k] = size(c)
		}
		sort.Slice(keys, func(i, j int) bool {
			if sizes[keys[i]] != sizes[keys[j]] {
				return sizes[keys[i]] > sizes[keys[j]]
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			nv, ok := shrink(x[k], sizes[k], sizes[k]-excess)
			if !ok {
				continue
			}
			out := make(map[string]any, len(x))
			for kk, c := range x {
				out[kk] = c
			}
			out[k] = nv
			return out, true
		}
		return v, false
	case string:
		return cutString(x, excess)
	}
	return v, false
}

func largest(x []any) (int, int) {
	bi, bs := 0, -1
	for i, c := range x {
		if sz := size(c); sz > bs {
			bi, bs = i, sz
		}
	}
	return bi, bs
}

// cutArray drops a run of middle elements worth about excess bytes (at least
// one element, and a head and a tail are kept) and inserts a marker string.
func cutArray(x []any, excess int) []any {
	if len(x) <= 2 {
		return x
	}
	// Marker elements from an earlier pass count as part of the run.
	sizes := make([]int, len(x))
	total := 0
	for i, c := range x {
		sizes[i] = size(c) + 1
		total += sizes[i]
	}
	want := excess + 240
	if want > total*9/10 {
		want = total * 9 / 10
	}
	// Remove from the centre outward so head and tail stay balanced.
	lo, hi := len(x)/2, len(x)/2
	removed := 0
	for removed < want && (lo > 1 || hi < len(x)-1) {
		if lo > 1 && (removed <= 0 || hi >= len(x)-1 || (hi-len(x)/2) >= (len(x)/2-lo)) {
			lo--
			removed += sizes[lo]
		} else if hi < len(x)-1 {
			removed += sizes[hi]
			hi++
		} else {
			break
		}
	}
	if hi == lo {
		hi = lo + 1
	}
	dropped := 0
	for i := lo; i < hi; i++ {
		if s, ok := x[i].(string); ok && isMarker(s) {
			dropped += markerCount(s)
		} else {
			dropped++
		}
	}
	out := make([]any, 0, len(x)-(hi-lo)+1)
	out = append(out, x[:lo]...)
	out = append(out, fmt.Sprintf("[%d items omitted from the middle of this list. %s]", dropped, Hint))
	out = append(out, x[hi:]...)
	return out
}

func isMarker(s string) bool {
	var n int
	_, err := fmt.Sscanf(s, "["+"%d items omitted", &n)
	return err == nil
}

func markerCount(s string) int {
	var n int
	fmt.Sscanf(s, "["+"%d items omitted", &n)
	return n
}

// cutString removes about excess bytes from the middle of a long string.
func cutString(s string, excess int) (string, bool) {
	const keepMin = 200
	if len(s) <= 2*keepMin+len(Hint)+80 {
		return s, false
	}
	keep := len(s) - excess - len(Hint) - 80
	if keep < 2*keepMin {
		keep = 2 * keepMin
	}
	if keep >= len(s) {
		keep = len(s) - 1
	}
	head := keep * 6 / 10
	tail := keep - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	ts := len(s) - tail
	for ts < len(s) && !utf8.RuneStart(s[ts]) {
		ts++
	}
	if ts <= head {
		return s, false
	}
	return s[:head] + fmt.Sprintf(" [%d of %d bytes omitted from the middle. %s] ", ts-head, len(s), Hint) + s[ts:], true
}
