// Package paths provides a single source of truth for all Tofi filesystem paths.
//
// Terminology:
//
//	$HOME       = user's OS home directory (e.g., /Users/example)
//	TOFI_HOME   = $HOME/.tofi — the root of all Tofi data
//
// All paths in Tofi MUST be derived from these two roots via this package.
// Never manually join ".tofi" to anything — use paths.TofiHome() instead.
package paths

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	once     sync.Once
	tofiHome string
)

// TofiHome returns the root Tofi data directory (~/.tofi).
// Can be overridden via TOFI_HOME environment variable.
func TofiHome() string {
	once.Do(func() {
		if env := os.Getenv("TOFI_HOME"); env != "" {
			tofiHome = env
		} else {
			home, _ := os.UserHomeDir()
			tofiHome = filepath.Join(home, ".tofi")
		}
	})
	return tofiHome
}

// SetTofiHome overrides the default for testing or custom deployments.
// Must be called before any path function. Not goroutine-safe with TofiHome().
func SetTofiHome(dir string) {
	tofiHome = dir
	// Reset once so it doesn't re-initialize
	once = sync.Once{}
	once.Do(func() {}) // mark as done
}

// ──────────────────────────────────────────────────────────────
// Top-level directories under TOFI_HOME
// ──────────────────────────────────────────────────────────────

// DB returns the path to the SQLite database.
func DB() string {
	return filepath.Join(TofiHome(), "tofi.db")
}

// Config returns the path to the config file.
func Config() string {
	return filepath.Join(TofiHome(), "config.yaml")
}

// LogsDir returns the directory for log files.
func LogsDir() string {
	return filepath.Join(TofiHome(), "logs")
}

// SkillsDir returns the global skills directory.
func SkillsDir() string {
	return filepath.Join(TofiHome(), "skills")
}

// PackagesDir returns the shared packages directory (pip, npm, etc.).
func PackagesDir() string {
	return filepath.Join(TofiHome(), "packages")
}

// PackagesRuntimeDir returns the shared runtime directory.
func PackagesRuntimeDir() string {
	return filepath.Join(PackagesDir(), "runtime")
}

// PackagesArtifactsDir returns the shared content-addressed artifact store.
func PackagesArtifactsDir() string {
	return filepath.Join(PackagesDir(), "artifacts")
}

// PackagesCacheDir returns the shared package-manager cache root.
func PackagesCacheDir() string {
	return filepath.Join(PackagesDir(), "cache")
}

// PackagesPipCacheDir returns the shared pip cache directory.
func PackagesPipCacheDir() string {
	return filepath.Join(PackagesCacheDir(), "pip")
}

// PackagesUVCacheDir returns the shared uv cache directory.
func PackagesUVCacheDir() string {
	return filepath.Join(PackagesCacheDir(), "uv")
}

// PackagesNPMCacheDir returns the shared npm cache directory.
func PackagesNPMCacheDir() string {
	return filepath.Join(PackagesCacheDir(), "npm")
}

// ──────────────────────────────────────────────────────────────
// Per-user directories under TOFI_HOME/users/{userID}/
// ──────────────────────────────────────────────────────────────

// UserDir returns the root directory for a specific user.
func UserDir(userID string) string {
	return filepath.Join(TofiHome(), "users", userID)
}

// UserChatDir returns the chat sessions directory for a user.
func UserChatDir(userID string) string {
	return filepath.Join(UserDir(userID), "chat")
}

// UserSkillsDir returns the skills directory for a user.
func UserSkillsDir(userID string) string {
	return filepath.Join(UserDir(userID), "skills")
}

// UserSandboxDir returns the sandbox root for a user's task execution.
func UserSandboxDir(userID, taskID string) string {
	return filepath.Join(UserDir(userID), "sandbox", taskID)
}

// UserMemoryDir returns the memory directory for a user.
func UserMemoryDir(userID string) string {
	return filepath.Join(UserDir(userID), "memory")
}

// UserTranscriptsDir returns the transcript directory for crash recovery.
func UserTranscriptsDir(userID string) string {
	return filepath.Join(UserDir(userID), "transcripts")
}

// UserUploadsDir returns the uploads directory for a user.
func UserUploadsDir(userID string) string {
	return filepath.Join(UserDir(userID), "uploads")
}

// UserArtifactsDir returns the artifacts directory for a user.
func UserArtifactsDir(userID string) string {
	return filepath.Join(UserDir(userID), "artifacts")
}

// UserBinDir returns the private binary directory for a user.
func UserBinDir(userID string) string {
	return filepath.Join(UserDir(userID), "bin")
}

// UserConfigDir returns the private config directory for a user.
func UserConfigDir(userID string) string {
	return filepath.Join(UserDir(userID), "config")
}

// UserCacheDir returns the private cache root for a user.
func UserCacheDir(userID string) string {
	return filepath.Join(UserDir(userID), "cache")
}

// UserStateDir returns the private state root for a user.
func UserStateDir(userID string) string {
	return filepath.Join(UserDir(userID), "state")
}

// UserToolRunStateDir returns the private last-run metadata directory for user tools.
func UserToolRunStateDir(userID string) string {
	return filepath.Join(UserStateDir(userID), "tool-runs")
}

// UserPythonDir returns the private Python root for a user.
func UserPythonDir(userID string) string {
	return filepath.Join(UserDir(userID), "python")
}

// UserPythonVenvsDir returns the private Python venv root for a user.
func UserPythonVenvsDir(userID string) string {
	return filepath.Join(UserPythonDir(userID), "venvs")
}

// UserDefaultPythonVenvDir returns the default Python venv path for a user.
func UserDefaultPythonVenvDir(userID string) string {
	return filepath.Join(UserPythonVenvsDir(userID), "default")
}

// UserNPMDir returns the private npm prefix for a user.
func UserNPMDir(userID string) string {
	return filepath.Join(UserDir(userID), "npm")
}

// UserUVDir returns the private uv data root for a user.
func UserUVDir(userID string) string {
	return filepath.Join(UserDir(userID), "uv")
}

// UserUVToolsDir returns the private uv tool environments root for a user.
func UserUVToolsDir(userID string) string {
	return filepath.Join(UserUVDir(userID), "tools")
}

// ──────────────────────────────────────────────────────────────
// Scoped chat directories (agents/agents have their own chat dirs)
// ──────────────────────────────────────────────────────────────

// ScopedChatDir returns the chat directory for a specific scope (agent/app).
func ScopedChatDir(userID, scope string) string {
	if scope == "" {
		return UserChatDir(userID)
	}
	return filepath.Join(UserDir(userID), "agents", scope, "chat")
}

// ──────────────────────────────────────────────────────────────
// Skill-specific paths
// ──────────────────────────────────────────────────────────────

// GlobalSkillDir returns the directory for a specific global skill.
func GlobalSkillDir(skillName string) string {
	return filepath.Join(SkillsDir(), skillName)
}

// UserSkillDir returns the directory for a user's specific skill.
func UserSkillDir(userID, skillName string) string {
	return filepath.Join(UserSkillsDir(userID), skillName)
}

// ──────────────────────────────────────────────────────────────
// Python virtual environment (isolated from system Python)
// ──────────────────────────────────────────────────────────────

// PythonVenvDir returns the Python venv directory for system skill dependencies.
func PythonVenvDir() string {
	return filepath.Join(PackagesDir(), "pyenv")
}

// PythonVenvBin returns the Python binary inside the venv.
func PythonVenvBin() string {
	return filepath.Join(PythonVenvDir(), "bin", "python3")
}

// PythonVenvPip returns the pip binary inside the venv.
func PythonVenvPip() string {
	return filepath.Join(PythonVenvDir(), "bin", "pip")
}
