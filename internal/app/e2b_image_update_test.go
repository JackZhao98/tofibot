package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// fakeImageGuest is one E2B computer as moveHome sees it: a file map, and answers to the few shell commands the
// move sends. Upgrade empties the files (a new sandbox from the current image).
type fakeImageGuest struct {
	computer.Provider
	files     map[string][]byte
	upgrades  int
	failWrite bool
}

func (g *fakeImageGuest) Upgrade(context.Context) error {
	g.upgrades++
	g.files = map[string][]byte{}
	return nil
}

func (g *fakeImageGuest) call(_ context.Context, name string, args any) (json.RawMessage, error) {
	a := args.(map[string]any)
	switch name {
	case "shell.exec":
		cmd := a["command"].(string)
		out := ""
		switch {
		case strings.HasPrefix(cmd, "du "):
			out = "1000"
		case strings.HasPrefix(cmd, "df "):
			out = "100000000000"
		case strings.Contains(cmd, "tar --exclude"): // pack: the archive is the home file itself
			dir := homeMoveDir(strings.TrimPrefix(strings.Fields(cmd)[2], "/workspace/home/uploads/.tofi-move-"))
			g.files[dir+"/part.aa"] = g.files["/workspace/home/notes.txt"]
			g.files[dir+"/sums"] = []byte("x  part.aa\n")
			g.files[dir+"/state"] = []byte("done")
		case strings.HasPrefix(cmd, "cat ") && strings.HasSuffix(cmd, "/sums"):
			out = string(g.files[strings.TrimPrefix(cmd, "cat ")])
		case strings.HasPrefix(cmd, "cat "):
			out = string(g.files[strings.Fields(cmd)[1]])
		case strings.Contains(cmd, "tar --no-same-owner"): // unpack
			dir := strings.Fields(strings.SplitN(cmd, "cd ", 2)[1])[0]
			g.files["/workspace/home/notes.txt"] = g.files[dir+"/part.aa"]
			g.files[dir+"/unpacked"] = []byte("done")
		}
		res, _ := json.Marshal(guestShellResult{Stdout: out})
		return res, nil
	case "files.export_chunk":
		data := g.files[a["path"].(string)]
		off := a["offset"].(int64)
		end := off + int64(a["limit"].(int))
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		b, _ := json.Marshal(guestWireChunk{Data: b64(data[off:end]), Next: end, Size: int64(len(data)), EOF: end == int64(len(data)), Version: "v"})
		return b, nil
	case "files.import_chunk":
		if g.failWrite {
			return nil, errors.New("guest unreachable")
		}
		path := a["path"].(string)
		if a["offset"].(int64) == 0 {
			g.files[path] = nil
		}
		g.files[path] = append(g.files[path], unb64(a["data_base64"].(string))...)
		return json.RawMessage(`{"status":"ok"}`), nil
	}
	return nil, errors.New("unexpected " + name)
}

func TestAnImageUpdateKeepsTheHomeFolder(t *testing.T) {
	s := &Server{homeMoveRoot: t.TempDir()}
	home := bytes.Repeat([]byte("my notes "), 200000) // more than three pages
	g := &fakeImageGuest{files: map[string][]byte{"/workspace/home/notes.txt": home}}
	if err := s.updateE2BImage(context.Background(), g, g.call, "m1"); err != nil {
		t.Fatal(err)
	}
	if g.upgrades != 1 {
		t.Fatalf("upgrades = %d", g.upgrades)
	}
	if !bytes.Equal(g.files["/workspace/home/notes.txt"], home) {
		t.Fatal("the home folder did not come back after the rebuild")
	}
	if s.parkedHomeMove() != "" {
		t.Fatal("the parked copy must go once the home folder is back")
	}
}

func TestAFailedRestoreKeepsTheParkedHomeForTheNextTry(t *testing.T) {
	s := &Server{homeMoveRoot: t.TempDir()}
	home := []byte("keep me")
	g := &fakeImageGuest{files: map[string][]byte{"/workspace/home/notes.txt": home}}
	g.failWrite = true
	if err := s.updateE2BImage(context.Background(), g, g.call, "m2"); err == nil {
		t.Fatal("an unreachable new computer must fail the update")
	}
	parked := s.parkedHomeMove()
	if parked != "m2" {
		t.Fatalf("parked = %q, want the move kept on the server", parked)
	}
	if b, _ := os.ReadFile(filepath.Join(s.homeMoveParkRoot(), parked, "part.aa")); !bytes.Equal(b, home) {
		t.Fatal("the parked part lost the home folder")
	}
	g.failWrite = false
	if err := restoreParkedHome(context.Background(), g.call, filepath.Join(s.homeMoveParkRoot(), parked), parked); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g.files["/workspace/home/notes.txt"], home) || s.parkedHomeMove() != "" {
		t.Fatal("the retry must put the home folder back and drop the parked copy")
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func unb64(s string) []byte { b, _ := base64.StdEncoding.DecodeString(s); return b }

func TestAHomeMoveReportsItsSteps(t *testing.T) {
	home := bytes.Repeat([]byte("n"), 1200000)
	from := &fakeImageGuest{files: map[string][]byte{"/workspace/home/notes.txt": home}}
	to := &fakeImageGuest{files: map[string][]byte{}}
	var steps []string
	var lastDone int64
	ctx := withHomeMoveProgress(context.Background(), func(step string, done, total int64) {
		if len(steps) == 0 || steps[len(steps)-1] != step {
			steps = append(steps, step)
		}
		if step == "copy" {
			lastDone = done
		}
	})
	if err := moveHome(ctx, from.call, to.call, "m3"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "pack,copy,unpack" {
		t.Fatalf("steps = %v", steps)
	}
	if lastDone < int64(len(home)) {
		t.Fatalf("copy reported %d of %d bytes", lastDone, len(home))
	}
	var m homeMoveState
	m.begin("e2b")
	m.progress("copy", 5, 10)
	if v := m.view(); v.Step != "copy" || v.Done != 5 || v.Total != 10 {
		t.Fatalf("view = %+v", v)
	}
}
