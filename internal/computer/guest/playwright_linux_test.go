//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeMCP is a stdio MCP server that echoes the endpoint it was started with.
const fakeMCP = `
const readline = require('node:readline');
const rl = readline.createInterface({input: process.stdin});
const arg = process.argv.indexOf('--cdp-endpoint');
const endpoint = arg > 0 ? process.argv[arg + 1] : '';
rl.on('line', line => {
  const m = JSON.parse(line);
  if (m.id === undefined) return;
  if (m.method === 'initialize') return console.log(JSON.stringify({jsonrpc: '2.0', id: m.id, result: {protocolVersion: '2025-06-18', capabilities: {tools: {}}, serverInfo: {name: 'fake', version: '1'}}}));
  if (m.method === 'tools/call') {
    const t = m.params.name;
    if (t === 'browser_crash') process.exit(3);
    const text = t + ' ' + JSON.stringify(m.params.arguments) + ' at ' + endpoint + ' in ' + process.cwd();
    return console.log(JSON.stringify({jsonrpc: '2.0', id: m.id, result: {content: [{type: 'text', text}], isError: t === 'browser_fail'}}));
  }
  console.log(JSON.stringify({jsonrpc: '2.0', id: m.id, error: {message: 'no ' + m.method}}));
});
`

func fakePlaywrightDir(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "node_modules", "@playwright", "mcp", "cli.js")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte(fakeMCP), 0o644); err != nil {
		t.Fatal(err)
	}
	old := playwrightMCPDir
	playwrightMCPDir = dir
	t.Cleanup(func() { playwrightMCPDir = old })
}

func TestPlaywrightSessionRelaysCallsAndRestartsOnANewChrome(t *testing.T) {
	fakePlaywrightDir(t)
	root := t.TempDir()
	bot := filepath.Join(root, "bots", "b")
	if err := os.MkdirAll(bot, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Service{root: root}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer s.stopPlaywright()

	first, err := s.playwrightFor(ctx, bot, 41001)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := first.call(ctx, "tools/call", map[string]any{"name": "browser_snapshot", "arguments": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := playwrightResult(root, "browser_snapshot", raw)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := res["text"].(string)
	if !strings.Contains(text, "http://127.0.0.1:41001") || !strings.Contains(text, root) {
		t.Fatalf("server was not attached to the endpoint or the workspace root: %q", text)
	}
	again, err := s.playwrightFor(ctx, bot, 41001)
	if err != nil || again != first {
		t.Fatalf("the session must be reused for the same Chrome: %v", err)
	}
	// A new Chrome has a new DevTools port: the old server is replaced.
	next, err := s.playwrightFor(ctx, bot, 41002)
	if err != nil || next == first {
		t.Fatalf("a new port must start a new server: %v", err)
	}
	select {
	case <-first.dead:
	case <-time.After(10 * time.Second):
		t.Fatal("the old server kept running")
	}
	raw, _ = next.call(ctx, "tools/call", map[string]any{"name": "browser_fail", "arguments": map[string]any{}})
	res, _ = playwrightResult(root, "browser_fail", raw)
	if res["is_error"] != true {
		t.Fatalf("a tool error must stay a result: %v", res)
	}
	// A server that dies mid-call is an error, and the next call starts a new one.
	_, err = next.call(ctx, "tools/call", map[string]any{"name": "browser_crash", "arguments": map[string]any{}})
	if err == nil {
		t.Fatal("a dead server answered")
	}
	s.dropPlaywright(next)
	third, err := s.playwrightFor(ctx, bot, 41002)
	if err != nil || third == next {
		t.Fatalf("no new server after a crash: %v", err)
	}
	if _, err := json.Marshal(third.port); err != nil {
		t.Fatal(err)
	}
}

func TestPlaywrightMissingPackageIsNamed(t *testing.T) {
	old := playwrightMCPDir
	playwrightMCPDir = t.TempDir()
	defer func() { playwrightMCPDir = old }()
	s := &Service{root: t.TempDir(), desktops: map[string]*desktop{}}
	d := &desktop{remotePort: 1, readyClosed: true}
	s.sharedDesktop = d
	_, err := s.browserMCP(context.Background(), "11111111-1111-1111-1111-111111111111", call("browser_snapshot", `{}`))
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("want a clear missing-package error, got %v", err)
	}
}
