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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	trusted       []*net.IPNet // reverse proxies whose X-Forwarded-For is believed
	secureCookies bool         // public origin is https: TLS ends at a proxy, so r.TLS is nil
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
 CREATE TABLE IF NOT EXISTS owner_auth_attempts (bucket TEXT PRIMARY KEY, window_start INTEGER NOT NULL, attempts INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS owner_auth_known_sources (account_key TEXT NOT NULL, source TEXT NOT NULL, last_ok INTEGER NOT NULL, PRIMARY KEY(account_key, source));`)
	if err != nil {
		return nil, err
	}
	var enabled, owners int
	if err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM owner_auth_settings), (SELECT COUNT(*) FROM workspace_owner)`).Scan(&enabled, &owners); err != nil {
		return nil, err
	}
	// Accounts mode keeps its first admin outside workspace_owner.
	accountClosed, err := accountBootstrapState(s)
	if err != nil {
		return nil, err
	}
	if !c.OwnerAuth && os.Getenv("TOFI_OWNER_AUTH") != "1" && enabled == 0 && owners == 0 && !accountClosed {
		return nil, nil
	}
	host, _, _ := net.SplitHostPort(c.Listen)
	ip := net.ParseIP(host)
	a := &ownerAuth{allowLAN: c.OwnerAllowLANHTTP || os.Getenv("TOFI_OWNER_ALLOW_LAN_HTTP") == "1", store: s, bootstrapPath: filepath.Join(c.DataDir, "owner-bootstrap.secret"), allowLoopback: (c.OwnerAllowLoopbackHTTP || os.Getenv("TOFI_OWNER_ALLOW_LOOPBACK_HTTP") == "1") && ip != nil && ip.IsLoopback(), active: make(map[string]map[*ownerRequest]struct{}), hashing: make(chan struct{}, 2), secureCookies: strings.HasPrefix(strings.ToLower(c.PublicOrigin), "https://")}
	if a.trusted, err = parseTrustedProxies(os.Getenv("TOFI_TRUSTED_PROXIES")); err != nil {
		return nil, err
	}
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
			`DELETE FROM owner_auth_known_sources`,
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
// cookieSecure marks session cookies Secure when the request arrived over TLS
// or the configured public origin is https (TLS terminated by a proxy).
func (a *ownerAuth) cookieSecure(r *http.Request) bool {
	return r.TLS != nil || (a != nil && a.secureCookies)
}
func (a *ownerAuth) issue(w http.ResponseWriter, r *http.Request) error {
	token := ownerRandom(32)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(ownerSessionLifetime)
	_, err := a.store.db.Exec(`INSERT INTO owner_sessions(token_hash,expires_at) VALUES(?,?)`, hash[:], expires.Unix())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: token, Path: "/", HttpOnly: true, Secure: a.cookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(ownerSessionLifetime.Seconds()), Expires: expires})
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

// Attempts are bounded per client address and per account identifier. There is
// deliberately no global bucket: anonymous traffic from many addresses must not
// be able to lock every sign-in. Hashing cost stays bounded by the `hashing`
// semaphore. Durable, so restarts do not reset the budget.
const (
	peerAttemptLimit    = 8
	accountAttemptLimit = 20
)

type attemptBucket struct {
	key   string
	limit int
}

// Bucket keys live in one table, so every kind has its own namespace prefix and
// its own hash input: an identifier typed into the login form (any string,
// including "id:<uuid>") can never name another kind of bucket.
func namespacedBucket(kind, subject string, limit int) attemptBucket {
	sum := sha256.Sum256([]byte(kind + "\x00" + subject))
	return attemptBucket{kind + ":" + base64.RawURLEncoding.EncodeToString(sum[:]), limit}
}

// accountBucket is the global per-identifier login budget.
func accountBucket(identifier string) attemptBucket {
	return namespacedBucket("login-acct", strings.ToLower(strings.TrimSpace(identifier)), accountAttemptLimit)
}

// passwordChangeBucket bounds current-password guesses by a signed-in session.
func passwordChangeBucket(accountID string) attemptBucket {
	return namespacedBucket("pwchange", accountID, accountAttemptLimit)
}

const knownSourceRetention = 90 * 24 * 3600
const knownSourcesPerAccount = 16

// knownSource reports whether this peer address previously proved the
// password of this login identifier.
func (a *ownerAuth) knownSource(r *http.Request, account string) bool {
	var n int
	err := a.store.db.QueryRow(`SELECT COUNT(*) FROM owner_auth_known_sources WHERE account_key=? AND source=? AND last_ok>?`,
		accountBucket(account).key, a.peerBucket(r), time.Now().Unix()-knownSourceRetention).Scan(&n)
	return err == nil && n > 0
}

// rememberSource records a proven password from this peer for the identifier.
func (a *ownerAuth) rememberSource(r *http.Request, account string) {
	key, now := accountBucket(account).key, time.Now().Unix()
	_, _ = a.store.db.Exec(`INSERT INTO owner_auth_known_sources(account_key,source,last_ok) VALUES(?,?,?) ON CONFLICT(account_key,source) DO UPDATE SET last_ok=excluded.last_ok`, key, a.peerBucket(r), now)
	_, _ = a.store.db.Exec(`DELETE FROM owner_auth_known_sources WHERE account_key=? AND (last_ok<=? OR source NOT IN (SELECT source FROM owner_auth_known_sources WHERE account_key=? ORDER BY last_ok DESC LIMIT ?))`, key, now-knownSourceRetention, key, knownSourcesPerAccount)
}

// allowAttempt charges the caller's address bucket and, when account is not
// empty, that identifier's global bucket. A peer address that earlier proved
// this account's password skips the global bucket (it still pays its address
// bucket), so strangers guessing from elsewhere cannot lock the real user out,
// while the guessing budget for unknown sources stays exactly what it was.
func (a *ownerAuth) allowAttempt(r *http.Request, account string) bool {
	buckets := []attemptBucket{{a.peerBucket(r), peerAttemptLimit}}
	if account != "" && !a.knownSource(r, account) {
		buckets = append(buckets, accountBucket(account))
	}
	return a.charge(buckets)
}

// allowPasswordChange charges the password-change bucket for a signed-in account.
func (a *ownerAuth) allowPasswordChange(accountID string) bool {
	return a.charge([]attemptBucket{passwordChangeBucket(accountID)})
}

func (a *ownerAuth) charge(buckets []attemptBucket) bool {
	now := time.Now().Unix()
	tx, err := a.store.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM owner_auth_attempts WHERE window_start<=?`, now-900); err != nil {
		return false
	}
	for _, b := range buckets {
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
func (s *Server) handleOwnerAuth(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p != "/api/auth/session" && p != "/api/auth/setup" && p != "/api/auth/setup/verify" && p != "/api/auth/login" && p != "/api/auth/logout" {
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
		http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
		respond()
		return true
	}
	if !a.transportOK(r) {
		writeErr(w, 400, "password_transport_required", "use HTTPS or an explicitly configured LAN HTTP or loopback SSH ingress")
		return true
	}
	limit := func(account string) bool {
		if a.allowAttempt(r, account) {
			return true
		}
		w.Header().Set("Retry-After", "900")
		writeErr(w, 429, "rate_limited", "too many authentication attempts; try again later")
		return false
	}
	var loginAccount string
	// A closed setup is a cheap read; it must never spend the attempt budget.
	if p != "/api/auth/login" {
		var consumed, owners int
		if a.store.db.QueryRow(`SELECT COALESCE((SELECT bootstrap_hash IS NULL FROM owner_auth_settings WHERE id=1),0), (SELECT COUNT(*) FROM workspace_owner)`).Scan(&consumed, &owners) == nil && (consumed != 0 || owners != 0) {
			writeErr(w, 409, "setup_unavailable", "owner setup is no longer available")
			return true
		}
	}
	if p == "/api/auth/setup/verify" {
		var v struct {
			BootstrapSecret string `json:"bootstrap_secret"`
		}
		if decodeStrict(r, 8<<10, &v) != nil {
			writeErr(w, 400, "invalid_request", "invalid setup request")
			return true
		}
		if !limit("") {
			return true
		}
		var saved []byte
		err := a.store.db.QueryRow(`SELECT bootstrap_hash FROM owner_auth_settings WHERE id=1`).Scan(&saved)
		if !verifyBootstrapSecret(saved, err, v.BootstrapSecret) {
			writeErr(w, 401, "invalid_bootstrap", "setup secret is invalid or already consumed")
			return true
		}
		writeJSON(w, 200, map[string]bool{"valid": true})
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
		if !limit("") {
			return true
		}
		v.Username = strings.TrimSpace(v.Username)
		v.Email = strings.ToLower(strings.TrimSpace(v.Email))
		// Secret first, then per-field errors, as in accounts mode.
		secretHash := sha256.Sum256([]byte(v.BootstrapSecret))
		var saved []byte
		err := a.store.db.QueryRow(`SELECT bootstrap_hash FROM owner_auth_settings WHERE id=1`).Scan(&saved)
		if err != nil || subtle.ConstantTimeCompare(saved, secretHash[:]) != 1 {
			writeErr(w, 401, "invalid_bootstrap", "setup secret is invalid or already consumed")
			return true
		}
		if e := checkAccountFields(v.Username, v.Email, v.Password); e != nil {
			// Validation failures are not guesses at the secret.
			a.refundAttempt(r, "")
			writeFieldErr(w, 400, e)
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
		// Malformed requests never reach a password check and are not charged.
		if decodeStrict(r, 8<<10, &v) != nil || len(v.Identifier) > 254 || len(v.Password) > 1024 {
			writeErr(w, 400, "invalid_request", "invalid login request")
			return true
		}
		loginAccount = strings.TrimSpace(v.Identifier)
		if loginAccount == "" {
			loginAccount = "-"
		}
		if !limit(loginAccount) {
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
	if loginAccount != "" {
		// A proven password gives the attempt back, so honest sign-ins never wear
		// down the budget that bounds guessing.
		a.refundAttempt(r, loginAccount)
		a.rememberSource(r, loginAccount)
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

// verifyBootstrapSecret is the shared constant-time check behind the
// non-consuming verify route. It never logs or returns the secret.
func verifyBootstrapSecret(saved []byte, queryErr error, secret string) bool {
	hash := sha256.Sum256([]byte(secret))
	return secret != "" && queryErr == nil && len(saved) == sha256.Size && subtle.ConstantTimeCompare(saved, hash[:]) == 1
}

func (a *ownerAuth) peerBucket(r *http.Request) string {
	ip := clientIP(r, a.trusted)
	// One IPv6 subscriber controls a whole /64; key on the prefix.
	if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() == nil {
		ip = parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	sum := sha256.Sum256([]byte(ip))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// refundAttempt gives back one attempt for a request that was rejected before
// it could test any secret (field validation) or that proved the password.
func (a *ownerAuth) refundAttempt(r *http.Request, account string) {
	a.refundBucket(a.peerBucket(r))
	if account != "" && !a.knownSource(r, account) {
		a.refundBucket(accountBucket(account).key)
	}
}
func (a *ownerAuth) refundBucket(key string) {
	_, _ = a.store.db.Exec(`UPDATE owner_auth_attempts SET attempts=attempts-1 WHERE attempts>0 AND bucket=?`, key)
}
