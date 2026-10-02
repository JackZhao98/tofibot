package executor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"

	"github.com/JackZhao98/tofibot/internal/paths"
)

var (
	factoryGOOS              = runtime.GOOS
	factoryLookPath          = exec.LookPath
	factoryNewGvisorExecutor = NewGvisorExecutor
)

// NewExecutor picks the right executor for this host. On Linux with
// `runsc` in PATH, returns a GvisorExecutor. DevExecutor is allowed only when
// TOFI_DEV=1; production must fail closed instead of silently running user
// code directly on the host.
//
// An empty homeDir falls back to paths.TofiHome().
func NewExecutor(homeDir string) Executor {
	exec, err := NewExecutorE(homeDir)
	if err != nil {
		log.Fatal(err)
	}
	return exec
}

func NewExecutorE(homeDir string) (Executor, error) {
	if homeDir == "" {
		homeDir = paths.TofiHome()
	}
	if factoryGOOS == "linux" {
		if runsc, err := factoryLookPath("runsc"); err == nil {
			g, err := factoryNewGvisorExecutor(homeDir, runsc)
			if err == nil {
				log.Printf("🛡️  [sandbox] gVisor executor active (runsc=%s)", runsc)
				return g, nil
			}
			if os.Getenv("TOFI_DEV") == "1" {
				log.Printf("⚠️  [sandbox] runsc found but init failed: %v — falling back to DevExecutor because TOFI_DEV=1", err)
				logDevExecutorBanner()
				return NewDevExecutor(homeDir), nil
			}
			return nil, fmt.Errorf("gVisor/runsc found at %s but executor initialization failed: %w", runsc, err)
		}
	}
	if os.Getenv("TOFI_DEV") != "1" {
		return nil, fmt.Errorf("gVisor/runsc is required outside local development. Install runsc or set TOFI_DEV=1 for local-only insecure execution")
	}
	logDevExecutorBanner()
	return NewDevExecutor(homeDir), nil
}

func logDevExecutorBanner() {
	log.Println("┌─────────────────────────────────────────────────────────────┐")
	log.Println("│  ⚠️  INSECURE DEV EXECUTOR                                  │")
	log.Println("│  runsc/gVisor unavailable — sandbox is cmd.Dir only.        │")
	log.Println("│  NEVER run this build in production with real users.        │")
	log.Println("└─────────────────────────────────────────────────────────────┘")
}
