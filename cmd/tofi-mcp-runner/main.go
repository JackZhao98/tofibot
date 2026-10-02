package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func main() {
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

func loadToken() (string, error) {
	path := os.Getenv("TOFI_MCP_RUNNER_TOKEN_FILE")
	if path == "" {
		path = "/runner/auth/token"
	}
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
	secret := make([]byte, 32)
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
