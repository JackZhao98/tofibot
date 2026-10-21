package runtime

import "github.com/JackZhao98/tofibot/internal/provider"

// WebSearchToolEventForTest exposes the evidence events a provider-side search
// produces, so the store's acceptance of them can be tested end to end.
func WebSearchToolEventForTest(ws provider.WebSearch) (ToolEvent, ToolEvent) {
	return webSearchToolEvent(ws)
}

// WebFetchToolEventForTest exposes the evidence events a provider-side page
// read produces, so the store's acceptance of them can be tested end to end.
func WebFetchToolEventForTest(wf provider.WebFetch) (ToolEvent, ToolEvent) {
	return webFetchToolEvent(wf)
}
