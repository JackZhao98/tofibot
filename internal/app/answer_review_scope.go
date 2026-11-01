package app

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// The completion review of a say answer has something to check only when the
// run's tool work left a result that may not support the draft, or when the
// draft itself states figures or a long run of claims. A short draft whose
// every tool call completed has neither, so the review is skipped and the
// answer ships without a spent turn. The decision reads tool statuses and the
// shape of the draft; it never classifies wording.

const reviewLongDraftRunes = 600

// reviewSelfConfirming lists the tools whose own completed result is the
// confirmation of their outcome. Every other review-arming tool, and any tool
// with an unknown effect, keeps the review armed even when its call completed:
// a completed call of those can mean a card or a request was created while the
// effect itself has not happened.
var reviewSelfConfirming = []string{"computer_files", "create_work_item", "update_work_item", "task_update"}

// toolWorkRecord remembers which review-arming calls ended without a
// confirmed result.
type toolWorkRecord struct {
	mu          sync.Mutex
	unconfirmed map[string]bool
	// unlisted is set by a review-arming call whose tool is outside
	// reviewSelfConfirming.
	unlisted bool
	restored bool
}

// note records one tool event. Only a review-arming call counts.
func (t *toolWorkRecord) note(ev runtime.ToolEvent) {
	if !completionReviewWork(ev.Name, ev.Risk) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !slices.Contains(reviewSelfConfirming, ev.Name) {
		t.unlisted = true
	}
	if ev.Status != "failed" {
		return
	}
	if t.unconfirmed == nil {
		t.unconfirmed = map[string]bool{}
	}
	t.unconfirmed[ev.Name] = true
}

// markRestored records that earlier tool work came back from the database
// without its results, which leaves nothing to rule out.
func (t *toolWorkRecord) markRestored() {
	t.mu.Lock()
	t.restored = true
	t.mu.Unlock()
}

func (t *toolWorkRecord) names() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.unconfirmed))
	for n := range t.unconfirmed {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (t *toolWorkRecord) clean() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.restored && !t.unlisted && len(t.unconfirmed) == 0
}

// draftSentences splits a draft at sentence ends and line breaks.
func draftSentences(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '.', '!', '?', '\n', '。', '！', '？':
			return true
		}
		return false
	})
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// figureSentences returns the sentences of the draft that carry a digit.
func figureSentences(text string) []string {
	var out []string
	for _, s := range draftSentences(text) {
		if strings.IndexFunc(s, unicode.IsDigit) >= 0 {
			out = append(out, s)
		}
	}
	return out
}

// draftMayClaimExtent reports whether the draft states a figure or is long
// enough to hold claims the record has to carry.
func draftMayClaimExtent(text string) bool {
	return utf8.RuneCountInString(text) > reviewLongDraftRunes || len(figureSentences(text)) > 0
}

// reviewSkippable is true when the review has nothing to check: every
// review-arming call of the run is from a self-confirming tool and completed,
// and the draft states no figures. A scheduled run is never skipped; its
// answer is also its receipt (the caller checks that).
func (t *toolWorkRecord) reviewSkippable(text string) bool {
	return t.clean() && !draftMayClaimExtent(text)
}

// reviewReminder is the review instruction with what to check in this draft.
func (t *toolWorkRecord) reviewReminder(base, text string) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nWhat to check in this draft:")
	if names := t.names(); len(names) > 0 {
		fmt.Fprintf(&b, "\n- These calls ended without a confirmed result: %s. Every sentence that relies on them lacks support until you inspect the current state or say the result is unknown.", strings.Join(names, ", "))
	}
	if fs := figureSentences(text); len(fs) > 0 {
		b.WriteString("\n- These sentences state figures; each must match a tool result:")
		for i, s := range fs {
			if i == 3 {
				break
			}
			fmt.Fprintf(&b, "\n  - %s", clipRunes(s, 160))
		}
	}
	if utf8.RuneCountInString(text) > reviewLongDraftRunes {
		b.WriteString("\n- The draft is long; confirm that each statement of extent holds for exactly what the results show.")
	}
	b.WriteString("\nThis review runs once. After correcting the unsupported sentences, send the answer again with say; a draft whose sentences are all supported is sent unchanged.")
	return b.String()
}
