package mcprunner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func removeFixture(t *testing.T) (*Runner, Spec, string) {
	t.Helper()
	startFile := filepath.Join(t.TempDir(), "starts")
	spec := Spec{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=TestRunnerHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": startFile}}
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, spec, fixtureManifest(t, r, spec)
}

func manifestHas(t *testing.T, dir, id string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(data), `"`+id+`"`)
}

// A registered, connected, idle consumer (the Settings card's connection check
// or a run's session) must never keep a plugin from being removed.
func TestRemoveSucceedsWhileServerIsConnectedAndIdle(t *testing.T) {
	r, _, dir := removeFixture(t)
	srv := httptest.NewServer(r.Handler("secret"))
	defer srv.Close()
	before := runtime.NumGoroutine()
	ctx := context.Background()
	cli, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp/fixture", HTTPClient: &http.Client{Transport: bearerTransport{token: "secret"}}}, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"message": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("fixture"); err != nil {
		t.Fatal("idle connected plugin could not be removed:", err)
	}
	if len(r.Statuses()) != 0 || manifestHas(t, dir, "fixture") {
		t.Fatal("plugin still registered after removal")
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "fixture")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plugin directory survived", err)
	}
	_ = cli.Close()
	srv.Close()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines leaked: before=%d after=%d\n%s", before, after, buf[:runtime.Stack(buf, true)])
	}
}

// A probe that is mid-flight when removal starts finishes inside the bounded
// wait; removal then completes instead of failing with "busy".
func TestRemoveWaitsForShortLeaseToDrain(t *testing.T) {
	r, _, dir := removeFixture(t)
	r.removeDrain = 3 * time.Second
	p, _ := r.get("fixture")
	_, release, err := r.acquire(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(150 * time.Millisecond); release() }()
	if err := r.Remove("fixture"); err != nil {
		t.Fatal("removal did not wait for the lease to drain:", err)
	}
	if len(r.Statuses()) != 0 || manifestHas(t, dir, "fixture") {
		t.Fatal("plugin still registered after removal")
	}
}

// A genuinely in-flight call still blocks removal, with a named error, and
// leaves the plugin fully usable.
func TestRemoveRefusedDuringInFlightCallLeavesStateIntact(t *testing.T) {
	r, _, dir := removeFixture(t)
	r.removeDrain = 100 * time.Millisecond
	p, _ := r.get("fixture")
	_, release, err := r.acquire(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Remove("fixture")
	if !errors.Is(err, ErrPluginBusy) {
		t.Fatalf("want ErrPluginBusy, got %v", err)
	}
	if len(r.Statuses()) != 1 || !manifestHas(t, dir, "fixture") {
		t.Fatal("state changed by a refused removal")
	}
	release()
	if _, err := r.Call(context.Background(), "fixture", "echo", map[string]any{"message": "still here"}); err != nil {
		t.Fatal("plugin unusable after refused removal:", err)
	}
	if err := r.Remove("fixture"); err != nil {
		t.Fatal(err)
	}
}
