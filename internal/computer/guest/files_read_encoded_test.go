package guest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/resultcut"
)

// Every page fits one model result once encoded, whatever the characters.
func TestFilesReadPagesFitTheModelBudgetWhenEncoded(t *testing.T) {
	cases := map[string]string{
		"angle": strings.Repeat(strings.Repeat("<a href=\"x\">&</a>", 12)+"\n", 600),
		"cjk":   strings.Repeat(strings.Repeat("日本語のテキスト", 20)+"\n", 600),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestService(t)
			dir, err := s.botDir(testBot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0660); err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			offset, pages := 1, 0
			for offset > 0 {
				if pages++; pages > 200 {
					t.Fatal("paging did not finish")
				}
				res, err := s.readFile(context.Background(), testBot, fileArgs{Path: "f.txt", Offset: offset})
				if err != nil {
					t.Fatal(err)
				}
				enc, _ := json.Marshal(res)
				if len(enc) > resultcut.ModelBudget {
					t.Fatalf("page %d encodes to %d bytes", pages, len(enc))
				}
				content := res["content"].(string)
				if !utf8.ValidString(content) {
					t.Fatal("page split a character")
				}
				if got.Len() > 0 {
					got.WriteByte('\n')
				}
				got.WriteString(content)
				next, more := res["next_offset"].(int)
				if !more {
					break
				}
				offset = next
			}
			if got.String() != strings.TrimSuffix(body, "\n") {
				t.Fatalf("pages do not rebuild the file (%d pages)", pages)
			}
		})
	}
}

func TestFilesReadCutsOneOverlongLineOnACharacterEdge(t *testing.T) {
	s := newTestService(t)
	dir, _ := s.botDir(testBot)
	long := strings.Repeat("字", 30000)
	if err := os.WriteFile(filepath.Join(dir, "line.txt"), []byte(long+"\nnext\n"), 0660); err != nil {
		t.Fatal(err)
	}
	res, err := s.readFile(context.Background(), testBot, fileArgs{Path: "line.txt"})
	if err != nil {
		t.Fatal(err)
	}
	content := res["content"].(string)
	enc, _ := json.Marshal(res)
	if !utf8.ValidString(content) || len(enc) > resultcut.ModelBudget || res["line_bytes_omitted"] == nil || res["next_offset"] != 2 {
		t.Fatalf("overlong line: len=%d enc=%d result=%v", len(content), len(enc), res["next_offset"])
	}
	next, err := s.readFile(context.Background(), testBot, fileArgs{Path: "line.txt", Offset: 2})
	if err != nil || next["content"] != "next" {
		t.Fatalf("next page = %v %v", next["content"], err)
	}
}
