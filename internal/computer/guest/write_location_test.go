package guest

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A write result states where the file now lives on the computer. The path
// argument is relative to the Bot's profile, which tells nobody where to look.
func TestWriteResultCarriesTheAbsoluteLocation(t *testing.T) {
	s := newTestService(t)
	status, out := postAction(t, s.Handler(), testBot, "run-1", "files.write", map[string]any{"path": "notes/one.txt", "content": "hello"})
	if status != 200 || !out.OK {
		t.Fatalf("write=%d %#v", status, out)
	}
	var res struct {
		Path     string `json:"path"`
		Location string `json:"location"`
	}
	if err := json.Unmarshal(mustJSON(t, out.Result), &res); err != nil {
		t.Fatal(err)
	}
	if res.Path != "notes/one.txt" {
		t.Fatalf("path argument must stay as given: %q", res.Path)
	}
	if !filepath.IsAbs(res.Location) || !strings.HasPrefix(res.Location, s.root) || !strings.HasSuffix(res.Location, filepath.Join("notes", "one.txt")) {
		t.Fatalf("location=%q root=%q", res.Location, s.root)
	}
}
