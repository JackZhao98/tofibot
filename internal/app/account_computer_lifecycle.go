package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type accountComputerStatus struct {
	AccountID         string `json:"account_id"`
	ComputerID        string `json:"computer_id"`
	Generation        string `json:"generation"`
	State             string `json:"state"`
	OperationID       string `json:"operation_id,omitempty"`
	Phase             string `json:"phase"`
	Error             string `json:"error"`
	Supported         bool   `json:"supported"`
	ResourcesReleased bool   `json:"resources_released"`
	Slot              int    `json:"slot"`
	QuotaBytes        int64  `json:"quota_bytes"`
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

type accountComputerTransition interface {
	acquire(context.Context) error
	release()
}

type accountComputerGate chan struct{}

func (gate accountComputerGate) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		// Cancellation can race with an available gate. Never admit a canceled
		// waiter even if the select chose the gate first.
		if err := ctx.Err(); err != nil {
			gate.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gate accountComputerGate) release() { <-gate }

func (g *AccountGateway) computerTransition(id string) accountComputerTransition {
	gate, _ := g.computerTransitions.LoadOrStore(id, accountComputerGate(make(chan struct{}, 1)))
	return gate.(accountComputerTransition)
}

func (g *AccountGateway) computerFenced(id string) bool {
	var state string
	err := g.root.store.db.QueryRow(`SELECT state FROM account_computer_lifecycle WHERE account_id=?`, id).Scan(&state)
	return err != sql.ErrNoRows && (err != nil || state != "active")
}

func (g *AccountGateway) computerStatus(ctx context.Context, target Account) (accountComputerStatus, error) {
	id := g.computerIdentity(target)
	data, err := g.computerControl(ctx, map[string]string{"op": "computer_status", "account_id": id})
	var out accountComputerStatus
	var fields map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(data, &out)
		if err == nil {
			err = json.Unmarshal(data, &fields)
		}
	}
	for _, field := range []string{"account_id", "generation", "state", "supported", "resources_released", "slot", "quota_bytes"} {
		if len(fields[field]) == 0 || string(fields[field]) == "null" {
			err = errors.New("incomplete computer status")
		}
	}
	if err != nil || out.AccountID != id || !canonicalUUID(out.Generation) || (out.State != "active" && out.State != "deleting" && out.State != "cleanup_failed" && out.State != "deleted") || out.ResourcesReleased != (out.State == "deleted") || (out.State == "deleted" && (out.Slot != 0 || out.QuotaBytes != 0)) || (out.State != "deleted" && (out.Slot < 1 || out.Slot > 250 || out.QuotaBytes < 8<<30 || out.QuotaBytes > 1024<<30)) || (out.State != "active" && !canonicalUUID(out.OperationID)) {
		return out, errors.New("computer status could not be verified")
	}
	out.AccountID, out.ComputerID = target.ID, id
	return out, nil
}

// Only the central gateway resolves identities and records an explicit intent.
// Account credentials, sessions, App storage and conversation rows are retained.
func (g *AccountGateway) adminComputer(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) < 2 || parts[1] != "computer" {
		return false
	}
	if len(parts) > 3 || (len(parts) == 3 && parts[2] != "recreate") {
		writeErr(w, 404, "not_found", "computer operation not found")
		return true
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	id := parts[0]
	var target Account
	if g.root.store.db.QueryRowContext(r.Context(), `SELECT id,username,email,role,disabled,must_change_password,legacy FROM accounts WHERE id=?`, id).Scan(&target.ID, &target.Username, &target.Email, &target.Role, &target.Disabled, &target.MustChangePassword, &target.Legacy) != nil {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	if target.Legacy || !canonicalUUID(id) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, accountComputerStatus{AccountID: id, ComputerID: g.computerIdentity(target), State: "unsupported", Error: "旧电脑迁移的磁盘身份验证仍依赖现有磁盘，暂不支持删除", Supported: false})
		} else {
			writeErr(w, 409, "legacy_computer_unsupported", "adopted legacy computer requires a separate ownership workflow")
		}
		return true
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		out, err := g.computerStatus(r.Context(), target)
		if err != nil {
			writeErr(w, 503, "computer_status_unavailable", "computer status unavailable; resources cannot be verified")
			return true
		}
		// Reconcile a lost successful response, never clear an App intent from
		// an active status read. Recreation requires its explicit operation.
		if out.State == "deleted" || out.State == "cleanup_failed" {
			g.root.store.db.Exec(`UPDATE account_computer_lifecycle SET state=?,updated_at=? WHERE account_id=? AND generation=? AND operation_id=? AND state!='active'`, out.State, time.Now().Unix(), id, out.Generation, out.OperationID)
			g.root.store.db.Exec(`UPDATE account_computer_audit SET state=?,updated_at=? WHERE account_id=? AND generation=? AND operation_id=?`, out.State, time.Now().Unix(), id, out.Generation, out.OperationID)
		}
		var localState, localOperation, localGeneration string
		if g.root.store.db.QueryRow(`SELECT state,operation_id,generation FROM account_computer_lifecycle WHERE account_id=? AND state!='active'`, id).Scan(&localState, &localOperation, &localGeneration) == nil {
			if out.State == "active" || out.OperationID != localOperation || localState == "recreating" {
				out.State, out.OperationID = localState, localOperation
				out.Generation = localGeneration
				out.Phase = localState
				out.ResourcesReleased = false
				out.Error = "操作尚未在 App 和 Worker 两侧确认完成，请重试原操作"
				if localState == "recreating" {
					var quota int
					if g.root.store.db.QueryRow(`SELECT quota_gib FROM account_computer_audit WHERE operation_id=?`, localOperation).Scan(&quota) == nil {
						out.QuotaBytes = int64(quota) << 30
					}
				}
			}
		}
		writeJSON(w, 200, out)
		return true
	}
	recreate := len(parts) == 3
	if (!recreate && r.Method != http.MethodDelete) || (recreate && r.Method != http.MethodPost) {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return true
	}
	var in struct {
		OperationID  string `json:"operation_id"`
		Generation   string `json:"expected_generation"`
		ComputerID   string `json:"confirm_computer_id"`
		AccountID    string `json:"confirm_account_id"`
		Username     string `json:"confirm_username"`
		Acknowledged bool   `json:"acknowledge_data_loss"`
		QuotaGiB     int    `json:"quota_gib,omitempty"`
	}
	if decodeStrict(r, 4096, &in) != nil || !canonicalUUID(in.OperationID) || !canonicalUUID(in.Generation) || in.ComputerID != g.computerIdentity(target) || in.AccountID != id || in.Username != target.Username || !in.Acknowledged || (recreate && (in.QuotaGiB < 8 || in.QuotaGiB > 1024)) || (!recreate && in.QuotaGiB != 0) {
		writeErr(w, 400, "confirmation_required", "explicit confirmation of this account, computer and data loss is required")
		return true
	}
	transition := g.computerTransition(id)
	if transition.acquire(r.Context()) != nil {
		writeErr(w, 409, "operation_canceled", "computer operation canceled before admission; refresh status")
		return true
	}
	defer transition.release()
	// Disconnecting the UI after accepting the intent cannot leave cancellation
	// dependent on the browser. All progress remains durable and retryable.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancel()
	op := "delete"
	intentState := "deleting"
	if recreate {
		op, intentState = "recreate", "recreating"
	}
	g.mu.Lock()
	fresh, authorized := g.session(r)
	if !authorized || fresh.ID != a.ID || fresh.Role != "admin" || fresh.MustChangePassword {
		g.mu.Unlock()
		writeErr(w, 403, "forbidden", "current admin session required")
		return true
	}
	// Bind again under the gateway lock before persisting an irreversible intent.
	err := g.root.store.db.QueryRowContext(ctx, `SELECT username,disabled FROM accounts WHERE id=?`, id).Scan(&target.Username, &target.Disabled)
	if err != nil || target.Username != in.Username || (recreate && target.Disabled) {
		g.mu.Unlock()
		writeErr(w, 409, "account_changed", "account changed; enable it before explicit recreation")
		return true
	}
	var previous accountComputerStatus
	err = g.root.store.db.QueryRowContext(ctx, `SELECT generation,operation_id,state FROM account_computer_lifecycle WHERE account_id=?`, id).Scan(&previous.Generation, &previous.OperationID, &previous.State)
	if err != nil && err != sql.ErrNoRows {
		g.mu.Unlock()
		writeErr(w, 500, "storage", "cannot verify computer intent")
		return true
	}
	if err == nil && previous.State != "active" && (previous.Generation != in.Generation || (previous.State != "deleted" && previous.OperationID != in.OperationID)) {
		g.mu.Unlock()
		writeErr(w, 409, "operation_changed", "retry the pending operation with its original generation")
		return true
	}
	current, statusErr := g.computerStatus(ctx, target)
	// A recreate response may be lost after Worker admission. Only the same
	// operation ID can finish that App intent without a second reservation.
	recreatedRetry := recreate && current.State == "active" && current.OperationID == in.OperationID && previous.State == "recreating" && previous.Generation == in.Generation
	if statusErr != nil || !current.Supported || (!recreatedRetry && current.Generation != in.Generation) || (recreate && !recreatedRetry && current.State != "deleted") || (!recreate && current.State == "deleted" && current.OperationID != in.OperationID) {
		g.mu.Unlock()
		writeErr(w, 409, "computer_changed", "computer unavailable or generation changed; refresh status")
		return true
	}
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err == nil {
		defer tx.Rollback()
		var oldID, oldGen, oldAction string
		var oldQuota int
		e := tx.QueryRowContext(ctx, `SELECT account_id,generation,action,quota_gib FROM account_computer_audit WHERE operation_id=?`, in.OperationID).Scan(&oldID, &oldGen, &oldAction, &oldQuota)
		if e != nil && e != sql.ErrNoRows {
			err = e
		} else if e == nil && (oldID != id || oldGen != in.Generation || oldAction != op || oldQuota != in.QuotaGiB) {
			err = errors.New("operation identity mismatch")
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO account_computer_lifecycle VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET generation=excluded.generation,operation_id=excluded.operation_id,state=excluded.state,actor_id=excluded.actor_id,updated_at=excluded.updated_at`, id, in.ComputerID, in.Generation, in.OperationID, intentState, a.ID, time.Now().Unix())
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO account_computer_audit VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at`, in.OperationID, id, in.ComputerID, in.Generation, op, a.ID, intentState, time.Now().Unix(), time.Now().Unix(), in.QuotaGiB)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		g.mu.Unlock()
		writeErr(w, 409, "intent_rejected", "computer deletion intent could not be saved")
		return true
	}
	initiator, _ := r.Context().Value(accountRequestKey{}).(*accountActiveRequest)
	var drains []<-chan struct{}
	for request, stop := range g.active[id] {
		if request != initiator {
			stop()
			drains = append(drains, request.done)
		}
	}
	workspace := g.workspaces[id]
	delete(g.workspaces, id)
	g.mu.Unlock()
	if workspace != nil {
		err = workspace.Close()
	}
	for _, done := range drains {
		if err != nil {
			break
		}
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil && !recreate {
		// Preserve attachment rows and names, but permanently label Guest bytes
		// before removing them. Recreating a blank disk cannot revive old IDs.
		dir := filepath.Join(g.config.DataDir, "accounts", id)
		if _, exists := os.Stat(filepath.Join(dir, "tofi.db")); exists == nil {
			var store *Store
			store, err = openStoreWithLimit(dir, false, g.config.AccountDBMaxBytes)
			if err == nil {
				_, err = store.db.Exec(`INSERT OR IGNORE INTO unavailable_guest_attachments(attachment_id) SELECT id FROM attachments WHERE disk_name LIKE 'vm:%'`)
				store.Close()
			}
		} else if !os.IsNotExist(exists) {
			err = exists
		}
	}
	if err == nil {
		data := map[string]any{"op": op, "account_id": in.ComputerID, "generation": in.Generation, "operation_id": in.OperationID}
		if recreate {
			data["quota_gib"] = in.QuotaGiB
		}
		_, err = g.computerControl(ctx, data)
	}
	var out accountComputerStatus
	if err == nil {
		out, err = g.computerStatus(ctx, target)
	}
	want := "deleted"
	if recreate {
		want = "active"
	}
	if err == nil && (out.State != want || out.OperationID != in.OperationID || (!recreate && out.Generation != in.Generation) || (recreate && (out.Generation == in.Generation || out.QuotaBytes != int64(in.QuotaGiB)<<30))) {
		err = errors.New("unverified lifecycle result")
	}
	state := "cleanup_failed"
	if recreate {
		state = "recreating"
	}
	if err == nil {
		state = want
	}
	generation := in.Generation
	if err == nil {
		generation = out.Generation
	}
	g.mu.Lock()
	_, saved := g.root.store.db.Exec(`UPDATE account_computer_lifecycle SET state=?,generation=?,updated_at=? WHERE account_id=? AND operation_id=?`, state, generation, time.Now().Unix(), id, in.OperationID)
	if saved == nil {
		_, saved = g.root.store.db.Exec(`UPDATE account_computer_audit SET state=?,updated_at=? WHERE operation_id=?`, state, time.Now().Unix(), in.OperationID)
	}
	// A read-only runtime may have opened while the operation drained. Drop it
	// after recreation so the next explicit workspace request resumes workers.
	metadataRuntime := g.workspaces[id]
	delete(g.workspaces, id)
	g.mu.Unlock()
	if metadataRuntime != nil {
		metadataRuntime.Close()
	}
	if err != nil || saved != nil {
		writeErr(w, 503, "computer_transition_pending", "电脑清理或重建尚未验证完成；账号和聊天记录保留。请刷新状态，使用同一操作重试；未确认资源已释放。")
		return true
	}
	writeJSON(w, 200, out)
	return true
}
