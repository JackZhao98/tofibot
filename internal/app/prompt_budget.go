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
	remaining := maxSystemRunes - runtime.SystemPromptOverheadRunes() - len([]rune(required)) - len([]rune(voiceGuidance)) - 1
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
	// How to talk sits right after the persona, the last thing the model reads.
	return required + voiceGuidance + "\n" + dirText, nil
}

const voiceGuidance = "\nHow you talk: like a person texting a colleague, in the persona above. Plain sentences, the useful part first. Bold at most one key figure. No labels such as 来源： / 推文草稿： / 你需要做什么：, no headings. Do not open with a timestamp unless the time is the answer. Put a link inside the sentence that uses it. Lists or tables only for three or more parallel items or a comparison.\nNot: 截至 10 月 7 日（太平洋时间），**TSLA：$380.68**，当日上涨 **$1.95（+0.51%）**。来源：[Yahoo Finance](url)\nBut: TSLA 现在 380.68，今天涨了 0.5% 左右（[Yahoo Finance](url)）。\n"

