package mcprunner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultIdleTimeout = 10 * time.Minute
	startupTimeout     = 45 * time.Second
	maxTools           = 1000
)

var ErrPluginInstanceChanged = errors.New("plugin instance changed; reattach before calling")

// Spec is an operator-approved installed MCP. Command and WorkDir are absolute
// paths in the runner container; no shell expansion or runtime downloads occur.
type Spec struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind,omitempty"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	WorkDir   string            `json:"work_dir"`
	Env       map[string]string `json:"env,omitempty"`
	SecretEnv map[string]string `json:"secret_env,omitempty"`
	Adapter   *AdapterPolicy    `json:"adapter,omitempty"`
}

type Status struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	ToolCount       int       `json:"tool_count"`
	LastUsed        time.Time `json:"last_used,omitempty"`
	AdapterIdentity string    `json:"adapter_identity,omitempty"`
}

type Runner struct {
	mu          sync.RWMutex
	installMu   sync.Mutex
	stateDir    string
	plugins     map[string]*plugin
	idleTimeout time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

type plugin struct {
	spec        Spec
	mu          sync.Mutex
	httpMu      sync.Mutex
	gogMu       sync.Mutex
	httpHandler http.Handler
	// ready serializes cold starts. No request context owns a child process.
	ready        chan struct{}
	startErr     error
	cli          *mcp.ClientSession
	stop         context.CancelFunc
	processGroup int
	tools        []mcp.Tool
	lastUsed     time.Time
	active       int
	retiring     bool
}

func New(specs []Spec, idleTimeout time.Duration) (*Runner, error) {
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{plugins: make(map[string]*plugin, len(specs)), idleTimeout: idleTimeout, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	for _, spec := range specs {
		if !validID(spec.ID) || !filepath.IsAbs(spec.Command) || !filepath.IsAbs(spec.WorkDir) {
			cancel()
			return nil, fmt.Errorf("invalid runner plugin %q", spec.ID)
		}
		if _, exists := r.plugins[spec.ID]; exists {
			cancel()
			return nil, fmt.Errorf("duplicate runner plugin %q", spec.ID)
		}
		for key := range spec.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") {
				cancel()
				return nil, fmt.Errorf("invalid environment key in %q", spec.ID)
			}
		}
		if err := validateAdapter(spec); err != nil {
			cancel()
			return nil, err
		}
		if spec.Adapter != nil {
			policy := *spec.Adapter
			spec.Adapter = &policy
			spec.Args = append([]string(nil), spec.Args...)
			env, secrets := make(map[string]string, len(spec.Env)), make(map[string]string, len(spec.SecretEnv))
			for key, value := range spec.Env {
				env[key] = value
			}
			for key, value := range spec.SecretEnv {
				secrets[key] = value
			}
			spec.Env, spec.SecretEnv = env, secrets
		}
		r.plugins[spec.ID] = &plugin{spec: spec}
	}
	go r.reap()
	return r, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func (r *Runner) get(id string) (*plugin, error) {
	r.mu.RLock()
	p := r.plugins[id]
	r.mu.RUnlock()
	if p == nil {
		return nil, errors.New("plugin not installed")
	}
	return p, nil
}

// retainPlugin binds an operation to the instance selected at its boundary.
// Registry validation and the active lease are atomic with Remove's busy check.
// No installation lock is held across body parsing, startup or tool execution.
func (r *Runner) retainPlugin(p *plugin) (func(), error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.plugins[p.spec.ID] != p {
		return nil, ErrPluginInstanceChanged
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retiring {
		return nil, ErrPluginInstanceChanged
	}
	if r.ctx.Err() != nil {
		return nil, errors.New("runner stopped")
	}
	p.active++
	p.lastUsed = time.Now()
	return func() { p.mu.Lock(); p.active--; p.lastUsed = time.Now(); p.mu.Unlock() }, nil
}

func (r *Runner) Statuses() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Status, 0, len(r.plugins))
	for _, p := range r.plugins {
		p.mu.Lock()
		state := "sleeping"
		if p.ready != nil {
			state = "starting"
		} else if p.cli != nil {
			state = "ready"
		}
		out = append(out, Status{ID: p.spec.ID, State: state, ToolCount: len(p.tools), LastUsed: p.lastUsed, AdapterIdentity: adapterIdentity(p.spec)})
		p.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Tools returns cached schema while sleeping. A first discovery starts the child.
func (r *Runner) Tools(ctx context.Context, id string) ([]mcp.Tool, error) {
	p, err := r.get(id)
	if err != nil {
		return nil, err
	}
	return r.toolsPlugin(ctx, p)
}

func (r *Runner) toolsPlugin(ctx context.Context, p *plugin) ([]mcp.Tool, error) {
	releaseInstance, err := r.retainPlugin(p)
	if err != nil {
		return nil, err
	}
	defer releaseInstance()
	p.mu.Lock()
	if p.tools != nil {
		tools := append([]mcp.Tool(nil), p.tools...)
		p.mu.Unlock()
		return tools, nil
	}
	p.mu.Unlock()
	cli, release, err := r.acquire(ctx, p)
	if err != nil {
		return nil, err
	}
	_ = cli
	defer release()
	p.mu.Lock()
	tools := append([]mcp.Tool(nil), p.tools...)
	p.mu.Unlock()
	return tools, nil
}

// Call never replays a tool after a transport failure: a write may have succeeded.
func (r *Runner) Call(ctx context.Context, id, tool string, arguments map[string]any) (*mcp.CallToolResult, error) {
	return r.call(ctx, id, &mcp.CallToolParams{Name: tool, Arguments: arguments})
}

func (r *Runner) call(ctx context.Context, id string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	p, err := r.get(id)
	if err != nil {
		return nil, err
	}
	return r.callPlugin(ctx, p, params)
}

func (r *Runner) callPlugin(ctx context.Context, p *plugin, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if p.spec.Adapter != nil {
		if err := adapterFeatures(params.InputResponses, params.RequestState, params.Meta); err != nil {
			return nil, err
		}
		// Modern identity/capability metadata is validated at the outer boundary;
		// the opted-in leaf establishes its identity through initialize instead.
		copy := *params
		copy.Meta = nil
		params = &copy
	}
	cli, release, err := r.acquire(ctx, p)
	if err != nil {
		return nil, err
	}
	defer release()
	p.mu.Lock()
	allowed := false
	for _, candidate := range p.tools {
		if candidate.Name == params.Name {
			allowed = true
			break
		}
	}
	p.mu.Unlock()
	if !allowed {
		return nil, errors.New("tool is not available in this plugin")
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	result, err := cli.CallTool(ctx, params)
	if err != nil {
		if ctx.Err() == nil {
			p.mu.Lock()
			if p.cli == cli {
				p.cli = nil
				go shutdown(cli, p.stop, p.processGroup)
			}
			p.mu.Unlock()
		}
		return nil, fmt.Errorf("tool result unknown: %w", err)
	}
	if p.spec.Adapter != nil && result != nil && (result.NeedsInput() || result.InputRequests != nil || result.RequestState != "") {
		return nil, ErrUnsupportedAdapterFeature
	}
	return result, nil
}

func (r *Runner) acquire(ctx context.Context, p *plugin) (*mcp.ClientSession, func(), error) {
	// Keep the selected instance registered throughout cold-start waiting. A
	// returned session gets its own lease before this temporary lease is released.
	releaseInstance, err := r.retainPlugin(p)
	if err != nil {
		return nil, nil, err
	}
	defer releaseInstance()
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := r.ctx.Err(); err != nil {
			return nil, nil, errors.New("runner stopped")
		}
		p.mu.Lock()
		if p.cli != nil {
			p.active++
			p.lastUsed = time.Now()
			cli := p.cli
			p.mu.Unlock()
			return cli, func() { p.mu.Lock(); p.active--; p.lastUsed = time.Now(); p.mu.Unlock() }, nil
		}
		if p.ready != nil {
			ready := p.ready
			p.mu.Unlock()
			select {
			case <-ready:
				p.mu.Lock()
				err := p.startErr
				p.mu.Unlock()
				if err != nil {
					return nil, nil, fmt.Errorf("plugin could not start: %w", err)
				}
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-r.ctx.Done():
				return nil, nil, errors.New("runner stopped")
			}
		}
		p.ready = make(chan struct{})
		p.startErr = nil
		ready := p.ready
		p.mu.Unlock()
		// Startup is owned by the runner, not by the first caller. This also
		// prevents cancellation of one waiter from killing another one's child.
		go func() {
			startCtx, cancel := context.WithTimeout(r.ctx, startupTimeout)
			defer cancel()
			cli, stop, pgid, tools, err := start(startCtx, r.ctx, p.spec)
			p.mu.Lock()
			p.startErr = err
			if err == nil && r.ctx.Err() == nil {
				p.cli, p.stop, p.processGroup, p.tools = cli, stop, pgid, tools
				p.lastUsed = time.Now()
			} else if cli != nil {
				_ = cli.Close()
				if stop != nil {
					stop()
				}
			}
			p.ready = nil
			close(ready)
			p.mu.Unlock()
		}()
		select {
		case <-ready:
			p.mu.Lock()
			started, startErr := p.cli != nil, p.startErr
			p.mu.Unlock()
			if !started {
				if startErr != nil {
					return nil, nil, fmt.Errorf("plugin could not start: %w", startErr)
				}
				return nil, nil, errors.New("plugin could not start or discover tools")
			}
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-r.ctx.Done():
			return nil, nil, errors.New("runner stopped")
		}
	}
}

func start(startCtx, life context.Context, spec Spec) (*mcp.ClientSession, context.CancelFunc, int, []mcp.Tool, error) {
	if err := validateAdapter(spec); err != nil {
		return nil, nil, 0, nil, err
	}
	procCtx, stop := context.WithCancel(life)
	processCmd := exec.CommandContext(procCtx, spec.Command, spec.Args...)
	processCmd.Dir = spec.WorkDir
	processCmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TMPDIR=/tmp"}
	for key, value := range spec.Env {
		processCmd.Env = append(processCmd.Env, key+"="+value)
	}
	for key, path := range spec.SecretEnv {
		if spec.Adapter != nil && key == "NOTION_TOKEN" {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				stop()
				return nil, nil, 0, nil, errors.New("Notion token file must be a private mode-0600 regular file")
			}
		}
		value, err := os.ReadFile(path)
		if err != nil {
			stop()
			return nil, nil, 0, nil, err
		}
		processCmd.Env = append(processCmd.Env, key+"="+strings.TrimSpace(string(value)))
	}
	processCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var tr mcp.Transport = &latestOnlyTransport{Transport: &mcp.CommandTransport{Command: processCmd}}
	leafProtocol := ProtocolVersion
	var clientOptions *mcp.ClientOptions
	if spec.Adapter != nil {
		leafProtocol = spec.Adapter.Protocol
		tr = notionLeafTransport{Transport: &mcp.CommandTransport{Command: processCmd}}
		clientOptions = &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}}
	}
	cli, err := mcp.NewClient(&mcp.Implementation{Name: "tofi-mcp-runner", Version: "1"}, clientOptions).Connect(startCtx, tr, &mcp.ClientSessionOptions{ProtocolVersion: leafProtocol})
	closeFailure := func(err error) (*mcp.ClientSession, context.CancelFunc, int, []mcp.Tool, error) {
		pid := 0
		if processCmd.Process != nil {
			pid = processCmd.Process.Pid
		}
		shutdown(cli, stop, pid)
		return nil, nil, 0, nil, err
	}
	if err != nil {
		return closeFailure(err)
	}
	if result := cli.InitializeResult(); result == nil || result.ProtocolVersion != leafProtocol {
		if spec.Adapter != nil {
			return closeFailure(ErrAdapterProtocolMismatch)
		}
		return closeFailure(ErrIncompatibleProtocol)
	}
	tools := make([]mcp.Tool, 0)
	req := &mcp.ListToolsParams{}
	seen := map[string]bool{}
	for {
		page, err := cli.ListTools(startCtx, req)
		if err != nil {
			return closeFailure(err)
		}
		for _, tool := range page.Tools {
			if tool == nil || tool.InputSchema == nil {
				return closeFailure(errors.New("invalid tool schema"))
			}
			tools = append(tools, *tool)
		}
		if len(tools) > maxTools {
			return closeFailure(errors.New("too many tools"))
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return closeFailure(errors.New("repeated tool cursor"))
		}
		seen[page.NextCursor] = true
		req.Cursor = page.NextCursor
	}
	pid := 0
	if processCmd != nil && processCmd.Process != nil {
		pid = processCmd.Process.Pid
	}
	return cli, stop, pid, tools, nil
}

func shutdown(cli *mcp.ClientSession, stop context.CancelFunc, processGroup int) {
	if cli != nil {
		_ = cli.Close()
	}
	if processGroup > 0 {
		_ = syscall.Kill(-processGroup, syscall.SIGTERM)
	}
	if stop != nil {
		stop()
	}
}

func (r *Runner) reap() {
	interval := r.idleTimeout / 4
	if interval > 15*time.Second {
		interval = 15 * time.Second
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.RLock()
		for _, p := range r.plugins {
			p.mu.Lock()
			if p.cli == nil || p.active != 0 || time.Since(p.lastUsed) < r.idleTimeout {
				p.mu.Unlock()
				continue
			}
			cli, stop, group := p.cli, p.stop, p.processGroup
			p.cli = nil
			p.stop = nil
			p.ready = make(chan struct{})
			ready := p.ready
			p.mu.Unlock()
			// Do not hold the plugin lock across the library's graceful exit wait.
			go func(p *plugin) { shutdown(cli, stop, group); p.mu.Lock(); p.ready = nil; close(ready); p.mu.Unlock() }(p)
		}
		r.mu.RUnlock()
	}
}

func (r *Runner) Close() {
	r.cancel()
	<-r.done
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.plugins {
		p.mu.Lock()
		cli, stop, group := p.cli, p.stop, p.processGroup
		p.cli = nil
		p.stop = nil
		p.mu.Unlock()
		shutdown(cli, stop, group)
	}
}
