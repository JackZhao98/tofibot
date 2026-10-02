package executor

import (
	"context"
	"fmt"
	"regexp"
)

// QuotaGate decides whether a user's next install should be persistent
// or ephemeral, and refreshes the user's usage inventory afterwards.
// The executor layer is interface-only; implementations live in the
// server package where the DB is accessible.
type QuotaGate interface {
	Decide(userID string) QuotaDecision
	BumpInventory(userID string)
}

// QuotaDecision is the output of a QuotaGate consult. Mode selects the
// sandbox mount for /home/tofi/.local; Hint is an optional note surfaced
// to the AI via the tool result meta block.
type QuotaDecision struct {
	Mode         InstallMode
	DiskUsagePct int
	Hint         string
}

// installPrefixPattern recognises `pip install`, `npm install`, `uv add`,
// `pnpm install` and common variants at the start of a command or right
// after a shell chaining operator (;, &, |, &&, ||).
var installPrefixPattern = regexp.MustCompile(
	`(?:^|[;&|]\s*|&&\s*|\|\|\s*)` +
		`(pip\s+install|pip3\s+install|python\s+-m\s+pip\s+install|python3\s+-m\s+pip\s+install|` +
		`uv\s+pip\s+install|uv\s+add|uv\s+tool\s+install|` +
		`npm\s+install|npm\s+i(\s|$)|pnpm\s+install|pnpm\s+add|yarn\s+add)`,
)

// IsInstallCommand returns true for shell commands that install packages.
// False positives are OK (worst case an extra BumpInventory call); false
// negatives mean the quota gate is bypassed for that call.
func IsInstallCommand(cmd string) bool {
	return installPrefixPattern.MatchString(cmd)
}

// WithQuota decorates an executor so every install command is routed
// through a QuotaGate. When gate or userID is empty, returns inner
// unchanged — quota enforcement is opt-in per request.
func WithQuota(inner Executor, userID string, gate QuotaGate) Executor {
	if gate == nil || userID == "" {
		return inner
	}
	return &quotaExecutor{inner: inner, userID: userID, gate: gate}
}

type quotaExecutor struct {
	inner  Executor
	userID string
	gate   QuotaGate
}

func (q *quotaExecutor) CreateSandbox(cfg SandboxConfig) (string, error) {
	return q.inner.CreateSandbox(cfg)
}

func (q *quotaExecutor) Cleanup(sandboxPath string) {
	q.inner.Cleanup(sandboxPath)
}

func (q *quotaExecutor) Execute(ctx context.Context, sandboxPath, userDir, command string, timeoutSec int, env map[string]string) (string, error) {
	install := IsInstallCommand(command)

	// Copy env so we don't mutate the caller's map.
	nextEnv := make(map[string]string, len(env)+1)
	for k, v := range env {
		nextEnv[k] = v
	}

	var decision QuotaDecision
	if install {
		decision = q.gate.Decide(q.userID)
		nextEnv["TOFI_INSTALL_MODE"] = string(decision.Mode)
	}

	output, err := q.inner.Execute(ctx, sandboxPath, userDir, command, timeoutSec, nextEnv)

	if install {
		go q.gate.BumpInventory(q.userID)
		output = appendQuotaMeta(output, decision)
	}
	return output, err
}

// appendQuotaMeta tacks a small structured block onto tool output so the
// AI can read disk_pct + install_mode + hint without any prompt injection.
// Format is stable enough for the AI to parse but explicitly labelled so
// humans reading transcripts aren't confused.
func appendQuotaMeta(output string, d QuotaDecision) string {
	meta := fmt.Sprintf("\n[tofi_meta]\ndisk_pct: %d\ninstall_mode: %s\n",
		d.DiskUsagePct, d.Mode)
	if d.Hint != "" {
		meta += "hint: " + d.Hint + "\n"
	}
	meta += "[/tofi_meta]"
	return output + meta
}
