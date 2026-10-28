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
	log.Fatal(http.ListenAndServe(addr, runner.Handler(token)))
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
	hub, err := mcprunner.NewHub(mcprunner.HubConfig{Root: root, Master: []byte(key), IsolateUIDs: isolate})
	if err != nil {
		return err
	}
	defer hub.Close()
	addr := os.Getenv("TOFI_MCP_RUNNER_LISTEN")
	if addr == "" {
		addr = "0.0.0.0:8390"
	}
	log.Printf("MCP runner (accounts, uid isolation=%v) listening on %s", isolate, addr)
	server := &http.Server{Addr: addr, Handler: hub, ReadHeaderTimeout: 10 * time.Second}
	return server.ListenAndServe()
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
