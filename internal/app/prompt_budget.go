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
	segs, err := assembleRunSystemSegments(parts)
	if err != nil {
		return "", err
	}
	return joinSystemSegments(segs), nil
}

// systemSegment is a contiguous slice of the assembled system prompt with the
// kind "view context" shows it under. The segments concatenate to exactly the
// bytes assembleRunSystem returns.
type systemSegment struct{ Kind, Text string }

func joinSystemSegments(segs []systemSegment) string {
	var b strings.Builder
	for _, seg := range segs {
		b.WriteString(seg.Text)
	}
	return b.String()
}

func assembleRunSystemSegments(parts runSystemPrompt) ([]systemSegment, error) {
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
		return nil, fmt.Errorf("fixed system policies exceed the %d-rune context budget", maxSystemRunes)
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
	// How to talk sits right after the persona, the last thing the model reads.
	// The separators belong to the segment that follows them, so an empty part
	// still leaves its newline in the right place.
	var segs []systemSegment
	pending := ""
	add := func(kind, text string) {
		if text == "" {
			return
		}
		segs = append(segs, systemSegment{kind, pending + text})
		pending = ""
	}
	add("system_guidance", parts.Core)
	for _, p := range []struct{ kind, text string }{
		{"tool_evidence_policy", toolEvidencePolicy},
		{"durable_guidance", parts.Durable},
		{"computer_environment", parts.Computer},
		{"capability_guidance", parts.Capabilities},
		{"extension_discovery_policy", discoveryPolicy},
	} {
		pending += "\n"
		add(p.kind, p.text)
	}
	if parts.BotInstructions != "" {
		add("bot_instructions", botStart+botText+botEnd)
	}
	add("voice_guidance", voiceGuidance)
	pending += "\n"
	add("extension_directory", dirText)
	if pending != "" {
		if n := len(segs); n > 0 {
			segs[n-1].Text += pending
		} else {
			segs = append(segs, systemSegment{"system_guidance", pending})
		}
	}
	return segs, nil
}

const voiceGuidance = "\nHow you talk: like a person texting a colleague, in the persona above. Plain sentences, the useful part first. Bold at most one key figure. No field labels, no headings. No opening timestamp unless the time is the answer. Links go inside the sentence that uses them. Lists or tables only for three or more parallel items or a comparison.\nGive the result, not how you got it: no tools, steps, source internals, re-checks or followed instructions; mention a gap only if it changes what the user should believe or do. Say you, never \"the user\". Tell a block as what did not happen and what you need. Match length to the question: a casual line gets a short reply. When the user jokes, vents or thanks you, respond to that first. Stop when the answer is done; offer more only for an obvious next step, never a routine closing offer. Bring up older issues only when they matter to the current question. When the person asks what you can do, or seems unsure what to ask for, answer with a few concrete tasks you could do for them now, grounded in the tools actually available in this run; you operate your own computer and browser, so any website is within reach. Pick different tasks each time instead of repeating a list.\nYour persona's scope sets what you own and pursue on your own initiative; it places no limit on conversation. A casual question outside it gets a direct answer with an ordinary person's view and no remark about your scope; mention a limit only when the question needs expertise you lack.\n"
