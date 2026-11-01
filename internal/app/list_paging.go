package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

// listPageBytes is the most encoded item bytes one list tool call returns. It
// sits under the result size the model receives, so a page is not cut again.
const listPageBytes = 18_000

// listOffsetProp is the shared `offset` parameter of every paged list tool.
var listOffsetProp = map[string]any{"type": "integer", "minimum": 0, "description": "Items to skip; use next_offset from the previous page."}

// pagedList packs items from offset until the page byte budget is reached (at
// least one item per page) and returns the object the model reads: the items
// under key, total, offset and, when items remain, next_offset with a note.
func pagedList[T any](key string, items []T, offset int) string {
	total := len(items)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	page := make([]json.RawMessage, 0)
	used := 0
	for _, it := range items[offset:] {
		b, err := json.Marshal(it)
		if err != nil {
			continue
		}
		if used+len(b)+1 > listPageBytes && len(page) > 0 {
			break
		}
		page = append(page, b)
		used += len(b) + 1
	}
	out := map[string]any{key: page, "total": total, "offset": offset}
	if next := offset + len(page); next < total {
		out["next_offset"] = next
		out["note"] = fmt.Sprintf("%d of %d %s shown; call again with the same arguments and offset=%d for the rest.", len(page), total, strings.ReplaceAll(key, "_", " "), next)
	}
	b, _ := json.Marshal(out)
	return string(b)
}
