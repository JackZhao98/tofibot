package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Account deletion is a resumable pipeline. The account row stays (disabled,
// deleting=1) until the last step, so a failed step is retried from the same
// DELETE request and nothing is ever half-forgotten. Steps are ordered so each
// can be repeated safely:
//
//	export    writes and verifies an encrypted copy of the Bot setup
//	          (account_export.go); nothing below runs unless it succeeded
//	computer  broker op "delete": stops the manager, removes the computer's
//	          state/socket/config/jail/disk and releases the ledger row
//	data      removes <data>/accounts/<id> (Bots, chats, memory, files)
//	sessions  removes the account's control-DB sessions
//	record    removes the account row itself
var accountDeleteSteps = []string{"export", "computer", "data", "sessions", "record"}

var (
	errAccountDeleting = errors.New("account is being deleted")
)

type accountDeleteError struct {
	status  int
	code    string
	message string
}

func (e *accountDeleteError) Error() string { return e.message }

func ensureAccountDeleteColumns(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(accounts)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var dflt sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if _, err = tx.Exec(exportTables); err != nil {
		return err
	}
	for _, c := range []struct{ name, ddl string }{
		{"deleting", `ALTER TABLE accounts ADD COLUMN deleting INTEGER NOT NULL DEFAULT 0`},
		{"delete_step", `ALTER TABLE accounts ADD COLUMN delete_step TEXT NOT NULL DEFAULT ''`},
		{"delete_error", `ALTER TABLE accounts ADD COLUMN delete_error TEXT NOT NULL DEFAULT ''`},
	} {
		if !have[c.name] {
			if _, err = tx.Exec(c.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// beginAccountDelete validates every precondition and persists the deleting
// state. caller is the acting admin's id.
func (g *AccountGateway) beginAccountDelete(ctx context.Context, callerID, id, confirm string) (Account, *accountDeleteError) {
	var target Account
	if id == callerID {
		return target, &accountDeleteError{409, "self_lockout", "cannot delete your own account"}
	}
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		// Only canonical UUID accounts own a deletable computer and data root.
		var legacy bool
		if g.root.store.db.QueryRowContext(ctx, `SELECT legacy FROM accounts WHERE id=?`, id).Scan(&legacy) == nil && legacy {
			return target, &accountDeleteError{409, "legacy_account_not_deletable", "the original owner account and its adopted computer cannot be deleted"}
		}
		return target, &accountDeleteError{404, "not_found", "account not found"}
	}
	// A lingering runtime is closed synchronously, after the lock is released
	// and before any export or removal step starts.
	var closing *Server
	defer func() {
		if closing != nil {
			closing.Close()
		}
	}()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return target, &accountDeleteError{503, "unavailable", "account gateway closed"}
	}
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		return target, &accountDeleteError{500, "storage", "cannot start deletion"}
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,username,email,role,disabled,legacy,deleting FROM accounts WHERE id=?`, id).Scan(&target.ID, &target.Username, &target.Email, &target.Role, &target.Disabled, &target.Legacy, &target.Deleting)
	if errors.Is(err, sql.ErrNoRows) {
		return target, &accountDeleteError{404, "not_found", "account not found"}
	}
	if err != nil {
		return target, &accountDeleteError{500, "storage", "cannot read account"}
	}
	if target.Legacy {
		// The adopted computer is an external disk with alias paths and an
		// ownership proof; removing it is not supported.
		return target, &accountDeleteError{409, "legacy_account_not_deletable", "the original owner account and its adopted computer cannot be deleted"}
	}
	if !target.Disabled {
		return target, &accountDeleteError{409, "account_not_deactivated", "deactivate the account before deleting it"}
	}
	if confirm == "" || confirm != target.Username {
		return target, &accountDeleteError{400, "confirmation_mismatch", "type the exact username to confirm deletion"}
	}
	if target.Role == "admin" {
		var others int
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE role='admin' AND disabled=0 AND id<>?`, id).Scan(&others) != nil {
			return target, &accountDeleteError{500, "storage", "cannot read admins"}
		}
		if others < 1 {
			return target, &accountDeleteError{409, "last_admin", "the last admin cannot be deleted"}
		}
	}
	if g.deleteRunning == nil {
		g.deleteRunning = map[string]bool{}
	}
	if g.deleteRunning[id] {
		return target, &accountDeleteError{409, "delete_in_progress", "this account is already being deleted"}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET deleting=1,delete_error='',delete_step='' WHERE id=? AND disabled=1`, id); err != nil {
		return target, &accountDeleteError{500, "storage", "cannot start deletion"}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_sessions WHERE account_id=?`, id); err != nil {
		return target, &accountDeleteError{500, "storage", "cannot start deletion"}
	}
	if err = tx.Commit(); err != nil {
		return target, &accountDeleteError{500, "storage", "cannot start deletion"}
	}
	g.cancelAccount(id)
	if runtime := g.workspaces[id]; runtime != nil {
		delete(g.workspaces, id)
		closing = runtime
	}
	g.deleteRunning[id] = true
	target.Deleting = true
	return target, nil
}

func (g *AccountGateway) deleteAccount(w http.ResponseWriter, r *http.Request, caller Account, id string) {
	if caller.Role != "admin" || caller.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return
	}
	var in struct {
		ConfirmUsername string `json:"confirm_username"`
	}
	if decodeStrict(r, 4<<10, &in) != nil {
		writeErr(w, 400, "invalid_request", "confirm_username is required")
		return
	}
	// Re-verify the caller inside the same authority as other account changes.
	var stillAdmin bool
	if g.root.store.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=? AND role='admin' AND disabled=0)`, caller.ID).Scan(&stillAdmin) != nil || !stillAdmin {
		writeErr(w, 403, "forbidden", "admin required")
		return
	}
	target, derr := g.beginAccountDelete(r.Context(), caller.ID, id, in.ConfirmUsername)
	if derr != nil {
		writeErr(w, derr.status, derr.code, derr.message)
		return
	}
	// The pipeline outlives a closed browser tab: it continues on its own context.
	code := g.runAccountDelete(context.WithoutCancel(r.Context()), target)
	if code != "" {
		writeErr(w, 503, "account_delete_failed", "account deletion stopped; retry to continue")
		return
	}
	out := map[string]any{"id": id, "deleted": true}
	if x, err := g.exportRow(r.Context(), `account_id=?`, id, true); err == nil {
		out["export"] = x
	}
	writeJSON(w, 200, out)
}

// runAccountDelete executes the remaining steps and returns "" on success or a
// short code for the step that stopped it. The code is persisted for the UI.
func (g *AccountGateway) runAccountDelete(ctx context.Context, a Account) string {
	defer func() {
		g.mu.Lock()
		delete(g.deleteRunning, a.ID)
		g.mu.Unlock()
	}()
	for _, step := range accountDeleteSteps {
		err := g.deleteStep(ctx, step, a)
		if err != nil {
			code := step + "_failed"
			var coded *accountDeleteError
			if errors.As(err, &coded) {
				code = coded.code
			}
			g.root.store.db.ExecContext(ctx, `UPDATE accounts SET delete_step=?,delete_error=? WHERE id=?`, step, code, a.ID)
			return code
		}
		if step != "record" {
			g.root.store.db.ExecContext(ctx, `UPDATE accounts SET delete_step=?,delete_error='' WHERE id=?`, step, a.ID)
		}
	}
	return ""
}

func (g *AccountGateway) deleteStep(ctx context.Context, step string, a Account) error {
	if g.deleteHook != nil {
		if err := g.deleteHook(step); err != nil {
			return err
		}
	}
	switch step {
	case "export":
		return g.exportAccount(ctx, a)
	case "computer":
		return g.deleteComputer(ctx, a.ID)
	case "data":
		return removeAccountDataDir(g.config.DataDir, a.ID)
	case "sessions":
		_, err := g.root.store.db.ExecContext(ctx, `DELETE FROM account_sessions WHERE account_id=?`, a.ID)
		return err
	case "record":
		// Guard again at the final step: only a disabled, deleting, non-legacy row goes.
		_, err := g.root.store.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=? AND deleting=1 AND disabled=1 AND legacy=0`, a.ID)
		return err
	}
	return fmt.Errorf("unknown deletion step %q", step)
}

func (g *AccountGateway) deleteComputer(ctx context.Context, id string) error {
	if g.config.AccountProvisionerSocket == "" {
		return nil // no Worker: this deployment has no computer to reclaim
	}
	// A deactivation whose broker call failed (503 computer_transition_pending)
	// leaves the account disabled here but not at the broker. Disabling is
	// idempotent, and an already-removed or unregistered computer is tolerated:
	// the delete below answers for the outcome.
	_, _ = g.computerControl(ctx, map[string]string{"op": "disable", "account_id": id})
	data, err := g.computerControl(ctx, map[string]string{"op": "delete", "account_id": id})
	if err != nil {
		return &accountDeleteError{code: "computer_unavailable", message: "computer deletion was not applied"}
	}
	var out struct {
		AccountID string `json:"account_id"`
		Deleted   bool   `json:"deleted"`
	}
	if json.Unmarshal(data, &out) != nil || out.AccountID != id || !out.Deleted {
		return &accountDeleteError{code: "computer_unverified", message: "computer deletion result unavailable"}
	}
	return nil
}

// removeAccountDataDir removes exactly <dataDir>/accounts/<id>. The id must be
// a canonical UUID; the accounts root and the account directory must be real
// directories (never symlinks) and must resolve to themselves.
func removeAccountDataDir(dataDir, id string) error {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return errors.New("invalid account identity")
	}
	if !filepath.IsAbs(dataDir) {
		return errors.New("absolute data directory required")
	}
	root := filepath.Join(dataDir, "accounts")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("unexpected accounts root")
	}
	target := filepath.Join(root, id)
	if filepath.Dir(target) != root {
		return errors.New("account path escapes its root")
	}
	info, err = os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("unexpected account data path")
	}
	return os.RemoveAll(target) // RemoveAll never follows symlinks inside the tree
}
