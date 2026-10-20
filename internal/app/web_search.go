package app

import (
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Every Bot can look things up without a computer: for Anthropic models the
// provider runs web search inside the model request (server-side), so a Bot
// checks a name, product or event that may be newer than the model before it
// answers. The searches appear in the run's evidence as web_search calls with
// their sources, and each is booked as a fee in usage_calls (kind web_search).
const webSearchMaxUsesPerRequest = 3

// webSearchMaxUses is the per-request search cap for a run, 0 when the run has
// no server-side search: the model is not an Anthropic one, the owner switched
// it off (TOFI_WEB_SEARCH=off), or a tool of the same name already exists.
func webSearchMaxUses(model string, tools []Tool) int {
	if runtime.ModelProvider(model) != providerAnthropic {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TOFI_WEB_SEARCH"))) {
	case "off", "0", "false", "no":
		return 0
	}
	for _, t := range tools {
		if t.Name == "web_search" {
			return 0
		}
	}
	return webSearchMaxUsesPerRequest
}
