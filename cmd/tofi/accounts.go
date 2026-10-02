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

func newConfiguredServer(config app.Config, getenv func(string) string) (applicationServer, error) {
	enabled := getenv("TOFI_MULTI_ACCOUNT")
	if enabled == "" || enabled == "0" {
		return app.NewServer(config)
	}
	if enabled != "1" {
		return nil, fmt.Errorf("TOFI_MULTI_ACCOUNT must be 0 or 1")
	}
	if !config.OwnerAuth || !filepath.IsAbs(config.DataDir) {
		return nil, fmt.Errorf("multi-account requires owner authentication and an absolute data directory")
	}
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
