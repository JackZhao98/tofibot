package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const purgeConfirmation = "PURGE"

// purgeWorkspaceData removes application state while retaining the database
// schema. Credentials and service configuration live outside these tables and
// are intentionally preserved; this operation resets user data, not the
// installation itself.
func (s *Store) purgeWorkspaceData() error {
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if name == "workspace_owner" || strings.HasPrefix(name, "owner_") {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	sort.Strings(tables)
	if _, err = s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.db.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range tables {
		// Names come only from sqlite_master, not from a request. Quote them so
		// future migrations can safely use punctuation in a table name.
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		if _, err = tx.Exec(`DELETE FROM ` + quoted); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) != root {
			return errors.New("attachment path escaped data directory")
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) routeAdmin(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "admin/purge" {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return true
	}
	var request struct {
		Confirm string `json:"confirm"`
	}
	if decodeStrict(r, 4<<10, &request) != nil || request.Confirm != purgeConfirmation {
		writeErr(w, http.StatusBadRequest, "explicit_purge_confirmation_required", "type PURGE to confirm")
		return true
	}

	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	s.mu.Lock()
	if s.purging {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, "purge_in_progress", "workspace purge is already in progress")
		return true
	}
	s.purging = true
	s.mu.Unlock()

	s.closeComputerControls()
	s.closeVMOAuth()
	s.stopConversationWorkers()
	s.stopSummaryWorkers()
	resumeWorkers := true
	defer func() {
		s.startSummaryWorkers()
		s.mu.Lock()
		s.queues = map[string]*conversationQueue{}
		s.runs = map[string]context.CancelFunc{}
		s.closing = false
		s.purging = false
		s.mu.Unlock()
		if resumeWorkers {
			if ids, err := s.store.workerConversationIDs(); err == nil {
				for _, id := range ids {
					s.startConversationWorker(id)
				}
			}
		}
		s.vmOAuthMu.Lock()
		s.vmOAuthClosing = false
		s.vmOAuth = map[string]*vmOAuthSession{}
		s.vmOAuthMu.Unlock()
	}()

	if s.microVM != nil {
		if _, err := s.microVM.Purge(r.Context()); err != nil {
			writeErr(w, http.StatusBadGateway, "computer_purge_failed", err.Error())
			return true
		}
	}
	if err := s.store.purgeWorkspaceData(); err != nil {
		writeErr(w, http.StatusInternalServerError, "workspace_purge_failed", err.Error())
		return true
	}
	if s.ownerAuth != nil {
		if err := s.ownerAuth.resetForPurge(); err != nil {
			writeErr(w, http.StatusInternalServerError, "owner_reset_failed", err.Error())
			return true
		}
	}
	s.mu.Lock()
	s.convMu = map[string]*sync.Mutex{}
	s.mu.Unlock()
	s.computerOwnerMu.Lock()
	s.computerOwners = map[string]string{}
	s.computerOwnerMu.Unlock()
	s.toolSnapshotMu.Lock()
	s.toolSnapshots = map[toolSnapshotKey]toolSnapshot{}
	s.toolSnapshotMu.Unlock()
	resumeWorkers = false
	writeJSON(w, http.StatusOK, map[string]any{"purged": true, "next_user_role": "admin"})
	return true
}
