package app

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/google/uuid"
)

const maxComputerGiB, maxComputerVCPUs, maxComputerMemoryMiB = 1024, 32, 32768

func validComputerAllocation(v computer.ResourceAllocation) bool {
	return v.VCPUs >= 1 && v.VCPUs <= maxComputerVCPUs && v.MemoryMiB >= 512 && v.MemoryMiB <= maxComputerMemoryMiB && v.DiskGiB >= 8 && v.DiskGiB <= maxComputerGiB
}

// adminResources lets an admin read and set one account's computer size
// (vCPUs, memory, disk). A user never changes it: the workspace route refuses
// the write, and this surface requires an admin whose password is updated.
func (g *AccountGateway) adminResources(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/resources") {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/resources")
	parsed, err := uuid.Parse(id)
	legacy := id == "legacy-owner" && g.legacyComputerPhase == "worker"
	if !legacy && (err != nil || parsed.String() != id) {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	var exists bool
	if g.root.store.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=? AND legacy=?)`, id, legacy).Scan(&exists) != nil || !exists {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeErr(w, 405, "method_not_allowed", "use GET or PUT")
		return true
	}
	if g.config.AccountProvisionerSocket == "" || !filepath.IsAbs(g.config.AccountComputerSocketRoot) {
		writeErr(w, 404, "not_configured", "Bot computer is not configured")
		return true
	}
	computerID := g.computerIdentity(Account{ID: id, Legacy: legacy})
	client, err := computer.New(computer.Config{Socket: filepath.Join(g.config.AccountComputerSocketRoot, computerID, "control.sock")})
	if err != nil {
		writeErr(w, 503, "computer_unavailable", "Bot computer unavailable")
		return true
	}
	current, err := client.Resources(r.Context())
	if err != nil {
		writeErr(w, 503, "computer_unavailable", err.Error())
		return true
	}
	// Disk can only grow, and only into the host's free space.
	free := int64(-1)
	if data, e := g.computerControl(r.Context(), map[string]string{"op": "capacity"}); e == nil {
		if c, e := parseAccountCapacity(data); e == nil {
			free = c.AdmissionRemaining >> 30
		}
	}
	limitDisk := func(res *computer.Resources) {
		if free < 0 {
			return
		}
		if res.Limits == nil {
			res.Limits = map[string]int{}
		}
		if room := res.Current.DiskGiB + int(free); res.Limits["max_disk_gib"] == 0 || room < res.Limits["max_disk_gib"] {
			res.Limits["max_disk_gib"] = room
		}
	}
	if r.Method == http.MethodGet {
		limitDisk(&current)
		writeJSON(w, 200, current)
		return true
	}
	var value computer.ResourceAllocation
	if decodeStrict(r, 4096, &value) != nil || !validComputerAllocation(value) {
		writeErr(w, 400, "invalid_resources", "provide vcpus, memory_mib and disk_gib in range")
		return true
	}
	if value.DiskGiB < current.Current.DiskGiB || value.DiskGiB < current.Desired.DiskGiB {
		writeErr(w, 400, "invalid_resources", "disk can only grow")
		return true
	}
	growDisk := value.DiskGiB > current.Desired.DiskGiB
	if growDisk {
		if free >= 0 && int64(value.DiskGiB-current.Current.DiskGiB) > free {
			writeErr(w, 409, "quota_not_applied", "not enough free host disk")
			return true
		}
		if _, status, code, msg := g.applyQuota(r.Context(), id, legacy, value.DiskGiB); code != "" {
			writeErr(w, status, code, msg)
			return true
		}
	}
	if value.VCPUs != current.Desired.VCPUs || value.MemoryMiB != current.Desired.MemoryMiB || growDisk {
		out, err := client.ConfigureResources(r.Context(), value)
		if err != nil {
			writeErr(w, 400, "resource_configuration_failed", err.Error())
			return true
		}
		current = out
	}
	limitDisk(&current)
	writeJSON(w, 200, current)
	return true
}
