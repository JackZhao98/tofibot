package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeSandboxLinks(t *testing.T) {
	id := "cfc0a960-1111-4222-8333-444455556666"
	other := "0d0d0d0d-1111-4222-8333-444455556666"
	files := []Attachment{{ID: id, Name: "销售 报表.xlsx"}, {ID: other, Name: "dup.csv"}, {ID: "x2", Name: "dup.csv"}}
	for name, c := range map[string]struct{ in, want string }{
		"api form":         {"[下载](sandbox:/api/attachments/" + id + ")", "[下载](/api/attachments/" + id + ")"},
		"upper scheme":     {"[x](SANDBOX:/api/attachments/" + id + ")", "[x](/api/attachments/" + id + ")"},
		"bare in text":     {"see sandbox:/api/attachments/" + id + " now", "see /api/attachments/" + id + " now"},
		"path by name":     {"[d](sandbox:/mnt/data/%E9%94%80%E5%94%AE%20%E6%8A%A5%E8%A1%A8.xlsx)", "[d](/api/attachments/" + id + ")"},
		"path unmatched":   {"[d](sandbox:/mnt/data/other.xlsx)", "[d](sandbox:/mnt/data/other.xlsx)"},
		"path ambiguous":   {"[d](sandbox:/mnt/data/dup.csv)", "[d](sandbox:/mnt/data/dup.csv)"},
		"https untouched":  {"[d](https://example.invalid/sandbox:/api/attachments/" + id + ")", "[d](https://example.invalid/sandbox:/api/attachments/" + id + ")"},
		"other scheme":     {"[d](file:///mnt/data/销售 报表.xlsx) [m](mailto:a@b.invalid)", "[d](file:///mnt/data/销售 报表.xlsx) [m](mailto:a@b.invalid)"},
		"word boundary":    {"mysandbox:/api/attachments/" + id, "mysandbox:/api/attachments/" + id},
		"no scheme at all": {"plain /api/attachments/" + id, "plain /api/attachments/" + id},
	} {
		if got := normalizeSandboxLinks(c.in, files); got != c.want {
			t.Fatalf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// What is stored is what the model wrote; what is served is normalized, in the
// list response and in the event copy alike.
func TestServedMessageNormalizesSandboxLinksButStorageKeepsRaw(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("linker", "", "model")
	raw := "[下载](sandbox:/api/attachments/abc)"
	m, err := s.AddAssistant(b.DMConversationID, b.ID, "", raw)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = s.db.QueryRow(`SELECT content FROM messages WHERE id=?`, m.ID).Scan(&stored); err != nil || stored != raw {
		t.Fatalf("stored=%q %v", stored, err)
	}
	served, _ := json.Marshal(m)
	if !strings.Contains(string(served), `[下载](/api/attachments/abc)`) || strings.Contains(string(served), "sandbox:") {
		t.Fatalf("served=%s", served)
	}
	user := Message{Role: "user", Content: raw}
	if b, _ := json.Marshal(user); !strings.Contains(string(b), "sandbox:") {
		t.Fatalf("a person's own text must be served as written: %s", b)
	}
}
