// Package guest implements the unprivileged computer agent that runs inside
// the shared Firecracker guest.  The package deliberately has no control-plane
// or host fallbacks: every operation is rooted in the guest workspace.
package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	// Ports below 1024 are reserved by the guest init policy. The host manager
	// connects to this fixed unprivileged AF_VSOCK port after dropping the agent
	// to UID/GID 1000.
	VsockPort       = 1052
	DefaultRoot     = "/workspace"
	DefaultDesktop  = 2
	MaxTimeout      = 120
	MaxOutput       = 256 * 1024
	MaxFileBytes    = 256 * 1024
	MaxActionBody   = 1 * 1024 * 1024
	MaxScreenshot   = 8 * 1024 * 1024
	MaxListEntries  = 4096
	MaxDesktopInput = 64 * 1024
)

const (
	ActionSourceModel  = "model"
	ActionSourceHuman  = "human"
	ActionSourceViewer = "viewer"
)

var botIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type ActionRequest struct {
	BotID         string                `json:"bot_id"`
	BotName       string                `json:"bot_name,omitempty"`
	RunID         string                `json:"run_id"`
	Action        string                `json:"action"`
	Args          json.RawMessage       `json:"args"`
	Source        string                `json:"source,omitempty"`
	WriteIdentity *tooloutcome.Identity `json:"write_identity,omitempty"`
}

type ActionResponse struct {
	OK      bool                 `json:"ok"`
	Result  any                  `json:"result,omitempty"`
	Error   string               `json:"error,omitempty"`
	Outcome *tooloutcome.Outcome `json:"outcome,omitempty"`
}

type Service struct {
	runnerMu      sync.Mutex
	runner        *mcprunner.Runner
	runnerHandler http.Handler
	runnerToken   string
	runnerClosed  bool
	root          string
	maxDesktop    int
	idleTimeout   time.Duration
	desktops      map[string]*desktop
	// sharedDesktop is the single graphical session for the workspace. The
	// desktops map retains logical Bot aliases for API compatibility, while
	// all aliases point at this one process set.
	sharedDesktop    *desktop
	terminals        map[string]*terminal
	holds            map[string]map[string]time.Time
	inputs           map[string]*inputSession
	inputLocks       map[string]*sync.Mutex
	fileWriteLocks   map[string]*sync.Mutex
	fileWriteMu      sync.Mutex
	workspaceAliasMu sync.Mutex
	sharedInputMu    sync.Mutex
	activeInput      *inputSession
	mu               sync.Mutex
	shutdown         func(context.Context) error
	nextDisplay      int
	reaperStop       chan struct{}
	reaperDone       chan struct{}
	closeOnce        sync.Once
	closeErr         error
	closed           bool
	oauthMu          sync.Mutex
	oauth            *oauthSession
	oauthTTL         time.Duration
	oauthClosed      bool
}

const sharedDesktopKey = "__shared__"

// New creates the guest service and its persistent workspace directories. The
// caller is expected to run the agent as the provisioned UID/GID 1000 user.
func New(root string, maxDesktop int) (*Service, error) {
	return NewWithIdleTimeout(root, maxDesktop, 15*time.Minute)
}

// NewWithIdleTimeout is used by acceptance tests and by the guest command's
// lifecycle configuration. A zero timeout disables automatic idle cleanup.
func NewWithIdleTimeout(root string, maxDesktop int, idleTimeout time.Duration) (*Service, error) {
	if strings.TrimSpace(root) == "" {
		root = DefaultRoot
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if maxDesktop <= 0 {
		maxDesktop = DefaultDesktop
	}
	for _, dir := range []string{
		root,
		filepath.Join(root, "bots"),
		filepath.Join(root, "browser"),
		filepath.Join(root, "shared"),
		filepath.Join(root, "shared", "bin"),
		filepath.Join(root, "home"),
		filepath.Join(root, "home", ".local", "bin"),
	} {
		if err := os.MkdirAll(dir, 0770); err != nil {
			return nil, fmt.Errorf("create guest workspace: %w", err)
		}
	}
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	s := &Service{
		root: root, maxDesktop: maxDesktop, idleTimeout: idleTimeout,
		desktops: make(map[string]*desktop), holds: make(map[string]map[string]time.Time),
		inputs: make(map[string]*inputSession), inputLocks: make(map[string]*sync.Mutex), fileWriteLocks: make(map[string]*sync.Mutex),
		terminals:   make(map[string]*terminal),
		oauthTTL:    10 * time.Minute,
		nextDisplay: 100, reaperStop: make(chan struct{}), reaperDone: make(chan struct{}),
	}
	s.startDesktopReaper()
	return s, nil
}

// Handler is exposed separately so unit tests can exercise the exact HTTP
// contract without opening a host TCP listener.
func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.handle) }

// SetShutdownHook lets the standalone command arrange guest poweroff after the
// HTTP response has been written. The endpoint is only reachable on the
// private vsock listener in production.
func (s *Service) SetShutdownHook(hook func(context.Context) error) { s.shutdown = hook }

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/runner/") {
		s.handleRunner(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/blobs/") {
		s.handleBlob(w, r)
		return
	}
	if r.URL.Path == "/v1/storage" {
		s.handleStorage(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/oauth/") {
		s.handleOAuth(w, r)
		return
	}
	if r.URL.Path == "/v1/desktop/stream" {
		s.handleDesktopStream(w, r)
		return
	}
	if r.URL.Path == "/health" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		status, result := s.healthStatus()
		writeJSON(w, status, ActionResponse{OK: status == http.StatusOK, Result: result})
		return
	}
	if r.URL.Path == "/v1/info" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"kind": "firecracker", "state": "ready", "workspace_root": s.root,
			"browser": "google-chrome-stable",
		})
		return
	}
	if r.URL.Path == "/v1/shutdown" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := s.Close(context.Background()); err != nil {
			// Desktop cleanup is best effort. Always flush the response and run
			// the shutdown hook so init can sync the workspace and power off;
			// report the bounded cleanup warning to the manager instead of
			// turning a recoverable teardown issue into a forced VM kill.
			writeJSON(w, http.StatusOK, ActionResponse{OK: true, Result: map[string]any{
				"shutting_down": true,
				"warning":       err.Error(),
			}})
		} else {
			writeJSON(w, http.StatusOK, ActionResponse{OK: true, Result: map[string]any{"shutting_down": true}})
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if s.shutdown != nil {
			go func() { _ = s.shutdown(context.Background()) }()
		}
		return
	}
	if r.URL.Path != "/v1/action" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxActionBody)
	defer r.Body.Close()
	var req ActionRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validateRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.action(r.Context(), req)
	if err != nil {
		if o, ok := tooloutcome.FromError(err); ok && req.WriteIdentity != nil && o.Status == tooloutcome.Validation && o.Certainty == "not_executed" {
			writeJSON(w, http.StatusConflict, ActionResponse{OK: false, Error: o.Message, Outcome: &o})
			return
		}
		status := http.StatusInternalServerError
		if errors.Is(err, errFileConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, errInvalidAction) || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "unknown field") || strings.Contains(err.Error(), "must be") || strings.Contains(err.Error(), "escapes") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, ActionResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse{OK: true, Result: result})
}

func validateRequest(req ActionRequest) error {
	if !botIDPattern.MatchString(req.BotID) {
		return errors.New("bot_id must be a canonical UUID")
	}
	if strings.TrimSpace(req.RunID) == "" {
		return errors.New("run_id is required")
	}
	if strings.TrimSpace(req.Action) == "" {
		return errors.New("action is required")
	}
	if req.Source != "" && req.Source != ActionSourceModel && req.Source != ActionSourceHuman && req.Source != ActionSourceViewer {
		return errors.New("source must be model, human, or viewer")
	}
	if len(req.Args) == 0 {
		req.Args = json.RawMessage(`{}`)
	}
	if !json.Valid(req.Args) {
		return errors.New("args must be valid JSON")
	}
	return nil
}

var errInvalidAction = errors.New("unsupported action")
var errFileConflict = errors.New("file_conflict")

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ActionResponse{OK: false, Error: message})
}
