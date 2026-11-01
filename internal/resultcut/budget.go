package resultcut

import "unicode/utf8"

// ModelBudget is the most bytes of one tool result the model receives. A
// result over it is cut in the middle.
const ModelBudget = 24000

// PageBudget is the most encoded bytes of paged content a tool puts in one
// result. The rest of ModelBudget is room for keys, headers and notes.
const PageBudget = 18000

// JSONCost returns how many bytes r takes inside a JSON string once encoded
// with HTML escaping. It never reports less than the encoder writes.
func JSONCost(r rune, size int) int {
	switch {
	case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
		return 2
	case r < 0x20 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029:
		return 6
	case r == utf8.RuneError && size <= 1:
		return 6
	}
	return size
}

// JSONLen returns how many bytes s takes inside a JSON string once encoded
// with HTML escaping.
func JSONLen(s string) int {
	n := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if r == utf8.RuneError {
			_, size = utf8.DecodeRuneInString(s[i:])
		}
		n += JSONCost(r, size)
	}
	return n
}

// FitJSON returns the byte length of the longest prefix of s, cut on a rune
// boundary, that takes at most budget bytes inside a JSON string. It returns
// at least one whole rune when s is not empty, so a caller that pages through
// s always moves forward.
func FitJSON(s string, budget int) int {
	used := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if r == utf8.RuneError {
			_, size = utf8.DecodeRuneInString(s[i:])
		}
		cost := JSONCost(r, size)
		if used+cost > budget && i > 0 {
			return i
		}
		used += cost
	}
	return len(s)
}

// FitRaw returns the byte length of the longest prefix of s, cut on a rune
// boundary, of at most budget bytes, and at least one rune when s is not empty.
func FitRaw(s string, budget int) int {
	if len(s) <= budget {
		return len(s)
	}
	end := budget
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	if end == 0 {
		_, end = utf8.DecodeRuneInString(s)
	}
	return end
}
