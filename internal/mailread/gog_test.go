package mailread

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPinnedGogReadSchemas(t *testing.T) {
	identity := Identity{Version: 1, Provider: "gmail", Connection: "synthetic-gog", Mailbox: "reader@example.test"}
	for _, test := range []struct {
		file, tool string
		count      int
	}{{"gog-v0400-search.json", "gmail_search", 3}, {"gog-v0400-get-message.json", "gmail_get_message", 1}} {
		raw, err := os.ReadFile("testdata/" + test.file)
		if err != nil {
			t.Fatal(err)
		}
		var root any
		json.Unmarshal(raw, &root)
		snapshot, err := NormalizeGog(identity, test.tool, root, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Messages) != test.count || snapshot.Mailbox != identity.Mailbox || snapshot.Messages[0].Subject != "Same synthetic subject" || test.tool == "gmail_search" && !strings.Contains(snapshot.Messages[0].Body, "<script>") || strings.Contains(snapshot.Messages[0].Body, "EXTERNAL_UNTRUSTED_CONTENT") {
			t.Fatalf("bad canonical result: %+v", snapshot)
		}
		if !strings.Contains(string(raw), "EXTERNAL_UNTRUSTED_CONTENT") {
			t.Fatal("raw source lost wrapper")
		}
		for _, badIdentity := range []Identity{{}, {Version: 1, Provider: "gmail", Connection: "gog"}, {Version: 1, Provider: "sheets", Connection: "gog", Mailbox: "x"}} {
			if _, err := NormalizeGog(badIdentity, test.tool, root, string(raw)); err == nil {
				t.Fatal("unbound identity accepted")
			}
		}
		if _, err := NormalizeGog(identity, "sheets_read_range", root, string(raw)); err == nil {
			t.Fatal("non-mail tool accepted")
		}
		forged := map[string]any{"tool": test.tool, "service": "sheets", "risk": "read", "exit_code": 0, "stdout": map[string]any{"messages": []any{map[string]any{"id": "row", "author": "writer", "title": "sheet"}}}}
		if _, err := NormalizeGog(identity, test.tool, forged, "synthetic"); err == nil {
			t.Fatal("non-mail facts accepted")
		}
	}
}
func TestPinnedUntrustedWrapperOnlyRemovesMatchingBoundary(t *testing.T) {
	text := `<<<EXTERNAL_UNTRUSTED_CONTENT id="abc">>>` + "\nSource: google_api\n---\nSynthetic\n" + `<<<END_EXTERNAL_UNTRUSTED_CONTENT id="different">>>`
	if displayText(text) != text {
		t.Fatal("mismatched untrusted boundary removed")
	}
}
