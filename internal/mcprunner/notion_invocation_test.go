package mcprunner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotionAdapterMalformedEnvKeysRejectedBeforeSecretRead(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"empty", ""},
		{"equals", "BASE_URL=https://synthetic.invalid/"},
		{"nul", "BASE_URL\x00"},
	} {
		for _, privateFile := range []bool{false, true} {
			name := tc.name + "/env"
			if privateFile {
				name = tc.name + "/secret-file"
			}
			t.Run(name, func(t *testing.T) {
				spec, log := notionFixtureSpec(t, NotionLeafProtocol)
				spec.SecretEnv["NOTION_TOKEN"] = filepath.Join(spec.WorkDir, "missing-synthetic-token")
				if privateFile {
					spec.SecretEnv[tc.key] = filepath.Join(spec.WorkDir, "missing-synthetic-override")
				} else {
					spec.Env[tc.key] = "synthetic-override"
				}
				r, err := New([]Spec{spec}, time.Minute)
				if r != nil {
					r.Close()
				}
				// New already rejects malformed public Env keys generically.
				if err == nil || privateFile && !errors.Is(err, ErrInvalidAdapterEnvKey) {
					t.Fatalf("malformed environment key accepted: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				cli, stop, group, tools, err := start(ctx, ctx, spec)
				cancel()
				if !errors.Is(err, ErrInvalidAdapterEnvKey) || cli != nil || stop != nil || group != 0 || tools != nil {
					t.Fatalf("malformed key did not reject before secret read/process creation: %v", err)
				}
				if fixtureMethods(t, log) != "" {
					t.Fatal("malformed key started child")
				}
				// This stricter policy belongs only to the explicit Notion profile.
				spec.Adapter = nil
				r, err = New([]Spec{spec}, time.Minute)
				if privateFile && err != nil {
					t.Fatal("Notion key policy affected generic configuration", err)
				}
				if !privateFile && err == nil {
					t.Fatal("generic public environment key rejection changed")
				}
				if r != nil {
					r.Close()
				}
			})
		}
	}
}

func TestNotionAdapterInvocationRejectedBeforeSecretRead(t *testing.T) {
	for _, override := range []bool{false, true} {
		spec, log := notionFixtureSpec(t, NotionLeafProtocol)
		spec.SecretEnv["NOTION_TOKEN"] = filepath.Join(spec.WorkDir, "missing-synthetic-token")
		want := ErrUnsupportedAdapterInvocation
		if override {
			spec.Env["BASE_URL"] = "https://synthetic.invalid"
			want = ErrAdapterCredentialOverride
		} else {
			spec.Args = append(spec.Args, "--transport", "http")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		cli, stop, group, tools, err := start(ctx, ctx, spec)
		cancel()
		if !errors.Is(err, want) || cli != nil || stop != nil || group != 0 || tools != nil {
			t.Fatalf("startup did not reject before secret read/process creation: %v", err)
		}
		if fixtureMethods(t, log) != "" {
			t.Fatal("rejected startup reached child")
		}
	}
}

func TestNotionInvocationPolicyLeavesGenericConfigUnchanged(t *testing.T) {
	spec, log := notionFixtureSpec(t, ProtocolVersion)
	spec.Adapter = nil
	spec.Args = append(spec.Args, "--transport", "http")
	spec.Env["BASE_URL"], spec.Env["OPENAPI_MCP_HEADERS"] = "https://synthetic.invalid", "{}"
	r, err := New([]Spec{spec}, time.Minute)
	if err != nil {
		t.Fatal("adapter-only policy affected generic configuration", err)
	}
	r.Close()
	if fixtureMethods(t, log) != "" {
		t.Fatal("configuration check executed child")
	}
}

func TestNotionAdapterRejectsAuthAndDestinationOverrides(t *testing.T) {
	for _, key := range []string{"OPENAPI_MCP_HEADERS", "BASE_URL"} {
		for _, privateFile := range []bool{false, true} {
			name := key + "/env"
			if privateFile {
				name = key + "/secret-file"
			}
			t.Run(name, func(t *testing.T) {
				spec, log := notionFixtureSpec(t, NotionLeafProtocol)
				if privateFile {
					spec.SecretEnv[key] = filepath.Join(spec.WorkDir, "synthetic-override")
				} else {
					spec.Env[key] = "synthetic-override"
				}
				r, err := New([]Spec{spec}, time.Minute)
				if r != nil {
					r.Close()
				}
				if err == nil || !strings.Contains(err.Error(), "override") {
					t.Fatalf("reserved source override accepted: %v", err)
				}
				if fixtureMethods(t, log) != "" {
					t.Fatal("invalid override started child")
				}
			})
		}
	}
}

func TestNotionAdapterRejectsNonStdioInvocationBeforeStartup(t *testing.T) {
	for _, args := range [][]string{
		{"--transport", "http"},
		{"--transport", "HTTP"},
		{"--transport=http"},
		{"--transport=HTTP"},
		{"--transport", "sse"},
		{"--transport"},
		{"--transport", "stdio", "--transport", "http"},
		{"--transport", "http", "--transport", "stdio"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			spec, log := notionFixtureSpec(t, NotionLeafProtocol)
			spec.Args = append(spec.Args, args...)
			r, err := New([]Spec{spec}, time.Minute)
			if r != nil {
				r.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "stdio") {
				t.Fatalf("non-stdio profile accepted: %v", err)
			}
			if fixtureMethods(t, log) != "" {
				t.Fatal("invalid invocation started child")
			}
		})
	}
}

func TestNotionAdapterStdioInvocationControls(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--transport", "stdio"},
		{"--transport", "STDIO"},
		{"--transport=stdio"},
		{"--transport", "stdio", "--transport", "STDIO"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			spec, log := notionFixtureSpec(t, NotionLeafProtocol)
			spec.Args = append(spec.Args, args...)
			r, err := New([]Spec{spec}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			if fixtureMethods(t, log) != "" {
				t.Fatal("configuration validation executed child")
			}
		})
	}
}
