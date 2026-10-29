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
	"sync/atomic"
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
	sbx *sandboxHost // nil without a sandbox

	// mu guards only the maps and counters below and is never held across file
	// or process work. Work on one account holds that account's lock (locks).
	mu       sync.Mutex
	closed   bool
	tenants  map[string]*tenant
	locks    map[string]*sync.Mutex
	opening  int
	internal string
	stop     chan struct{}
	stopped  sync.WaitGroup
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
	// AccountIdleTimeout closes an account that has had no request and no running
	// plugin for this long, so MaxAccounts counts accounts in use rather than
	// accounts ever seen (default 30 minutes). Its state stays; the next request
	// opens it again.
	AccountIdleTimeout time.Duration
	// Limits caps every child process of an account (default DefaultLimits when
	// IsolateUIDs is on; none otherwise, because a cap on the Runner's own uid
	// would cap the Runner).
	Limits Limits
	// MinFreeBytes refuses installs and imports while the data filesystem has less
	// than this free (default 1 GiB; MinFreeDisabled turns the guard off).
	MinFreeBytes uint64
	// Sandbox is the per-account network namespace, cgroup and disk quota (see
	// sandbox.go). It applies only with IsolateUIDs. The zero value is off.
	Sandbox SandboxConfig
}

// MinFreeDisabled is a MinFreeBytes value that turns the disk guard off.
const MinFreeDisabled = ^uint64(0)

type tenant struct {
	id      string
	runner  *Runner
	handler http.Handler
	token   string // internal; the per-request header is replaced with it
	dir     string // <root>/accounts/<id>
	uid     int    // 0 without isolation

	sandbox *accountSandbox
	quota   *quotaMeter

	inflight atomic.Int64
	lastUsed atomic.Int64 // unix nanoseconds
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
	if cfg.AccountIdleTimeout <= 0 {
		cfg.AccountIdleTimeout = 30 * time.Minute
	}
	if cfg.IsolateUIDs && cfg.Limits.zero() {
		cfg.Limits = DefaultLimits
	}
	switch cfg.MinFreeBytes {
	case 0:
		cfg.MinFreeBytes = 1 << 30
	case MinFreeDisabled:
		cfg.MinFreeBytes = 0
	}
	cfg.Sandbox = cfg.Sandbox.withDefaults()
	if cfg.UIDBase <= 0 {
		cfg.UIDBase = 20000
	}
	if cfg.MaxAccounts <= 0 {
		cfg.MaxAccounts = 256
	}
	if cfg.UIDBase+cfg.MaxAccounts > MaxUID {
		return nil, fmt.Errorf("uid range %d..%d does not fit below %d (a userns-remapped or rootless daemon maps 65536 uids)", cfg.UIDBase, cfg.UIDBase+cfg.MaxAccounts, MaxUID)
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
	reg, err := loadUIDRegistry(filepath.Join(cfg.Root, "uids.json"), cfg.UIDBase, cfg.IsolateUIDs)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	h := &Hub{cfg: cfg, reg: reg, tenants: map[string]*tenant{}, locks: map[string]*sync.Mutex{}, internal: hex.EncodeToString(secret), stop: make(chan struct{})}
	if cfg.IsolateUIDs && cfg.Sandbox.enabled() {
		if h.sbx, err = newSandboxHost(cfg.Sandbox); err != nil {
			return nil, err
		}
	}
	h.stopped.Add(1)
	go h.evictLoop()
	if cfg.IsolateUIDs && cfg.Sandbox.DiskBytes > 0 {
		h.stopped.Add(1)
		go h.quotaLoop()
	}
	return h, nil
}

// evictLoop closes idle accounts.
func (h *Hub) evictLoop() {
	defer h.stopped.Done()
	interval := h.cfg.AccountIdleTimeout / 4
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.evictIdle(time.Now())
		}
	}
}

// evictIdle closes every account that has been idle since before now minus the
// idle timeout: no request in flight, none for the whole period, and no plugin
// process running. An account being opened or purged is skipped this round.
func (h *Hub) evictIdle(now time.Time) int {
	h.mu.Lock()
	ids := make([]string, 0, len(h.tenants))
	for id := range h.tenants {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	evicted := 0
	for _, id := range ids {
		lock := h.lockFor(id)
		if !lock.TryLock() {
			continue
		}
		h.mu.Lock()
		t := h.tenants[id]
		idle := t != nil && t.inflight.Load() == 0 && now.Sub(time.Unix(0, t.lastUsed.Load())) >= h.cfg.AccountIdleTimeout
		h.mu.Unlock()
		if idle {
			for _, st := range t.runner.Statuses() {
				if st.State != "sleeping" {
					idle = false
					break
				}
			}
		}
		if idle {
			h.mu.Lock()
			delete(h.tenants, id)
			h.mu.Unlock()
			h.closeTenant(t)
			evicted++
		}
		lock.Unlock()
	}
	return evicted
}

// closeTenant stops the account's Runner and every process left under its uid.
func (h *Hub) closeTenant(t *tenant) {
	t.runner.Close()
	if t.uid > 0 {
		killAllOf(t.uid)
	}
	t.sandbox.Close()
}

func (h *Hub) lockFor(id string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		h.locks[id] = l
	}
	return l
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
	// The token epoch is not a secret: the gateway reads it to derive the current
	// token. It is 0 for an account that never revoked, whether or not it exists.
	if rest == "epoch" && req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"epoch": h.reg.epoch(id)})
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
	if rest == "v1/token/revoke" && req.Method == http.MethodPost {
		// Authorised by the current token; the answer is the epoch whose token works from now on.
		epoch, err := h.reg.bumpEpoch(id)
		if err != nil {
			log.Printf("hub: token revocation of account %s failed: %v", id, err)
			http.Error(w, "could not revoke the token", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"epoch": epoch})
		return
	}
	t, release, err := h.acquire(id)
	if errors.Is(err, ErrAccountPurged) {
		http.Error(w, "account was deleted", http.StatusGone)
		return
	}
	if err != nil {
		log.Printf("hub: account %s unavailable: %v", id, err)
		http.Error(w, "account Runner unavailable", http.StatusServiceUnavailable)
		return
	}
	defer release()
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
	want := "Bearer " + AccountTokenAt(h.cfg.Master, id, h.reg.epoch(id))
	got := req.Header.Get("Authorization")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ErrAccountPurged is returned for an account that was deleted. Its id is never
// served again.
var ErrAccountPurged = errors.New("account was deleted")

// tenant returns the account's Runner, creating its state on first use.
func (h *Hub) tenant(id string) (*tenant, error) {
	t, release, err := h.acquire(id)
	if err != nil {
		return nil, err
	}
	release()
	return t, nil
}

// acquire returns the account's Runner and marks a request in flight, so the
// account is not evicted under it. Only this account's lock is held while its
// state is opened; other accounts are not delayed by the file and process work.
func (h *Hub) acquire(id string) (*tenant, func(), error) {
	lock := h.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, nil, errors.New("hub closed")
	}
	if t := h.tenants[id]; t != nil {
		t.inflight.Add(1)
		h.mu.Unlock()
		return t, h.releaser(t), nil
	}
	if h.reg.isPurged(id) {
		h.mu.Unlock()
		return nil, nil, ErrAccountPurged
	}
	if len(h.tenants)+h.opening >= h.cfg.MaxAccounts {
		h.mu.Unlock()
		return nil, nil, errors.New("too many accounts")
	}
	h.opening++
	h.mu.Unlock()
	t, err := h.openLocked(id)
	h.mu.Lock()
	h.opening--
	if err == nil && h.closed {
		err = errors.New("hub closed")
	}
	if err != nil {
		h.mu.Unlock()
		if t != nil {
			h.closeTenant(t)
		}
		return nil, nil, err
	}
	h.tenants[id] = t
	t.inflight.Add(1)
	h.mu.Unlock()
	return t, h.releaser(t), nil
}

func (h *Hub) releaser(t *tenant) func() {
	return func() {
		t.lastUsed.Store(time.Now().UnixNano())
		t.inflight.Add(-1)
	}
}

func (h *Hub) accountDir(id string) string { return filepath.Join(h.cfg.Root, "accounts", id) }

// openAccountHook runs at the start of every account open. Tests use it to hold
// one account's open while others are served.
var openAccountHook func(id string)

// Layout of one account:
//
//	<root>/accounts/<id>/                 root-owned, 0711: others may pass, not list
//	<root>/accounts/<id>/manifest.json    root-owned 0600: the plugins may not edit it
//	<root>/accounts/<id>/state/           root-owned 0711: the account may pass, not change it
//	<root>/accounts/<id>/state/plugins/   root-owned 0711: same
//	<root>/accounts/<id>/state/plugins/<plugin>/   account uid, 0700: plugin code, data, tokens
//	<root>/accounts/<id>/tmp/             account uid, 0700: TMPDIR of every child
//
// The Runner (root) creates, fills and deletes things only below plugins/<plugin>
// and tmp/, and only through descriptor-relative, symlink-refusing access
// (confined.go). state/ and plugins/ belong to root so the account cannot swap
// either for a link into another account's tree.
func (h *Hub) openLocked(id string) (*tenant, error) {
	if openAccountHook != nil {
		openAccountHook(id)
	}
	dir := h.accountDir(id)
	state, tmp := filepath.Join(dir, "state"), filepath.Join(dir, "tmp")
	var cred *syscall.Credential
	if h.cfg.IsolateUIDs {
		// The directory owner is authoritative; the registry must agree on every open.
		if err := h.reg.check(id, dir); err != nil {
			return nil, err
		}
		uid, err := h.reg.uid(id)
		if err != nil {
			return nil, err
		}
		cred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
	}
	if err := prepareAccountDir(dir, cred); err != nil {
		return nil, err
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
	var sandbox *accountSandbox
	if h.sbx != nil && cred != nil {
		if sandbox, err = h.sbx.openAccount(int(cred.Uid), int(cred.Gid)); err != nil {
			return nil, fmt.Errorf("account sandbox: %w", err)
		}
	}
	var quota *quotaMeter
	if cred != nil {
		quota = newQuotaMeter(dir, h.cfg.Sandbox.DiskBytes)
	}
	opts := Options{Cred: cred, Tmp: tmp, ManifestDir: dir, Limits: h.cfg.Limits, MinFreeBytes: h.cfg.MinFreeBytes, Sandbox: sandbox}
	if quota != nil {
		opts.Admit = quota.admit
	}
	runner, err := NewWithOptions(specs, h.cfg.IdleTimeout, opts)
	if err != nil {
		sandbox.Close()
		return nil, err
	}
	if err := runner.SetStateDir(state); err != nil {
		runner.Close()
		sandbox.Close()
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		runner.Close()
		sandbox.Close()
		return nil, err
	}
	token := hex.EncodeToString(secret)
	t := &tenant{id: id, runner: runner, handler: runner.Handler(token), token: token, dir: dir, sandbox: sandbox, quota: quota}
	if cred != nil {
		t.uid = int(cred.Uid)
	}
	t.lastUsed.Store(time.Now().UnixNano())
	return t, nil
}

// prepareAccountDir creates the account's directories with the owners described
// above. An existing state/ or plugins/ that an older layout gave to the account
// is taken back; one that is not a real directory fails the account.
func prepareAccountDir(dir string, cred *syscall.Credential) error {
	if err := mkdirPlain(dir, 0711); err != nil {
		return err
	}
	top, err := openDirRoot(dir)
	if err != nil {
		return err
	}
	defer top.Close()
	stateMode := os.FileMode(0700)
	if cred != nil {
		stateMode = 0711
	}
	for _, name := range []string{"state", filepath.Join("state", "plugins")} {
		if err := mkdirIn(top, name, stateMode); err != nil {
			return err
		}
		if cred != nil {
			// Root-owned: the account's uid may pass through, never change it.
			if err := chownAt(top, name, 0, 0); err != nil {
				return fmt.Errorf("account isolation: %w", err)
			}
			if err := top.Chmod(name, stateMode); err != nil {
				return err
			}
		}
	}
	if err := mkdirIn(top, "tmp", 0700); err != nil {
		return err
	}
	if err := chownIn(cred, top, "tmp"); err != nil {
		return fmt.Errorf("account isolation: %w", err)
	}
	return nil
}

// mkdirIn creates name below r if it is missing and requires a real directory.
func mkdirIn(r *os.Root, name string, mode os.FileMode) error {
	info, err := r.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := r.Mkdir(name, mode); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", name)
	}
	return nil
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
// stays reserved and the id is tombstoned: a deleted account is never recreated,
// so a late request with a still-valid token cannot bring it back.
func (h *Hub) Purge(id string) error {
	if !ValidAccountID(id) {
		return errors.New("invalid account")
	}
	lock := h.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	// The tombstone goes first: whatever fails below, the id cannot be served again.
	if err := h.reg.markPurged(id); err != nil {
		return err
	}
	h.mu.Lock()
	t := h.tenants[id]
	delete(h.tenants, id)
	h.mu.Unlock()
	if t != nil {
		h.closeTenant(t)
	} else if uid, ok := h.reg.uidOfAccount(id); ok && h.cfg.IsolateUIDs {
		killAllOf(uid)
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
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	tenants := h.tenants
	h.tenants = map[string]*tenant{}
	h.mu.Unlock()
	close(h.stop)
	h.stopped.Wait()
	for _, t := range tenants {
		h.closeTenant(t)
	}
}
