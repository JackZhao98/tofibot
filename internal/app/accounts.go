package app

// AccountGateway is deliberately opt-in until the complete isolation release
// (including host provisioning and UI) passes acceptance. No public route can
// select a filesystem path, workspace instance or computer socket.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Account struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
	Legacy             bool   `json:"-"`
}
type AccountGateway struct {
	root                *Server
	config              Config
	auth                *ownerAuth
	mu                  sync.Mutex
	workspaces          map[string]*Server
	active              map[string]map[*accountActiveRequest]context.CancelFunc
	closed              bool
	runtimeFactory      func(Config) (*Server, error)
	legacyComputerPhase string
}

func NewAccountGateway(c Config) (*AccountGateway, error) {
	if c.DataDir == "" {
		return nil, errors.New("account gateway requires explicit data directory")
	}
	if c.AccountDBMaxBytes == 0 {
		c.AccountDBMaxBytes = 1 << 30
	}
	controlConfig := c
	controlConfig.AccountControlPlane = true
	root, err := NewServer(controlConfig)
	if err != nil {
		return nil, err
	}
	g := &AccountGateway{root: root, config: c, workspaces: map[string]*Server{}, active: map[string]map[*accountActiveRequest]context.CancelFunc{}}
	g.auth = root.ownerAuth
	if g.auth == nil {
		g.auth = &ownerAuth{store: root.store, hashing: make(chan struct{}, 2), allowLoopback: c.OwnerAllowLoopbackHTTP, allowLAN: c.OwnerAllowLANHTTP}
	}
	tx, err := root.store.db.Begin()
	if err != nil {
		root.Close()
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS accounts(id TEXT PRIMARY KEY,username TEXT NOT NULL COLLATE NOCASE UNIQUE,email TEXT NOT NULL COLLATE NOCASE UNIQUE,role TEXT NOT NULL CHECK(role IN ('admin','user')),salt BLOB NOT NULL,password_hash BLOB NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,must_change_password INTEGER NOT NULL DEFAULT 0,legacy INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS accounts_legacy ON accounts(legacy) WHERE legacy=1;
 CREATE TABLE IF NOT EXISTS account_sessions(token_hash BLOB PRIMARY KEY,account_id TEXT NOT NULL REFERENCES accounts(id),expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS account_migration(id INTEGER PRIMARY KEY CHECK(id=1),legacy_sessions_imported INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO account_migration(id,legacy_sessions_imported) VALUES(1,0);
 CREATE TABLE IF NOT EXISTS account_bootstrap(id INTEGER PRIMARY KEY CHECK(id=1),consumed INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO account_bootstrap(id,consumed) VALUES(1,0);
 INSERT OR IGNORE INTO accounts(id,username,email,role,salt,password_hash,legacy,created_at) SELECT 'legacy-owner',username,email,'admin',salt,password_hash,1,created_at FROM workspace_owner WHERE id=1;
 UPDATE account_bootstrap SET consumed=1 WHERE EXISTS(SELECT 1 FROM accounts);
 INSERT OR IGNORE INTO account_sessions(token_hash,account_id,expires_at) SELECT token_hash,'legacy-owner',expires_at FROM owner_sessions WHERE EXISTS(SELECT 1 FROM accounts WHERE id='legacy-owner') AND (SELECT legacy_sessions_imported FROM account_migration WHERE id=1)=0;
 UPDATE account_migration SET legacy_sessions_imported=1 WHERE id=1;`)
	if err != nil {
		root.Close()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		root.Close()
		return nil, err
	}
	if err = g.validateLegacyComputer(context.Background()); err != nil {
		root.Close()
		return nil, err
	}
	if c.AccountMaintenance {
		return g, nil
	}
	// Background work belongs to enabled workspace runtimes, independent of login.
	rows, err := root.store.db.Query(`SELECT id,legacy FROM accounts WHERE disabled=0 AND must_change_password=0`)
	if err != nil {
		root.Close()
		return nil, err
	}
	var accounts []Account
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Legacy); err != nil {
			break
		}
		accounts = append(accounts, a)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	for _, a := range accounts {
		if _, err = g.workspace(a); err != nil {
			g.Close()
			return nil, err
		}
	}
	return g, nil
}
func (g *AccountGateway) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	for id := range g.active {
		g.cancelAccount(id)
	}
	workspaces := g.workspaces
	g.workspaces = map[string]*Server{}
	g.mu.Unlock()
	for _, s := range workspaces {
		s.Close()
	}
	return g.root.Close()
}
func (g *AccountGateway) Listen() string { return g.root.Listen() }

func (g *AccountGateway) Handler() http.Handler { return http.HandlerFunc(g.handle) }
func (g *AccountGateway) session(r *http.Request) (Account, bool) {
	var a Account
	if !g.auth.transportOK(r) {
		return a, false
	}
	cookie, err := r.Cookie(ownerCookie)
	if err != nil || len(cookie.Value) != 43 {
		return a, false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	err = g.root.store.db.QueryRow(`SELECT a.id,a.username,a.email,a.role,a.disabled,a.must_change_password,a.legacy FROM account_sessions s JOIN accounts a ON a.id=s.account_id WHERE s.token_hash=? AND s.expires_at>? AND a.disabled=0`, hash[:], time.Now().Unix()).Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Disabled, &a.MustChangePassword, &a.Legacy)
	return a, err == nil
}
func (g *AccountGateway) issue(w http.ResponseWriter, r *http.Request, id string) error {
	token := ownerRandom(32)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(ownerSessionLifetime)
	_, err := g.root.store.db.Exec(`INSERT INTO account_sessions(token_hash,account_id,expires_at) VALUES(?,?,?)`, hash[:], id, expires.Unix())
	if err != nil {
		return err
	}
	r.Header.Del("Cookie")
	r.AddCookie(&http.Cookie{Name: ownerCookie, Value: token})
	http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: int(ownerSessionLifetime.Seconds()), Expires: expires})
	return nil
}
func (g *AccountGateway) create(ctx context.Context, username, email, password string, first bool) (Account, error) {
	if strings.TrimSpace(username) == "" && strings.TrimSpace(email) == "" {
		return Account{}, errors.New("username or email is required")
	}
	a := Account{ID: uuid.NewString(), Username: strings.TrimSpace(username), Email: strings.TrimSpace(email), Role: "user", MustChangePassword: !first}
	// An omitted email is a supported username-only invited account. Keep the
	// unique database identity populated without inventing a deliverable address.
	if a.Email == "" {
		a.Email = a.ID + "@account.invalid"
	}
	if a.Username == "" {
		a.Username = "user-" + a.ID
	}
	if !validateOwner(a.Username, a.Email, password) {
		return Account{}, errors.New("invalid account fields or password")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Account{}, err
	}
	hash, err := g.auth.hash(ctx, password, salt)
	if err != nil {
		return Account{}, err
	}
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	if first {
		a.Role = "admin"
		result, e := tx.ExecContext(ctx, `UPDATE account_bootstrap SET consumed=1 WHERE id=1 AND consumed=0 AND NOT EXISTS(SELECT 1 FROM accounts)`)
		if e != nil {
			return Account{}, e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return Account{}, errors.New("setup unavailable")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,email,role,salt,password_hash,must_change_password,legacy,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, a.Username, a.Email, a.Role, salt, hash, a.MustChangePassword, a.Legacy, time.Now().Unix())
	if err != nil {
		return Account{}, err
	}
	if !a.Legacy {
		if _, err = g.computerRequest(ctx, "reserve", a.ID); err != nil {
			return Account{}, err
		}
		// Keep an uncertain reservation after a commit error; orphan reconciliation
		// must prove no account/data exists before releasing its promise.
	}
	return a, tx.Commit()
}
func (g *AccountGateway) workspace(a Account) (*Server, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, errors.New("account gateway closed")
	}
	var disabled bool
	if err := g.root.store.db.QueryRow(`SELECT disabled FROM accounts WHERE id=?`, a.ID).Scan(&disabled); err != nil || disabled {
		return nil, errors.New("account unavailable")
	}
	if s := g.workspaces[a.ID]; s != nil {
		return s, nil
	}
	if _, err := uuid.Parse(a.ID); err != nil && !a.Legacy {
		return nil, errors.New("invalid stored account identity")
	}
	// New runtimes start without owner credentials, engine, inherited MCP paths,
	// transcription key or the legacy personal computer. Provisioning is pending.
	c := Config{AccountDBMaxBytes: g.config.AccountDBMaxBytes, DataDir: filepath.Join(g.config.DataDir, "accounts", a.ID), UIDir: g.config.UIDir, Listen: g.root.listen, PublicOrigin: g.root.publicOrigin, Provider: "openai_codex", IsolatedWorkspace: true, Environment: g.root.instance.Environment, OwnerAllowLoopbackHTTP: g.config.OwnerAllowLoopbackHTTP}
	if a.Legacy {
		c = g.config
		c.AccountRuntime = true
		c.AccountControlPlane = false
	}
	if a.Legacy && g.legacyComputerPhase != "" {
		// Historical account IDs, store, credentials and sessions stay intact.
		// Both adoption and rollback use the current disk's account Guest Runner.
		c.IsolatedWorkspace = true
		c.LocalRunnerURL, c.LocalRunnerTokenFile = "", ""
	}
	if g.config.AccountMaintenance {
		c.AccountControlPlane = true // Metadata reads cannot start background jobs.
	}
	if (!a.Legacy || g.legacyComputerPhase == "worker") && g.config.AccountProvisionerSocket != "" {
		if !filepath.IsAbs(g.config.AccountComputerSocketRoot) {
			return nil, errors.New("absolute account socket root required")
		}
		c.ComputerSocket = filepath.Join(g.config.AccountComputerSocketRoot, g.computerIdentity(a), "control.sock")
		c.ComputerEnsure = func(ctx context.Context) error {
			if g.config.AccountMaintenance {
				return errors.New("account computer startup is fenced during maintenance")
			}
			var disabled, change bool
			if err := g.root.store.db.QueryRowContext(ctx, `SELECT disabled,must_change_password FROM accounts WHERE id=?`, a.ID).Scan(&disabled, &change); err != nil || disabled || change {
				return errors.New("account computer unavailable")
			}
			_, err := g.computerRequest(ctx, "ensure", g.computerIdentity(a))
			return err
		}
	}
	createRuntime := g.runtimeFactory
	if createRuntime == nil {
		createRuntime = NewServer
	}
	s, err := createRuntime(c)
	if err != nil {
		return nil, err
	}
	g.workspaces[a.ID] = s
	return s, nil
}
func (g *AccountGateway) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		g.root.handle(w, r)
		return
	}
	if g.config.AccountMaintenance && r.Method != http.MethodGet && r.Method != http.MethodHead && r.URL.Path != "/api/auth/login" && r.URL.Path != "/api/auth/logout" {
		writeErr(w, 503, "maintenance", "workspace writes are fenced during ownership verification")
		return
	}
	if r.Method != http.MethodGet && (!g.root.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		writeErr(w, 403, "csrf", "origin rejected")
		return
	}
	if r.URL.Path == "/api/server-info" && r.Method == http.MethodGet {
		workspace := g.root
		if account, valid := g.session(r); valid && !account.MustChangePassword {
			var err error
			workspace, err = g.workspace(account)
			if err != nil {
				writeErr(w, 503, "workspace_unavailable", "workspace unavailable")
				return
			}
		}
		writeJSON(w, 200, map[string]any{"service": "tofi", "protocol_version": 1, "instance_id": workspace.instance.ID, "environment": workspace.instance.Environment, "auth": map[string]string{"mode": "accounts"}})
		return
	}
	a, ok := g.session(r)
	if r.URL.Path == "/api/auth/session" && r.Method == http.MethodGet {
		var consumed int
		g.root.store.db.QueryRow(`SELECT consumed FROM account_bootstrap WHERE id=1`).Scan(&consumed)
		value := map[string]any{"enabled": true, "setup_required": consumed == 0, "authenticated": ok, "password_transport_allowed": g.auth.transportOK(r), "multi_account": true}
		if ok {
			value["owner"] = a
		}
		writeJSON(w, 200, value)
		return
	}
	if (r.URL.Path == "/api/auth/setup" || r.URL.Path == "/api/auth/login") && r.Method == http.MethodPost {
		if !g.auth.transportOK(r) {
			writeErr(w, 403, "transport", "secure password transport required")
			return
		}
		if !g.auth.allowAttempt(r) {
			writeErr(w, 429, "rate_limited", "try later")
			return
		}
		if r.URL.Path == "/api/auth/setup" {
			var in struct{ Username, Email, Password string }
			if decodeStrict(r, 8<<10, &in) != nil {
				writeErr(w, 400, "invalid_request", "invalid account")
				return
			}
			created, err := g.create(r.Context(), in.Username, in.Email, in.Password, true)
			if err != nil {
				writeErr(w, 409, "setup_unavailable", "setup unavailable or invalid fields")
				return
			}
			a = created
		} else {
			var in struct{ Identifier, Password string }
			if decodeStrict(r, 8<<10, &in) != nil || len(in.Password) > 1024 || len(in.Identifier) > 254 {
				writeErr(w, 400, "invalid_request", "invalid login")
				return
			}
			var salt, saved []byte
			err := g.root.store.db.QueryRow(`SELECT id,salt,password_hash FROM accounts WHERE disabled=0 AND (username=? COLLATE NOCASE OR email=? COLLATE NOCASE)`, strings.TrimSpace(in.Identifier), strings.TrimSpace(in.Identifier)).Scan(&a.ID, &salt, &saved)
			if err != nil {
				writeErr(w, 401, "invalid_credentials", "invalid identifier or password")
				return
			}
			hash, err := g.auth.hash(r.Context(), in.Password, salt)
			if err != nil || subtle.ConstantTimeCompare(hash, saved) != 1 {
				writeErr(w, 401, "invalid_credentials", "invalid identifier or password")
				return
			}
		}
		if err := g.issue(w, r, a.ID); err != nil {
			writeErr(w, 500, "storage", "cannot create session")
			return
		}
		writeJSON(w, 200, g.sessionState(r))
		return
	}
	if !ok {
		writeErr(w, 401, "auth_required", "sign-in required")
		return
	}
	r, a, cleanup, valid := g.register(r)
	if !valid {
		writeErr(w, 401, "auth_required", "sign-in required")
		return
	}
	defer cleanup()
	if g.adminQuota(w, r, a) {
		return
	}
	if g.manageAccount(w, r, a) {
		return
	}
	if a.MustChangePassword {
		writeErr(w, 403, "password_change_required", "change your initial password before using the workspace")
		return
	}
	if r.URL.Path == "/api/admin/capacity" {
		g.adminCapacity(w, r, a)
		return
	}
	if r.URL.Path == "/api/admin/accounts" {
		if a.Role != "admin" {
			writeErr(w, 403, "forbidden", "admin required")
			return
		}
		switch r.Method {
		case http.MethodPost:
			var in struct{ Username, Email, Password string }
			if decodeStrict(r, 8<<10, &in) != nil {
				writeErr(w, 400, "invalid_request", "invalid account")
				return
			}
			created, err := g.create(r.Context(), in.Username, in.Email, in.Password, false)
			if err != nil {
				writeErr(w, 400, "invalid_account", "invalid or duplicate account")
				return
			}
			writeJSON(w, 201, created)
			return
		case http.MethodGet:
			rows, err := g.root.store.db.Query(`SELECT id,username,email,role,disabled,must_change_password FROM accounts ORDER BY created_at,id`)
			if err != nil {
				writeErr(w, 500, "storage", "cannot list accounts")
				return
			}
			defer rows.Close()
			items := []Account{}
			for rows.Next() {
				var item Account
				if rows.Scan(&item.ID, &item.Username, &item.Email, &item.Role, &item.Disabled, &item.MustChangePassword) != nil {
					writeErr(w, 500, "storage", "cannot list accounts")
					return
				}
				items = append(items, item)
			}
			writeJSON(w, 200, items)
			return
		}
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return
	}
	// Central account management is never dispatched into a workspace. Codex
	// credentials belong to the selected workspace and remain available there.
	codexPath := r.URL.Path == "/api/auth/codex" || r.URL.Path == "/api/auth/codex/connect" || strings.HasPrefix(r.URL.Path, "/api/auth/codex/connect/")
	if (strings.HasPrefix(r.URL.Path, "/api/auth/") && !codexPath) || strings.HasPrefix(r.URL.Path, "/api/admin/") {
		writeErr(w, 403, "unavailable", "account management surface not available")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/computer/resources") && r.Method != http.MethodGet {
		writeErr(w, 403, "quota_management_required", "computer allocation requires central capacity management")
		return
	}
	workspace, err := g.workspace(a)
	if err != nil {
		writeErr(w, 503, "workspace_unavailable", "workspace unavailable")
		return
	}
	workspace.route(w, r.WithContext(context.WithValue(r.Context(), portableAccountAuthKey{}, true)))
}
