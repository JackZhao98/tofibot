package app

import "strings"

const (
	scheduleMissingReceiptError = "scheduled task returned without confirming a completed result"
	scheduleUnconfirmedLabel    = "\n\nUNCONFIRMED assistant output (not proof of completion):\n"
	maxScheduleFailureRunes     = 2400 // Includes the error, label and truncation marker.
)

// scheduleFailureDetail is only for a successful engine return with no receipt.
// Content is the runtime's public final text (reasoning is already excluded),
// never a transcript, stream draft, tool result or an infrastructure error.
func scheduleFailureDetail(content string, bot Bot) string {
	content = strings.TrimSpace(stripProgressTags(cleanBotOutput(content, bot)))
	if content == "" {
		return scheduleMissingReceiptError
	}
	prefix := scheduleMissingReceiptError + scheduleUnconfirmedLabel
	const marker = "\n[… truncated]"
	runes := []rune(content)
	available := maxScheduleFailureRunes - len([]rune(prefix))
	if len(runes) > available {
		content = string(runes[:available-len([]rune(marker))]) + marker
	}
	return prefix + content
}
