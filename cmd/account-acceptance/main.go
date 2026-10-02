// Isolated local acceptance only: creates disposable state, never loads production config.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JackZhao98/tofibot/internal/app"
)

func main() {
	dir := os.Getenv("TOFI_ACCEPTANCE_DATA_DIR")
	cleanup := false
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "tofi-account-acceptance-")
		if err != nil {
			log.Fatal(err)
		}
		cleanup = true
	} else {
		abs, err := filepath.Abs(dir)
		if err != nil {
			log.Fatal(err)
		}
		tmp, err := filepath.EvalSymlinks(os.TempDir())
		if err != nil {
			log.Fatal(err)
		}
		if filepath.Dir(abs) != tmp || !strings.HasPrefix(filepath.Base(abs), "tofi-account-acceptance-") {
			log.Fatal("TOFI_ACCEPTANCE_DATA_DIR must be a dedicated tofi-account-acceptance-* directory directly below the system temp directory")
		}
		if err := os.Mkdir(abs, 0700); err != nil && !os.IsExist(err) {
			log.Fatal(err)
		}
		dir, err = filepath.EvalSymlinks(abs)
		if err != nil || dir != abs {
			log.Fatal("TOFI_ACCEPTANCE_DATA_DIR must be a real directory, not a symlink")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			log.Fatal(err)
		}
	}
	if cleanup {
		defer os.RemoveAll(dir)
	}
	listen := os.Getenv("TOFI_ACCEPTANCE_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8349"
	}
	host, _, splitErr := net.SplitHostPort(listen)
	addr, parseErr := netip.ParseAddr(strings.Trim(host, "[]"))
	if splitErr != nil || parseErr != nil || !addr.IsLoopback() {
		log.Fatal("TOFI_ACCEPTANCE_LISTEN must use a loopback IP and port")
	}
	uiDir := os.Getenv("TOFI_ACCEPTANCE_UI_DIR")
	if uiDir == "" {
		uiDir = "./ui/dist"
	}
	uiDir, err := filepath.Abs(uiDir)
	if err != nil {
		log.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(uiDir, "index.html")); err != nil || info.IsDir() {
		log.Fatal("TOFI_ACCEPTANCE_UI_DIR must point to a built UI containing index.html")
	}
	// The first fixture account is legacy-shaped, but must never inherit the
	// host's personal computer socket or external integration defaults.
	config := app.Config{DataDir: dir, UIDir: uiDir, Environment: "acceptance", IsolatedWorkspace: true, OwnerAuth: true, OwnerAllowLoopbackHTTP: true, Listen: listen, PublicOrigin: "http://" + listen}
	if os.Getenv("TOFI_ACCEPTANCE_REAL_WORKER") == "1" {
		config.AccountProvisionerSocket = os.Getenv("TOFI_ACCEPTANCE_BROKER_SOCKET")
		config.AccountComputerSocketRoot = os.Getenv("TOFI_ACCEPTANCE_SOCKET_ROOT")
		config.AccountComputerDiskGiB = 8
		if !filepath.IsAbs(config.AccountProvisionerSocket) || !filepath.IsAbs(config.AccountComputerSocketRoot) || !strings.HasPrefix(config.AccountProvisionerSocket, dir+string(filepath.Separator)) || !strings.HasPrefix(config.AccountComputerSocketRoot, dir+string(filepath.Separator)) {
			log.Fatal("real Worker sockets must be absolute paths below the dedicated acceptance data directory")
		}
		approvalPath := os.Getenv("TOFI_ACCEPTANCE_RUNTIME_APPROVAL")
		ids := strings.Split(os.Getenv("TOFI_ACCEPTANCE_ACCOUNT_IDS"), ",")
		if approvalPath == "" || len(ids) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
			log.Fatal("real Worker requires an approval packet and exactly two account UUIDs")
		}
		var approval struct {
			PolicyReviewed  bool     `json:"policy_reviewed"`
			ProfileReviewed bool     `json:"profile_reviewed"`
			ProfileName     string   `json:"profile_name"`
			AccountIDs      []string `json:"account_ids"`
			Capacity        struct {
				CPUCores       int    `json:"cpu_cores"`
				MemoryMiB      int    `json:"memory_mib"`
				DiskBytes      int64  `json:"disk_bytes"`
				AvailableBytes int64  `json:"available_bytes"`
				HeadroomBytes  int64  `json:"headroom_bytes"`
				CheckedAt      string `json:"checked_at"`
			} `json:"capacity"`
		}
		contents, err := os.ReadFile(approvalPath)
		if err != nil || json.Unmarshal(contents, &approval) != nil {
			log.Fatal("cannot read runtime approval packet")
		}
		checkedAt, timeErr := time.Parse(time.RFC3339, approval.Capacity.CheckedAt)
		if !approval.PolicyReviewed || !approval.ProfileReviewed || approval.ProfileName == "" || approval.ProfileName != os.Getenv("TOFI_ACCEPTANCE_PROFILE_NAME") || len(approval.AccountIDs) != 2 || approval.AccountIDs[0] != ids[0] || approval.AccountIDs[1] != ids[1] || approval.Capacity.CPUCores < 2 || approval.Capacity.MemoryMiB < 2048 || approval.Capacity.DiskBytes < 32<<30 || approval.Capacity.HeadroomBytes <= 0 || approval.Capacity.AvailableBytes < approval.Capacity.DiskBytes+approval.Capacity.HeadroomBytes || timeErr != nil || time.Since(checkedAt) < 0 || time.Since(checkedAt) > 5*time.Minute {
			log.Fatal("approval packet must match both account UUIDs and named temporary profile, with a <=5m snapshot covering 2 CPUs, 2048 MiB, 32 GiB disk, and positive headroom")
		}
		if info, err := os.Lstat(config.AccountProvisionerSocket); err != nil || info.Mode()&os.ModeSocket == 0 {
			log.Fatal("real Worker broker socket is unavailable")
		}
		if info, err := os.Lstat(config.AccountComputerSocketRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			log.Fatal("real Worker account socket root must be a real directory")
		}
	} else if os.Getenv("TOFI_ACCEPTANCE_REAL_WORKER") != "" && os.Getenv("TOFI_ACCEPTANCE_REAL_WORKER") != "0" {
		log.Fatal("TOFI_ACCEPTANCE_REAL_WORKER must be 0 or 1")
	}
	g, err := app.NewAccountGateway(config)
	if err != nil {
		log.Fatal(err)
	}
	defer g.Close()
	log.Printf("isolated account acceptance on %s", listen)
	server := &http.Server{Addr: listen, Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-signals.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("isolated API graceful shutdown: %v", err)
			_ = server.Close()
		}
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("isolated API listener stopped: %v", err)
		}
	}
}
