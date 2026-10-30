package app

import (
	"regexp"
	"strings"
)

// The sidebar shows the newest message as one line of plain text. The stored message is Markdown, so the preview
// keeps the words and drops the syntax around them.

// previewSourceRunes is how much of a message the list query reads before the syntax is removed; the window is
// wider than the preview so that a link near the end of the preview is whole when it is read.
const (
	previewRunes       = 160
	previewSourceRunes = 480
)

var (
	// A link or image reads as its label. A link cut by the end of the window reads as its label too.
	previewLinkRE   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*(?:\)|$)`)
	previewMarkRE   = regexp.MustCompile("[*`]+")
	previewLineRE   = regexp.MustCompile(`(?m)^[ \t]*(?:#+|>+)[ \t]*`)
	previewLeadUnd  = regexp.MustCompile(`(^|[\s(])_+`)
	previewTrailUnd = regexp.MustCompile(`_+([\s).,;:!?]|$)`)
	previewSpaceRE  = regexp.MustCompile(`\s+`)
)

func plainPreview(text string, max int) string {
	text = previewLinkRE.ReplaceAllString(text, "$1")
	text = previewMarkRE.ReplaceAllString(text, "")
	text = previewLineRE.ReplaceAllString(text, "")
	text = previewLeadUnd.ReplaceAllString(text, "$1")
	text = previewTrailUnd.ReplaceAllString(text, "$1")
	text = strings.TrimSpace(previewSpaceRE.ReplaceAllString(text, " "))
	return clipHead(text, max)
}

func clipHead(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
