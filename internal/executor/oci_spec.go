package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// InstallMode controls whether pip/npm/uv installs persist across runs.
// Persistent installs write to users/{uid}. Ephemeral installs write to
// runs/{runID}/overlay and die with the sandbox teardown.
type InstallMode string

const (
	InstallModePersistent InstallMode = "persistent"
	InstallModeEphemeral  InstallMode = "ephemeral"
)

// OCISpecOptions captures everything needed to generate an OCI runtime bundle
// spec for a single gVisor sandbox invocation.
type OCISpecOptions struct {
	// Command is the shell command to run inside the sandbox.
	Command string

	// UID/GID the process runs as inside the sandbox.
	UID uint32
	GID uint32

	// RunDir is the host path of this run's ephemeral dir (~/.tofi/runs/{runID}).
	// Contains tmp/, overlay/, and config.json.
	RunDir string

	// UserDir is the host path of the user's persistent dir (~/.tofi/users/{uid}).
	// Mounted at /home/tofi/.local when InstallMode=Persistent.
	UserDir string

	// PkgDir is the host path of shared packages (~/.tofi/packages). Mounted read-only.
	PkgDir string

	// InstallMode switches the /home/tofi/.local mount between persistent (UserDir)
	// and ephemeral (RunDir/overlay/local).
	InstallMode InstallMode

	// TimeoutSec is forwarded to RLIMIT_CPU. Zero means no CPU limit (use with care).
	TimeoutSec int

	// ExtraEnv are additional KEY=VALUE pairs (skill secrets, TOFI_* hints).
	ExtraEnv []string
}

const (
	sandboxHomePath  = "/home/tofi"
	sandboxLocalPath = "/home/tofi/.local"
	sandboxWorkPath  = "/work"
	sandboxCachePath = "/opt/tofi/cache"
	sandboxTmpPath   = "/tmp"
	maxFileSizeBytes = 500 * 1024 * 1024 // RLIMIT_FSIZE: 500MB per file
	maxOpenFiles     = 4096
	maxProcesses     = 512
)

// BuildOCIConfig returns the OCI config.json contents (as indented JSON bytes)
// for a single gVisor sandbox run.
func BuildOCIConfig(opts OCISpecOptions) ([]byte, error) {
	if opts.RunDir == "" {
		return nil, fmt.Errorf("RunDir is required")
	}
	if opts.PkgDir == "" {
		return nil, fmt.Errorf("PkgDir is required")
	}
	if opts.Command == "" {
		return nil, fmt.Errorf("Command is required")
	}

	localSource := opts.UserDir
	if opts.InstallMode == InstallModeEphemeral {
		localSource = filepath.Join(opts.RunDir, "overlay", "local")
	}
	if localSource == "" {
		return nil, fmt.Errorf("persistent mode requires UserDir")
	}

	venvPath := sandboxLocalPath + "/python/venvs/default"

	env := []string{
		"HOME=" + sandboxHomePath,
		"PATH=" + sandboxLocalPath + "/bin:" + sandboxLocalPath + "/python/venvs/default/bin:/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=" + sandboxTmpPath,
		"LANG=en_US.UTF-8",
		"TERM=dumb",
		"PIP_CACHE_DIR=" + sandboxCachePath + "/cache/pip",
		"UV_CACHE_DIR=" + sandboxCachePath + "/cache/uv",
		"NPM_CONFIG_CACHE=" + sandboxCachePath + "/cache/npm",
		"PIP_REQUIRE_VIRTUALENV=true",
		"VIRTUAL_ENV=" + venvPath,
		"PYTHONPATH=" + venvPath + "/lib",
		"UV_TOOL_DIR=" + sandboxLocalPath + "/uv/tools",
		"UV_TOOL_BIN_DIR=" + sandboxLocalPath + "/bin",
		"NPM_CONFIG_PREFIX=" + sandboxLocalPath + "/npm",
		"XDG_CONFIG_HOME=" + sandboxLocalPath + "/config",
		"XDG_CACHE_HOME=" + sandboxLocalPath + "/cache",
		"TOFI_INSTALL_MODE=" + string(opts.InstallMode),
	}
	env = append(env, opts.ExtraEnv...)

	rlimits := []map[string]any{
		{"type": "RLIMIT_NPROC", "hard": maxProcesses, "soft": maxProcesses},
		{"type": "RLIMIT_NOFILE", "hard": maxOpenFiles, "soft": maxOpenFiles},
		{"type": "RLIMIT_FSIZE", "hard": maxFileSizeBytes, "soft": maxFileSizeBytes},
	}
	if opts.TimeoutSec > 0 {
		rlimits = append(rlimits, map[string]any{
			"type": "RLIMIT_CPU",
			"hard": opts.TimeoutSec,
			"soft": opts.TimeoutSec,
		})
	}

	mounts := []map[string]any{
		{"destination": "/proc", "type": "proc", "source": "proc"},
		{
			"destination": "/dev",
			"type":        "tmpfs",
			"source":      "tmpfs",
			"options":     []string{"nosuid", "noexec", "size=64m"},
		},
		{
			"destination": sandboxCachePath,
			"type":        "bind",
			"source":      opts.PkgDir,
			"options":     []string{"bind", "ro"},
		},
		{
			"destination": sandboxLocalPath,
			"type":        "bind",
			"source":      localSource,
			"options":     []string{"bind", "rw"},
		},
		{
			"destination": sandboxWorkPath,
			"type":        "bind",
			"source":      opts.RunDir,
			"options":     []string{"bind", "rw"},
		},
		{
			"destination": sandboxTmpPath,
			"type":        "bind",
			"source":      filepath.Join(opts.RunDir, "tmp"),
			"options":     []string{"bind", "rw"},
		},
	}
	// Host paths that vary by distro — /lib64 is x86_64-only, some slim
	// containers drop /sbin. Only mount what actually exists; bind-mounting
	// a non-existent source makes runsc abort the sandbox boot.
	//
	// /run/systemd/resolve is included so glibc can follow the
	// /etc/resolv.conf symlink (Ubuntu 24.04's default). Without it DNS
	// resolution inside the sandbox fails with EAI_AGAIN even though we
	// share the host network namespace.
	for _, hostPath := range []string{"/usr", "/bin", "/lib", "/lib64", "/sbin", "/etc", "/run/systemd/resolve"} {
		if _, err := os.Stat(hostPath); err != nil {
			continue
		}
		mounts = append(mounts, map[string]any{
			"destination": hostPath,
			"type":        "bind",
			"source":      hostPath,
			"options":     []string{"bind", "ro"},
		})
	}

	spec := map[string]any{
		"ociVersion": "1.0.2",
		"process": map[string]any{
			"terminal": false,
			"user":     map[string]any{"uid": opts.UID, "gid": opts.GID},
			"args":     []string{"/bin/sh", "-c", opts.Command},
			"env":      env,
			"cwd":      sandboxWorkPath,
			"rlimits":  rlimits,
			"capabilities": map[string]any{
				"bounding":    []string{},
				"effective":   []string{},
				"inheritable": []string{},
				"permitted":   []string{},
				"ambient":     []string{},
			},
			"noNewPrivileges": true,
		},
		"root": map[string]any{
			"path":     "rootfs",
			"readonly": true,
		},
		"hostname": "tofi-sandbox",
		"mounts":   mounts,
		"linux": map[string]any{
			"namespaces": []map[string]any{
				{"type": "pid"},
				{"type": "ipc"},
				{"type": "uts"},
				{"type": "mount"},
				// network namespace intentionally omitted — runsc runs
				// with --network=host (rootless mode requirement), so the
				// sandbox shares the host network ns. When the egress
				// allowlist branch lands we flip this back on with
				// --network=sandbox.
			},
		},
	}

	return json.MarshalIndent(spec, "", "  ")
}
