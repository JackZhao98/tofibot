package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MaxOutputBytes caps stdout+stderr capture at 1MB per stream.
const MaxOutputBytes = 1 << 20

// MaxTimeout is the hard ceiling for any single sandbox command.
const MaxTimeout = 120

// DevExecutor runs commands directly on the host via `sh -c`. It is NOT a
// security boundary — the sandbox dir only scopes the working directory, the
// shell can `cd` out freely, there is no mount/network/syscall isolation.
//
// DevExecutor exists so local Mac development and runsc-less Linux hosts can
// still run Tofi end-to-end. Production must use GvisorExecutor. The factory
// logs a startup banner when DevExecutor is selected.
type DevExecutor struct {
	homeDir string
}

// NewDevExecutor builds a development-only executor.
func NewDevExecutor(homeDir string) *DevExecutor {
	return &DevExecutor{homeDir: homeDir}
}

// NewDirectExecutor is an alias preserved for call sites that haven't been
// migrated yet. New code should call NewDevExecutor or NewExecutor.
//
// Deprecated: use NewDevExecutor.
func NewDirectExecutor(homeDir string) *DevExecutor {
	return NewDevExecutor(homeDir)
}

// CreateSandbox materialises the per-run and per-user directory tree.
func (d *DevExecutor) CreateSandbox(cfg SandboxConfig) (string, error) {
	pkgDir := filepath.Join(cfg.HomeDir, "packages")
	for _, sub := range []string{
		".local/bin",
		"node_modules/.bin",
		".pip",
		"runtime/bin",
		"artifacts",
		"cache/pip",
		"cache/uv",
		"cache/npm",
	} {
		if err := os.MkdirAll(filepath.Join(pkgDir, sub), 0755); err != nil {
			return "", fmt.Errorf("create packages dir %s: %w", sub, err)
		}
	}

	sandboxDir := filepath.Join(cfg.HomeDir, "sandbox", cfg.CardID)
	if err := os.MkdirAll(filepath.Join(sandboxDir, "tmp"), 0755); err != nil {
		return "", fmt.Errorf("create sandbox dir: %w", err)
	}

	if cfg.UserID != "" {
		if err := ensureUserRuntimeDirs(filepath.Join(cfg.HomeDir, "users", cfg.UserID)); err != nil {
			return "", fmt.Errorf("create user runtime dirs: %w", err)
		}
	}
	return sandboxDir, nil
}

// Execute runs command inside the sandbox dir with a capped timeout.
func (d *DevExecutor) Execute(ctx context.Context, sandboxPath, userDir, command string, timeoutSec int, env map[string]string) (string, error) {
	homeDir := d.homeDir
	if homeDir == "" {
		homeDir = filepath.Dir(filepath.Dir(sandboxPath))
	}
	return executeInSandboxInternal(ctx, sandboxPath, homeDir, userDir, command, timeoutSec, env)
}

// Cleanup removes the per-run directory. Refuses any path without "/sandbox/"
// to prevent a miswired caller from rm-rf-ing something important.
func (d *DevExecutor) Cleanup(sandboxPath string) {
	if sandboxPath == "" || !strings.Contains(sandboxPath, "/sandbox/") {
		return
	}
	_ = os.RemoveAll(sandboxPath)
}

// buildSafePATH returns a PATH that prepends shared-package bin dirs, then the
// usual system paths, then any host version-manager paths (pyenv/nvm/volta)
// that the developer needs during local runs.
func buildSafePATH(pkgDir string) string {
	paths := []string{
		filepath.Join(pkgDir, ".local/bin"),
		filepath.Join(pkgDir, "node_modules/.bin"),
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
	for _, p := range strings.Split(os.Getenv("PATH"), ":") {
		if strings.Contains(p, ".pyenv") ||
			strings.Contains(p, ".nvm") ||
			strings.Contains(p, ".volta") {
			paths = append(paths, p)
		}
	}
	return strings.Join(paths, ":")
}

func executeInSandboxInternal(parentCtx context.Context, sandboxPath, homeDir, userDir, command string, timeoutSec int, extraEnv map[string]string) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	if timeoutSec > MaxTimeout {
		timeoutSec = MaxTimeout
	}

	command = fixUnbalancedQuotes(command)
	logAuditLine(sandboxPath, command)

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	pkgDir := filepath.Join(homeDir, "packages")
	if userDir == "" {
		userDir = sandboxPath
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = sandboxPath
	log.Printf("🧪 [dev-exec] %.80s…", command)

	if err := ensureUserRuntimeDirs(userDir); err != nil {
		return "", fmt.Errorf("prepare user runtime dirs: %w", err)
	}
	shimDir, shimEnv, err := createRuntimeShims(sandboxPath, userDir, pkgDir)
	if err != nil {
		return "", fmt.Errorf("prepare runtime shims: %w", err)
	}

	venvDir := filepath.Join(userDir, "python", "venvs", "default")
	userBinDir := filepath.Join(userDir, "bin")
	runtimeBinDir := filepath.Join(pkgDir, "runtime", "bin")
	pathValue := strings.Join([]string{
		shimDir,
		userBinDir,
		filepath.Join(venvDir, "bin"),
		runtimeBinDir,
		buildSafePATH(pkgDir),
	}, ":")

	env := []string{
		"HOME=" + userDir,
		"PATH=" + pathValue,
		"TMPDIR=" + filepath.Join(sandboxPath, "tmp"),
		"LANG=en_US.UTF-8",
		"TERM=dumb",
		"XDG_CONFIG_HOME=" + filepath.Join(userDir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(userDir, "cache"),
		"NODE_PATH=" + filepath.Join(pkgDir, "node_modules"),
		"PIP_CACHE_DIR=" + filepath.Join(pkgDir, "cache", "pip"),
		"PIP_REQUIRE_VIRTUALENV=true",
		"PYTHONPATH=" + filepath.Join(venvDir, "lib"),
		"UV_CACHE_DIR=" + filepath.Join(pkgDir, "cache", "uv"),
		"UV_TOOL_DIR=" + filepath.Join(userDir, "uv", "tools"),
		"UV_TOOL_BIN_DIR=" + userBinDir,
		"NPM_CONFIG_PREFIX=" + filepath.Join(userDir, "npm"),
		"NPM_CONFIG_CACHE=" + filepath.Join(userDir, "cache", "npm"),
		"VIRTUAL_ENV=" + venvDir,
		"TOFI_USER_ROOT=" + userDir,
		"TOFI_USER_BIN=" + userBinDir,
		"TOFI_USER_STATE_DIR=" + filepath.Join(userDir, "state"),
		"TOFI_USER_VENV=" + venvDir,
		"TOFI_RUNTIME_BIN=" + runtimeBinDir,
	}
	for k, v := range shimEnv {
		env = append(env, k+"="+v)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, limit: MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: MaxOutputBytes}

	runErr := cmd.Run()

	output := stdout.String()
	if errOut := stderr.String(); errOut != "" {
		if output != "" {
			output += "\n"
		}
		output += errOut
	}

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("command timed out after %d seconds", timeoutSec)
	}
	if runErr != nil {
		return output, fmt.Errorf("command failed: %v\n%s", runErr, output)
	}
	return strings.TrimRight(output, "\n"), nil
}

func ensureUserRuntimeDirs(userDir string) error {
	for _, dir := range []string{
		userDir,
		filepath.Join(userDir, "bin"),
		filepath.Join(userDir, "config"),
		filepath.Join(userDir, "config", "npm"),
		filepath.Join(userDir, "cache"),
		filepath.Join(userDir, "cache", "npm"),
		filepath.Join(userDir, "state"),
		filepath.Join(userDir, "state", "tool-runs"),
		filepath.Join(userDir, "python"),
		filepath.Join(userDir, "python", "venvs"),
		filepath.Join(userDir, "npm"),
		filepath.Join(userDir, "uv"),
		filepath.Join(userDir, "uv", "tools"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func createRuntimeShims(sandboxPath, userDir, pkgDir string) (string, map[string]string, error) {
	shimDir := filepath.Join(sandboxPath, ".tofi-shims")
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		return "", nil, err
	}

	pythonPath, err := resolveHostTool("python3")
	if err != nil {
		return "", nil, fmt.Errorf("python3 not found: %w", err)
	}
	nodePath, _ := resolveHostTool("node")
	npmPath, _ := resolveHostTool("npm")
	npxPath, _ := resolveHostTool("npx")
	uvPath, _ := resolveHostTool("uv")

	shimEnv := map[string]string{"TOFI_RUNTIME_PYTHON": pythonPath}
	if nodePath != "" {
		shimEnv["TOFI_RUNTIME_NODE"] = nodePath
	}
	if npmPath != "" {
		shimEnv["TOFI_RUNTIME_NPM"] = npmPath
	}
	if npxPath != "" {
		shimEnv["TOFI_RUNTIME_NPX"] = npxPath
	}
	if uvPath != "" {
		shimEnv["TOFI_RUNTIME_UV"] = uvPath
	}

	shims := map[string]string{
		"python":  runtimePythonShim(),
		"python3": runtimePythonShim(),
		"pip":     runtimePipShim(),
		"pip3":    runtimePipShim(),
	}
	if nodePath != "" {
		shims["node"] = passthroughShim("runtime-node", "$TOFI_RUNTIME_NODE")
	}
	if npmPath != "" {
		shims["npm"] = passthroughShim("runtime-npm", "$TOFI_RUNTIME_NPM")
	}
	if npxPath != "" {
		shims["npx"] = passthroughShim("runtime-npx", "$TOFI_RUNTIME_NPX")
	}
	if uvPath != "" {
		shims["uv"] = passthroughShim("runtime-uv", "$TOFI_RUNTIME_UV")
	}

	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(content), 0755); err != nil {
			return "", nil, err
		}
	}
	return shimDir, shimEnv, nil
}

func resolveHostTool(name string) (string, error) {
	return exec.LookPath(name)
}

func runtimePythonShim() string {
	return `#!/bin/sh
set -eu
STAMP="${TOFI_USER_STATE_DIR}/tool-runs/python"
mkdir -p "$(dirname "$STAMP")"
touch "$STAMP"
if [ ! -x "${TOFI_USER_VENV}/bin/python" ]; then
  mkdir -p "$(dirname "$TOFI_USER_VENV")"
  "$TOFI_RUNTIME_PYTHON" -m venv "$TOFI_USER_VENV"
fi
exec "${TOFI_USER_VENV}/bin/python" "$@"
`
}

func runtimePipShim() string {
	return `#!/bin/sh
set -eu
STAMP="${TOFI_USER_STATE_DIR}/tool-runs/pip"
mkdir -p "$(dirname "$STAMP")"
touch "$STAMP"
if [ ! -x "${TOFI_USER_VENV}/bin/python" ]; then
  mkdir -p "$(dirname "$TOFI_USER_VENV")"
  "$TOFI_RUNTIME_PYTHON" -m venv "$TOFI_USER_VENV"
fi
exec "${TOFI_USER_VENV}/bin/python" -m pip "$@"
`
}

func passthroughShim(toolName, targetVar string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
STAMP="${TOFI_USER_STATE_DIR}/tool-runs/%s"
mkdir -p "$(dirname "$STAMP")"
touch "$STAMP"
exec %s "$@"
`, toolName, targetVar)
}

// fixUnbalancedQuotes removes a single dangling trailing double quote. LLMs
// occasionally emit commands like `--flag "value"` with an extra " that
// causes sh to report "Unterminated quoted string".
func fixUnbalancedQuotes(command string) string {
	n := strings.Count(command, `"`)
	if n%2 != 0 && strings.HasSuffix(strings.TrimSpace(command), `"`) {
		command = strings.TrimSpace(command)
		command = command[:len(command)-1]
	}
	return command
}

// limitedWriter caps total bytes written to the underlying writer. Further
// writes are silently discarded. Used to bound sandbox stdout/stderr.
type limitedWriter struct {
	w       io.Writer
	limit   int
	written int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.written >= lw.limit {
		return len(p), nil
	}
	remaining := lw.limit - lw.written
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += n
	return len(p), err
}
