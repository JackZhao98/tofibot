package app

// Single-account restore from the backup repository (design 9.2). Steps are
// fence, fetch, swap; the swap is two renames on one filesystem, so a crash
// between them is settled by recoverRestores. The account's login, role, group
// and quota live in the root database and are not touched.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/backup"
)

const (
	restoreKeepDays  = 14
	beforeRestoreTag = ".before-restore-"
	restoreStamp     = "20060102T150405.000000000Z"
)

var errAccountRestoring = errors.New("account is being restored")

func (g *AccountGateway) accountsDir() string { return filepath.Join(g.config.DataDir, "accounts") }

// beforeRestoreDirs lists the saved copies of an account, newest first.
func (g *AccountGateway) beforeRestoreDirs(id string) []string {
	entries, _ := os.ReadDir(g.accountsDir())
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), beforeRestoreTag+id+"-") {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// fence stops the account's runtime and answers its requests with
// account_restoring until the returned release is called.
func (g *AccountGateway) fence(id string) (release func(), err error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, errors.New("account gateway closed")
	}
	if g.restoring == nil {
		g.restoring = map[string]bool{}
	}
	g.restoring[id] = true
	g.cancelAccount(id)
	rt := g.workspaces[id]
	delete(g.workspaces, id)
	g.mu.Unlock()
	if rt != nil {
		rt.Close()
	}
	return func() { g.mu.Lock(); delete(g.restoring, id); g.mu.Unlock() }, nil
}

type restoreError struct {
	status int
	code   string
}

func (e *restoreError) Error() string { return e.code }

// restoreAccount replaces the account's data with the copy in snapshot snap.
func (g *AccountGateway) restoreAccount(ctx context.Context, id, confirm, snap string) (*backup.Report, error) {
	var username string
	var legacy bool
	err := g.root.store.db.QueryRowContext(ctx, `SELECT username,legacy FROM accounts WHERE id=? AND deleting=0`, id).Scan(&username, &legacy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, &restoreError{404, "not_found"}
	case err != nil:
		return nil, err
	case legacy:
		return nil, &restoreError{409, "account_restore_unsupported_legacy"}
	case confirm != username:
		return nil, &restoreError{400, "confirmation_mismatch"}
	}
	if !g.beginBackupJob() {
		return nil, &restoreError{409, "backup_in_progress"}
	}
	defer g.endBackupJob()
	repo, _, err := g.backupRepo(ctx)
	if err != nil {
		return nil, err
	}
	m, err := repo.Manifest(ctx, snap)
	if err != nil {
		return nil, err
	}
	if m.InstanceID != g.root.instance.ID {
		return nil, &restoreError{409, "backup_instance_mismatch"}
	}
	release, err := g.fence(id)
	if err != nil {
		return nil, err
	}
	defer release()
	dir := filepath.Join(g.accountsDir(), id)
	staging := filepath.Join(g.accountsDir(), ".restore-"+id)
	_ = os.RemoveAll(staging)
	rep, err := repo.Restore(ctx, snap, id, staging, backup.RestoreOpts{MaxSchema: SchemaVersion, Existing: dir})
	if err != nil {
		_ = os.RemoveAll(staging)
		return rep, err
	}
	return rep, g.swapAccountDir(id, staging)
}

// swapAccountDir puts next in place of the account's directory, keeping the old one.
func (g *AccountGateway) swapAccountDir(id, next string) error {
	dir := filepath.Join(g.accountsDir(), id)
	saved := filepath.Join(g.accountsDir(), beforeRestoreTag+id+"-"+time.Now().UTC().Format(restoreStamp))
	if _, err := os.Stat(dir); err == nil {
		if err = os.Rename(dir, saved); err != nil {
			return err
		}
	}
	if err := os.Rename(next, dir); err != nil {
		_ = os.Rename(saved, dir)
		return err
	}
	return nil
}

// undoAccountRestore swaps the newest saved copy back; the copy it replaces is saved in turn.
func (g *AccountGateway) undoAccountRestore(ctx context.Context, id string) error {
	saved := g.beforeRestoreDirs(id)
	if len(saved) == 0 {
		return &restoreError{404, "nothing_to_undo"}
	}
	if !g.beginBackupJob() {
		return &restoreError{409, "backup_in_progress"}
	}
	defer g.endBackupJob()
	release, err := g.fence(id)
	if err != nil {
		return err
	}
	defer release()
	return g.swapAccountDir(id, filepath.Join(g.accountsDir(), saved[0]))
}

// recoverRestores settles a swap a crash cut in half: the account directory
// is gone, its saved copy is not.
func (g *AccountGateway) recoverRestores() {
	entries, _ := os.ReadDir(g.accountsDir())
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".restore-") {
			dir := filepath.Join(g.accountsDir(), strings.TrimPrefix(name, ".restore-"))
			if _, err := os.Stat(dir); err != nil {
				if saved := g.beforeRestoreDirs(strings.TrimPrefix(name, ".restore-")); len(saved) > 0 {
					_ = os.Rename(filepath.Join(g.accountsDir(), saved[0]), dir)
				}
			}
			_ = os.RemoveAll(filepath.Join(g.accountsDir(), name))
		}
	}
}

// sweepRestoreLeftovers removes saved copies older than 14 days.
func (g *AccountGateway) sweepRestoreLeftovers(at time.Time) {
	entries, _ := os.ReadDir(g.accountsDir())
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, beforeRestoreTag) || len(name) < len(restoreStamp) {
			continue
		}
		if t, err := time.Parse(restoreStamp, name[len(name)-len(restoreStamp):]); err == nil && at.Sub(t) > restoreKeepDays*24*time.Hour {
			_ = os.RemoveAll(filepath.Join(g.accountsDir(), name))
		}
	}
}

func (g *AccountGateway) adminAccountBackups(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/accounts/"), "/")
	id, action := parts[0], strings.Join(parts[1:], "/")
	ctx := r.Context()
	fail := func(err error) {
		var re *restoreError
		code, _ := errorCode(err)
		switch {
		case errors.As(err, &re):
			writeErr(w, re.status, re.code, "restore refused")
		case code == "backup_schema_too_new":
			writeErr(w, 409, "schema_too_new", "the snapshot is newer than this program")
		case code != "backup_failed":
			writeErr(w, backupStatusFor(code), code, "restore stopped; the account was not changed")
		default:
			storageFailure(w, "restore account", err)
		}
	}
	switch {
	case action == "backups" && r.Method == http.MethodGet:
		repo, _, err := g.backupRepo(ctx)
		if err != nil {
			fail(err)
			return
		}
		snaps, err := repo.List(ctx)
		if err != nil {
			fail(err)
			return
		}
		out := []map[string]any{}
		for i := len(snaps) - 1; i >= 0; i-- {
			if !snaps[i].Committed {
				continue
			}
			m, merr := repo.Manifest(ctx, snaps[i].Name)
			if merr != nil {
				continue
			}
			for _, e := range m.Accounts {
				if e.ID == id {
					out = append(out, map[string]any{"name": m.Snapshot, "at": m.CreatedAt, "reason": m.Reason, "rows": e.Rows, "files": len(e.Disk)})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"snapshots": out, "undo_available": len(g.beforeRestoreDirs(id)) > 0})
	case action == "restore" && r.Method == http.MethodPost:
		var in struct {
			Snapshot        string `json:"snapshot"`
			ConfirmUsername string `json:"confirm_username"`
		}
		if decodeStrict(r, 4<<10, &in) != nil || in.Snapshot == "" {
			writeErr(w, 400, "invalid_request", "snapshot and confirm_username are required")
			return
		}
		rep, err := g.restoreAccount(ctx, id, in.ConfirmUsername, in.Snapshot)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"restored": true, "report": rep, "undo_until": time.Now().Add(restoreKeepDays * 24 * time.Hour).UTC().Format(time.RFC3339)})
	case action == "restore/undo" && r.Method == http.MethodPost:
		if err := g.undoAccountRestore(ctx, id); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"undone": true})
	default:
		writeErr(w, 405, "method_not_allowed", "unsupported method")
	}
}
