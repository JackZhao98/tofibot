// Schema version of every SQLite database the server opens: the instance
// database (data/tofi.db) and each account database
// (data/accounts/<id>/tofi.db). They all go through openStoreWithLimit, which
// is the only place that opens SQLite and the only place that migrates.
//
// The version is recorded twice, in one transaction, so a restored or copied
// file always carries its own version:
//   - table tofi_schema(key,value): schema_version (what the file was last
//     migrated to) and app_version (the release that wrote it);
//   - PRAGMA user_version: the same schema number, readable by `sqlite3`
//     without the app.
//
// SchemaVersion rises by one whenever a migrate* step changes what an older
// binary can read. Open rules:
//   - older than the binary (or never stamped: every database before this
//     record): migrate forward, then stamp. Migration steps are idempotent
//     (CREATE ... IF NOT EXISTS, ensureColumn); a run that dies half way
//     leaves the old stamp, so the next start repeats it safely.
//   - newer than the binary: refuse before anything is written
//     (SchemaError, kind too_new). The database is never migrated down.
//   - unreadable, negative, or the two records disagree: refuse (kind garbled).
//
// The container image label io.tofi.data-schema is derived from the same
// number (DataSchemaLabel); TestDockerfileLabelMatchesSchemaVersion holds it.
package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SchemaVersion is the schema this binary writes and understands.
const SchemaVersion = 1

// Version (update_notice.go) is the release that built this binary; it is
// recorded as app_version in every database this binary migrates.

// DataSchemaLabelPrefix and DataSchemaLabel give the image label value.
const DataSchemaLabelPrefix = "tofi-account-data-v"

func DataSchemaLabel() string { return fmt.Sprintf("%s%d", DataSchemaLabelPrefix, SchemaVersion) }

const (
	schemaTooNew = "too_new"
	schemaGarble = "garbled"
)

// SchemaError is the named refusal for a database this binary must not open.
type SchemaError struct {
	Kind      string // schemaTooNew or schemaGarble
	Path      string // the database file
	Role      string // "instance database" or "account database"
	Found     int    // too_new: the version in the file
	Supported int
	AppBy     string // app_version recorded in the file, if any
	Detail    string // garbled: what was wrong
}

func (e *SchemaError) Error() string {
	if e.Kind == schemaTooNew {
		by := ""
		if e.AppBy != "" {
			by = " (written by TOFI " + e.AppBy + ")"
		}
		return fmt.Sprintf("schema_too_new: the %s %s has schema version %d%s, but this TOFI %s supports up to %d; "+
			"it was not opened or changed. Run `sudo tofi update` to install the newer version, "+
			"or `sudo tofi restore <backup>` to put back a backup made before the update (`sudo tofi backups` lists them)",
			e.Role, e.Path, e.Found, by, Version, e.Supported)
	}
	return fmt.Sprintf("schema_version_unreadable: the %s %s has an unreadable schema version (%s); "+
		"it was not opened or changed. Run `sudo tofi restore <backup>` to put back a backup, or ask for help before starting TOFI again",
		e.Role, e.Path, e.Detail)
}

func schemaRole(dbPath string) string {
	if filepath.Base(filepath.Dir(filepath.Dir(dbPath))) == "accounts" {
		return "account database"
	}
	return "instance database"
}

type schemaRecord struct {
	version int // 0 = never stamped
	appBy   string
}

// readSchemaRecord reads and validates both records without writing.
func readSchemaRecord(db *sql.DB, path string) (schemaRecord, error) {
	garbled := func(format string, args ...any) error {
		return &SchemaError{Kind: schemaGarble, Path: path, Role: schemaRole(path), Supported: SchemaVersion, Detail: fmt.Sprintf(format, args...)}
	}
	var u int64
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&u); err != nil {
		return schemaRecord{}, garbled("PRAGMA user_version: %v", err)
	}
	if u < 0 || u > 1<<31-1 {
		return schemaRecord{}, garbled("PRAGMA user_version is %d", u)
	}
	var hasTable int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tofi_schema'`).Scan(&hasTable); err != nil {
		return schemaRecord{}, garbled("cannot read the database: %v", err)
	}
	rec := schemaRecord{version: int(u)}
	if hasTable == 0 {
		return rec, nil
	}
	rows, err := db.Query(`SELECT key, CAST(value AS TEXT) FROM tofi_schema`)
	if err != nil {
		return schemaRecord{}, garbled("tofi_schema: %v", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return schemaRecord{}, garbled("tofi_schema row: %v", err)
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return schemaRecord{}, garbled("tofi_schema: %v", err)
	}
	if len(values) == 0 {
		return rec, nil // an emptied record is the same as none
	}
	raw, ok := values["schema_version"]
	if !ok {
		return schemaRecord{}, garbled("tofi_schema has no schema_version row")
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 1 || strconv.Itoa(v) != strings.TrimSpace(raw) {
		return schemaRecord{}, garbled("schema_version is %q", raw)
	}
	if u != 0 && int(u) != v {
		return schemaRecord{}, garbled("schema_version %d disagrees with PRAGMA user_version %d", v, u)
	}
	return schemaRecord{version: v, appBy: values["app_version"]}, nil
}

func tooNew(path string, rec schemaRecord) *SchemaError {
	return &SchemaError{Kind: schemaTooNew, Path: path, Role: schemaRole(path), Found: rec.version, Supported: SchemaVersion, AppBy: rec.appBy}
}

// checkSchemaVersion runs before any migration touches the file.
func checkSchemaVersion(db *sql.DB, path string) error {
	rec, err := readSchemaRecord(db, path)
	if err != nil {
		return err
	}
	if rec.version > SchemaVersion {
		return tooNew(path, rec)
	}
	return nil
}

// stampSchemaVersion records the version and the writing release after every
// migration step succeeded, in one transaction.
func stampSchemaVersion(db *sql.DB, path string) error {
	rec, err := readSchemaRecord(db, path)
	if err != nil {
		return err
	}
	if rec.version > SchemaVersion {
		return tooNew(path, rec)
	}
	if rec.version == SchemaVersion && rec.appBy == Version {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS tofi_schema(key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	for k, v := range map[string]string{"schema_version": strconv.Itoa(SchemaVersion), "app_version": Version, "migrated_at": now()} {
		if _, err = tx.Exec(`INSERT INTO tofi_schema(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, SchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// preflightAccountSchemas refuses to start when any account database under
// dataDir/accounts is newer than this binary or unreadable. Account databases
// are otherwise opened lazily, so a rolled-back image would start and then
// fail account by account. It only reads, and never creates a file.
func preflightAccountSchemas(dataDir string) error {
	entries, err := os.ReadDir(filepath.Join(dataDir, "accounts"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dataDir, "accounts", e.Name(), "tofi.db")
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
			continue
		}
		db, err := sql.Open("sqlite", path+sqliteBusyDSN)
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(1)
		err = checkSchemaVersion(db, path)
		db.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
