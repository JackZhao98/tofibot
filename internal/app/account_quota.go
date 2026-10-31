package app

import (
	"context"
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
		QuotaGiB *int `json:"quota_gib"`
		// ServerBytesLimit: absent = unchanged, null = unlimited, number = limit.
		ServerBytesLimit json.RawMessage `json:"server_bytes_limit"`
	}
	if decodeStrict(r, 4096, &in) != nil || (in.QuotaGiB == nil && len(in.ServerBytesLimit) == 0) {
		writeErr(w, 400, "invalid_request", "provide quota_gib, server_bytes_limit, or both")
		return true
	}
	if in.QuotaGiB != nil && (*in.QuotaGiB < 8 || *in.QuotaGiB > 1024) {
		writeErr(w, 400, "invalid_request", "quota must be an integer from 8 to 1024 GiB")
		return true
	}
	var newLimit *int64
	limitSet := len(in.ServerBytesLimit) > 0
	if limitSet && string(in.ServerBytesLimit) != "null" {
		var n int64
		if json.Unmarshal(in.ServerBytesLimit, &n) != nil || n < 0 || n > 1<<60 {
			writeErr(w, 400, "invalid_request", "server_bytes_limit must be null (unlimited) or a byte count of 0 or more")
			return true
		}
		newLimit = &n
	}
	var out struct {
		AccountID        string `json:"account_id"`
		QuotaBytes       int64  `json:"quota_bytes,omitempty"`
		Applied          bool   `json:"applied,omitempty"`
		Provisioned      bool   `json:"provisioned,omitempty"`
		ServerBytesLimit *int64 `json:"server_bytes_limit"`
	}
	if in.QuotaGiB != nil {
		q, status, code, msg := g.applyQuota(r.Context(), id, legacy, *in.QuotaGiB)
		if code != "" {
			writeErr(w, status, code, msg)
			return true
		}
		out.QuotaBytes, out.Applied, out.Provisioned = q.QuotaBytes, q.Applied, q.Provisioned
	}
	if limitSet {
		var v any
		if newLimit != nil {
			v = *newLimit
		}
		if _, err = g.root.store.db.ExecContext(r.Context(), `INSERT INTO account_storage_quota(account_id,server_bytes_limit) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET server_bytes_limit=excluded.server_bytes_limit`, id, v); err != nil {
			writeErr(w, 500, "storage", "cannot save the storage limit")
			return true
		}
	}
	out.ServerBytesLimit = g.storageLimitFor(id)
	out.AccountID = id
	writeJSON(w, 200, out)
	return true
}

type quotaResult struct {
	AccountID   string `json:"account_id"`
	QuotaBytes  int64  `json:"quota_bytes"`
	Applied     bool   `json:"applied"`
	Provisioned bool   `json:"provisioned"`
}

// applyQuota asks the broker for a new disk size and verifies the answer. A non-empty code is the error to return.
func (g *AccountGateway) applyQuota(ctx context.Context, id string, legacy bool, gib int) (out quotaResult, status int, code, msg string) {
	computerID := g.computerIdentity(Account{ID: id, Legacy: legacy})
	data, err := g.computerControl(ctx, map[string]any{"op": "quota", "account_id": computerID, "quota_gib": gib})
	if err != nil {
		return out, 409, "quota_not_applied", "quota could not be verified; shrinking requires an offline workflow, growth requires capacity and confirmed Bot computer cleanup"
	}
	if json.Unmarshal(data, &out) != nil || out.AccountID != computerID || out.QuotaBytes != int64(gib)<<30 || !out.Applied {
		return out, 503, "quota_unverified", "Bot computer quota result unavailable; refresh capacity before retrying"
	}
	return out, 0, "", ""
}
