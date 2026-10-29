package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func main() {
	// Plugins start through this binary so their resource limits are set after the
	// uid change and before their first instruction (mcprunner.Limits).
	if mcprunner.RunLauncher(os.Args) {
		return
	}
	if os.Getenv("TOFI_MCP_RUNNER_MODE") == "accounts" {
		if err := serveAccounts(); err != nil {
			log.Fatal(err)
		}
		return
	}
	stateDir := os.Getenv("TOFI_MCP_RUNNER_STATE")
	if stateDir == "" {
		stateDir = "/runner/state"
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		log.Fatal(err)
	}
	specs, err := mcprunner.LoadRecords(filepath.Join(stateDir, "manifest.json"))
	if err != nil {
		log.Fatal(err)
	}
	runner, err := mcprunner.New(specs, 10*time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()
	if err := runner.SetStateDir(stateDir); err != nil {
		log.Fatal(err)
	}
	token, err := loadToken()
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("TOFI_MCP_RUNNER_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8390"
	}
	log.Printf("MCP runner listening on %s", addr)
	log.Fatal(newServer(addr, runner.Handler(token)).ListenAndServe())
}

// serveAccounts runs the multi-account Runner: one process, one namespace,
// token and uid per account. It must run as root with the capabilities listed
// in docs/decisions/0071-runner-container.md; it refuses to run accounts
// as one shared uid unless that is asked for by name (development only).
func serveAccounts() error {
	root := os.Getenv("TOFI_MCP_RUNNER_ROOT")
	if root == "" {
		root = "/runner/accounts"
	}
	keyPath := os.Getenv("TOFI_MCP_RUNNER_MASTER_KEY_FILE")
	if keyPath == "" {
		keyPath = "/runner/auth/master"
	}
	isolate := os.Getenv("TOFI_MCP_RUNNER_ALLOW_SHARED_UID") != "1"
	if isolate && os.Geteuid() != 0 {
		return errors.New("the multi-account Runner must run as root to give each account its own uid; set TOFI_MCP_RUNNER_ALLOW_SHARED_UID=1 only for development")
	}
	key, err := loadSecret(keyPath, 32)
	if err != nil {
		return err
	}
	// The gateway derives account tokens from the same key, so it must be able to read it.
	if raw := os.Getenv("TOFI_MCP_RUNNER_AUTH_UID"); raw != "" {
		uid, err := strconv.Atoi(raw)
		if err != nil || uid <= 0 {
			return errors.New("TOFI_MCP_RUNNER_AUTH_UID must be a positive uid")
		}
		if err := os.Chown(keyPath, uid, -1); err != nil {
			return err
		}
		if err := os.Chmod(keyPath, 0400); err != nil {
			return err
		}
	}
	cfg := mcprunner.HubConfig{Root: root, Master: []byte(key), IsolateUIDs: isolate}
	if raw := os.Getenv("TOFI_MCP_RUNNER_MIN_FREE_MB"); raw != "" {
		mb, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return errors.New("TOFI_MCP_RUNNER_MIN_FREE_MB must be a number of megabytes (0 turns the guard off)")
		}
		cfg.MinFreeBytes = mb << 20
		if mb == 0 {
			cfg.MinFreeBytes = mcprunner.MinFreeDisabled
		}
	}
	if isolate {
		if cfg.Sandbox, err = sandboxFromEnv(); err != nil {
			return err
		}
	}
	hub, err := mcprunner.NewHub(cfg)
	if err != nil {
		return err
	}
	defer hub.Close()
	addr := os.Getenv("TOFI_MCP_RUNNER_LISTEN")
	if addr == "" {
		addr = "0.0.0.0:8390"
	}
	log.Printf("MCP runner (accounts, uid isolation=%v) listening on %s", isolate, addr)
	return newServer(addr, hub).ListenAndServe()
}

// sandboxFromEnv reads the per-account sandbox settings. Network namespaces are
// required by default (the isolation claim between accounts depends on them);
// cgroups are used when the host allows and logged loudly when not.
func sandboxFromEnv() (mcprunner.SandboxConfig, error) {
	var cfg mcprunner.SandboxConfig
	var err error
	if cfg.Network, err = mcprunner.ParseSandboxMode(os.Getenv("TOFI_MCP_RUNNER_NETNS"), mcprunner.SandboxRequired); err != nil {
		return cfg, errors.New("TOFI_MCP_RUNNER_NETNS: " + err.Error())
	}
	if cfg.Cgroups, err = mcprunner.ParseSandboxMode(os.Getenv("TOFI_MCP_RUNNER_CGROUPS"), mcprunner.SandboxAuto); err != nil {
		return cfg, errors.New("TOFI_MCP_RUNNER_CGROUPS: " + err.Error())
	}
	number := func(name string, scale uint64, def uint64) (uint64, error) {
		raw := os.Getenv(name)
		if raw == "" {
			return def, nil
		}
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return 0, errors.New(name + " must be a whole number")
		}
		return n * scale, nil
	}
	if cfg.MemoryBytes, err = number("TOFI_MCP_RUNNER_ACCOUNT_MEMORY_MB", 1<<20, mcprunner.DefaultAccountMemory); err != nil {
		return cfg, err
	}
	if cfg.Pids, err = number("TOFI_MCP_RUNNER_ACCOUNT_PIDS", 1, mcprunner.DefaultAccountPids); err != nil {
		return cfg, err
	}
	if cfg.DiskBytes, err = number("TOFI_MCP_RUNNER_ACCOUNT_DISK_MB", 1<<20, mcprunner.DefaultAccountDisk); err != nil {
		return cfg, err // 0 turns the quota off
	}
	cfg.RequireSeccomp = os.Getenv("TOFI_MCP_RUNNER_REQUIRE_SECCOMP") == "1"
	cfg.EgressInterface = os.Getenv("TOFI_MCP_RUNNER_EGRESS_INTERFACE")
	return cfg, nil
}

// newServer bounds how long a client may hold a connection without finishing a
// request. There is no WriteTimeout: tool calls and MCP streams are long by design.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // the largest body is a 40 MiB import on the local network
		IdleTimeout:       2 * time.Minute,
	}
}

func loadToken() (string, error) {
	path := os.Getenv("TOFI_MCP_RUNNER_TOKEN_FILE")
	if path == "" {
		path = "/runner/auth/token"
	}
	return loadSecret(path, 32)
}

// loadSecret reads a hex secret, creating it exclusively on first start.
func loadSecret(path string, size int) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	secret := make([]byte, size)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		data, err = os.ReadFile(path)
		return strings.TrimSpace(string(data)), err
	}
	if err != nil {
		return "", err
	}
	value := hex.EncodeToString(secret)
	if _, err := f.WriteString(value); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return value, nil
}
