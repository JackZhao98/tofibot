package mcprunner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNotionAdapterHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_NOTION_ADAPTER_FIXTURE") != "1" {
		return
	}
	version := os.Getenv("TOFI_NOTION_ADAPTER_VERSION")
	server := mcp.NewServer(&mcp.Implementation{Name: "synthetic-notion-leaf", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{version}})
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			f, err := os.OpenFile(os.Getenv("TOFI_NOTION_ADAPTER_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(2)
			}
			_, _ = f.WriteString(method + "\n")
			_ = f.Close()
			if method == "tools/call" && os.Getenv("TOFI_NOTION_ADAPTER_LOSS") == "1" {
				os.Exit(3)
			}
			return next(ctx, method, request)
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "synthetic_write", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest, struct {
		Target string `json:"target"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "synthetic result"}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func notionFixtureSpec(t *testing.T, version string) (Spec, string) {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	log := filepath.Join(dir, "methods")
	if err := os.WriteFile(token, []byte("synthetic-notion-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return Spec{ID: "notion-fixture", Kind: "npm", Command: os.Args[0], Args: []string{"-test.run=^TestNotionAdapterHelperProcess$"}, WorkDir: dir,
		Env: map[string]string{"TOFI_NOTION_ADAPTER_FIXTURE": "1", "TOFI_NOTION_ADAPTER_VERSION": version, "TOFI_NOTION_ADAPTER_LOG": log}, SecretEnv: map[string]string{"NOTION_TOKEN": token},
		Adapter: &AdapterPolicy{ID: NotionAdapterID, Package: NotionPackage, Version: NotionPackageVersion, SourceRevision: NotionSourceRevision, Protocol: NotionLeafProtocol}}, log
}

func fixtureMethods(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNotionAdapterExactProtocolBeforeListing(t *testing.T) {
	for _, version := range []string{NotionLeafProtocol, "2024-11-05"} {
		t.Run(version, func(t *testing.T) {
			spec, log := notionFixtureSpec(t, version)
			r, err := New([]Spec{spec}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			tools, err := r.Tools(context.Background(), spec.ID)
			methods := fixtureMethods(t, log)
			if strings.Contains(methods, "server/discover") || strings.Contains(methods, "tools/call") {
				t.Fatalf("unexpected methods %q", methods)
			}
			if version == NotionLeafProtocol {
				if err != nil || len(tools) != 1 || tools[0].InputSchema == nil {
					t.Fatalf("tools=%v err=%v", tools, err)
				}
				if strings.Count(methods, "initialize\n") != 1 || strings.Count(methods, "tools/list\n") != 1 {
					t.Fatal(methods)
				}
			} else if !errors.Is(err, ErrAdapterProtocolMismatch) || strings.Contains(methods, "tools/list") {
				t.Fatalf("negotiated version escaped owner check: %v %q", err, methods)
			}
		})
	}
}

func TestNotionAdapterUnsupportedFeaturesBeforeStartup(t *testing.T) {
	for _, params := range []*mcp.CallToolParams{
		{Name: "synthetic_write", RequestState: "synthetic-state"},
		{Name: "synthetic_write", InputResponses: mcp.InputResponseMap{}},
		{Name: "synthetic_write", Meta: mcp.Meta{"io.example/extension": map[string]any{}}},
		{Name: "synthetic_write", Meta: mcp.Meta{mcp.MetaKeyClientCapabilities: map[string]any{"extensions": map[string]any{"io.example/extension": map[string]any{}}}}},
	} {
		spec, log := notionFixtureSpec(t, NotionLeafProtocol)
		r, err := New([]Spec{spec}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.call(context.Background(), spec.ID, params)
		r.Close()
		if !errors.Is(err, ErrUnsupportedAdapterFeature) || fixtureMethods(t, log) != "" {
			t.Fatal("unsupported feature started or called leaf")
		}
	}
}

func TestNotionAdapterPrivateModernBridgeAndIdentity(t *testing.T) {
	spec, log := notionFixtureSpec(t, NotionLeafProtocol)
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	identity := r.Statuses()[0].AdapterIdentity
	for _, identityQuery := range []string{"", "stale"} {
		request := httptest.NewRequest(http.MethodPost, "/mcp/"+spec.ID+"?"+AdapterIdentityQuery+"="+identityQuery, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer synthetic-private")
		request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
		w := httptest.NewRecorder()
		r.Handler("synthetic-private").ServeHTTP(w, request)
		if w.Code != http.StatusConflict || fixtureMethods(t, log) != "" {
			t.Fatal("missing/stale identity reached child")
		}
	}
	remote := httptest.NewServer(r.Handler("synthetic-private"))
	defer remote.Close()
	endpoint := remote.URL + "/mcp/" + spec.ID + "?" + AdapterIdentityQuery + "=" + identity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "synthetic-modern-parent", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}}).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: bearerTransport{token: "synthetic-private"}}}, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatal("outer protocol changed")
	}
	if _, err := client.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "synthetic_write", Arguments: map[string]any{"target": "fixture"}, RequestState: "unsupported"}); err == nil {
		t.Fatal("modern state crossed bridge")
	}
	if strings.Contains(fixtureMethods(t, log), "tools/call") {
		t.Fatal("unsupported modern call reached leaf")
	}
	if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "synthetic_write", Arguments: map[string]any{"target": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(fixtureMethods(t, log), "tools/call\n") != 1 {
		t.Fatal("basic fixture call was replayed")
	}
}

func TestNotionAdapterNoReplayAfterUnknownEffect(t *testing.T) {
	spec, log := notionFixtureSpec(t, NotionLeafProtocol)
	spec.Env["TOFI_NOTION_ADAPTER_LOSS"] = "1"
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Call(context.Background(), spec.ID, "synthetic_write", map[string]any{"target": "fixture"}); err == nil {
		t.Fatal("expected unknown result")
	}
	if strings.Count(fixtureMethods(t, log), "tools/call\n") != 1 {
		t.Fatal("unknown-effect call replayed")
	}
}

func TestNotionAdapterPolicyAndPrivateTokenEntry(t *testing.T) {
	for _, mutate := range []func(*Spec){
		func(s *Spec) { s.Adapter.ID = "other" }, func(s *Spec) { s.Adapter.Package = "community-notion" }, func(s *Spec) { s.Adapter.Version = "latest" }, func(s *Spec) { s.Adapter.Protocol = ProtocolVersion }, func(s *Spec) { s.Adapter.SourceRevision = "unverified" }, func(s *Spec) { s.Kind = "pypi" }, func(s *Spec) { s.Env["NOTION_TOKEN"] = "embedded-secret" },
	} {
		spec, _ := notionFixtureSpec(t, NotionLeafProtocol)
		mutate(&spec)
		if r, err := New([]Spec{spec}, time.Minute); err == nil {
			r.Close()
			t.Fatal("invalid adapter profile accepted")
		}
	}
	spec, log := notionFixtureSpec(t, NotionLeafProtocol)
	if err := os.Chmod(spec.SecretEnv["NOTION_TOKEN"], 0644); err != nil {
		t.Fatal(err)
	}
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Tools(context.Background(), spec.ID); err == nil || fixtureMethods(t, log) != "" {
		t.Fatal("non-private token started child")
	}
}

func TestNotionAdapterIdentityIncludesPinAndProtocol(t *testing.T) {
	spec, _ := notionFixtureSpec(t, NotionLeafProtocol)
	original := adapterIdentity(spec)
	for _, mutate := range []func(*AdapterPolicy){func(a *AdapterPolicy) { a.ID += "-next" }, func(a *AdapterPolicy) { a.Package += "-next" }, func(a *AdapterPolicy) { a.Version = "2.5.3" }, func(a *AdapterPolicy) { a.SourceRevision += "next" }, func(a *AdapterPolicy) { a.Protocol = ProtocolVersion }} {
		copy := *spec.Adapter
		mutate(&copy)
		changed := spec
		changed.Adapter = &copy
		if adapterIdentity(changed) == original {
			t.Fatal("adapter identity missed changed policy")
		}
	}
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	spec.Adapter.Version = "caller-mutation"
	if r.Statuses()[0].AdapterIdentity != original {
		t.Fatal("caller mutated installed policy identity")
	}
}

func TestNotionAdapterCannotBeEnabledThroughInstallJSON(t *testing.T) {
	r, err := New(nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/plugins", strings.NewReader(`{"id":"notion","kind":"npm","package":"@notionhq/notion-mcp-server","version":"2.5.2","binary":"notion-mcp-server","adapter":{"id":"notion-stdio-v1"}}`))
	request.Header.Set("Authorization", "Bearer synthetic-private")
	w := httptest.NewRecorder()
	r.Handler("synthetic-private").ServeHTTP(w, request)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid installation request") {
		t.Fatal("ordinary install request accepted private adapter configuration")
	}
}

func TestRemovedAdapterRejectsOldIdentityBeforeStartup(t *testing.T) {
	spec, log := notionFixtureSpec(t, ProtocolVersion)
	oldIdentity := adapterIdentity(spec)
	spec.Adapter = nil
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	request := httptest.NewRequest(http.MethodPost, "/mcp/"+spec.ID+"?"+AdapterIdentityQuery+"="+oldIdentity, strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer synthetic-private")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	w := httptest.NewRecorder()
	r.Handler("synthetic-private").ServeHTTP(w, request)
	if w.Code != http.StatusConflict || fixtureMethods(t, log) != "" {
		t.Fatal("removed adapter accepted its old approval-bound identity")
	}
}
