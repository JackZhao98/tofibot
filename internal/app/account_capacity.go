package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

type accountDiskCapacity struct {
	AccountID      string `json:"account_id"`
	ComputerID     string `json:"computer_id,omitempty"`
	Slot           int    `json:"slot"`
	QuotaBytes     int64  `json:"quota_bytes"`
	State          string `json:"state"`
	LogicalBytes   int64  `json:"logical_bytes"`
	AllocatedBytes int64  `json:"allocated_bytes"`
	PendingQuota   bool   `json:"pending_quota"`
}

type accountCapacity struct {
	ExternalPromised           int64                 `json:"external_promised_bytes"`
	ExternalAllocated          int64                 `json:"external_allocated_bytes"`
	ExternalUnallocated        int64                 `json:"external_unallocated_promises_bytes"`
	TotalBytes                 int64                 `json:"total_bytes"`
	AvailableBytes             int64                 `json:"available_bytes"`
	AllocatedBytes             int64                 `json:"allocated_bytes"`
	PromisedBytes              int64                 `json:"promised_bytes"`
	UnallocatedPromises        int64                 `json:"unallocated_promises_bytes"`
	AdmissionRemaining         int64                 `json:"admission_remaining_bytes"`
	ExternalReserved           int64                 `json:"external_reserved_bytes"`
	PerAccountInternalReserved int64                 `json:"per_account_internal_reserved_bytes"`
	InternalReserved           int64                 `json:"internal_reserved_bytes"`
	InternalAllocated          int64                 `json:"internal_allocated_bytes"`
	InternalUnallocated        int64                 `json:"internal_unallocated_reserved_bytes"`
	SafetyReserved             int64                 `json:"safety_reserved_bytes"`
	SnapshotReserved           int64                 `json:"snapshot_reserved_bytes"`
	SnapshotAllocated          int64                 `json:"snapshot_allocated_bytes"`
	SnapshotUnallocated        int64                 `json:"snapshot_unallocated_reserved_bytes"`
	Warning                    bool                  `json:"warning"`
	Accounts                   []accountDiskCapacity `json:"accounts"`
}

func parseAccountCapacity(data []byte) (accountCapacity, error) {
	var out accountCapacity
	var fields map[string]json.RawMessage
	err := json.Unmarshal(data, &fields)
	if err == nil {
		err = json.Unmarshal(data, &out)
	}
	if err != nil {
		return out, err
	}
	for _, key := range []string{"total_bytes", "available_bytes", "allocated_bytes", "promised_bytes", "unallocated_promises_bytes", "admission_remaining_bytes", "warning", "accounts"} {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return out, errors.New("incomplete capacity metrics")
		}
	}
	if out.TotalBytes <= 0 || out.AvailableBytes < 0 || out.AvailableBytes > out.TotalBytes || len(out.Accounts) > 250 {
		return out, errors.New("invalid host capacity metrics")
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(fields["accounts"], &rows) != nil {
		return out, errors.New("invalid account capacity records")
	}
	for _, row := range rows {
		if value, present := row["pending_quota"]; present && string(value) == "null" {
			return out, errors.New("invalid pending quota state")
		}
		for _, key := range []string{"account_id", "slot", "quota_bytes", "state", "logical_bytes", "allocated_bytes"} {
			if len(row[key]) == 0 || string(row[key]) == "null" {
				return out, errors.New("incomplete account capacity records")
			}
		}
	}
	ids, slots := map[string]bool{}, map[int]bool{}
	var promised, allocated int64
	for _, a := range out.Accounts {
		id, err := uuid.Parse(a.AccountID)
		if err != nil || id.String() != a.AccountID || ids[a.AccountID] || slots[a.Slot] || a.Slot < 1 || a.Slot > 250 || a.QuotaBytes < 8<<30 || a.QuotaBytes > 1024<<30 || a.LogicalBytes < 0 || a.LogicalBytes > a.QuotaBytes || a.AllocatedBytes < 0 || (a.State != "reserved" && a.State != "ready" && a.State != "disabled") || (a.State == "ready" && a.LogicalBytes == 0) {
			return out, errors.New("invalid account capacity metrics")
		}
		ids[a.AccountID], slots[a.Slot] = true, true
		promised += a.QuotaBytes
		allocated += min(a.AllocatedBytes, a.QuotaBytes)
	}
	// Reservation fields are optional for legacy brokers; present values must be consistent.
	if out.ExternalReserved < 0 || out.PerAccountInternalReserved < 0 || out.InternalReserved < 0 || out.SafetyReserved < 0 || out.PerAccountInternalReserved > (1<<62)/int64(max(1, len(out.Accounts))) || out.InternalReserved != int64(len(out.Accounts))*out.PerAccountInternalReserved || out.ExternalReserved > (1<<62)-out.InternalReserved || out.SafetyReserved != out.ExternalReserved+out.InternalReserved {
		return out, errors.New("invalid capacity reservations")
	}
	// Internal copy measurements are optional for legacy brokers, but paired.
	_, internalAllocatedPresent := fields["internal_allocated_bytes"]
	_, internalUnallocatedPresent := fields["internal_unallocated_reserved_bytes"]
	if internalAllocatedPresent || internalUnallocatedPresent {
		if !internalAllocatedPresent || !internalUnallocatedPresent || string(fields["internal_allocated_bytes"]) == "null" || string(fields["internal_unallocated_reserved_bytes"]) == "null" || out.InternalAllocated < 0 || out.InternalAllocated > out.InternalReserved || out.InternalUnallocated != out.InternalReserved-out.InternalAllocated {
			return out, errors.New("inconsistent internal capacity metrics")
		}
	} else {
		out.InternalUnallocated = out.InternalReserved
	}
	// Dynamic external measurements travel as a complete, internally consistent group.
	externalPresent := false
	for _, key := range []string{"external_promised_bytes", "external_allocated_bytes", "external_unallocated_promises_bytes"} {
		if _, ok := fields[key]; ok {
			externalPresent = true
		}
	}
	if externalPresent {
		for _, key := range []string{"external_promised_bytes", "external_allocated_bytes", "external_unallocated_promises_bytes"} {
			if len(fields[key]) == 0 || string(fields[key]) == "null" {
				return out, errors.New("incomplete external capacity metrics")
			}
		}
	}
	if out.ExternalPromised < 0 || out.ExternalPromised > 1<<60 || out.ExternalAllocated < 0 || out.ExternalAllocated > out.ExternalPromised || out.ExternalUnallocated != out.ExternalPromised-out.ExternalAllocated {
		return out, errors.New("inconsistent external capacity metrics")
	}
	// Hibernation snapshot promises are optional for legacy brokers.
	if out.SnapshotReserved < 0 || out.SnapshotReserved > 1<<60 || out.SnapshotAllocated < 0 || out.SnapshotAllocated > out.SnapshotReserved || out.SnapshotUnallocated != out.SnapshotReserved-out.SnapshotAllocated {
		return out, errors.New("inconsistent snapshot capacity metrics")
	}
	if out.PromisedBytes != promised || out.AllocatedBytes != allocated || out.UnallocatedPromises != promised-allocated || out.AdmissionRemaining > out.AvailableBytes-out.UnallocatedPromises-out.ExternalUnallocated-out.ExternalReserved-out.InternalUnallocated-out.SnapshotUnallocated {
		return out, errors.New("inconsistent capacity metrics")
	}
	return out, nil
}

func (g *AccountGateway) adminCapacity(w http.ResponseWriter, r *http.Request, a Account) {
	if a.Role != "admin" {
		writeErr(w, http.StatusForbidden, "forbidden", "admin required")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported method")
		return
	}
	data, err := g.computerControl(r.Context(), map[string]string{"op": "capacity"})
	var capacity accountCapacity
	if err == nil {
		capacity, err = parseAccountCapacity(data)
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "capacity_unavailable", "computer capacity metrics unavailable")
		return
	}
	// Broker identities remain canonical UUIDs during validation. Project the
	// owner's row to its unchanged public account ID only after validation so
	// the existing Admin quota control can find the adopted disk.
	if g.legacyComputerPhase == "worker" {
		for i := range capacity.Accounts {
			if capacity.Accounts[i].AccountID == g.config.AccountLegacyComputerUUID {
				capacity.Accounts[i].ComputerID = capacity.Accounts[i].AccountID
				capacity.Accounts[i].AccountID = "legacy-owner"
			}
		}
	}
	writeJSON(w, http.StatusOK, capacity)
}
