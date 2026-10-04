package app

import (
	"database/sql"
	"errors"
	"strings"
)

// Reserve the existing host import-marker contract without adding an importer.
// Only new native ingress writes positive records; migration never backfills.
func migrateMCPReviewProvenance(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_message_ingress(
	 message_id TEXT PRIMARY KEY, created_at TEXT NOT NULL,
	 FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE);
	 CREATE TABLE IF NOT EXISTS portability_provenance(
	 kind TEXT NOT NULL, target_id TEXT NOT NULL, source_json TEXT NOT NULL,
	 PRIMARY KEY(kind,target_id));`)
	if err != nil {
		return err
	}
	// CREATE IF NOT EXISTS must not silently accept an incompatible preexisting
	// table and let malformed host evidence enter a later review query.
	for _, table := range []struct {
		name    string
		columns []string
		primary []int
	}{
		{"user_message_ingress", []string{"message_id", "created_at"}, []int{1, 0}},
		{"portability_provenance", []string{"kind", "target_id", "source_json"}, []int{1, 2, 0}},
	} {
		rows, err := db.Query(`PRAGMA table_info(` + table.name + `)`)
		if err != nil {
			return err
		}
		index, compatible := 0, true
		for rows.Next() {
			var cid, notNull, primary int
			var name, typ string
			var defaultValue any
			if err = rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primary); err != nil {
				break
			}
			if index >= len(table.columns) || name != table.columns[index] || !strings.EqualFold(typ, "TEXT") || primary != table.primary[index] || (name != "message_id" && notNull != 1) {
				compatible = false
			}
			index++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if !compatible || index != len(table.columns) {
			return errors.New("incompatible AutoReview message provenance schema")
		}
	}
	return nil
}
