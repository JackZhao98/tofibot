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

// The same switch gives those Bots the provider's server-side page reader: a
// search returns titles and links, and a specific figure or attribution is
// only as good as the page text the Bot read. Each page it reads appears in
// the run's evidence as a web_fetch call with its address, and is counted in
// usage_calls (kind web_fetch; the provider charges no fee, the page text is
// billed as ordinary input on the request that read it).
// One request may read at most this many pages; the run's own limit (the web_fetch_limit setting) bounds all its requests.
const webFetchMaxUsesPerRequest = 5

// Runs whose model has no server-side search get the server-run tools instead
// (web_tools.go, decision 0068): same names, ordered provider chains, results
// that name their source. The model's own tools win whenever they are offered.

// OpenAI models (API key and ChatGPT sign-in alike) get OpenAI's hosted
// web_search tool the same way. It has no hosted page reader, so their
// web_fetch stays the server-run builtin reader (web_tools.go). The hosted tool
// takes no per-request cap and its fee is not in the price table, so those
// searches are booked as a count with no fee.

// hostedSearchProvider reports whether the model's provider runs web search
// inside the model request.
func hostedSearchProvider(model string) bool {
	switch runtime.ModelProvider(model) {
	case providerAnthropic, providerOpenAI, providerCodex:
		return true
	}
	return false
}

// webSearchMaxUses is the per-request search cap for a run, 0 when the run has
// no server-side search: the model's provider has none, the owner switched
// it off (TOFI_WEB_SEARCH=off), or a tool of the same name already exists.
func webSearchMaxUses(model string, tools []Tool) int {
	if !hostedSearchProvider(model) {
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

// webFetchMaxUses is the per-request page cap for a run, 0 when the run has no
// server-side page reader: whenever webSearchMaxUses is 0, and when a tool of
// the same name already exists.
func webFetchMaxUses(model string, tools []Tool) int {
	if runtime.ModelProvider(model) != providerAnthropic || webSearchMaxUses(model, nil) == 0 {
		return 0
	}
	for _, t := range tools {
		if t.Name == "web_fetch" {
			return 0
		}
	}
	return webFetchMaxUsesPerRequest
}
