package guest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// files.list names the kind of every entry without following links, and
// files.export_chunk with no_symlinks refuses a link in any path component.
func TestListNamesEntryTypesAndExportCanRefuseSymlinks(t *testing.T) {
	s := newTestService(t)
	bot, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(bot, "out")
	if err = os.MkdirAll(filepath.Join(out, "real"), 0o770); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(out, "real", "a.txt"), []byte("a"), 0o660); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(out, "f.txt"), []byte("f"), 0o660); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("f.txt", filepath.Join(out, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("real", filepath.Join(out, "dirlink")); err != nil {
		t.Fatal(err)
	}
	status, resp := postAction(t, s.Handler(), testBot, "run-1", "files.list", map[string]any{"path": "out"})
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("list: %d %#v", status, resp)
	}
	var entries []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		MTimeNS int64  `json:"mtime_ns"`
	}
	raw, _ := json.Marshal(resp.Result)
	if err = json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"real": "dir", "f.txt": "file", "link.txt": "symlink", "dirlink": "symlink"}
	if len(entries) != len(want) {
		t.Fatalf("entries=%+v", entries)
	}
	for _, e := range entries {
		if want[e.Name] != e.Type {
			t.Errorf("%s: type %q want %q", e.Name, e.Type, want[e.Name])
		}
		if e.MTimeNS == 0 {
			t.Errorf("%s: no mtime", e.Name)
		}
	}
	export := func(p string, noLinks bool) (int, ActionResponse) {
		return postAction(t, s.Handler(), testBot, "run-1", "files.export_chunk", map[string]any{"path": p, "no_symlinks": noLinks})
	}
	for _, p := range []string{"out/link.txt", "out/dirlink/a.txt"} {
		if status, resp = export(p, true); status == http.StatusOK || resp.OK {
			t.Errorf("%s exported with no_symlinks", p)
		}
		if status, resp = export(p, false); status != http.StatusOK || !resp.OK {
			t.Errorf("%s no longer exports for publish_file: %d %#v", p, status, resp)
		}
	}
	for _, p := range []string{"out/f.txt", "out/real/a.txt"} {
		if status, resp = export(p, true); status != http.StatusOK || !resp.OK {
			t.Errorf("%s refused: %d %#v", p, status, resp)
		}
	}
}
