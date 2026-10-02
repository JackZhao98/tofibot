//go:build linux

package guest

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func readTerminalUntil(t *testing.T, s *Service, bot, id string, want func([]byte, map[string]any) bool) ([]byte, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var all []byte
	var result map[string]any
	var cursor uint64
	for time.Now().Before(deadline) {
		var err error
		result, err = s.terminalRead(bot, terminalReadArgs{TerminalID: id, Cursor: cursor, MaxBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		chunk, err := base64.StdEncoding.DecodeString(result["data_base64"].(string))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, chunk...)
		cursor = result["next_cursor"].(uint64)
		if want(all, result) {
			return all, result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out reading terminal; output=%q result=%v", all, result)
	return nil, nil
}

func TestTerminalUsesRealPTYAndOutlivesOpenContext(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := s.terminalOpen(ctx, testBot, "run", terminalOpenArgs{Command: `test -t 0 && read value && printf 'got:%s' "$value"`, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	id := r["id"].(string)
	if _, err := s.terminalWrite(testBot, "run", ActionSourceModel, terminalWriteArgs{TerminalID: id, Data: "alive\n"}); err != nil {
		t.Fatal(err)
	}
	out, result := readTerminalUntil(t, s, testBot, id, func(b []byte, m map[string]any) bool {
		return strings.Contains(string(b), "got:alive") && m["exited"].(bool)
	})
	if !strings.Contains(string(out), "got:alive") || result["exit_code"].(int) != 0 {
		t.Fatalf("output=%q result=%v", out, result)
	}
}

func TestTerminalResizeChangesKernelWindow(t *testing.T) {
	s := newTestService(t)
	r, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: `trap 'stty size' WINCH; printf ready; while :; do sleep 1; done`, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	id := r["id"].(string)
	defer s.terminalClose(testBot, terminalIDArgs{TerminalID: id})
	readTerminalUntil(t, s, testBot, id, func(b []byte, _ map[string]any) bool { return strings.Contains(string(b), "ready") })
	if _, err := s.terminalResize(testBot, terminalResizeArgs{TerminalID: id, Cols: 123, Rows: 45}); err != nil {
		t.Fatal(err)
	}
	out, _ := readTerminalUntil(t, s, testBot, id, func(b []byte, _ map[string]any) bool { return strings.Contains(string(b), "45 123") })
	if !strings.Contains(string(out), "45 123") {
		t.Fatalf("resize output=%q", out)
	}
}

func TestTerminalBinaryOutputRingAndGap(t *testing.T) {
	s := newTestService(t)
	command := fmt.Sprintf(`head -c %d /dev/zero; printf '\377'`, terminalRingBytes+32)
	r, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	id := r["id"].(string)
	_, result := readTerminalUntil(t, s, testBot, id, func(_ []byte, m map[string]any) bool { return m["exited"].(bool) })
	result, err = s.terminalRead(testBot, terminalReadArgs{TerminalID: id, Cursor: 0, MaxBytes: MaxOutput})
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(result["data_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !result["gap"].(bool) || !result["closed"].(bool) || !result["drained"].(bool) || len(data) != terminalRingBytes || data[len(data)-1] != 0xff {
		t.Fatalf("gap=%v len=%d last=%x", result["gap"], len(data), data[len(data)-1])
	}
	page, err := s.terminalRead(testBot, terminalReadArgs{TerminalID: id, Cursor: result["cursor"].(uint64), MaxBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if !page["closed"].(bool) || page["drained"].(bool) {
		t.Fatalf("partial closed page must not be drained: %v", page)
	}
}

func TestTerminalCloseKillsProcessGroupAndScopesBot(t *testing.T) {
	s := newTestService(t)
	r, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: `sleep 30 & echo child:$!; wait`})
	if err != nil {
		t.Fatal(err)
	}
	id := r["id"].(string)
	out, _ := readTerminalUntil(t, s, testBot, id, func(b []byte, _ map[string]any) bool { return strings.Contains(string(b), "child:") })
	line := strings.Split(strings.Split(string(out), "child:")[1], "\r")[0]
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse child pid from %q: %v", out, err)
	}
	if _, err := s.terminalRead(testBotB, terminalReadArgs{TerminalID: id}); err == nil {
		t.Fatal("other Bot read terminal")
	}
	if _, err := s.terminalClose(testBotB, terminalIDArgs{TerminalID: id}); err == nil {
		t.Fatal("other Bot closed terminal")
	}
	if _, err := s.terminalClose(testBot, terminalIDArgs{TerminalID: id}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		fields := strings.Fields(string(state))
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH || (len(fields) > 2 && fields[2] == "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	t.Fatalf("child survived close: %s", state)
}

func TestTerminalPerBotLimit(t *testing.T) {
	s := newTestService(t)
	for i := 0; i < terminalMaxPerBot; i++ {
		if _, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: "sleep 30"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: "sleep 30"}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("limit err=%v", err)
	}
}

func TestTerminalExitIsNotHeldByInheritedTTY(t *testing.T) {
	s := newTestService(t)
	r, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: `setsid sh -c 'sleep 30' </dev/null >/dev/tty 2>&1 & disown; exit 7`})
	if err != nil {
		t.Fatal(err)
	}
	_, result := readTerminalUntil(t, s, testBot, r["id"].(string), func(_ []byte, m map[string]any) bool { return m["exited"].(bool) })
	if result["exit_code"].(int) != 7 {
		t.Fatalf("exit result=%v", result)
	}
}

func TestTerminalCloseInterruptsBlockedWrite(t *testing.T) {
	s := newTestService(t)
	r, err := s.terminalOpen(context.Background(), testBot, "run", terminalOpenArgs{Command: `sleep 30`})
	if err != nil {
		t.Fatal(err)
	}
	id := r["id"].(string)
	done := make(chan struct{})
	go func() {
		_, _ = s.terminalWrite(testBot, "run", ActionSourceModel, terminalWriteArgs{TerminalID: id, DataBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'x'}, MaxActionBody))})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := s.terminalClose(testBot, terminalIDArgs{TerminalID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal write remained blocked after close")
	}
}

func TestTerminalCancelRunIsBotScopedAndPreservesHumanTakeover(t *testing.T) {
	s := newTestService(t)
	open := func(bot, run string) string {
		r, err := s.terminalOpen(context.Background(), bot, run, terminalOpenArgs{Command: "sleep 30"})
		if err != nil {
			t.Fatal(err)
		}
		return r["id"].(string)
	}
	target := open(testBot, "run-a")
	otherRun := open(testBot, "run-b")
	otherBot := open(testBotB, "run-a")
	human := open(testBot, "run-a")
	if _, err := s.terminalWrite(testBot, "human-control:x", ActionSourceHuman, terminalWriteArgs{TerminalID: human, Data: "x"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.action(context.Background(), ActionRequest{BotID: testBot, RunID: "run-a", Source: ActionSourceModel, Action: "terminal.cancel_run", Args: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["cancelled"].(int) != 1 {
		t.Fatalf("cancel result=%v", result)
	}
	assertClosed := func(bot, id string, want bool) {
		got, err := s.terminalRead(bot, terminalReadArgs{TerminalID: id})
		if err != nil {
			t.Fatal(err)
		}
		if got["closed"].(bool) != want {
			t.Fatalf("terminal %s closed=%v want=%v", id, got["closed"], want)
		}
	}
	assertClosed(testBot, target, true)
	assertClosed(testBot, otherRun, false)
	assertClosed(testBotB, otherBot, false)
	assertClosed(testBot, human, false)
}
