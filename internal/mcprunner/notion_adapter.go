package mcprunner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	NotionAdapterID      = "notion-stdio-v1"
	NotionPackage        = "@notionhq/notion-mcp-server"
	NotionPackageVersion = "2.5.2"
	NotionSourceRevision = "730ae781ba28beeaf0865025a3f2ed4c25ea2387"
	NotionLeafProtocol   = "2025-11-25"
	AdapterIdentityQuery = "tofi_adapter"
)

var ErrUnsupportedAdapterFeature = errors.New("Notion adapter supports basic tool requests only; modern interaction fields and extensions are unsupported")
var ErrAdapterProtocolMismatch = errors.New("Notion adapter requires exact leaf protocol 2025-11-25")
var ErrUnsupportedAdapterInvocation = errors.New("Notion adapter requires stdio transport; other or ambiguous transport arguments are unsupported")
var ErrAdapterCredentialOverride = errors.New("Notion adapter does not support header or API destination overrides")

// AdapterPolicy is operator-owned private configuration, never accepted by
// InstallRequest. These source pins do not verify installed package bytes.
// Published artifact/runtime verification is required before deployment.
type AdapterPolicy struct {
	ID             string `json:"id"`
	Package        string `json:"package"`
	Version        string `json:"version"`
	SourceRevision string `json:"source_revision"`
	Protocol       string `json:"protocol"`
}

func validateAdapter(spec Spec) error {
	if spec.Adapter == nil {
		return nil
	}
	a := spec.Adapter
	if spec.Kind != "npm" || a.ID != NotionAdapterID || a.Package != NotionPackage || a.Version != NotionPackageVersion || a.SourceRevision != NotionSourceRevision || a.Protocol != NotionLeafProtocol {
		return errors.New("unsupported or unpinned MCP adapter")
	}
	if _, supplied := spec.Env["NOTION_TOKEN"]; supplied || !filepath.IsAbs(spec.SecretEnv["NOTION_TOKEN"]) {
		return errors.New("Notion adapter requires NOTION_TOKEN through the existing private secret-file entry")
	}
	// The vendor gives OPENAPI_MCP_HEADERS priority over NOTION_TOKEN and
	// BASE_URL changes its API destination. Neither belongs to this profile.
	for _, key := range []string{"OPENAPI_MCP_HEADERS", "BASE_URL"} {
		_, public := spec.Env[key]
		_, private := spec.SecretEnv[key]
		if public || private {
			return ErrAdapterCredentialOverride
		}
	}
	// HTTP mode has a different gateway-auth contract and may make an identity
	// request at startup. Reject it before reading secrets or creating a child.
	for i := 0; i < len(spec.Args); i++ {
		arg := spec.Args[i]
		if arg == "--transport" {
			i++
			if i >= len(spec.Args) || !strings.EqualFold(spec.Args[i], "stdio") {
				return ErrUnsupportedAdapterInvocation
			}
		} else if strings.HasPrefix(arg, "--transport=") && !strings.EqualFold(strings.TrimPrefix(arg, "--transport="), "stdio") {
			return ErrUnsupportedAdapterInvocation
		}
	}
	return nil
}

func adapterIdentity(spec Spec) string {
	if spec.Adapter == nil {
		return ""
	}
	data, _ := json.Marshal(struct {
		Policy                       *AdapterPolicy
		OuterProtocol, Kind, Command string
		Args                         []string
	}{spec.Adapter, ProtocolVersion, spec.Kind, spec.Command, spec.Args})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func adapterFeatures(responses mcp.InputResponseMap, state string, meta mcp.Meta) error {
	if responses != nil || state != "" {
		return ErrUnsupportedAdapterFeature
	}
	for key, value := range meta {
		switch key {
		case mcp.MetaKeyProtocolVersion, mcp.MetaKeyClientInfo:
		case mcp.MetaKeyClientCapabilities:
			// Decode both SDK structs and raw JSON maps. No optional client
			// capability is implemented across this deliberately basic bridge.
			data, err := json.Marshal(value)
			var capabilities map[string]json.RawMessage
			if err != nil || json.Unmarshal(data, &capabilities) != nil || len(capabilities) != 0 {
				return ErrUnsupportedAdapterFeature
			}
		default:
			return ErrUnsupportedAdapterFeature
		}
	}
	return nil
}

// Constrain SDK-generated requests too, including any interaction retry.
// Explicit legacy selection never probes or falls back to another era.
type notionLeafTransport struct{ mcp.Transport }

func (t notionLeafTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return notionLeafConnection{Connection: c}, nil
}

type notionLeafConnection struct{ mcp.Connection }

func (c notionLeafConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if q, ok := message.(*jsonrpc.Request); ok {
		switch q.Method {
		case "initialize":
			var p struct {
				Protocol string `json:"protocolVersion"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.Protocol != NotionLeafProtocol {
				return ErrAdapterProtocolMismatch
			}
		case "tools/call":
			var p mcp.CallToolParamsRaw
			if json.Unmarshal(q.Params, &p) != nil {
				return ErrUnsupportedAdapterFeature
			}
			if err := adapterFeatures(p.InputResponses, p.RequestState, p.Meta); err != nil {
				return err
			}
		case "notifications/initialized", "tools/list", "ping", "notifications/cancelled":
		default:
			return ErrUnsupportedAdapterFeature
		}
	}
	return c.Connection.Write(ctx, message)
}
