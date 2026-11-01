package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/resultcut"
)

// A read_attachment page of characters that JSON escapes arrives whole: the
// encoded result fits the model budget and the pages rebuild the file.
func TestReadAttachmentPagesFitTheModelBudgetWhenEncoded(t *testing.T) {
	s, _, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("reader", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	body := strings.Repeat("<div class=\"a\">&amp;</div>\n", 3000) + strings.Repeat("字", 20000)
	_, run, files := sendWithFiles(t, s, conv.ID, bot.ID, "files", "pb-1", map[string][]byte{"page.txt": []byte(body)}, []string{"page.txt"})
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	var got strings.Builder
	var next float64
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("paging did not finish")
		}
		out, err := s.attachmentTools(conv, run)[0].Execute(context.Background(), json.RawMessage(`{"attachment_id":"`+files[0].ID+`","offset":`+jsonNumber(next)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > resultcut.ModelBudget {
			t.Fatalf("page %d encodes to %d bytes", pages, len(out))
		}
		var page map[string]any
		if json.Unmarshal([]byte(out), &page) != nil {
			t.Fatal("page is not JSON")
		}
		content := page["content"].(string)
		if !utf8.ValidString(content) {
			t.Fatal("page split a character")
		}
		got.WriteString(content)
		n, more := page["next_offset"].(float64)
		if !more {
			break
		}
		if n <= next {
			t.Fatalf("next_offset %v does not advance", n)
		}
		next = n
	}
	if got.String() != body {
		t.Fatal("pages do not rebuild the attachment")
	}
}

func TestGoogleTextPageStaysUnderTheModelBudget(t *testing.T) {
	data := []byte(strings.Repeat("日本語, ", 20000))
	var got strings.Builder
	offset := 0
	for pages := 0; pages < 100; pages++ {
		page := googleTextPage("doc.txt", data, offset, 0)
		if len(page) > resultcut.ModelBudget {
			t.Fatalf("page is %d bytes", len(page))
		}
		got.WriteString(page[strings.Index(page, "\n\n")+2:])
		i := strings.Index(page, "call again with offset ")
		if i < 0 {
			break
		}
		if _, err := fmt.Sscanf(page[i+len("call again with offset "):], "%d", &offset); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != string(data) {
		t.Fatal("pages do not rebuild the file")
	}
}
