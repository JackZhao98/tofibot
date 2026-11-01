package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadSkillPagesToTheEnd(t *testing.T) {
	body := strings.Repeat("héllo wörld line\n", 5000) // multi-byte, more than one page
	tools := skillTools([]Skill{{Name: "big", Description: "d", Body: body}}, 0)
	var read func(context.Context, json.RawMessage) (string, error)
	for _, tl := range tools {
		if tl.Name == "read_skill" {
			read = tl.Execute
		}
	}
	var got strings.Builder
	offset := 0
	for i := 0; i < 20; i++ {
		out, err := read(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":"big","offset":%d}`, offset)))
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(out) || len(out) > 21_000 {
			t.Fatalf("page %d invalid or too large: %d", i, len(out))
		}
		text := out
		if offset > 0 {
			text = text[strings.Index(text, "]\n")+2:]
		}
		idx := strings.LastIndex(text, "\n[truncated: ")
		if idx < 0 {
			got.WriteString(text)
			break
		}
		var a, b, total, next int
		if _, err := fmt.Sscanf(text[idx:], "\n[truncated: bytes %d-%d of %d shown; call again with offset=%d", &a, &b, &total, &next); err != nil {
			t.Fatal(err, text[idx:])
		}
		got.WriteString(text[:idx])
		offset = next
	}
	if got.String() != body {
		t.Fatalf("paged text differs: %d vs %d", got.Len(), len(body))
	}
}

func TestPageTextPastEndSaysSo(t *testing.T) {
	if out := pageText("abc", 10, 5); !strings.Contains(out, "past the end") {
		t.Fatal(out)
	}
}
