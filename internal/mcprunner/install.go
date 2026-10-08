package mcprunner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	packageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	versionName = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+_-]*$`)
	binName     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// InstallRequest is an exact, owner-approved package installation. Version is
// mandatory; the runner never resolves "latest" at plugin startup.
type InstallRequest struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"` // npm, pypi, or builtin_gog
	Package string            `json:"package,omitempty"`
	Version string            `json:"version,omitempty"`
	Binary  string            `json:"binary,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type installedRecord struct {
	Request InstallRequest `json:"request"`
	Spec    Spec           `json:"spec"`
}

func (r *Runner) SetStateDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("runner state directory must be absolute")
	}
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0700); err != nil {
		return err
	}
	r.mu.Lock()
	r.stateDir = dir
	r.mu.Unlock()
	return nil
}

func LoadRecords(path string) ([]Spec, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Spec{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []installedRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	specs := make([]Spec, 0, len(records))
	for _, record := range records {
		specs = append(specs, record.Spec)
	}
	return specs, nil
}

func (r *Runner) Install(ctx context.Context, input InstallRequest) error {
	if !validID(input.ID) || len(input.Args) > 32 {
		return errors.New("invalid plugin ID or arguments")
	}
	for _, arg := range input.Args {
		if len(arg) > 1024 || strings.ContainsRune(arg, 0) {
			return errors.New("invalid plugin argument")
		}
	}
	if input.Kind != "builtin_gog" && (!packageName.MatchString(input.Package) || !versionName.MatchString(input.Version) || !binName.MatchString(input.Binary)) {
		return errors.New("a package, pinned version, and binary name are required")
	}
	if input.Kind != "builtin_gog" && input.Kind != "npm" && input.Kind != "pypi" {
		return errors.New("unsupported package kind")
	}
	if input.Kind == "builtin_gog" && len(input.Env) > 0 {
		return errors.New("gogcli environment is managed by Runner")
	}
	if len(input.Env) > 32 {
		return errors.New("too many environment variables")
	}
	for key, value := range input.Env {
		if !envName.MatchString(key) || len(value) > 16<<10 || strings.HasPrefix(key, "TOFI_MCP_RUNNER_") || key == "PATH" || key == "HOME" || key == "TMPDIR" {
			return errors.New("invalid plugin environment")
		}
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	r.mu.RLock()
	dir := r.stateDir
	_, exists := r.plugins[input.ID]
	r.mu.RUnlock()
	if dir == "" {
		return errors.New("runner installation is disabled")
	}
	if exists {
		return errors.New("plugin already installed")
	}
	target := filepath.Join(dir, "plugins", input.ID)
	if _, err := os.Lstat(target); err == nil {
		return errors.New("plugin directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = os.RemoveAll(target)
		}
	}()
	stage := target
	if err := os.MkdirAll(filepath.Join(target, "home"), 0700); err != nil {
		return err
	}
	installCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var command string
	args := append([]string(nil), input.Args...)
	switch input.Kind {
	case "npm":
		if err := runInstaller(installCtx, "npm", "install", "--prefix", stage, "--ignore-scripts", "--no-audit", "--no-fund", "--", input.Package+"@"+input.Version); err != nil {
			return err
		}
		command = filepath.Join(stage, "node_modules", ".bin", input.Binary)
	case "pypi":
		if err := runInstaller(installCtx, "python3", "-m", "venv", stage); err != nil {
			return err
		}
		if err := runInstaller(installCtx, filepath.Join(stage, "bin", "python"), "-m", "pip", "install", "--disable-pip-version-check", "--no-input", input.Package+"=="+input.Version); err != nil {
			return err
		}
		command = filepath.Join(stage, "bin", input.Binary)
	case "builtin_gog":
		command = "/usr/local/bin/gog"
		if len(args) == 0 {
			args = []string{"mcp", "--allow-tool", "gmail"}
		}
		if err := os.MkdirAll(filepath.Join(stage, "home"), 0700); err != nil {
			return err
		}
		password := make([]byte, 32)
		if _, err := rand.Read(password); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "keyring-password"), []byte(hex.EncodeToString(password)), 0600); err != nil {
			return err
		}
	}
	if _, err := os.Stat(command); err != nil {
		return fmt.Errorf("installed executable missing: %w", err)
	}
	spec := Spec{ID: input.ID, Kind: input.Kind, Command: command, Args: args, WorkDir: target, Env: map[string]string{"HOME": filepath.Join(target, "home")}}
	if len(input.Env) > 0 {
		secrets, err := installSecretEnv(target, input.Env)
		if err != nil {
			return err
		}
		spec.SecretEnv = secrets
	}
	if input.Kind == "builtin_gog" {
		spec.Env["GOG_HOME"] = filepath.Join(target, "home")
		spec.Env["GOG_KEYRING_BACKEND"] = "file"
		if spec.SecretEnv == nil {
			spec.SecretEnv = map[string]string{}
		}
		spec.SecretEnv["GOG_KEYRING_PASSWORD"] = filepath.Join(target, "keyring-password")
	}
	manifest := filepath.Join(dir, "manifest.json")
	old, err := os.ReadFile(manifest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var records []installedRecord
	if len(old) > 0 {
		if err := json.Unmarshal(old, &records); err != nil {
			return err
		}
	}
	input.Env = nil // Values live only in mode-0600 files, never in the manifest.
	records = append(records, installedRecord{Request: input, Spec: spec})
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic0600(manifest, data); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	r.mu.Lock()
	r.plugins[input.ID] = &plugin{spec: spec}
	r.mu.Unlock()
	installed = true
	return nil
}

func installSecretEnv(target string, values map[string]string) (map[string]string, error) {
	dir := filepath.Join(target, "secrets")
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(values))
	for key, value := range values {
		path := filepath.Join(dir, key)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			return nil, err
		}
		paths[key] = path
	}
	return paths, nil
}

func runInstaller(ctx context.Context, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "PIP_NO_INPUT=1", "npm_config_update_notifier=false"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("package installation failed: %w: %.400s", err, output)
	}
	return nil
}

func (r *Runner) Remove(id string) error {
	if !validID(id) {
		return errors.New("invalid plugin ID")
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	r.mu.RLock()
	p := r.plugins[id]
	dir := r.stateDir
	r.mu.RUnlock()
	if p == nil || dir == "" {
		return errors.New("plugin not installed")
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	p.mu.Lock()
	if p.active > 0 || p.ready != nil {
		p.mu.Unlock()
		return errors.New("plugin is busy")
	}
	cli, stop, group := p.cli, p.stop, p.processGroup
	// Block new leases before releasing the busy-check lock. Shutdown and
	// manifest writes may take time; an old handler must not restart this child.
	p.retiring = true
	p.cli = nil
	p.stop = nil
	p.mu.Unlock()
	removed := false
	defer func() {
		if !removed {
			p.mu.Lock()
			p.retiring = false
			p.mu.Unlock()
		}
	}()
	shutdown(cli, stop, group)
	manifest := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var records []installedRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	filtered := make([]installedRecord, 0, len(records))
	for _, record := range records {
		if record.Spec.ID != id {
			filtered = append(filtered, record)
		}
	}
	if len(filtered) == len(records) {
		return errors.New("plugin not installed")
	}
	updated, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic0600(manifest, updated); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.plugins, id)
	removed = true
	r.mu.Unlock()
	return os.RemoveAll(filepath.Join(dir, "plugins", id))
}

func writeAtomic0600(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
