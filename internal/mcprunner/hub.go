package mcprunner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Hub serves many accounts from one process. Each account has its own URL
// namespace (/a/<account id>/...), its own bearer token, its own state
// directory, its own Runner (its own plugin processes), and, when isolation is
// on, its own uid. Nothing is shared between accounts except the process that
// routes requests, and that process holds no per-account secret except the
// master key from which tokens are derived.
type Hub struct {
	cfg HubConfig
	reg *uidRegistry

	mu       sync.Mutex
	closed   bool
	tenants  map[string]*tenant
	internal string
}

// HubConfig configures a Hub. Root is a directory the Hub owns; Master is the
// key tokens are derived from.
type HubConfig struct {
	Root   string
	Master []byte
	// IdleTimeout is how long a started plugin may sit unused before it sleeps.
	IdleTimeout time.Duration
	// IsolateUIDs runs every process of an account as an account-specific uid. It
	// needs CAP_SETUID, CAP_SETGID, CAP_CHOWN, CAP_DAC_OVERRIDE and CAP_FOWNER.
	// Without it accounts are separated by directory and token only, which is
	// meant for tests and development.
	IsolateUIDs bool
	// UIDBase is the first uid handed out (default 20000).
	UIDBase     int
	MaxAccounts int
}

type tenant struct {
	id      string
	runner  *Runner
	handler http.Handler
	token   string // internal; the per-request header is replaced with it
	dir     string // <root>/accounts/<id>
}

// NewHub validates the configuration and prepares the root directory.
func NewHub(cfg HubConfig) (*Hub, error) {
	if !filepath.IsAbs(cfg.Root) {
		return nil, errors.New("hub root must be an absolute path")
	}
	if len(cfg.Master) < MinMasterKeyBytes {
		return nil, errors.New("master key is too short")
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 10 * time.Minute
	}
	if cfg.UIDBase <= 0 {
		cfg.UIDBase = 20000
	}
	if cfg.MaxAccounts <= 0 {
		cfg.MaxAccounts = 256
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, "accounts"), 0711); err != nil {
		return nil, err
	}
	if cfg.IsolateUIDs {
		// An account's uid must be able to pass through, never to list.
		for _, d := range []string{cfg.Root, filepath.Join(cfg.Root, "accounts")} {
			if err := os.Chmod(d, 0711); err != nil {
				return nil, err
			}
		}
	}
	reg, err := loadUIDRegistry(filepath.Join(cfg.Root, "uids.json"), cfg.UIDBase)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &Hub{cfg: cfg, reg: reg, tenants: map[string]*tenant{}, internal: hex.EncodeToString(secret)}, nil
}

// ServeHTTP routes /a/<id>/<path> to account <id>. The credential is checked
// against that account's token before any state of the account is touched, and
// a refusal never says whether the account exists.
func (h *Hub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/healthz" && req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
		return
	}
	id, rest, ok := splitAccountPath(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	if !h.authorized(req, id) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if rest == "" && req.Method == http.MethodDelete {
		if err := h.Purge(id); err != nil {
			log.Printf("hub: purge of account %s failed: %v", id, err)
			http.Error(w, "could not remove the account state", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
		return
	}
	t, err := h.tenant(id)
	if err != nil {
		log.Printf("hub: account %s unavailable: %v", id, err)
		http.Error(w, "account Runner unavailable", http.StatusServiceUnavailable)
		return
	}
	clone := req.Clone(req.Context())
	clone.URL.Path, clone.URL.RawPath = "/"+rest, ""
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	t.handler.ServeHTTP(w, clone)
}

// splitAccountPath returns the account id and the remainder (without a leading
// slash) of /a/<id>/<rest>. Encoded slashes and dot segments are refused.
func splitAccountPath(req *http.Request) (id, rest string, ok bool) {
	p := req.URL.Path
	if req.URL.RawPath != "" && req.URL.RawPath != p {
		return "", "", false
	}
	if !strings.HasPrefix(p, AccountPathPrefix) || strings.Contains(p, "..") || strings.Contains(p, "//") {
		return "", "", false
	}
	tail := strings.TrimPrefix(p, AccountPathPrefix)
	id, rest, _ = strings.Cut(tail, "/")
	if !ValidAccountID(id) {
		return "", "", false
	}
	return id, rest, true
}

func (h *Hub) authorized(req *http.Request, id string) bool {
	want := "Bearer " + AccountToken(h.cfg.Master, id)
	got := req.Header.Get("Authorization")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// tenant returns the account's Runner, creating its state on first use.
func (h *Hub) tenant(id string) (*tenant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("hub closed")
	}
	if t := h.tenants[id]; t != nil {
		return t, nil
	}
	if len(h.tenants) >= h.cfg.MaxAccounts {
		return nil, errors.New("too many accounts")
	}
	t, err := h.openLocked(id)
	if err != nil {
		return nil, err
	}
	h.tenants[id] = t
	return t, nil
}

func (h *Hub) accountDir(id string) string { return filepath.Join(h.cfg.Root, "accounts", id) }

// Layout of one account:
//
//	<root>/accounts/<id>/            root-owned, 0711: others may pass, not list
//	<root>/accounts/<id>/manifest.json   root-owned 0600: the plugins may not edit it
//	<root>/accounts/<id>/state/      account uid, 0700: plugin code, data, tokens
//	<root>/accounts/<id>/tmp/        account uid, 0700: TMPDIR of every child
func (h *Hub) openLocked(id string) (*tenant, error) {
	dir := h.accountDir(id)
	state, tmp := filepath.Join(dir, "state"), filepath.Join(dir, "tmp")
	var cred *syscall.Credential
	if h.cfg.IsolateUIDs {
		uid, err := h.reg.uid(id)
		if err != nil {
			return nil, err
		}
		cred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
	}
	for _, d := range []struct {
		path string
		mode os.FileMode
		own  bool
	}{{dir, 0711, false}, {state, 0700, true}, {filepath.Join(state, "plugins"), 0700, true}, {tmp, 0700, true}} {
		if err := mkdirPlain(d.path, d.mode); err != nil {
			return nil, err
		}
		if d.own {
			if err := chownTo(cred, d.path); err != nil {
				return nil, fmt.Errorf("account isolation: %w", err)
			}
		}
	}
	specs, err := LoadRecords(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if err := validateSpecWithin(spec, state); err != nil {
			return nil, err
		}
	}
	runner, err := NewWithOptions(specs, h.cfg.IdleTimeout, Options{Cred: cred, Tmp: tmp, ManifestDir: dir})
	if err != nil {
		return nil, err
	}
	if err := runner.SetStateDir(state); err != nil {
		runner.Close()
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		runner.Close()
		return nil, err
	}
	token := hex.EncodeToString(secret)
	return &tenant{id: id, runner: runner, handler: runner.Handler(token), token: token, dir: dir}, nil
}

// mkdirPlain creates a directory that must be a real directory (never a symlink).
func mkdirPlain(path string, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// Purge stops the account's processes and removes all of its state. The uid
// stays reserved.
func (h *Hub) Purge(id string) error {
	if !ValidAccountID(id) {
		return errors.New("invalid account")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.tenants[id]; t != nil {
		t.runner.Close()
		delete(h.tenants, id)
	}
	dir := h.accountDir(id)
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return errors.New("account state is not a directory")
	}
	return os.RemoveAll(dir)
}

// Close stops every account's processes.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for id, t := range h.tenants {
		t.runner.Close()
		delete(h.tenants, id)
	}
}
