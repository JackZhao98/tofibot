package app

import (
	"database/sql"
	"fmt"
)

// applyAccountDBLimit bounds SQLite's main database file by limiting its page
// count. It also tunes WAL checkpointing and post-checkpoint journal retention;
// those WAL settings are operational bounds, not a filesystem quota.
func applyAccountDBLimit(db *sql.DB, maxBytes int64) error {
	if db == nil {
		return fmt.Errorf("apply account database limit: nil database")
	}
	if maxBytes <= 0 {
		return fmt.Errorf("apply account database limit: maxBytes must be positive")
	}

	var pageSize, pageCount int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return fmt.Errorf("read SQLite page size: %w", err)
	}
	if pageSize <= 0 {
		return fmt.Errorf("invalid SQLite page size %d", pageSize)
	}
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return fmt.Errorf("read SQLite page count: %w", err)
	}
	maxPages := maxBytes / pageSize
	if maxPages < 1 {
		return fmt.Errorf("account database limit %d bytes is smaller than SQLite page size %d", maxBytes, pageSize)
	}
	if pageCount > maxPages {
		return fmt.Errorf("account database already uses %d pages (%d bytes), above limit of %d bytes", pageCount, pageCount*pageSize, maxBytes)
	}

	var appliedPages int64
	query := fmt.Sprintf(`PRAGMA max_page_count=%d`, maxPages)
	if err := db.QueryRow(query).Scan(&appliedPages); err != nil {
		return fmt.Errorf("set SQLite maximum page count: %w", err)
	}
	if appliedPages > maxPages {
		return fmt.Errorf("SQLite maximum page count read back as %d, above requested %d", appliedPages, maxPages)
	}
	// WAL checkpointing is per connection. Keep the threshold finite and use a
	// journal retention target no larger than this database's configured cap.
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint=256`); err != nil {
		return fmt.Errorf("configure SQLite WAL autocheckpoint: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA journal_size_limit=%d`, maxBytes)); err != nil {
		return fmt.Errorf("configure SQLite journal size limit: %w", err)
	}
	return nil
}
