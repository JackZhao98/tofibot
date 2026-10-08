package mcprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pause after the outer identity check, before the SDK dispatches the RPC.
type instancePausedBody struct {
	r               io.Reader
	entered, resume chan struct{}
	once            sync.Once
}

func (b *instancePausedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.resume })
	return b.r.Read(p)
}
func (*instancePausedBody) Close() error { return nil }

func fixtureManifest(t *testing.T, r *Runner, spec Spec) string {
	t.Helper()
	dir := t.TempDir()
	if err := r.SetStateDir(dir); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]installedRecord{{Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureRPCRequest(t *testing.T, spec Spec, method string) *http.Request {
	t.Helper()
	params := map[string]any{"_meta": mcp.Meta{
		mcp.MetaKeyProtocolVersion:    ProtocolVersion,
		mcp.MetaKeyClientInfo:         map[string]any{"name": "synthetic-parent", "version": "1"},
		mcp.MetaKeyClientCapabilities: map[string]any{},
	}}
	if method == "tools/call" {
		params["name"] = "synthetic_write"
		params["arguments"] = map[string]any{"target": "original-approved-target"}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+spec.ID+"?"+AdapterIdentityQuery+"="+adapterIdentity(spec), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer synthetic-private")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	req.Header.Set("MCP-Method", method)
	if method == "tools/call" {
		req.Header.Set("MCP-Name", "synthetic_write")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req
}

func TestPluginInstanceBoundThroughBodyAndDispatch(t *testing.T) {
	for _, scenario := range []struct {
		method     string
		samePolicy bool
	}{
		{"tools/call", false}, {"tools/list", false}, {"server/discover", false}, {"private-call", false},
		{"tools/call", true}, {"tools/list", true}, {"server/discover", true}, {"private-call", true},
	} {
		profile := "modern"
		if scenario.samePolicy {
			profile = "same-policy"
		}
		t.Run(scenario.method+"/"+profile, func(t *testing.T) {
			method := scenario.method
			old, oldLog := notionFixtureSpec(t, NotionLeafProtocol)
			r, err := New([]Spec{old}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			fixtureManifest(t, r, old)
			req := fixtureRPCRequest(t, old, method)
			if method == "private-call" {
				req = httptest.NewRequest(http.MethodPost, "/v1/plugins/"+old.ID+"/call", strings.NewReader(`{"name":"synthetic_write","arguments":{"target":"original-approved-target"}}`))
				req.Header.Set("Authorization", "Bearer synthetic-private")
			}
			paused := &instancePausedBody{r: req.Body, entered: make(chan struct{}), resume: make(chan struct{})}
			req.Body = paused
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { r.Handler("synthetic-private").ServeHTTP(w, req); close(done) }()
			resumed := false
			defer func() {
				if !resumed {
					close(paused.resume)
				}
				<-done
			}()
			select {
			case <-paused.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not reach body")
			}
			// The idle old child really is removed. Publication models Install's
			// final registry update without installing any provider package.
			if err := r.Remove(old.ID); err != nil {
				t.Fatal(err)
			}
			oldMethods := fixtureMethods(t, oldLog)
			version := ProtocolVersion
			if scenario.samePolicy {
				version = NotionLeafProtocol
			}
			replacement, replacementLog := notionFixtureSpec(t, version)
			if scenario.samePolicy {
				if adapterIdentity(replacement) != adapterIdentity(old) {
					t.Fatal("same-policy fixture identity changed")
				}
			} else {
				replacement.Adapter = nil
			}
			r.installMu.Lock()
			r.mu.Lock()
			r.plugins[old.ID] = &plugin{spec: replacement}
			r.mu.Unlock()
			r.installMu.Unlock()
			close(paused.resume)
			resumed = true
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stale request did not finish")
			}
			if methods := fixtureMethods(t, replacementLog); methods != "" {
				t.Fatalf("stale request reached replacement: HTTP %d methods=%q", w.Code, methods)
			}
			if fixtureMethods(t, oldLog) != oldMethods {
				t.Fatal("removed child restarted")
			}
			if w.Code < 400 && !strings.Contains(w.Body.String(), `"isError":true`) && !strings.Contains(w.Body.String(), `"error":`) {
				t.Fatalf("stale request succeeded: HTTP %d body=%s", w.Code, w.Body.String())
			}
			fresh := httptest.NewRecorder()
			r.Handler("synthetic-private").ServeHTTP(fresh, fixtureRPCRequest(t, replacement, "tools/call"))
			if fresh.Code != http.StatusOK || strings.Contains(fresh.Body.String(), `"error":`) || strings.Contains(fresh.Body.String(), `"isError":true`) || strings.Count(fixtureMethods(t, replacementLog), "tools/call\n") != 1 {
				t.Fatalf("fresh replacement request failed: HTTP %d body=%s methods=%q", fresh.Code, fresh.Body.String(), fixtureMethods(t, replacementLog))
			}
		})
	}
}

func TestPluginInstanceCannotRestartDuringRemoval(t *testing.T) {
	spec, log := notionFixtureSpec(t, NotionLeafProtocol)
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fixtureManifest(t, r, spec)
	p, _ := r.get(spec.ID)
	entered, resume := make(chan struct{}), make(chan struct{})
	// A synthetic stop blocks at the real shutdown boundary, after Remove's
	// busy check but before its registry deletion. No child is needed here.
	p.mu.Lock()
	p.stop = func() { close(entered); <-resume }
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- r.Remove(spec.ID) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("removal never reached shutdown")
	}
	for _, operation := range []func() error{
		func() error { _, err := r.toolsPlugin(context.Background(), p); return err },
		func() error {
			_, err := r.callPlugin(context.Background(), p, &mcp.CallToolParams{Name: "synthetic_write"})
			return err
		},
		func() error {
			_, release, err := r.acquire(context.Background(), p)
			if release != nil {
				release()
			}
			return err
		},
	} {
		if err := operation(); !errors.Is(err, ErrPluginInstanceChanged) {
			t.Errorf("removing instance acquired: %v", err)
		}
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if fixtureMethods(t, log) != "" {
		t.Fatal("removing child was started")
	}
	if _, err := r.toolsPlugin(context.Background(), p); !errors.Is(err, ErrPluginInstanceChanged) {
		t.Fatal("removed cached instance remained usable", err)
	}
}

func TestPluginInstanceLeaseAndFailedRemovalRecovery(t *testing.T) {
	spec, log := notionFixtureSpec(t, NotionLeafProtocol)
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dir := fixtureManifest(t, r, spec)
	p, _ := r.get(spec.ID)
	_, release, err := r.acquire(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(spec.ID); err == nil || err.Error() != "plugin is busy" {
		t.Fatal("acquired session removed", err)
	}
	release()
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	if active != 0 {
		t.Fatalf("acquisition leaked %d leases", active)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("invalid synthetic manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(spec.ID); err == nil {
		t.Fatal("invalid manifest removal succeeded")
	}
	// A failed removal must restore the registered instance. Cached reads remain
	// usable while sleeping and a call can restart only that original child.
	methods := fixtureMethods(t, log)
	if tools, err := r.toolsPlugin(context.Background(), p); err != nil || len(tools) != 1 {
		t.Fatal("cached read after failed removal", err)
	}
	if fixtureMethods(t, log) != methods {
		t.Fatal("cached read restarted child")
	}
	if _, err := r.Call(context.Background(), spec.ID, "synthetic_write", map[string]any{"target": "recovered"}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, release, err := r.acquire(cancelled, p); !errors.Is(err, context.Canceled) {
		if release != nil {
			release()
		}
		t.Fatal("cancelled acquisition", err)
	}
	p.mu.Lock()
	active = p.active
	p.mu.Unlock()
	if active != 0 {
		t.Fatalf("cancelled acquisition leaked %d leases", active)
	}
	data, _ := json.Marshal([]installedRecord{{Spec: spec}})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(spec.ID); err != nil {
		t.Fatal("recovered idle instance could not be removed", err)
	}
	for _, operation := range []func() error{
		func() error { _, err := r.toolsPlugin(context.Background(), p); return err },
		func() error {
			_, err := r.callPlugin(context.Background(), p, &mcp.CallToolParams{Name: "synthetic_write"})
			return err
		},
		func() error {
			_, release, err := r.acquire(context.Background(), p)
			if release != nil {
				release()
			}
			return err
		},
	} {
		if err := operation(); !errors.Is(err, ErrPluginInstanceChanged) {
			t.Errorf("removed instance acquired: %v", err)
		}
	}
}
