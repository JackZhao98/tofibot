package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type pagedRow struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

func TestPagedListWalksEveryRowOnce(t *testing.T) {
	rows := make([]pagedRow, 200)
	for i := range rows {
		rows[i] = pagedRow{ID: i, Text: strings.Repeat("r", 500)}
	}
	seen := 0
	offset := 0
	for pages := 0; pages < 100; pages++ {
		raw := pagedList("rows", rows, offset)
		var got struct {
			Rows       []pagedRow `json:"rows"`
			Total      int        `json:"total"`
			Offset     int        `json:"offset"`
			NextOffset *int       `json:"next_offset"`
			Note       string     `json:"note"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if got.Total != 200 || got.Offset != offset || len(got.Rows) == 0 || len(raw) > 24_000 {
			t.Fatalf("page at %d: total=%d rows=%d bytes=%d", offset, got.Total, len(got.Rows), len(raw))
		}
		for i, r := range got.Rows {
			if r.ID != offset+i {
				t.Fatalf("row %d out of order at offset %d", r.ID, offset)
			}
		}
		seen += len(got.Rows)
		if got.NextOffset == nil {
			if got.Note != "" {
				t.Fatal("last page carries a note")
			}
			break
		}
		if *got.NextOffset != offset+len(got.Rows) || !strings.Contains(got.Note, fmt.Sprintf("offset=%d", *got.NextOffset)) {
			t.Fatalf("next_offset=%d note=%q", *got.NextOffset, got.Note)
		}
		offset = *got.NextOffset
	}
	if seen != 200 {
		t.Fatalf("saw %d rows", seen)
	}
}

func TestPagedListEmptyAndPastEnd(t *testing.T) {
	if got := pagedList("rows", []pagedRow{}, 0); !strings.Contains(got, `"rows":[]`) || !strings.Contains(got, `"total":0`) {
		t.Fatal(got)
	}
	if got := pagedList("rows", []pagedRow{{ID: 1}}, 9); !strings.Contains(got, `"rows":[]`) || strings.Contains(got, "next_offset") {
		t.Fatal(got)
	}
}
