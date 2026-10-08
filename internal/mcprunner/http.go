package mcprunner

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler is a private control plane for the Tofi app. The container port must
// never be published to the host; the bearer token is an additional boundary.
func (r *Runner) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/v1/plugins" && req.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"plugins": r.Statuses()})
			return
		}
		if req.URL.Path == "/v1/plugins" && req.Method == http.MethodPost {
			var input InstallRequest
			if err := decodeJSON(req, &input); err != nil {
				http.Error(w, "invalid installation request", http.StatusBadRequest)
				return
			}
			if err := r.Install(req.Context(), input); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": input.ID, "state": "sleeping"})
			return
		}
		if strings.HasPrefix(req.URL.Path, "/v1/plugins/") && req.Method == http.MethodDelete {
			id := strings.TrimPrefix(req.URL.Path, "/v1/plugins/")
			if !validID(id) {
				http.NotFound(w, req)
				return
			}
			if err := r.Remove(id); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		if strings.HasPrefix(req.URL.Path, "/mcp/") {
			id := strings.TrimPrefix(req.URL.Path, "/mcp/")
			if !validID(id) {
				http.NotFound(w, req)
				return
			}
			p, err := r.get(id)
			if err != nil {
				http.NotFound(w, req)
				return
			}
			// Stateless HTTP for 2026-07-28 requires an explicit version header.
			// The SDK can otherwise bootstrap a legacy initialize even when its
			// supported version list is narrowed, so reject that path here.
			if req.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			if req.Header.Get("MCP-Protocol-Version") != ProtocolVersion {
				http.Error(w, "Unsupported protocol version: Tofi requires MCP "+ProtocolVersion, http.StatusBadRequest)
				return
			}
			if req.URL.Query().Get(AdapterIdentityQuery) != adapterIdentity(p.spec) {
				http.Error(w, "MCP adapter identity changed; reattach before calling", http.StatusConflict)
				return
			}
			handler, err := r.mcpHandler(req.Context(), p)
			if err != nil {
				writePluginError(w, err)
				return
			}
			handler.ServeHTTP(w, req)
			return
		}
		if parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/"); len(parts) == 5 && parts[0] == "v1" && parts[1] == "plugins" && validID(parts[2]) && parts[3] == "gog" {
			if _, err := r.get(parts[2]); err != nil {
				http.NotFound(w, req)
				return
			}
			id := parts[2]
			switch {
			case parts[4] == "start" && req.Method == http.MethodPost:
				var input GogStartRequest
				if err := decodeJSON(req, &input); err != nil {
					http.Error(w, "invalid authorization request", 400)
					return
				}
				result, err := r.GogStart(req.Context(), id, input)
				if err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			case parts[4] == "finish" && req.Method == http.MethodPost:
				var input GogFinishRequest
				if err := decodeJSON(req, &input); err != nil {
					http.Error(w, "invalid callback", 400)
					return
				}
				if err := r.GogFinish(req.Context(), id, input); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"connected": true})
			case parts[4] == "status" && req.Method == http.MethodGet:
				result, err := r.GogStatus(id)
				if err != nil {
					http.Error(w, "Google account status unavailable", 502)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"connected": result.Email != "", "email": result.Email, "scope": result.Scope})
			case parts[4] == "send" && req.Method == http.MethodPost:
				var input GogSendRequest
				if err := decodeJSON(req, &input); err != nil {
					http.Error(w, "invalid mail request", 400)
					return
				}
				result, err := r.GogSend(req.Context(), id, input)
				if err != nil {
					http.Error(w, err.Error(), 409)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"sent": true, "result": result})
			case parts[4] == "check" && req.Method == http.MethodPost:
				if err := r.GogCheck(req.Context(), id); err != nil {
					http.Error(w, err.Error(), 502)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"read_verified": true})
			case parts[4] == "disconnect" && req.Method == http.MethodPost:
				if err := r.GogDisconnect(req.Context(), id); err != nil {
					http.Error(w, err.Error(), 502)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"connected": false})
			default:
				http.NotFound(w, req)
			}
			return
		}
		parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		if len(parts) != 4 || parts[0] != "v1" || parts[1] != "plugins" || !validID(parts[2]) {
			http.NotFound(w, req)
			return
		}
		id, action := parts[2], parts[3]
		p, err := r.get(id)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		switch {
		case action == "tools" && req.Method == http.MethodGet:
			tools, err := r.toolsPlugin(req.Context(), p)
			if err != nil {
				writePluginError(w, err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tools": tools})
		case action == "call" && req.Method == http.MethodPost:
			var input struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := decodeJSON(req, &input); err != nil || input.Name == "" {
				http.Error(w, "invalid call", http.StatusBadRequest)
				return
			}
			result, err := r.callPlugin(req.Context(), p, &mcp.CallToolParams{Name: input.Name, Arguments: input.Arguments})
			if err != nil {
				// The child may have completed a mutating operation before losing
				// its response. A client must never silently replay this call.
				if errors.Is(err, ErrIncompatibleProtocol) {
					writePluginError(w, err)
				} else {
					http.Error(w, "tool result unknown", http.StatusBadGateway)
				}
				return
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			http.NotFound(w, req)
		}
	})
}

// mcpHandler presents each installed stdio plugin as a stable private HTTP MCP
// endpoint. Once discovered, its tool catalog survives process sleep.
func (r *Runner) mcpHandler(ctx context.Context, p *plugin) (http.Handler, error) {
	p.httpMu.Lock()
	defer p.httpMu.Unlock()
	if p.httpHandler != nil {
		return p.httpHandler, nil
	}
	tools, err := r.toolsPlugin(ctx, p)
	if err != nil {
		return nil, err
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "tofi-runner-" + p.spec.ID, Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{ProtocolVersion}})
	// The HTTP identity gate runs before reading the body. Revalidate the same
	// instance at actual RPC dispatch, including cached discover/list responses,
	// and hold it through the handler so removal cannot race process acquisition.
	mcpServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			release, err := r.retainPlugin(p)
			if err != nil {
				return nil, err
			}
			defer release()
			return next(ctx, method, request)
		}
	})
	for _, tool := range tools {
		name := tool.Name
		mcpServer.AddTool(&tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if p.spec.Adapter != nil {
				if err := adapterFeatures(request.Params.InputResponses, request.Params.RequestState, request.Params.Meta); err != nil {
					return nil, err
				}
			}
			arguments := map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
					return nil, err
				}
			}
			result, err := r.callPlugin(ctx, p, &mcp.CallToolParams{Name: name, Arguments: arguments, InputResponses: request.Params.InputResponses, RequestState: request.Params.RequestState})
			if err != nil {
				message := "Tool result unknown; the operation may have completed. Do not retry automatically."
				if errors.Is(err, ErrIncompatibleProtocol) {
					message = ErrIncompatibleProtocol.Error()
				}
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}, nil
			}
			return result, nil
		})
	}
	p.httpHandler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true})
	return p.httpHandler, nil
}

func decodeJSON(req *http.Request, target any) error {
	if req.Body == nil {
		return errors.New("missing body")
	}
	defer req.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// Only the protocol incompatibility message is safe to expose from startup;
// other plugin errors may include command paths or third-party diagnostics.
func writePluginError(w http.ResponseWriter, err error) {
	message := "plugin unavailable"
	if errors.Is(err, ErrIncompatibleProtocol) {
		message = ErrIncompatibleProtocol.Error()
	}
	http.Error(w, message, http.StatusBadGateway)
}
