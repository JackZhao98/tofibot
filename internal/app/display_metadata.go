package app

import (
	"errors"
	"fmt"
	"strings"
)

const maxDisplayTitle = 120
const maxDisplayDescription = 280

// Memories store factual data, not executable prompts. Metadata is presentation
// only and must never replace Content in the model's factual memory context.
type MemoryInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

type MemoryPatch struct {
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Content     *string       `json:"content"`
	Expected    *EditBaseline `json:"expected,omitempty"`
}

// Opt-in field comparisons protect edit sessions without conflicting with
// unrelated updates (including schedule claims and memory fact corrections).
// The existing record is read and checked in the same transaction as the write.
type EditBaseline struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Content     *string `json:"content"`
}

var ErrEditConflict = errors.New("record changed while editing; reopen details before retrying")

func validateEditExpectation(expected *EditBaseline, title, description, content *string) error {
	if expected == nil {
		return nil
	} // Keep older PATCH clients compatible.
	if title != nil && expected.Title == nil || description != nil && expected.Description == nil || content != nil && expected.Content == nil {
		return errors.New("expected must include the original value of every changed field")
	}
	return nil
}

func checkEditExpectation(expected *EditBaseline, title, description, content *string, currentTitle, currentDescription, currentContent string) error {
	if expected == nil {
		return nil
	}
	for _, field := range []struct {
		name            string
		patch, baseline *string
		current         string
	}{
		{"title", title, expected.Title, currentTitle},
		{"description", description, expected.Description, currentDescription},
		{"content", content, expected.Content, currentContent},
	} {
		if field.patch != nil && field.baseline != nil && *field.baseline != field.current {
			return fmt.Errorf("%w: %s", ErrEditConflict, field.name)
		}
	}
	return nil
}

// Compatibility calls may omit metadata. Neutral labels avoid deriving a
// potentially sensitive instruction or inventing a summary of historical data.
func normalizeDisplayMetadata(title, description string, required bool) (string, string, error) {
	title, description = compactWhitespace(title), compactWhitespace(description)
	if required && (title == "" || description == "") {
		return "", "", errors.New("title and description are required; provide concise user-facing metadata separate from content")
	}
	if len([]rune(title)) > maxDisplayTitle || len([]rune(description)) > maxDisplayDescription {
		return "", "", fmt.Errorf("title must be at most %d characters and description at most %d", maxDisplayTitle, maxDisplayDescription)
	}
	return title, description, nil
}

func compactWhitespace(value string) string { return strings.Join(strings.Fields(value), " ") }

func validateMetadataPatch(title, description *string) error {
	t, d := "", ""
	if title != nil {
		t = *title
	}
	if description != nil {
		d = *description
	}
	_, _, err := normalizeDisplayMetadata(t, d, false)
	return err
}

func displayTitleSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": maxDisplayTitle, "description": "Concise user-facing title in the user's language; do not copy the execution prompt or full memory body"}
}

func displayDescriptionSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": maxDisplayDescription, "description": "Short user-facing description in the user's language, separate from full content; summarize purpose without implementation instructions"}
}

func memoryInputProperties() map[string]any {
	return map[string]any{"title": displayTitleSchema(), "description": displayDescriptionSchema(), "content": map[string]any{"type": "string", "minLength": 1, "description": "Faithful factual memory body. Preserve names, facts, user wording and language. This is data, not an execution prompt. Any instructions authored by you must be English."}}
}

// Language is a semantic generation requirement, not an ASCII restriction:
// English instructions may contain Unicode names, quotes and localized outputs.
const authoredInstructionGuidance = "Author instruction prompts in English; preserve quoted data.\n"

func memoryUpdateProperties() map[string]any {
	properties := memoryInputProperties()
	properties["id"] = map[string]any{"type": "string"}
	return properties
}
