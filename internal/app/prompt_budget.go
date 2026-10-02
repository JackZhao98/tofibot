package app

import (
	"fmt"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type runSystemPrompt struct {
	Core, BotInstructions, Durable, Computer, Extensions, Capabilities string
}

// Fixed policies are never tail-truncated. User-authored instructions and the
// optional metadata directory share the remaining space; the original text is
// preserved verbatim within its share. Reserve the runtime agent suffix too, so
// the provider-facing system remains within the limit. History/memory and tool
// schemas have separate budgets; this is not a total input-token limit.
func assembleRunSystem(parts runSystemPrompt) (string, error) {
	discoveryPolicy, directory := extensions.DiscoveryInstructionParts(parts.Extensions)
	fixed := []string{parts.Core, toolEvidencePolicy, parts.Durable, parts.Computer, parts.Capabilities, discoveryPolicy}
	required := strings.Join(fixed, "\n")
	const botStart = "\nBot instructions:\n"
	const botEnd = "\nEnd of Bot instructions.\n"
	remaining := maxSystemRunes - runtime.SystemPromptOverheadRunes() - len([]rune(required)) - 1
	if parts.BotInstructions != "" {
		remaining -= len([]rune(botStart + botEnd))
	}
	if remaining < 0 {
		return "", fmt.Errorf("fixed system policies exceed the %d-rune context budget", maxSystemRunes)
	}
	botSize, dirSize := len([]rune(parts.BotInstructions)), len([]rune(directory))
	botBudget := remaining
	if dirSize > 0 {
		botBudget = remaining * 3 / 4
		if dirSize < remaining-botBudget {
			botBudget = remaining - dirSize
		}
		reserve := dirSize
		if reserve > 256 {
			reserve = 256
		}
		if botBudget > remaining-reserve {
			botBudget = remaining - reserve
		}
	}
	if botSize < botBudget {
		botBudget = botSize
	}
	if botBudget < 0 {
		botBudget = 0
	}
	botText := parts.BotInstructions
	if botSize > botBudget {
		const notice = "\n[Bot instructions truncated to fit context.]"
		if botBudget >= len([]rune(notice)) {
			botText = trimRunes(botText, botBudget-len([]rune(notice))) + notice
		} else {
			botText = trimRunes(botText, botBudget)
		}
	}
	dirText := extensions.BoundCapabilityDirectory(directory, remaining-len([]rune(botText)))
	if parts.BotInstructions != "" {
		required += botStart + botText + botEnd
	}
	return required + "\n" + dirText, nil
}
