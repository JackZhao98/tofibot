package app

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Quota changes are central operations. A workspace cannot supply a disk path,
// manager socket, or another account's identity to its computer resource API.
func (g *AccountGateway) adminQuota(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/quota") {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/quota")
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
	if r.Method != http.MethodPatch {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return true
	}
	var in struct {
		QuotaGiB int `json:"quota_gib"`
	}
	if decodeStrict(r, 4096, &in) != nil || in.QuotaGiB < 8 || in.QuotaGiB > 1024 {
		writeErr(w, 400, "invalid_request", "quota must be an integer from 8 to 1024 GiB")
		return true
	}
	computerID := g.computerIdentity(Account{ID: id, Legacy: legacy})
	data, err := g.computerControl(r.Context(), map[string]any{"op": "quota", "account_id": computerID, "quota_gib": in.QuotaGiB})
	if err != nil {
		writeErr(w, 409, "quota_not_applied", "quota could not be verified; shrinking requires an offline workflow, growth requires capacity and confirmed computer cleanup")
		return true
	}
	var out struct {
		AccountID   string `json:"account_id"`
		QuotaBytes  int64  `json:"quota_bytes"`
		Applied     bool   `json:"applied"`
		Provisioned bool   `json:"provisioned"`
	}
	if json.Unmarshal(data, &out) != nil || out.AccountID != computerID || out.QuotaBytes != int64(in.QuotaGiB)<<30 || !out.Applied {
		writeErr(w, 503, "quota_unverified", "computer quota result unavailable; refresh capacity before retrying")
		return true
	}
	out.AccountID = id
	writeJSON(w, 200, out)
	return true
}
