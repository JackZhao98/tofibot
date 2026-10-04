package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/argon2"
)

const ownerCookie = "tofi_owner_session"
const ownerSessionLifetime = 30 * 24 * time.Hour

// No owner credentials or session tokens are exposed to the agent runtime.
type ownerAuth struct {
	store         *Store
	bootstrapPath string
	allowLoopback bool
	allowLAN      bool
	mu            sync.Mutex // Serializes session validation/registration with revocation.
	active        map[string]map[*ownerRequest]struct{}
	hashing       chan struct{}
}
type ownerRequest struct{ cancel context.CancelFunc }
type ownerIdentity struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}
type ownerSessionState struct {
	Enabled                  bool           `json:"enabled"`
	SetupRequired            bool           `json:"setup_required"`
	Authenticated            bool           `json:"authenticated"`
	PasswordTransportAllowed bool           `json:"password_transport_allowed"`
	Owner                    *ownerIdentity `json:"owner,omitempty"`
}

func initializeOwnerAuth(s *Store, c Config) (*ownerAuth, error) {
	if c.IsolatedWorkspace || c.AccountRuntime {
		return nil, nil
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS owner_auth_settings (id INTEGER PRIMARY KEY CHECK(id=1), bootstrap_hash BLOB);
 CREATE TABLE IF NOT EXISTS workspace_owner (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, email TEXT NOT NULL, salt BLOB NOT NULL, password_hash BLOB NOT NULL, created_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS owner_sessions (token_hash BLOB PRIMARY KEY, expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS owner_auth_attempts (bucket TEXT PRIMARY KEY, window_start INTEGER NOT NULL, attempts INTEGER NOT NULL);`)
	if err != nil {
		return nil, err
	}
	var enabled, owners int
	if err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM owner_auth_settings), (SELECT COUNT(*) FROM workspace_owner)`).Scan(&enabled, &owners); err != nil {
		return nil, err
	}
	accountClosed, accountPersisted, err := accountBootstrapState(s)
	if err != nil {
		return nil, err
	}
	if accountPersisted && !accountClosed && owners == 0 && enabled == 0 {
		return nil, errors.New("missing persisted owner bootstrap state")
	}
	if !c.OwnerAuth && os.Getenv("TOFI_OWNER_AUTH") != "1" && enabled == 0 && owners == 0 && !accountClosed {
		return nil, nil
	}
	host, _, _ := net.SplitHostPort(c.Listen)
	ip := net.ParseIP(host)
	a := &ownerAuth{allowLAN: c.OwnerAllowLANHTTP || os.Getenv("TOFI_OWNER_ALLOW_LAN_HTTP") == "1", store: s, bootstrapPath: filepath.Join(c.DataDir, "owner-bootstrap.secret"), allowLoopback: (c.OwnerAllowLoopbackHTTP || os.Getenv("TOFI_OWNER_ALLOW_LOOPBACK_HTTP") == "1") && ip != nil && ip.IsLoopback(), active: make(map[string]map[*ownerRequest]struct{}), hashing: make(chan struct{}, 2)}
	var saved []byte
	var consumed bool
	if enabled != 0 {
		if err = s.db.QueryRow(`SELECT bootstrap_hash, bootstrap_hash IS NULL FROM owner_auth_settings WHERE id=1`).Scan(&saved, &consumed); err != nil {
			return nil, err
		}
	}
	if owners != 0 || accountClosed || consumed {
		_, err = s.db.Exec(`INSERT INTO owner_auth_settings(id) VALUES(1) ON CONFLICT(id) DO UPDATE SET bootstrap_hash=NULL`)
		if err == nil {
			err = removeOwnerBootstrap(a.bootstrapPath)
		}
		return a, err
	}
	if enabled != 0 && len(saved) != sha256.Size {
		return nil, errors.New("invalid persisted owner bootstrap hash")
	}
	secret, err := readOwnerBootstrap(a.bootstrapPath)
	if errors.Is(err, os.ErrNotExist) && enabled == 0 {
		secret = ownerRandom(32)
		var f *os.File
		f, err = os.OpenFile(a.bootstrapPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.WriteString(secret + "\n")
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("owner bootstrap file: %w", err)
	}
	hash := sha256.Sum256([]byte(secret))
	if enabled != 0 {
		if subtle.ConstantTimeCompare(saved, hash[:]) != 1 {
			return nil, errors.New("owner bootstrap file does not match persisted setup")
		}
	} else {
		_, err = s.db.Exec(`INSERT INTO owner_auth_settings(id,bootstrap_hash) VALUES(1,?)`, hash[:])
	}
	return a, err
}
func readOwnerBootstrap(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 128 {
		return "", errors.New("expected a regular 0600 bootstrap file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, actual) {
		return "", errors.New("bootstrap file changed")
	}
	buf := make([]byte, 129)
	n, err := f.Read(buf)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(buf[:n]))
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid bootstrap file")
	}
	return secret, nil
}
func removeOwnerBootstrap(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func ownerRandom(size int) string {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (a *ownerAuth) transportOK(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// Normalize IPv4-mapped IPv6 before applying the actual peer boundary.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return (a.allowLoopback && ip.IsLoopback()) || (a.allowLAN && (ip.IsPrivate() || ip.IsLoopback()))
}
func (s *Server) ownerAuthInfo() map[string]any {
	info := map[string]any{"mode": s.ownerAuthMode()}
	if s.ownerAuth != nil && s.ownerAuth.allowLAN {
		info["lan_http"] = true
	}
	return info
}
func (s *Server) ownerAuthMode() string {
	if s.ownerAuth != nil {
		return "owner-password"
	}
	return "none"
}
func (a *ownerAuth) state(r *http.Request) (ownerSessionState, error) {
	st := ownerSessionState{Enabled: true, PasswordTransportAllowed: a.transportOK(r)}
	var owner ownerIdentity
	err := a.store.db.QueryRow(`SELECT username,email FROM workspace_owner WHERE id=1`).Scan(&owner.Username, &owner.Email)
	if errors.Is(err, sql.ErrNoRows) {
		st.SetupRequired = true
		return st, nil
	}
	if err != nil {
		return st, err
	}
	// Tofi is currently single-owner. The first (and only) workspace account
	// therefore has the admin role; keeping it explicit lets the client render
	// destructive workspace controls without inferring privilege from a name.
	owner.Role = "admin"
	// Never disclose the owner identity to an unauthenticated visitor.
	_, _, ok := a.session(r)
	if ok {
		st.Authenticated = true
		st.Owner = &owner
	}
	return st, nil
}

// resetForPurge revokes the current owner and creates a fresh one-time setup
// secret. The next account created through /api/auth/setup is the new admin.
func (a *ownerAuth) resetForPurge() error {
	if a == nil {
		return nil
	}
	secret := ownerRandom(32)
	hash := sha256.Sum256([]byte(secret))
	temporary := a.bootstrapPath + ".purge-" + ownerRandom(8)
	f, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(secret + "\n"); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.store.db.Begin()
	if err == nil {
		defer tx.Rollback()
		for _, query := range []string{
			`DELETE FROM owner_sessions`,
			`DELETE FROM owner_auth_attempts`,
			`DELETE FROM workspace_owner`,
			`DELETE FROM owner_auth_settings`,
		} {
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO owner_auth_settings(id,bootstrap_hash) VALUES(1,?)`, hash[:])
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err = os.Rename(temporary, a.bootstrapPath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
func (a *ownerAuth) session(r *http.Request) (string, time.Time, bool) {
	if !a.transportOK(r) {
		return "", time.Time{}, false
	}
	cookie, err := r.Cookie(ownerCookie)
	if err != nil || len(cookie.Value) != 43 {
		return "", time.Time{}, false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	var expiry int64
	err = a.store.db.QueryRow(`SELECT expires_at FROM owner_sessions WHERE token_hash=?`, hash[:]).Scan(&expiry)
	return string(hash[:]), time.Unix(expiry, 0), err == nil && time.Now().Unix() < expiry
}
func (a *ownerAuth) issue(w http.ResponseWriter, r *http.Request) error {
	token := ownerRandom(32)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(ownerSessionLifetime)
	_, err := a.store.db.Exec(`INSERT INTO owner_sessions(token_hash,expires_at) VALUES(?,?)`, hash[:], expires.Unix())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: int(ownerSessionLifetime.Seconds()), Expires: expires})
	// Give the response the same state semantics as a subsequent session request.
	r.AddCookie(&http.Cookie{Name: ownerCookie, Value: token})
	return nil
}
func (a *ownerAuth) hash(ctx context.Context, password string, salt []byte) ([]byte, error) {
	select {
	case a.hashing <- struct{}{}:
		defer func() { <-a.hashing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32), nil
}

// Count failed and successful attempts alike. Durable global and per-peer bounds
// limit password hashing and do not trust proxy-supplied client addresses.
func (a *ownerAuth) allowAttempt(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	sum := sha256.Sum256([]byte(host))
	peer := base64.RawURLEncoding.EncodeToString(sum[:])
	now := time.Now().Unix()
	tx, err := a.store.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM owner_auth_attempts WHERE window_start<=?`, now-900); err != nil {
		return false
	}
	for _, b := range []struct {
		key   string
		limit int
	}{{"global", 64}, {peer, 8}} {
		var n int
		err = tx.QueryRow(`SELECT attempts FROM owner_auth_attempts WHERE bucket=?`, b.key).Scan(&n)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if n >= b.limit {
			return false
		}
		if _, err = tx.Exec(`INSERT INTO owner_auth_attempts(bucket,window_start,attempts) VALUES(?,?,1) ON CONFLICT(bucket) DO UPDATE SET attempts=attempts+1`, b.key, now); err != nil {
			return false
		}
	}
	return tx.Commit() == nil
}
func validateOwner(username, email, password string) bool {
	if len(username) < 3 || len(username) > 64 || len(email) > 254 || len(password) < 12 || len(password) > 1024 {
		return false
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}
func (s *Server) handleOwnerAuth(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p != "/api/auth/session" && p != "/api/auth/setup" && p != "/api/auth/login" && p != "/api/auth/logout" {
		return false
	}
	if (p == "/api/auth/session" && r.Method != http.MethodGet) || (p != "/api/auth/session" && r.Method != http.MethodPost) {
		writeErr(w, 405, "method_not_allowed", "method not allowed")
		return true
	}
	a := s.ownerAuth
	if a == nil {
		if p == "/api/auth/session" {
			writeJSON(w, 200, ownerSessionState{})
		} else {
			writeErr(w, 409, "auth_disabled", "owner authentication is not enabled")
		}
		return true
	}
	respond := func() {
		st, err := a.state(r)
		if err != nil {
			writeErr(w, 500, "storage", "cannot read authentication state")
		} else {
			writeJSON(w, 200, st)
		}
	}
	if p == "/api/auth/session" {
		respond()
		return true
	}
	if p == "/api/auth/logout" {
		a.mu.Lock()
		key, _, ok := a.session(r)
		if ok {
			_, err := a.store.db.Exec(`DELETE FROM owner_sessions WHERE token_hash=?`, []byte(key))
			if err != nil {
				a.mu.Unlock()
				writeErr(w, 500, "storage", "cannot revoke session")
				return true
			}
			for req := range a.active[key] {
				req.cancel()
			}
			delete(a.active, key)
		}
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
		respond()
		return true
	}
	if !a.transportOK(r) {
		writeErr(w, 400, "password_transport_required", "use HTTPS or an explicitly configured LAN HTTP or loopback SSH ingress")
		return true
	}
	if !a.allowAttempt(r) {
		w.Header().Set("Retry-After", "900")
		writeErr(w, 429, "rate_limited", "too many authentication attempts; try again later")
		return true
	}
	if p == "/api/auth/setup" {
		var v struct {
			BootstrapSecret string `json:"bootstrap_secret"`
			Username        string `json:"username"`
			Email           string `json:"email"`
			Password        string `json:"password"`
		}
		if decodeStrict(r, 8<<10, &v) != nil {
			writeErr(w, 400, "invalid_request", "invalid setup request")
			return true
		}
		v.Username = strings.TrimSpace(v.Username)
		v.Email = strings.ToLower(strings.TrimSpace(v.Email))
		if !validateOwner(v.Username, v.Email, v.Password) {
			writeErr(w, 400, "invalid_request", "use a 3–64 character username, a valid email, and a 12–1024 byte password")
			return true
		}
		secretHash := sha256.Sum256([]byte(v.BootstrapSecret))
		var saved []byte
		err := a.store.db.QueryRow(`SELECT bootstrap_hash FROM owner_auth_settings WHERE id=1`).Scan(&saved)
		if err != nil || subtle.ConstantTimeCompare(saved, secretHash[:]) != 1 {
			writeErr(w, 401, "invalid_bootstrap", "setup secret is invalid or already consumed")
			return true
		}
		salt := make([]byte, 16)
		if _, err = rand.Read(salt); err != nil {
			writeErr(w, 500, "auth_unavailable", "cannot initialize owner")
			return true
		}
		hash, err := a.hash(r.Context(), v.Password, salt)
		if err != nil {
			return true
		}
		tx, err := a.store.db.Begin()
		if err == nil {
			defer tx.Rollback()
			result, e := tx.Exec(`UPDATE owner_auth_settings SET bootstrap_hash=NULL WHERE id=1 AND bootstrap_hash=?`, secretHash[:])
			err = e
			if err == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					err = errors.New("setup consumed")
				}
			}
			if err == nil {
				_, err = tx.Exec(`INSERT INTO workspace_owner(id,username,email,salt,password_hash,created_at) VALUES(1,?,?,?,?,?)`, v.Username, v.Email, salt, hash, time.Now().Unix())
			}
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
		if err != nil {
			writeErr(w, 409, "setup_unavailable", "owner setup is no longer available")
			return true
		}
		// A stale file cannot authorize setup once its durable hash is consumed.
		_ = removeOwnerBootstrap(a.bootstrapPath)
	} else {
		var v struct {
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		}
		if decodeStrict(r, 8<<10, &v) != nil || len(v.Identifier) > 254 || len(v.Password) > 1024 {
			writeErr(w, 400, "invalid_request", "invalid login request")
			return true
		}
		var username, email string
		var salt, saved []byte
		err := a.store.db.QueryRow(`SELECT username,email,salt,password_hash FROM workspace_owner WHERE id=1`).Scan(&username, &email, &salt, &saved)
		if err != nil {
			writeErr(w, 401, "invalid_credentials", "invalid identifier or password")
			return true
		}
		hash, err := a.hash(r.Context(), v.Password, salt)
		if err != nil {
			return true
		}
		identifier := strings.TrimSpace(v.Identifier)
		if subtle.ConstantTimeCompare(hash, saved) != 1 || (!strings.EqualFold(identifier, username) && !strings.EqualFold(identifier, email)) {
			writeErr(w, 401, "invalid_credentials", "invalid identifier or password")
			return true
		}
	}
	// Replace an existing cookie so subsequent state inspection sees the new token.
	r.Header.Del("Cookie")
	if err := a.issue(w, r); err != nil {
		writeErr(w, 500, "storage", "cannot create session")
		return true
	}
	respond()
	return true
}

// These narrow exceptions already require a device bearer/pairing secret or a
// stored OAuth state. Administrative pairing creation remains owner-only.
func ownerIndependentRoute(r *http.Request) bool {
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if r.Method == http.MethodGet && r.URL.Path == "/api/server-info" {
		return true
	}
	if len(p) == 2 && p[0] == "computers" && p[1] == "pair" && r.Method == http.MethodPost {
		return true
	}
	if len(p) >= 3 && p[0] == "computers" && p[1] != "" {
		if len(p) == 3 && ((p[2] == "capabilities" && r.Method == http.MethodPatch) || (p[2] == "jobs" && r.Method == http.MethodGet)) {
			return true
		}
		if len(p) == 5 && p[2] == "jobs" && p[3] != "" && p[4] == "result" && r.Method == http.MethodPost {
			return true
		}
		if len(p) == 5 && p[2] == "jobs" && p[3] != "" && p[4] == "status" && r.Method == http.MethodGet {
			return true
		}
	}
	if len(p) == 5 && p[0] == "extensions" && p[1] == "local-mcp" && p[2] == "gog" && p[3] == "oauth" && p[4] == "callback" && r.Method == http.MethodGet {
		return true
	}
	return len(p) == 5 && p[0] == "extensions" && p[1] == "mcp" && p[2] != "" && p[3] == "oauth" && p[4] == "callback" && r.Method == http.MethodGet
}
func (s *Server) ownerAuthorized(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	a := s.ownerAuth
	if a == nil || ownerIndependentRoute(r) {
		return r, func() {}, true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key, expiry, ok := a.session(r)
	if !ok {
		writeErr(w, 401, "auth_required", "owner sign-in required")
		return r, func() {}, false
	}
	ctx, cancel := context.WithDeadline(r.Context(), expiry)
	req := &ownerRequest{cancel: cancel}
	if a.active[key] == nil {
		a.active[key] = make(map[*ownerRequest]struct{})
	}
	a.active[key][req] = struct{}{}
	cleanup := func() {
		cancel()
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.active[key], req)
		if len(a.active[key]) == 0 {
			delete(a.active, key)
		}
	}
	return r.WithContext(ctx), cleanup, true
}
func (a *ownerAuth) close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, requests := range a.active {
		for req := range requests {
			req.cancel()
		}
	}
}
