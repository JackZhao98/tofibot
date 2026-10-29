package app

// Gateway side of storage accounting (computer-decoupling S3). The root store
// holds the per-account limit and the daily snapshots; each workspace keeps its
// own live counters (storage_accounting.go).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	accountStorageDDL = `CREATE TABLE IF NOT EXISTS account_storage(
 account_id TEXT NOT NULL, measured_at TEXT NOT NULL,
 server_bytes INTEGER NOT NULL, server_physical_bytes INTEGER NOT NULL,
 computer_logical_bytes INTEGER, computer_allocated_bytes INTEGER,
 detail TEXT NOT NULL, PRIMARY KEY(account_id,measured_at));
 CREATE TABLE IF NOT EXISTS account_storage_quota(
 account_id TEXT PRIMARY KEY, server_bytes_limit INTEGER);`
	storageSnapshotKeepDays = 90
	computerUsageTTL        = 60 * time.Second
)

// storageLimitFor reads the account's server byte limit; nil = unlimited. A
// read error fails open (no refusal) so a root-store hiccup cannot block files.
func (g *AccountGateway) storageLimitFor(id string) *int64 {
	var v sql.NullInt64
	if err := g.root.store.db.QueryRow(`SELECT server_bytes_limit FROM account_storage_quota WHERE account_id=?`, id).Scan(&v); err != nil || !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// snapshotDetail is the JSON kept in account_storage.detail.
type snapshotDetail struct {
	Classes          []StorageClass `json:"classes"`
	FilesBytes       int64          `json:"files_bytes"`
	DatabaseBytes    int64          `json:"database_bytes"`
	PendingCopyCount int            `json:"pending_copy_count"`
	StorageDetail
}

// recordStorageSnapshot keeps one row per account per UTC day (the latest of
// that day) and drops rows older than 90 days.
func (g *AccountGateway) recordStorageSnapshot(a Account, rep StorageReport) {
	var comp *ComputerUsage
	if g.config.AccountProvisionerSocket != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		comp = g.computerUsageFor(ctx, a)
		cancel()
	}
	detail, _ := json.Marshal(snapshotDetail{Classes: rep.Classes, FilesBytes: rep.FilesBytes, DatabaseBytes: rep.DatabaseBytes, PendingCopyCount: rep.PendingCopyCount, StorageDetail: rep.Detail})
	var logical, allocated any
	if comp != nil {
		logical, allocated = comp.LogicalBytes, comp.AllocatedBytes
	}
	day := rep.MeasuredAt
	if len(day) >= 10 {
		day = day[:10]
	}
	tx, err := g.root.store.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	cutoff := time.Now().UTC().AddDate(0, 0, -storageSnapshotKeepDays).Format(time.RFC3339Nano)
	if _, err = tx.Exec(`DELETE FROM account_storage WHERE account_id=? AND (substr(measured_at,1,10)=? OR measured_at<?)`, a.ID, day, cutoff); err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO account_storage(account_id,measured_at,server_bytes,server_physical_bytes,computer_logical_bytes,computer_allocated_bytes,detail) VALUES(?,?,?,?,?,?,?)`,
		a.ID, rep.MeasuredAt, rep.ServerBytes, rep.PhysicalFilesBytes, logical, allocated, string(detail)); err != nil {
		return
	}
	_ = tx.Commit()
}

// computerDisks returns the host's per-computer disk figures, cached for 60 s.
func (g *AccountGateway) computerDisks(ctx context.Context) map[string]accountDiskCapacity {
	if g.config.AccountProvisionerSocket == "" {
		return nil
	}
	g.storageMu.Lock()
	defer g.storageMu.Unlock()
	if g.diskCache != nil && time.Since(g.diskCacheAt) < computerUsageTTL {
		return g.diskCache
	}
	data, err := g.computerControl(ctx, map[string]string{"op": "capacity"})
	if err != nil {
		return g.diskCache // stale beats nothing; nil when never measured
	}
	capacity, err := parseAccountCapacity(data)
	if err != nil {
		return g.diskCache
	}
	m := map[string]accountDiskCapacity{}
	for _, d := range capacity.Accounts {
		m[d.AccountID] = d
	}
	g.diskCache, g.diskCacheAt = m, time.Now()
	return m
}

func (g *AccountGateway) computerUsageFor(ctx context.Context, a Account) *ComputerUsage {
	d, ok := g.computerDisks(ctx)[g.computerIdentity(a)]
	if !ok {
		return nil
	}
	return &ComputerUsage{LogicalBytes: d.LogicalBytes, AllocatedBytes: d.AllocatedBytes, QuotaBytes: d.QuotaBytes}
}

// wireStorage gives a new workspace its limit, snapshot and computer hooks.
func (g *AccountGateway) wireStorage(c *Config, a Account) {
	id := a.ID
	c.StorageLimit = func() *int64 { return g.storageLimitFor(id) }
	c.HostMinFreeBytes = g.config.HostMinFreeBytes
	c.StorageSnapshot = func(rep StorageReport) { g.recordStorageSnapshot(a, rep) }
	if g.config.AccountProvisionerSocket != "" {
		c.ComputerUsage = func(ctx context.Context) *ComputerUsage { return g.computerUsageFor(ctx, a) }
	}
}

// --- admin API --------------------------------------------------------

type adminStorageHost struct {
	FreeBytes    *int64 `json:"free_bytes"`
	TotalBytes   *int64 `json:"total_bytes"`
	MinFreeBytes int64  `json:"min_free_bytes"`
	Low          bool   `json:"low"`
}

type adminStorageTotals struct {
	Accounts               int   `json:"accounts"`
	ServerBytes            int64 `json:"server_bytes"`
	PhysicalFilesBytes     int64 `json:"physical_files_bytes"`
	ComputerLogicalBytes   int64 `json:"computer_logical_bytes"`
	ComputerAllocatedBytes int64 `json:"computer_allocated_bytes"`
	PendingCopyCount       int   `json:"pending_copy_count"`
}

// adminStorageAccount is one account in GET /api/admin/storage. Source says
// where the figures came from: "live" (open workspace), "snapshot" (closed or
// disabled account, last reconcile) or "none" (never measured).
type adminStorageAccount struct {
	AccountID          string         `json:"account_id"`
	Username           string         `json:"username"`
	Email              string         `json:"email"`
	Disabled           bool           `json:"disabled"`
	Source             string         `json:"source"`
	MeasuredAt         string         `json:"measured_at"`
	ServerBytes        int64          `json:"server_bytes"`
	FilesBytes         int64          `json:"files_bytes"`
	DatabaseBytes      int64          `json:"database_bytes"`
	PhysicalFilesBytes int64          `json:"physical_files_bytes"`
	PendingCopyCount   int            `json:"pending_copy_count"`
	LimitBytes         *int64         `json:"limit_bytes"`
	OverLimit          bool           `json:"over_limit"`
	Classes            []StorageClass `json:"classes"`
	Computer           *ComputerUsage `json:"computer"`
}

type adminStorageResponse struct {
	Version  int                   `json:"version"`
	Host     adminStorageHost      `json:"host"`
	Totals   adminStorageTotals    `json:"totals"`
	Accounts []adminStorageAccount `json:"accounts"`
}

type storageSeriesPoint struct {
	MeasuredAt             string `json:"measured_at"`
	ServerBytes            int64  `json:"server_bytes"`
	ServerPhysicalBytes    int64  `json:"server_physical_bytes"`
	ComputerLogicalBytes   *int64 `json:"computer_logical_bytes"`
	ComputerAllocatedBytes *int64 `json:"computer_allocated_bytes"`
}

func (g *AccountGateway) hostState() adminStorageHost {
	floor := g.config.HostMinFreeBytes
	if floor == 0 {
		floor = defaultHostMinFreeBytes
	}
	h := adminStorageHost{MinFreeBytes: floor}
	if free, total, err := hostDiskSpace(g.config.DataDir); err == nil {
		f, t := int64(free), int64(total)
		h.FreeBytes, h.TotalBytes = &f, &t
		h.Low = floor > 0 && f < floor
	}
	return h
}

// accountStorageEntry builds one account's row: live from an open workspace,
// otherwise from the latest snapshot.
func (g *AccountGateway) accountStorageEntry(ctx context.Context, a Account, disks map[string]accountDiskCapacity) adminStorageAccount {
	e := adminStorageAccount{AccountID: a.ID, Username: a.Username, Email: a.Email, Disabled: a.Disabled, Source: "none", Classes: []StorageClass{}, LimitBytes: g.storageLimitFor(a.ID)}
	g.mu.Lock()
	ws := g.workspaces[a.ID]
	g.mu.Unlock()
	if ws != nil && ws.store != nil {
		if rep, err := ws.store.storageLive(); err == nil {
			e.Source = "live"
			e.fill(rep)
		}
	}
	if e.Source == "none" {
		var raw string
		var logical, allocated sql.NullInt64
		err := g.root.store.db.QueryRowContext(ctx, `SELECT measured_at,server_bytes,server_physical_bytes,computer_logical_bytes,computer_allocated_bytes,detail FROM account_storage WHERE account_id=? ORDER BY measured_at DESC LIMIT 1`, a.ID).Scan(&e.MeasuredAt, &e.ServerBytes, &e.PhysicalFilesBytes, &logical, &allocated, &raw)
		if err == nil {
			e.Source = "snapshot"
			var d snapshotDetail
			if json.Unmarshal([]byte(raw), &d) == nil {
				e.Classes, e.FilesBytes, e.DatabaseBytes, e.PendingCopyCount = d.Classes, d.FilesBytes, d.DatabaseBytes, d.PendingCopyCount
				if e.Classes == nil {
					e.Classes = []StorageClass{}
				}
			}
			if logical.Valid {
				e.Computer = &ComputerUsage{LogicalBytes: logical.Int64, AllocatedBytes: allocated.Int64}
			}
		}
	}
	if d, ok := disks[g.computerIdentity(a)]; ok {
		e.Computer = &ComputerUsage{LogicalBytes: d.LogicalBytes, AllocatedBytes: d.AllocatedBytes, QuotaBytes: d.QuotaBytes}
	}
	if e.LimitBytes != nil {
		e.OverLimit = e.ServerBytes >= *e.LimitBytes
	}
	return e
}

func (e *adminStorageAccount) fill(rep StorageReport) {
	e.MeasuredAt, e.ServerBytes, e.FilesBytes, e.DatabaseBytes = rep.MeasuredAt, rep.ServerBytes, rep.FilesBytes, rep.DatabaseBytes
	e.PhysicalFilesBytes, e.PendingCopyCount, e.Classes = rep.PhysicalFilesBytes, rep.PendingCopyCount, rep.Classes
}

func (g *AccountGateway) adminStorage(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/accounts/"
	one := strings.HasPrefix(r.URL.Path, prefix) && strings.HasSuffix(r.URL.Path, "/storage")
	if r.URL.Path != "/api/admin/storage" && !one {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method_not_allowed", "use GET")
		return true
	}
	disks := g.computerDisks(r.Context())
	if one {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/storage")
		if parsed, err := uuid.Parse(id); id != "legacy-owner" && (err != nil || parsed.String() != id) {
			writeErr(w, 404, "not_found", "account not found")
			return true
		}
		var target Account
		err := g.root.store.db.QueryRowContext(r.Context(), `SELECT id,username,email,role,disabled,legacy FROM accounts WHERE id=?`, id).Scan(&target.ID, &target.Username, &target.Email, &target.Role, &target.Disabled, &target.Legacy)
		if err != nil {
			writeErr(w, 404, "not_found", "account not found")
			return true
		}
		series := []storageSeriesPoint{}
		rows, err := g.root.store.db.QueryContext(r.Context(), `SELECT measured_at,server_bytes,server_physical_bytes,computer_logical_bytes,computer_allocated_bytes FROM account_storage WHERE account_id=? ORDER BY measured_at`, id)
		if err != nil {
			writeErr(w, 500, "storage", "cannot read storage history")
			return true
		}
		defer rows.Close()
		for rows.Next() {
			var p storageSeriesPoint
			var l, al sql.NullInt64
			if rows.Scan(&p.MeasuredAt, &p.ServerBytes, &p.ServerPhysicalBytes, &l, &al) != nil {
				writeErr(w, 500, "storage", "cannot read storage history")
				return true
			}
			if l.Valid {
				p.ComputerLogicalBytes, p.ComputerAllocatedBytes = &l.Int64, &al.Int64
			}
			series = append(series, p)
		}
		writeJSON(w, 200, map[string]any{"version": storageAPIVersion, "account": g.accountStorageEntry(r.Context(), target, disks), "series": series})
		return true
	}
	rows, err := g.root.store.db.QueryContext(r.Context(), `SELECT id,username,email,role,disabled,legacy FROM accounts ORDER BY created_at,id`)
	if err != nil {
		writeErr(w, 500, "storage", "cannot list accounts")
		return true
	}
	var accounts []Account
	for rows.Next() {
		var x Account
		if rows.Scan(&x.ID, &x.Username, &x.Email, &x.Role, &x.Disabled, &x.Legacy) != nil {
			rows.Close()
			writeErr(w, 500, "storage", "cannot list accounts")
			return true
		}
		accounts = append(accounts, x)
	}
	rows.Close()
	out := adminStorageResponse{Version: storageAPIVersion, Host: g.hostState(), Accounts: []adminStorageAccount{}}
	for _, x := range accounts {
		e := g.accountStorageEntry(r.Context(), x, disks)
		out.Accounts = append(out.Accounts, e)
		out.Totals.Accounts++
		out.Totals.ServerBytes += e.ServerBytes
		out.Totals.PhysicalFilesBytes += e.PhysicalFilesBytes
		out.Totals.PendingCopyCount += e.PendingCopyCount
		if e.Computer != nil {
			out.Totals.ComputerLogicalBytes += e.Computer.LogicalBytes
			out.Totals.ComputerAllocatedBytes += e.Computer.AllocatedBytes
		}
	}
	writeJSON(w, 200, out)
	return true
}
