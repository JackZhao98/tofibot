package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/JackZhao98/tofibot/internal/app"
)

type applicationServer interface {
	Handler() http.Handler
	Listen() string
	Close() error
}

// computerBackend resolves TOFI_COMPUTER_BACKEND (none | kvm). Unset means
// kvm when any computer socket is configured and none otherwise, so an
// install made before the variable existed behaves as it did.
func computerBackend(value string, configured bool) (string, error) {
	switch value {
	case "":
		if configured {
			return app.ComputerBackendKVM, nil
		}
		return app.ComputerBackendNone, nil
	case app.ComputerBackendNone, app.ComputerBackendKVM:
		return value, nil
	}
	return "", fmt.Errorf("TOFI_COMPUTER_BACKEND must be none or kvm")
}

func newConfiguredServer(config app.Config, getenv func(string) string) (applicationServer, error) {
	enabled := getenv("TOFI_MULTI_ACCOUNT")
	if enabled != "" && enabled != "0" && enabled != "1" {
		return nil, fmt.Errorf("TOFI_MULTI_ACCOUNT must be 0 or 1")
	}
	multi := enabled == "1"
	var configured bool
	if multi {
		configured = getenv("TOFI_ACCOUNT_PROVISIONER_SOCKET") != "" || getenv("TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT") != "" || getenv("TOFI_ACCOUNT_COMPUTER_DISK_GIB") != ""
	} else {
		configured = config.ComputerSocket != "" || getenv("TOFI_COMPUTER_SOCKET") != ""
	}
	backend, err := computerBackend(getenv("TOFI_COMPUTER_BACKEND"), configured)
	if err != nil {
		return nil, err
	}
	config.ComputerBackend = backend
	if backend == app.ComputerBackendNone {
		config.ComputerSocket = ""
	}
	if !multi {
		return app.NewServer(config)
	}
	if !config.OwnerAuth || !filepath.IsAbs(config.DataDir) {
		return nil, fmt.Errorf("multi-account requires owner authentication and an absolute data directory")
	}
	if backend == app.ComputerBackendKVM {
		config.AccountProvisionerSocket = getenv("TOFI_ACCOUNT_PROVISIONER_SOCKET")
		config.AccountComputerSocketRoot = getenv("TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT")
		if !filepath.IsAbs(config.AccountProvisionerSocket) || !filepath.IsAbs(config.AccountComputerSocketRoot) {
			return nil, fmt.Errorf("multi-account requires fixed absolute Worker socket paths")
		}
		quota, err := strconv.Atoi(getenv("TOFI_ACCOUNT_COMPUTER_DISK_GIB"))
		if err != nil || quota < 8 || quota > 1024 {
			return nil, fmt.Errorf("TOFI_ACCOUNT_COMPUTER_DISK_GIB must be 8..1024")
		}
		config.AccountComputerDiskGiB = quota
		config.AccountLegacyComputerUUID = getenv("TOFI_ACCOUNT_LEGACY_COMPUTER_UUID")
	}
	// The Runner container is independent of the computer: it is configured with either backend.
	config.RunnerGatewayURL = getenv("TOFI_RUNNER_URL")
	config.RunnerMasterKeyFile = getenv("TOFI_RUNNER_MASTER_KEY_FILE")
	if (config.RunnerGatewayURL == "") != (config.RunnerMasterKeyFile == "") || (config.RunnerGatewayURL != "" && !filepath.IsAbs(config.RunnerMasterKeyFile)) {
		return nil, fmt.Errorf("TOFI_RUNNER_URL and an absolute TOFI_RUNNER_MASTER_KEY_FILE must be set together")
	}
	maintenance := getenv("TOFI_ACCOUNT_MAINTENANCE")
	if maintenance != "" && maintenance != "0" && maintenance != "1" {
		return nil, fmt.Errorf("TOFI_ACCOUNT_MAINTENANCE must be 0 or 1")
	}
	config.AccountMaintenance = maintenance == "1"
	if value := getenv("TOFI_ACCOUNT_DB_MAX_BYTES"); value != "" {
		limit, err := strconv.ParseInt(value, 10, 64)
		if err != nil || limit <= 0 {
			return nil, fmt.Errorf("TOFI_ACCOUNT_DB_MAX_BYTES must be positive")
		}
		config.AccountDBMaxBytes = limit
	}
	return app.NewAccountGateway(config)
}
