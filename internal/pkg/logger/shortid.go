package logger

// ShortID truncates an identifier to 8 characters for log readability.
// IDs come from user-controlled sources (app slugs, connector IDs), so
// they can be shorter than 8 — naive id[:8] panics on those.
func ShortID(id string) string {
	const maxLen = 8
	runes := []rune(id)
	if len(runes) <= maxLen {
		return id
	}
	return string(runes[:maxLen])
}
