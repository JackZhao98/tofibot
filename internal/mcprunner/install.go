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
	"syscall"
	"time"
)

var (
	packageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	versionName = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+_-]*$`)
	binName     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sriSHA512   = regexp.MustCompile(`^sha512-[A-Za-z0-9+/]{86}==$`)
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
	// Integrity is an optional npm SRI digest (sha512-...) of the package
	// tarball. When present the install fails unless the registry served that
	// exact artifact. Dependencies of the package are not covered.
	Integrity string `json:"integrity,omitempty"`
}

type installedRecord struct {
	Request InstallRequest `json:"request"`
	Spec    Spec           `json:"spec"`
}

func (r *Runner) SetStateDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("runner state directory must be absolute")
	}
	if r.opts.ManifestDir == "" {
		// Single-tenant: the directory is the operator's. In the multi-account Runner
		// the Hub has already created state/ and plugins/ with their owners.
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0700); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.stateDir = dir
	r.mu.Unlock()
	if _, err := r.pluginsRoot(); err != nil {
		r.mu.Lock()
		r.stateDir = ""
		r.mu.Unlock()
		return err
	}
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

// gogBinary is the bundled gogcli; tests point it at a fixture.
var gogBinary = "/usr/local/bin/gog"

func (r *Runner) Install(ctx context.Context, input InstallRequest) error {
	return r.install(ctx, input, nil)
}

// install is Install with an optional step that fills the new plugin directory
// before the plugin is recorded. A failure there removes the directory and
// leaves the manifest untouched.
func (r *Runner) install(ctx context.Context, input InstallRequest, fill func(plugin *os.Root) error) error {
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
	if input.Integrity != "" && (input.Kind != "npm" || !sriSHA512.MatchString(input.Integrity)) {
		return errors.New("integrity must be an npm sha512 digest and applies to npm packages only")
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
	if err := checkFreeSpace(dir, r.opts.MinFreeBytes); err != nil {
		return err
	}
	if err := r.admit(); err != nil {
		return err
	}
	target := filepath.Join(dir, "plugins", input.ID)
	plugins, err := r.pluginsRoot()
	if err != nil {
		return err
	}
	defer plugins.Close()
	if _, err := plugins.Lstat(input.ID); err == nil {
		return errors.New("plugin directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := plugins.Mkdir(input.ID, 0700); err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = plugins.RemoveAll(input.ID)
		}
	}()
	// Installers run as the account's uid and write into this directory.
	if err := chownIn(r.opts.Cred, plugins, input.ID); err != nil {
		return err
	}
	pr, err := plugins.OpenRoot(input.ID)
	if err != nil {
		return err
	}
	defer pr.Close()
	if err := pr.Mkdir("home", 0700); err != nil {
		return err
	}
	if err := chownIn(r.opts.Cred, pr, "home"); err != nil {
		return err
	}
	installCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var command string
	args := append([]string(nil), input.Args...)
	switch input.Kind {
	case "npm":
		// The installer runs inside the plugin directory with relative paths, so its
		// command line (visible to other uids in /proc) names no account path.
		if err := r.runInstaller(installCtx, target, "npm", "install", "--prefix", ".", "--ignore-scripts", "--no-audit", "--no-fund", "--", input.Package+"@"+input.Version); err != nil {
			return err
		}
		if input.Integrity != "" {
			if err := verifyInstalledIntegrity(pr, input.Package, input.Version, input.Integrity); err != nil {
				return err
			}
		}
		command = filepath.Join(target, "node_modules", ".bin", input.Binary)
	case "pypi":
		if err := r.runInstaller(installCtx, target, "python3", "-m", "venv", "."); err != nil {
			return err
		}
		if err := r.runInstaller(installCtx, target, "./bin/python", "-m", "pip", "install", "--disable-pip-version-check", "--no-input", input.Package+"=="+input.Version); err != nil {
			return err
		}
		command = filepath.Join(target, "bin", input.Binary)
	case "builtin_gog":
		command = gogBinary
		if len(args) == 0 {
			args = []string{"mcp", "--allow-tool", "gmail"}
		}
		password := make([]byte, 32)
		if _, err := rand.Read(password); err != nil {
			return err
		}
		if err := pr.WriteFile("keyring-password", []byte(hex.EncodeToString(password)), 0600); err != nil {
			return err
		}
	}
	if input.Kind == "builtin_gog" {
		if _, err := os.Stat(command); err != nil {
			return fmt.Errorf("installed executable missing: %w", err)
		}
	} else if _, err := pr.Lstat(strings.TrimPrefix(command, target+string(filepath.Separator))); err != nil {
		return fmt.Errorf("installed executable missing: %w", err)
	}
	spec := r.stamp(Spec{ID: input.ID, Kind: input.Kind, Command: command, Args: args, WorkDir: target, Env: map[string]string{"HOME": filepath.Join(target, "home")}})
	if input.Kind != "builtin_gog" {
		// Provenance for the connection details; operator-visible, never a secret.
		spec.Package, spec.Version = input.Package, input.Version
	}
	if len(input.Env) > 0 {
		secrets, err := installSecretEnv(pr, target, input.Env)
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
	if fill != nil {
		if err := fill(pr); err != nil {
			return err
		}
	}
	if err := chownTreeIn(r.opts.Cred, plugins, input.ID); err != nil {
		return err
	}
	manifest := r.manifestPath(dir)
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
		return err
	}
	r.mu.Lock()
	r.plugins[input.ID] = &plugin{spec: spec}
	r.mu.Unlock()
	installed = true
	return nil
}

// verifyInstalledIntegrity compares the digest npm recorded for the installed
// top-level package (npm itself verified the downloaded tarball against it)
// with the one the person pinned. Only the named package is covered.
func verifyInstalledIntegrity(stage *os.Root, pkg, version, want string) error {
	data, err := stage.ReadFile(filepath.Join("node_modules", ".package-lock.json"))
	if err != nil {
		return errors.New("package integrity could not be read after installation")
	}
	var lock struct {
		Packages map[string]struct {
			Version   string `json:"version"`
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return errors.New("package integrity could not be read after installation")
	}
	entry, ok := lock.Packages["node_modules/"+pkg]
	if !ok || entry.Version != strings.TrimPrefix(version, "v") || entry.Integrity != want {
		return errors.New("installed package does not match the pinned integrity digest")
	}
	return nil
}

func installSecretEnv(pr *os.Root, target string, values map[string]string) (map[string]string, error) {
	if err := pr.Mkdir("secrets", 0700); err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(values))
	for key, value := range values {
		if err := pr.WriteFile(filepath.Join("secrets", key), []byte(value), 0600); err != nil {
			return nil, err
		}
		paths[key] = filepath.Join(target, "secrets", key)
	}
	return paths, nil
}

func (r *Runner) runInstaller(ctx context.Context, dir, command string, args ...string) error {
	command, args, cred := confine(r.opts.Cred, r.opts.Limits, r.opts.Sandbox, command, args)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	tmp := r.opts.Tmp
	if tmp == "" {
		tmp = "/tmp"
	}
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + tmp, "TMPDIR=" + tmp, "PIP_NO_INPUT=1", "npm_config_update_notifier=false"}
	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	}
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
	// The plugin directory must still be the real directory the Runner created. A
	// symlink there means the account tried to point the Runner at someone else's
	// tree; nothing is touched.
	plugins, err := r.pluginsRoot()
	if err != nil {
		return err
	}
	defer plugins.Close()
	if info, err := plugins.Lstat(id); err == nil && !info.IsDir() {
		return errors.New("plugin directory is not a real directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	// Retire first: no new lease (health check, discovery, tool call) can start
	// from here on, so the leases left are only those already in flight.
	p.mu.Lock()
	p.retiring = true
	p.mu.Unlock()
	removed := false
	defer func() {
		if !removed {
			p.mu.Lock()
			p.retiring = false
			p.mu.Unlock()
		}
	}()
	// Leases are per request, so an idle connection holds none; a probe that was
	// mid-flight when removal began finishes within the bounded wait. Only a call
	// that outlasts it keeps the plugin.
	deadline := time.Now().Add(r.removeDrain)
	for {
		p.mu.Lock()
		if p.active == 0 && p.ready == nil {
			break
		}
		p.mu.Unlock()
		if !time.Now().Before(deadline) {
			return ErrPluginBusy
		}
		time.Sleep(10 * time.Millisecond)
	}
	cli, stop, group := p.cli, p.stop, p.processGroup
	p.cli = nil
	p.stop = nil
	p.mu.Unlock()
	shutdown(cli, stop, group)
	manifest := r.manifestPath(dir)
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
	return plugins.RemoveAll(id)
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

// InstalledRecord is one entry of a Runner manifest: the request that was
// approved and the spec it produced.
type InstalledRecord = installedRecord

// ParseRecords reads a manifest.json written by any Runner.
func ParseRecords(data []byte) ([]InstalledRecord, error) {
	var records []installedRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// UseGogBinary replaces the path of the bundled gogcli and returns a function
// that restores it. It exists for tests of code that installs the gog plugin
// on a machine without gogcli.
func UseGogBinary(path string) (restore func()) {
	old := gogBinary
	gogBinary = path
	return func() { gogBinary = old }
}
