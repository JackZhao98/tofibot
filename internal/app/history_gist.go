package app

import "strings"

// ownVerbatimReplies is how many of the Bot's own most recent replies stay
// verbatim in its history; older ones are reduced to their gist.
const ownVerbatimReplies = 3

// ownGistRunes bounds the gist kept for an older reply of the Bot itself.
const ownGistRunes = 120

// ownReplyGist keeps the first sentence of an older reply (bounded) and marks
// it as a gist, so the model keeps the facts it stated without its phrasing.
func ownReplyGist(content string) string {
	text := strings.Join(strings.Fields(content), " ")
	if text == "" {
		return text
	}
	cut := len([]rune(text))
	for i, r := range []rune(text) {
		if strings.ContainsRune("。！？.!?\n", r) {
			cut = i + 1
			break
		}
	}
	first := string([]rune(text)[:cut])
	first = trimRunes(first, ownGistRunes)
	return "(earlier reply, gist) " + first
}
