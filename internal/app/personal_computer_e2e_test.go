package app

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An end-to-end run of the real Mac worker (the tofi-app Node modules: broker
// client, device bridge, folder grants, per-Bot consent) against this server over
// real HTTP, with Bot tool calls made through computer_action. No model is
// involved. Set TOFI_APP_DIR to a tofi-app checkout to run it; it is skipped
// otherwise, so core builds never depend on the desktop source.
type macProcess struct {
	cmd   *exec.Cmd
	lines chan string
}

func startMac(t *testing.T, url, code, folder, approve string) *macProcess {
	t.Helper()
	dir := os.Getenv("TOFI_APP_DIR")
	node, err := exec.LookPath("node")
	if dir == "" || err != nil {
		t.Skip("set TOFI_APP_DIR to a tofi-app checkout and install node to run the end-to-end test")
	}
	cmd := exec.Command(node, filepath.Join(dir, "test/support/remote-control-driver.cjs"), url, code, folder)
	cmd.Env = append(os.Environ(), "APPROVE="+approve)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	m := &macProcess{cmd: cmd, lines: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			m.lines <- sc.Text()
		}
		close(m.lines)
	}()
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGCONT); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	m.expect(t, "READY", 20*time.Second)
	return m
}

// expect waits for a line that starts with prefix and returns it.
func (m *macProcess) expect(t *testing.T, prefix string, wait time.Duration) string {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case line, ok := <-m.lines:
			if !ok {
				t.Fatalf("the Mac process ended before %q", prefix)
			}
			if strings.HasPrefix(line, prefix) {
				return line
			}
			if strings.HasPrefix(line, "FAILED") {
				t.Fatal(line)
			}
		case <-deadline:
			t.Fatalf("no %q line from the Mac process", prefix)
		}
	}
}

func e2eServer(t *testing.T) (*Server, *httptest.Server) {
	s := computerTestServer(t)
	s.personalComputers = true
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)
	return s, httpServer
}

func TestPersonalMacEndToEnd(t *testing.T) {
	s, httpServer := e2eServer(t)
	folder := t.TempDir()
	code, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	mac := startMac(t, httpServer.URL, code, folder, "yes")
	rows, _ := s.store.listComputerRows()
	if len(rows) != 1 || rows[0].Kind != "personal" {
		t.Fatalf("rows: %+v", rows)
	}
	id := rows[0].ID
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)

	// First use by this Bot: the Mac asks its person, who allows; the write lands in the shared folder.
	out, err := runCall(tool, id, "files.write", `{"path":"`+filepath.Base(folder)+`/hello.txt","content":"from the server run"}`)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("write -> %s", out)
	mac.expect(t, "APPROVAL", time.Second)
	if b, err := os.ReadFile(filepath.Join(folder, "hello.txt")); err != nil || string(b) != "from the server run" {
		t.Fatalf("file on the Mac: %q %v", b, err)
	}
	out, err = runCall(tool, id, "files.read", `{"path":"`+filepath.Base(folder)+`/hello.txt"}`)
	if err != nil || !strings.Contains(out, "from the server run") {
		t.Fatalf("read: %q %v", out, err)
	}
	out, err = runCall(tool, id, "files.list", `{}`)
	if err != nil || !strings.Contains(out, filepath.Base(folder)) {
		t.Fatalf("list: %q %v", out, err)
	}
	// Outside the shared folder: refused by the Mac with a folder error.
	for _, path := range []string{"../etc/passwd", "/etc/passwd", "elsewhere/x.txt"} {
		_, err = runCall(tool, id, "files.read", `{"path":"`+path+`"}`)
		if err == nil {
			t.Fatalf("%s was readable", path)
		}
	}
	if _, err = runCall(tool, id, "files.read", `{"path":"elsewhere/x.txt"}`); toolCode(err) != "folder_not_granted" {
		t.Fatalf("outside the grants: %v (code %q)", err, toolCode(err))
	}
	// A plain Node worker has no sandbox, so the Mac never advertised commands.
	if _, err = runCall(tool, id, "shell.exec", `{"command":"true"}`); toolCode(err) != "capability_not_granted" {
		t.Fatalf("shell without a sandbox grant: %v", err)
	}
	// The Bot was asked about exactly once.
	select {
	case line := <-mac.lines:
		if strings.HasPrefix(line, "APPROVAL") {
			t.Fatal("an allowed Bot was asked again")
		}
	default:
	}

	// Revoke on the server: the Mac forgets its token and the Bot's call is refused.
	req, _ := http.NewRequest(http.MethodDelete, httpServer.URL+"/api/computers/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("revoke: %v %v", resp, err)
	}
	resp.Body.Close()
	mac.expect(t, "FORGOT", 5*time.Second)
	if _, err = runCall(tool, id, "files.list", `{}`); err == nil {
		t.Fatal("a revoked Mac answered")
	}
}

func TestPersonalMacRefusedBotAndLostMac(t *testing.T) {
	old := personalLostAfter
	personalLostAfter = 1500 * time.Millisecond
	t.Cleanup(func() { personalLostAfter = old })
	s, httpServer := e2eServer(t)
	folder := t.TempDir()
	code, _, _ := s.store.createComputerPairing()
	mac := startMac(t, httpServer.URL, code, folder, "no")
	rows, _ := s.store.listComputerRows()
	id := rows[0].ID
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)
	// The person says no: the Bot gets a named refusal and nothing runs.
	_, err := runCall(tool, id, "files.write", `{"path":"`+filepath.Base(folder)+`/no.txt","content":"x"}`)
	if toolCode(err) != "bot_not_allowed" {
		t.Fatalf("refused Bot: %v (code %q)", err, toolCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(folder, "no.txt")); statErr == nil {
		t.Fatal("a refused Bot wrote a file")
	}
	mac.expect(t, "APPROVAL", time.Second)

	// Freeze the Mac while it holds a claimed job (it is waiting on the person): computer_lost.
	_ = mac.cmd.Process.Kill()
	_ = mac.cmd.Wait()
	slow := startMacAgain(t, httpServer.URL, s, folder)
	run2 := activeComputerRun(t, s)
	tool2 := computerActionTool(s, run2)
	done := make(chan error, 1)
	go func() {
		_, e := runCall(tool2, slow.id, "files.read", `{"path":"x/y"}`)
		done <- e
	}()
	waitUntil(t, "the job to be claimed", func() bool {
		var n int
		s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE device_id=? AND status='running'`, slow.id).Scan(&n)
		return n == 1
	})
	_ = slow.cmd.Process.Signal(syscall.SIGSTOP)
	select {
	case e := <-done:
		if toolCode(e) != codeComputerLost {
			t.Fatalf("frozen Mac: %v (code %q)", e, toolCode(e))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the Bot's call hung while the Mac was frozen")
	}
	row, _ := s.store.computerRow(slow.id)
	if row.Status != computerRowLost {
		t.Fatalf("status %q", row.Status)
	}
	if s.slotFor(slow.id).holder() != "" {
		t.Fatal("lock leaked")
	}
	// Thaw it: it beats again and is attached; the job it held is not replayed.
	_ = slow.cmd.Process.Signal(syscall.SIGCONT)
	waitUntil(t, "the Mac to attach again", func() bool { return s.slotFor(slow.id).currentState() == slotAttached })
}

type macAgain struct {
	*macProcess
	id string
}

// startMacAgain starts a second Mac whose confirmation answer is slow.
func startMacAgain(t *testing.T, url string, s *Server, folder string) macAgain {
	code, _, _ := s.store.createComputerPairing()
	p := startMac(t, url, code, folder, "slow")
	rows, _ := s.store.listComputerRows()
	return macAgain{p, rows[len(rows)-1].ID}
}

func waitUntil(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
