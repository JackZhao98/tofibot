package app

// Computers as rows (computer-decoupling S5/S6). One row per computer of the
// account: the VM the administrator assigned (kind "vm", at most one today) and
// any number of the person's own computers (kind "personal", at most
// Config.MaxPersonalComputers, default 6). The row is the durable fact; what is
// reachable right now is the registry in computer_registry.go.
//
// Nothing here is shared between accounts: every account has its own store, so
// a request in one account can never read another account's rows, and the
// administrator's views never touch a person's personal rows (see
// adminVMSummary in computer_attach.go).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// Row status. "attached" means the computer is part of the workspace (it may
// still be offline); "lost" means it stopped answering without a detach;
// "detached" means a person or the administrator removed it from use cleanly.
const (
	computerRowAttached = "attached"
	computerRowLost     = "lost"
	computerRowDetached = "detached"
)

// DefaultMaxPersonalComputers is the number of personal computers an account
// may connect when the administrator has not set another limit.
const DefaultMaxPersonalComputers = 6

// ComputerRow is the stored shape of one computer.
type ComputerRow struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Platform        string            `json:"platform"`
	Region          *string           `json:"region"`
	Labels          map[string]string `json:"labels"`
	ProtocolVersion int               `json:"protocol_version"`
	Status          string            `json:"status"`
	LastSeen        string            `json:"last_seen,omitempty"`
	Capabilities    []string          `json:"capabilities"`
}

// migrateComputerRows widens the computers table. It rebuilds the table once
// when it still carries the old CHECK(platform='darwin') / CHECK(kind='mac')
// constraints, because SQLite cannot drop a constraint in place, then adds the
// columns of the row model. Idempotent; existing paired computers keep their id,
// token hash and capabilities and become kind "personal".
func migrateComputerRows(db *sql.DB) error {
	var ddl string
	if err := db.QueryRow(`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name='computers'`).Scan(&ddl); err != nil {
		return err
	}
	if strings.Contains(ddl, "CHECK(platform='darwin')") || strings.Contains(ddl, "CHECK(kind='mac')") {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, stmt := range []string{
			`ALTER TABLE computers RENAME TO computers_pre_rows`,
			`CREATE TABLE computers(
 id TEXT PRIMARY KEY, name TEXT NOT NULL, platform TEXT NOT NULL,
 kind TEXT NOT NULL DEFAULT 'personal', token_hash BLOB NOT NULL,
 capabilities TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, revoked_at TEXT
)`,
			`INSERT INTO computers(id,name,platform,kind,token_hash,capabilities,last_seen,created_at,revoked_at)
 SELECT id,name,platform,CASE kind WHEN 'mac' THEN 'personal' ELSE kind END,token_hash,capabilities,last_seen,created_at,revoked_at FROM computers_pre_rows`,
			`DROP TABLE computers_pre_rows`,
		} {
			if _, err = tx.Exec(stmt); err != nil {
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	for _, col := range []struct{ name, ddl string }{
		{"region", `ALTER TABLE computers ADD COLUMN region TEXT`},
		{"labels", `ALTER TABLE computers ADD COLUMN labels TEXT NOT NULL DEFAULT '{}'`},
		{"protocol_version", `ALTER TABLE computers ADD COLUMN protocol_version INTEGER NOT NULL DEFAULT 0`},
		{"status", `ALTER TABLE computers ADD COLUMN status TEXT NOT NULL DEFAULT 'attached'`},
		{"detached_at", `ALTER TABLE computers ADD COLUMN detached_at TEXT`},
	} {
		if err := ensureColumn(db, "computers", col.name, col.ddl); err != nil {
			return err
		}
	}
	// Run records carry the computer a run used, for later metering. All three
	// are nullable: a run that used no computer has none, and nothing reads them yet.
	for _, col := range []struct{ name, ddl string }{
		{"computer_id", `ALTER TABLE runs ADD COLUMN computer_id TEXT`},
		{"region", `ALTER TABLE runs ADD COLUMN region TEXT`},
		{"compute_ms", `ALTER TABLE runs ADD COLUMN compute_ms INTEGER`},
	} {
		if err := ensureColumn(db, "runs", col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

func scanComputerRow(sc interface{ Scan(...any) error }) (ComputerRow, error) {
	var row ComputerRow
	var region sql.NullString
	var labels, caps string
	if err := sc.Scan(&row.ID, &row.Name, &row.Kind, &row.Platform, &region, &labels, &row.ProtocolVersion, &row.Status, &row.LastSeen, &caps); err != nil {
		return ComputerRow{}, err
	}
	if region.Valid {
		v := region.String
		row.Region = &v
	}
	row.Labels = map[string]string{}
	_ = json.Unmarshal([]byte(labels), &row.Labels)
	row.Capabilities = []string{}
	_ = json.Unmarshal([]byte(caps), &row.Capabilities)
	return row, nil
}

const computerRowColumns = `id,name,kind,platform,region,labels,protocol_version,status,last_seen,capabilities`

// computerRow returns one non-revoked computer.
func (s *Store) computerRow(id string) (ComputerRow, error) {
	return scanComputerRow(s.db.QueryRow(`SELECT `+computerRowColumns+` FROM computers WHERE id=? AND revoked_at IS NULL`, id))
}

// listComputerRows returns the non-revoked computers, the VM first.
func (s *Store) listComputerRows() ([]ComputerRow, error) {
	rows, err := s.db.Query(`SELECT ` + computerRowColumns + ` FROM computers WHERE revoked_at IS NULL ORDER BY CASE kind WHEN 'vm' THEN 0 ELSE 1 END, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComputerRow
	for rows.Next() {
		row, err := scanComputerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// upsertVMComputer records the account's VM. A VM row has no device token: its
// token hash is empty, which authenticateDevice can never match.
func (s *Store) upsertVMComputer(id, name, platform, region string, caps []string, protocol int, status string) error {
	capsJSON, _ := json.Marshal(caps)
	t := now()
	var regionValue any
	if strings.TrimSpace(region) != "" {
		regionValue = region
	}
	_, err := s.db.Exec(`INSERT INTO computers(id,name,platform,kind,token_hash,capabilities,last_seen,created_at,region,protocol_version,status)
 VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name,platform=excluded.platform,capabilities=excluded.capabilities,last_seen=excluded.last_seen,
  region=excluded.region,protocol_version=excluded.protocol_version,status=excluded.status,
  detached_at=CASE WHEN excluded.status='detached' THEN excluded.last_seen ELSE NULL END,revoked_at=NULL`,
		id, name, platform, computer.RowKindVM, []byte{}, string(capsJSON), t, t, regionValue, protocol, status)
	return err
}

// setComputerStatus changes a row's status and last-seen time.
func (s *Store) setComputerStatus(id, status string) error {
	t := now()
	var detached any
	if status == computerRowDetached {
		detached = t
	}
	_, err := s.db.Exec(`UPDATE computers SET status=?,last_seen=?,detached_at=? WHERE id=? AND revoked_at IS NULL`, status, t, detached, id)
	return err
}

// touchComputer records a heartbeat.
func (s *Store) touchComputer(id string) {
	_, _ = s.db.Exec(`UPDATE computers SET last_seen=? WHERE id=? AND revoked_at IS NULL`, now(), id)
}

// countPersonalComputers is the number of non-revoked personal computers.
func (s *Store) countPersonalComputers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM computers WHERE kind='personal' AND revoked_at IS NULL`).Scan(&n)
	return n, err
}

var errTooManyComputers = errors.New("personal computer limit reached")

// recordRunComputer notes on the run record which computer a run used and for
// how long. The columns are for future metering; nothing displays them.
func (s *Store) recordRunComputer(runID, computerID, region string, elapsed time.Duration) {
	if s == nil || s.db == nil || runID == "" || computerID == "" {
		return
	}
	var regionValue any
	if region != "" {
		regionValue = region
	}
	_, _ = s.db.Exec(`UPDATE runs SET computer_id=?,region=COALESCE(?,region),compute_ms=COALESCE(compute_ms,0)+? WHERE id=?`, computerID, regionValue, elapsed.Milliseconds(), runID)
}
