package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBoundedSearchJSONNamesWhatIsLeft(t *testing.T) {
	var msgs []Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, Message{ID: fmt.Sprintf("m%d", i), Seq: int64(i), Role: "user", Content: strings.Repeat("x", 3000)})
	}
	var got struct {
		Total      int               `json:"total"`
		Order      string            `json:"order"`
		NextOffset *int              `json:"next_offset"`
		Note       string            `json:"note"`
		Results    []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(boundedSearchJSON(msgs, 200, 100, true)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 200 || got.Order != "newest" || got.NextOffset == nil || *got.NextOffset != 100+len(got.Results) || len(got.Results) >= 50 || got.Note == "" {
		t.Fatalf("%+v", got)
	}
}

func TestSearchPageWalksNewestFirst(t *testing.T) {
	s, _, c := scheduleTestStore(t)
	defer s.Close()
	for i := 0; i < 5; i++ {
		if _, _, err := s.AddMessage(c.ID, "user", "", "", fmt.Sprintf("needle %d", i), fmt.Sprintf("k%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	first, total, err := s.SearchPage(c.ID, "needle", 2, 0, true)
	if err != nil || total != 5 || len(first) != 2 || first[0].Seq <= first[1].Seq {
		t.Fatalf("first=%+v total=%d err=%v", first, total, err)
	}
	rest, _, err := s.SearchPage(c.ID, "needle", 2, 4, true)
	if err != nil || len(rest) != 1 || rest[0].Content != "needle 0" {
		t.Fatalf("rest=%+v err=%v", rest, err)
	}
}
