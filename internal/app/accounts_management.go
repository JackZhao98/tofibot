package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"
)

type accountActiveRequest struct{ token string }

func (g *AccountGateway) register(r *http.Request) (*http.Request, Account, func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.session(r)
	if !ok {
		return r, a, func() {}, false
	}
	cookie, _ := r.Cookie(ownerCookie)
	hash := sha256.Sum256([]byte(cookie.Value))
	var expires int64
	if g.root.store.db.QueryRow(`SELECT expires_at FROM account_sessions WHERE token_hash=?`, hash[:]).Scan(&expires) != nil {
		return r, a, func() {}, false
	}
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(expires, 0))
	request := &accountActiveRequest{token: string(hash[:])}
	if g.active[a.ID] == nil {
		g.active[a.ID] = map[*accountActiveRequest]context.CancelFunc{}
	}
	g.active[a.ID][request] = cancel
	cleanup := func() {
		cancel()
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.active[a.ID], request)
		if len(g.active[a.ID]) == 0 {
			delete(g.active, a.ID)
		}
	}
	return r.WithContext(ctx), a, cleanup, true
}

// Caller holds mu. Revoke live requests before returning from a committed change.
func (g *AccountGateway) cancelAccount(id string) {
	for _, cancel := range g.active[id] {
		cancel()
	}
}
func (g *AccountGateway) manageAccount(w http.ResponseWriter, r *http.Request, a Account) bool {
	if r.URL.Path == "/api/auth/logout" && r.Method == http.MethodPost {
		cookie, _ := r.Cookie(ownerCookie)
		hash := sha256.Sum256([]byte(cookie.Value))
		g.mu.Lock()
		_, err := g.root.store.db.Exec(`DELETE FROM account_sessions WHERE token_hash=?`, hash[:])
		if err == nil {
			for req, cancel := range g.active[a.ID] {
				if req.token == string(hash[:]) {
					cancel()
				}
			}
		}
		g.mu.Unlock()
		if err != nil {
			writeErr(w, 500, "storage", "cannot revoke session")
			return true
		}
		http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		r.Header.Del("Cookie")
		writeJSON(w, 200, g.sessionState(r))
		return true
	}
	if r.URL.Path == "/api/auth/password" && r.Method == http.MethodPost {
		if !g.auth.transportOK(r) {
			writeErr(w, 403, "transport", "secure password transport required")
			return true
		}
		if !g.auth.allowAttempt(r) {
			writeErr(w, 429, "rate_limited", "try later")
			return true
		}
		var in struct {
			CurrentPassword string `json:"current_password"`
			Password        string `json:"password"`
		}
		if decodeStrict(r, 8<<10, &in) != nil || len(in.CurrentPassword) > 1024 || !validateOwner(a.Username, a.Email, in.Password) {
			writeErr(w, 400, "invalid_request", "invalid password")
			return true
		}
		var salt, saved []byte
		if g.root.store.db.QueryRow(`SELECT salt,password_hash FROM accounts WHERE id=? AND disabled=0`, a.ID).Scan(&salt, &saved) != nil {
			writeErr(w, 401, "auth_required", "account unavailable")
			return true
		}
		old, err := g.auth.hash(r.Context(), in.CurrentPassword, salt)
		if err != nil || subtle.ConstantTimeCompare(old, saved) != 1 {
			writeErr(w, 401, "invalid_credentials", "incorrect current password")
			return true
		}
		nextSalt := make([]byte, 16)
		if _, err = rand.Read(nextSalt); err != nil {
			writeErr(w, 500, "auth_unavailable", "password unavailable")
			return true
		}
		next, err := g.auth.hash(r.Context(), in.Password, nextSalt)
		if err != nil {
			writeErr(w, 400, "auth_unavailable", "password unavailable")
			return true
		}
		g.mu.Lock()
		tx, err := g.root.store.db.BeginTx(r.Context(), nil)
		if err == nil {
			defer tx.Rollback()
			res, e := tx.ExecContext(r.Context(), `UPDATE accounts SET salt=?,password_hash=?,must_change_password=0 WHERE id=? AND disabled=0 AND password_hash=?`, nextSalt, next, a.ID, saved)
			err = e
			if err == nil {
				n, _ := res.RowsAffected()
				if n != 1 {
					err = errors.New("account changed")
				}
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `DELETE FROM account_sessions WHERE account_id=?`, a.ID)
			}
			if err == nil {
				err = tx.Commit()
			}
		}
		if err == nil {
			g.cancelAccount(a.ID)
		}
		g.mu.Unlock()
		if err != nil {
			writeErr(w, 409, "account_changed", "account changed; sign in again")
			return true
		}
		if g.issue(w, r, a.ID) != nil {
			writeErr(w, 500, "storage", "password updated; sign in again")
			return true
		}
		writeJSON(w, 200, g.sessionState(r))
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/accounts/") {
		return false
	}
	if a.Role != "admin" {
		writeErr(w, 403, "forbidden", "admin required")
		return true
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/accounts/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return true
	}
	var in struct {
		Disabled *bool   `json:"disabled"`
		Role     *string `json:"role"`
		Password string  `json:"initial_password"`
	}
	if r.Method == http.MethodDelete {
		value := true
		in.Disabled = &value
	} else if decodeStrict(r, 8<<10, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid account change")
		return true
	}
	if in.Role != nil && *in.Role != "admin" && *in.Role != "user" {
		writeErr(w, 400, "invalid_request", "invalid role")
		return true
	}
	if id == a.ID && ((in.Disabled != nil && *in.Disabled) || (in.Role != nil && *in.Role != "admin")) {
		writeErr(w, 409, "self_lockout", "cannot disable or demote your own account")
		return true
	}
	var salt, hash []byte
	if in.Password != "" {
		if !g.auth.transportOK(r) || len(in.Password) < 12 || len(in.Password) > 1024 {
			writeErr(w, 400, "invalid_request", "invalid initial password")
			return true
		}
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			writeErr(w, 500, "auth_unavailable", "password unavailable")
			return true
		}
		var err error
		hash, err = g.auth.hash(r.Context(), in.Password, salt)
		if err != nil {
			writeErr(w, 400, "auth_unavailable", "password unavailable")
			return true
		}
	}
	g.mu.Lock()
	tx, err := g.root.store.db.BeginTx(r.Context(), nil)
	var target Account
	if err == nil {
		defer tx.Rollback()
		var role string
		err = tx.QueryRowContext(r.Context(), `SELECT role FROM accounts WHERE id=? AND disabled=0`, a.ID).Scan(&role)
		if err == nil && role != "admin" {
			err = errors.New("admin required")
		}
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT id,username,email,role,disabled,must_change_password,legacy FROM accounts WHERE id=?`, id).Scan(&target.ID, &target.Username, &target.Email, &target.Role, &target.Disabled, &target.MustChangePassword, &target.Legacy)
		}
		if err == nil {
			disabled := target.Disabled
			role := target.Role
			if in.Disabled != nil {
				disabled = *in.Disabled
			}
			if in.Role != nil {
				role = *in.Role
			}
			if target.Role == "admin" && !target.Disabled && (disabled || role != "admin") {
				var count int
				err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM accounts WHERE role='admin' AND disabled=0`).Scan(&count)
				if err == nil && count <= 1 {
					err = errors.New("last admin cannot be disabled or demoted")
				}
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE accounts SET role=?,disabled=? WHERE id=?`, role, disabled, id)
			}
			if err == nil && in.Password != "" {
				_, err = tx.ExecContext(r.Context(), `UPDATE accounts SET salt=?,password_hash=?,must_change_password=1 WHERE id=?`, salt, hash, id)
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `DELETE FROM account_sessions WHERE account_id=?`, id)
			}
			if err == nil {
				err = tx.Commit()
				target.Role = role
				target.Disabled = disabled
				target.MustChangePassword = target.MustChangePassword || in.Password != ""
			}
		}
	}
	var runtime *Server
	var computerErr error
	if err == nil {
		g.cancelAccount(id)
		if target.Disabled {
			runtime = g.workspaces[id]
			delete(g.workspaces, id)
		}
		if in.Disabled != nil && (!target.Legacy || g.legacyComputerPhase == "worker") {
			op := "restore"
			if target.Disabled {
				op = "disable"
			}
			_, computerErr = g.computerRequest(r.Context(), op, g.computerIdentity(target))
		}
	}
	g.mu.Unlock()
	if err != nil {
		writeErr(w, 409, "account_change_rejected", "account missing, changed, or last admin protected")
		return true
	}
	if runtime != nil {
		runtime.Close()
	}
	if computerErr != nil {
		writeErr(w, 503, "computer_transition_pending", "account state saved; computer transition requires retry")
		return true
	}
	writeJSON(w, 200, target)
	return true
}

func (g *AccountGateway) sessionState(r *http.Request) map[string]any {
	a, ok := g.session(r)
	var consumed int
	g.root.store.db.QueryRow(`SELECT consumed FROM account_bootstrap WHERE id=1`).Scan(&consumed)
	out := map[string]any{"enabled": true, "setup_required": consumed == 0, "authenticated": ok, "password_transport_allowed": g.auth.transportOK(r), "multi_account": true}
	if ok {
		out["owner"] = a
	}
	return out
}
