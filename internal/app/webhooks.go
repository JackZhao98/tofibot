package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type webhookEndpoint struct {
	HookID, AccountID, WorkspaceID, ConversationID, TargetIdentity, CreatedBy string
	TokenHash                                                                 []byte
	Version                                                                   int64
	Revoked                                                                   bool
	CreatedAt, RotatedAt                                                      string
}
type webhookMetadata struct {
	Configured     bool   `json:"configured"`
	Enabled        bool   `json:"enabled"`
	HookID         string `json:"hook_id,omitempty"`
	Version        int64  `json:"version,omitempty"`
	URL            string `json:"url,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	RotatedAt      string `json:"rotated_at,omitempty"`
	LastAcceptedAt string `json:"last_accepted_at,omitempty"`
	Secret         string `json:"secret,omitempty"`
}

func migrateWebhookRegistry(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS webhook_endpoints(hook_id TEXT PRIMARY KEY,account_id TEXT NOT NULL,workspace_instance_id TEXT NOT NULL,conversation_id TEXT NOT NULL,target_identity TEXT NOT NULL,created_by TEXT NOT NULL,token_hash BLOB NOT NULL CHECK(length(token_hash)=32),version INTEGER NOT NULL,revoked_at TEXT,created_at TEXT NOT NULL,rotated_at TEXT NOT NULL,UNIQUE(account_id,conversation_id));`)
	return err
}

const webhookEndpointColumns = `hook_id,account_id,workspace_instance_id,conversation_id,target_identity,created_by,token_hash,version,revoked_at,created_at,rotated_at`

func scanWebhook(r interface{ Scan(...any) error }) (webhookEndpoint, error) {
	var e webhookEndpoint
	var revoked sql.NullString
	err := r.Scan(&e.HookID, &e.AccountID, &e.WorkspaceID, &e.ConversationID, &e.TargetIdentity, &e.CreatedBy, &e.TokenHash, &e.Version, &revoked, &e.CreatedAt, &e.RotatedAt)
	e.Revoked = revoked.Valid
	return e, err
}
func (s *Store) webhookByHook(ctx context.Context, id string) (webhookEndpoint, error) {
	return scanWebhook(s.db.QueryRowContext(ctx, `SELECT `+webhookEndpointColumns+` FROM webhook_endpoints WHERE hook_id=?`, id))
}
func webhookKey() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(secret))
	return secret, hash[:], nil
}
func webhookMatches(e webhookEndpoint, secret string) bool {
	h := sha256.Sum256([]byte(secret))
	return !e.Revoked && len(e.TokenHash) == 32 && len(secret) == 43 && subtle.ConstantTimeCompare(e.TokenHash, h[:]) == 1
}
func webhookPublicPath(path string) (string, bool) {
	p := strings.Split(path, "/")
	if len(p) == 4 && p[1] == "api" && p[2] == "webhooks" && p[3] != "" {
		return p[3], true
	}
	return "", false
}
func webhookManagementPath(path string) (id string, rotate bool, ok bool) {
	p := strings.Split(path, "/")
	if (len(p) == 5 || len(p) == 6 && p[5] == "rotate") && p[1] == "api" && p[2] == "conversations" && p[3] != "" && p[4] == "webhook" {
		return p[3], len(p) == 6, true
	}
	return "", false, false
}

// Only configured origin and actual transport/peer are trusted. Forwarded
// headers and request Host never determine a hook URL or peer rate bucket.
func (s *Server) webhookOrigin() (string, bool) {
	u, err := url.Parse(s.publicOrigin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	if u.Scheme == "https" {
		return strings.TrimRight(s.publicOrigin, "/"), true
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && s.ownerAuth != nil && s.ownerAuth.allowLoopback && ip != nil && ip.IsLoopback() {
		return strings.TrimRight(s.publicOrigin, "/"), true
	}
	return "", false
}
func (s *Server) webhookAvailable() bool {
	_, ok := s.webhookOrigin()
	if !ok || s.ownerAuth == nil {
		return false
	}
	var count int
	return s.store.db.QueryRow(`SELECT COUNT(*) FROM workspace_owner`).Scan(&count) == nil && count == 1
}
func webhookTransport(auth *ownerAuth, r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && auth != nil && auth.allowLoopback && ip != nil && ip.IsLoopback()
}

type webhookBudget struct {
	start time.Time
	count int
}
type webhookLimiter struct {
	mu     sync.Mutex
	peers  map[string]webhookBudget
	global webhookBudget
	active chan struct{}
}

func newWebhookLimiter() *webhookLimiter {
	return &webhookLimiter{peers: map[string]webhookBudget{}, active: make(chan struct{}, 32)}
}
func webhookLock(ctx context.Context, mu *sync.Mutex) bool {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return false
		}
		if mu.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
func (l *webhookLimiter) acquire(r *http.Request) (func(), bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil {
		return nil, false
	}
	peer := ip.String()
	now := time.Now()
	l.mu.Lock()
	for k, v := range l.peers {
		if now.Sub(v.start) >= time.Minute {
			delete(l.peers, k)
		}
	}
	if now.Sub(l.global.start) >= time.Minute {
		l.global = webhookBudget{start: now}
	}
	b, exists := l.peers[peer]
	if !exists {
		b = webhookBudget{start: now}
	}
	if l.global.count >= 300 || b.count >= 60 || !exists && len(l.peers) >= 1024 {
		l.mu.Unlock()
		return nil, false
	}
	l.global.count++
	b.count++
	l.peers[peer] = b
	l.mu.Unlock()
	select {
	case l.active <- struct{}{}:
		return func() { <-l.active }, true
	default:
		return nil, false
	}
}

// Decode one bounded object and reject duplicate keys, unknown fields and
// invalid UTF-8 before normal JSON unmarshalling can normalize them.
func webhookObject(w http.ResponseWriter, r *http.Request, max int64, allowed ...string) (map[string]json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, errors.New("object required")
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, valid := t.(string)
		if !valid {
			return nil, errors.New("invalid field")
		}
		known := false
		for _, a := range allowed {
			if a == key {
				known = true
			}
		}
		if _, dup := out[key]; dup || !known {
			return nil, errors.New("unexpected field")
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return nil, err
		}
		out[key] = raw
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return out, nil
}
func webhookInput(w http.ResponseWriter, r *http.Request) (webhookEnvelope, int) {
	var in webhookEnvelope
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		return in, 415
	}
	if r.URL.RawQuery != "" {
		return in, 400
	}
	size := 0
	for k, v := range r.Header {
		size += len(k)
		for _, s := range v {
			size += len(s)
		}
	}
	if size > 8192 {
		return in, 400
	}
	timestamp, err := strconv.ParseInt(r.Header.Get("X-Tofi-Timestamp"), 10, 64)
	current := time.Now().Unix()
	if err != nil || timestamp < current-300 || timestamp > current+300 {
		return in, 400
	}
	obj, err := webhookObject(w, r, 32<<10, "event_id", "content")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return in, 413
		}
		return in, 400
	}
	if len(obj) != 2 || json.Unmarshal(obj["event_id"], &in.EventID) != nil || json.Unmarshal(obj["content"], &in.Content) != nil {
		return in, 400
	}
	if len(in.EventID) < 1 || len(in.EventID) > 128 || strings.TrimSpace(in.Content) == "" || len(in.Content) > 16<<10 {
		return in, 400
	}
	for _, c := range []byte(in.EventID) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", rune(c))) {
			return in, 400
		}
	}
	return in, 0
}
func webhookError(w http.ResponseWriter, status int) {
	if status == 429 {
		w.Header().Set("Retry-After", "60")
	}
	writeErr(w, status, "webhook_unavailable", map[int]string{400: "invalid webhook request", 401: "webhook authentication required", 403: "webhook request rejected", 409: "webhook target or event unavailable", 413: "webhook request too large", 415: "JSON required", 429: "try later", 503: "webhook unavailable"}[status])
}
func webhookAdmissionResult(w http.ResponseWriter, receipt webhookReceipt, err error) {
	if err != nil {
		status := 503
		switch {
		case errors.Is(err, errWebhookTarget), errors.Is(err, errWebhookConflict):
			status = 409
		case errors.Is(err, errWebhookCapacity):
			status = 429
		}
		webhookError(w, status)
		return
	}
	status := 202
	if receipt.Duplicate {
		status = 200
	}
	writeJSON(w, status, receipt)
}
func webhookRequest(w http.ResponseWriter, r *http.Request, root *Server, auth *ownerAuth) (webhookEnvelope, string, func(), bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, 405, "method_not_allowed", "use POST")
		return webhookEnvelope{}, "", func() {}, false
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		webhookError(w, 403)
		return webhookEnvelope{}, "", func() {}, false
	}
	if _, ok := root.webhookOrigin(); !ok || !webhookTransport(auth, r) {
		webhookError(w, 401)
		return webhookEnvelope{}, "", func() {}, false
	}
	release, ok := root.webhookLimits.acquire(r)
	if !ok {
		webhookError(w, 429)
		return webhookEnvelope{}, "", func() {}, false
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(3 * time.Second))
	cleanup := func() { _ = http.NewResponseController(w).SetReadDeadline(time.Time{}); release() }
	value := r.Header.Values("Authorization")
	if len(value) != 1 || !strings.HasPrefix(value[0], "Bearer ") || len(value[0]) != 50 {
		cleanup()
		webhookError(w, 401)
		return webhookEnvelope{}, "", func() {}, false
	}
	in, status := webhookInput(w, r)
	if status != 0 {
		cleanup()
		webhookError(w, status)
		return in, "", func() {}, false
	}
	secret := strings.TrimPrefix(value[0], "Bearer ")
	if strings.Contains(in.Content, secret) || strings.Contains(in.EventID, secret) {
		cleanup()
		webhookError(w, 400)
		return in, "", func() {}, false
	}
	return in, secret, cleanup, true
}

func (g *AccountGateway) webhookIngress(w http.ResponseWriter, r *http.Request, hook string) {
	in, secret, release, ok := webhookRequest(w, r, g.root, g.auth)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	e, err := g.root.store.webhookByHook(ctx, hook)
	if err != nil || !webhookMatches(e, secret) {
		webhookError(w, 401)
		return
	}
	var a Account
	err = g.root.store.db.QueryRowContext(ctx, `SELECT id,legacy,disabled,must_change_password FROM accounts WHERE id=?`, e.AccountID).Scan(&a.ID, &a.Legacy, &a.Disabled, &a.MustChangePassword)
	if err != nil || a.Disabled || a.MustChangePassword {
		webhookError(w, 401)
		return
	}
	// Enabled runtimes are restored at gateway startup. Ingress does not call
	// the contextless workspace constructor: a temporarily unavailable runtime
	// yields a bounded 503 until normal account lifecycle restores it.
	if !webhookLock(ctx, &g.mu) {
		webhookError(w, 503)
		return
	}
	s := g.workspaces[a.ID]
	closed := g.closed
	g.mu.Unlock()
	if s == nil || closed {
		webhookError(w, 503)
		return
	}
	ready := s.modelConfigured()
	if !webhookLock(ctx, &g.mu) {
		webhookError(w, 503)
		return
	}
	current, err := g.root.store.webhookByHook(ctx, hook)
	var disabled, change bool
	accountErr := g.root.store.db.QueryRowContext(ctx, `SELECT disabled,must_change_password FROM accounts WHERE id=?`, a.ID).Scan(&disabled, &change)
	if err != nil || current.Version != e.Version || !webhookMatches(current, secret) || accountErr != nil || disabled || change || g.closed || g.workspaces[a.ID] != s {
		g.mu.Unlock()
		webhookError(w, 401)
		return
	}
	if e.WorkspaceID != s.instance.ID {
		g.mu.Unlock()
		webhookError(w, 409)
		return
	}
	if !s.purgeMu.TryLock() {
		g.mu.Unlock()
		webhookError(w, 503)
		return
	}
	receipt, run, err := s.store.AdmitWebhook(ctx, e, in, ready)
	s.purgeMu.Unlock()
	g.mu.Unlock()
	if err == nil && run.Status == "queued" {
		if c, e := s.store.GetConversation(run.ConversationID); e == nil {
			s.enqueue(c, run)
		}
	}
	webhookAdmissionResult(w, receipt, err)
}
func (s *Server) webhookIngress(w http.ResponseWriter, r *http.Request, hook string) {
	if !s.webhookStandalone {
		webhookError(w, 401)
		return
	}
	in, secret, release, ok := webhookRequest(w, r, s, s.ownerAuth)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ready := s.modelConfigured()
	if !s.purgeMu.TryLock() {
		webhookError(w, 503)
		return
	}
	e, err := s.store.webhookByHook(ctx, hook)
	var ownerCount int
	ownerErr := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_owner`).Scan(&ownerCount)
	if err != nil || !webhookMatches(e, secret) || ownerErr != nil || ownerCount != 1 || e.AccountID != "standalone-owner" {
		s.purgeMu.Unlock()
		webhookError(w, 401)
		return
	}
	if e.WorkspaceID != s.instance.ID {
		s.purgeMu.Unlock()
		webhookError(w, 409)
		return
	}
	receipt, run, err := s.store.AdmitWebhook(ctx, e, in, ready)
	s.purgeMu.Unlock()
	if err == nil && run.Status == "queued" {
		if c, e := s.store.GetConversation(run.ConversationID); e == nil {
			s.enqueue(c, run)
		}
	}
	webhookAdmissionResult(w, receipt, err)
}

// Caller holds the account lifecycle or standalone purge/session fence.
func manageWebhook(w http.ResponseWriter, r *http.Request, registry *Store, s *Server, origin, accountID, conversationID string, rotate bool, expected int64) {
	if origin == "" {
		webhookError(w, 503)
		return
	}
	if rotate && r.Method != http.MethodPost || !rotate && r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		allow := "GET, POST, DELETE"
		if rotate {
			allow = "POST"
		}
		w.Header().Set("Allow", allow)
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, identity, err := s.store.webhookTarget(ctx, conversationID, r.Method == http.MethodPost && !rotate)
	if err != nil {
		webhookError(w, 409)
		return
	}
	e, err := scanWebhook(registry.db.QueryRowContext(ctx, `SELECT `+webhookEndpointColumns+` FROM webhook_endpoints WHERE account_id=? AND conversation_id=?`, accountID, conversationID))
	if err != nil && err != sql.ErrNoRows {
		webhookError(w, 503)
		return
	}
	exists := err == nil
	valid := exists && e.WorkspaceID == s.instance.ID && e.TargetIdentity == identity
	metadata := func(e webhookEndpoint) webhookMetadata {
		m := webhookMetadata{Configured: true, Enabled: !e.Revoked, HookID: e.HookID, Version: e.Version, URL: origin + "/api/webhooks/" + e.HookID, CreatedAt: e.CreatedAt, RotatedAt: e.RotatedAt}
		var accepted sql.NullInt64
		if s.store.db.QueryRowContext(ctx, `SELECT MAX(accepted_at) FROM webhook_deliveries WHERE hook_id=?`, e.HookID).Scan(&accepted) == nil && accepted.Valid {
			m.LastAcceptedAt = time.Unix(accepted.Int64, 0).UTC().Format(time.RFC3339)
		}
		return m
	}
	if r.Method == http.MethodGet {
		if !valid {
			writeJSON(w, 200, webhookMetadata{})
			return
		}
		writeJSON(w, 200, metadata(e))
		return
	}
	if rotate || r.Method == http.MethodDelete {
		if !valid || e.Revoked || e.Version != expected {
			webhookError(w, 409)
			return
		}
	} else {
		if valid && !e.Revoked {
			webhookError(w, 409)
			return
		}
	}
	if r.Method == http.MethodDelete {
		res, err := registry.db.ExecContext(ctx, `UPDATE webhook_endpoints SET revoked_at=?,version=version+1 WHERE hook_id=? AND version=? AND revoked_at IS NULL`, now(), e.HookID, expected)
		if err != nil {
			webhookError(w, 503)
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			webhookError(w, 409)
			return
		}
		w.WriteHeader(204)
		return
	}
	secret, hash, err := webhookKey()
	if err != nil {
		webhookError(w, 503)
		return
	}
	t := now()
	if valid {
		e.Version++
		e.TokenHash = hash
		e.Revoked = false
		e.RotatedAt = t
		res, err := registry.db.ExecContext(ctx, `UPDATE webhook_endpoints SET token_hash=?,version=?,revoked_at=NULL,rotated_at=? WHERE hook_id=? AND version=?`, hash, e.Version, t, e.HookID, e.Version-1)
		if err != nil {
			webhookError(w, 503)
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			webhookError(w, 409)
			return
		}
	} else {
		version := int64(1)
		if exists {
			version = e.Version + 1
		}
		e = webhookEndpoint{HookID: newID(), AccountID: accountID, WorkspaceID: s.instance.ID, ConversationID: conversationID, TargetIdentity: identity, CreatedBy: accountID, TokenHash: hash, Version: version, CreatedAt: t, RotatedAt: t}
		_, err = registry.db.ExecContext(ctx, `INSERT INTO webhook_endpoints(`+webhookEndpointColumns+`) VALUES(?,?,?,?,?,?,?,?,NULL,?,?) ON CONFLICT(account_id,conversation_id) DO UPDATE SET hook_id=excluded.hook_id,workspace_instance_id=excluded.workspace_instance_id,target_identity=excluded.target_identity,created_by=excluded.created_by,token_hash=excluded.token_hash,version=excluded.version,revoked_at=NULL,created_at=excluded.created_at,rotated_at=excluded.rotated_at`, e.HookID, e.AccountID, e.WorkspaceID, e.ConversationID, e.TargetIdentity, e.CreatedBy, hash, e.Version, t, t)
		if err != nil {
			webhookError(w, 503)
			return
		}
	}
	m := metadata(e)
	m.Secret = secret
	status := 201
	if rotate {
		status = 200
	}
	writeJSON(w, status, m)
}
func webhookManagementInput(w http.ResponseWriter, r *http.Request, rotate bool) (int64, bool) {
	if rotate && r.Method != http.MethodPost || !rotate && r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		allow := "GET, POST, DELETE"
		if rotate {
			allow = "POST"
		}
		w.Header().Set("Allow", allow)
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return 0, false
	}
	if r.Method == http.MethodGet {
		return 0, true
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(3 * time.Second))
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	var expected int64
	if rotate || r.Method == http.MethodDelete {
		obj, err := webhookObject(w, r, 1024, "expected_version")
		if err != nil || len(obj) != 1 || json.Unmarshal(obj["expected_version"], &expected) != nil || expected < 1 {
			webhookError(w, 400)
			return 0, false
		}
	} else {
		obj, err := webhookObject(w, r, 1024)
		if err != nil || len(obj) != 0 {
			webhookError(w, 400)
			return 0, false
		}
	}
	return expected, true
}

// Management responses (including the one-time key) are written only after
// lifecycle locks are released. A slow client cannot hold the account fence.
type webhookResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *webhookResponse) Header() http.Header { return b.header }
func (b *webhookResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *webhookResponse) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(data)
}
func (b *webhookResponse) flush(w http.ResponseWriter) {
	for key, values := range b.header {
		w.Header()[key] = values
	}
	if b.status != 0 {
		w.WriteHeader(b.status)
	}
	_, _ = w.Write(b.body.Bytes())
	b.body.Reset()
}
func webhookOwnerSession(ctx context.Context, auth *ownerAuth, r *http.Request) bool {
	if auth == nil || !auth.transportOK(r) {
		return false
	}
	cookie, err := r.Cookie(ownerCookie)
	if err != nil || len(cookie.Value) != 43 {
		return false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	var valid bool
	return auth.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM owner_sessions WHERE token_hash=? AND expires_at>?) AND EXISTS(SELECT 1 FROM workspace_owner WHERE id=1)`, hash[:], time.Now().Unix()).Scan(&valid) == nil && valid
}
func (g *AccountGateway) webhookManage(w http.ResponseWriter, r *http.Request, a Account, s *Server, id string, rotate bool) {
	if !webhookTransport(g.auth, r) {
		webhookError(w, 403)
		return
	}
	expected, valid := webhookManagementInput(w, r, rotate)
	if !valid {
		return
	}
	buffer := &webhookResponse{header: make(http.Header)}
	defer buffer.flush(w)
	w = buffer
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	// Use control-plane origin/auth because isolated workspace owner auth is off.
	origin, _ := g.root.webhookOrigin()
	if !webhookLock(ctx, &g.mu) {
		webhookError(w, 503)
		return
	}
	defer g.mu.Unlock()
	current, ok := g.session(r)
	if !ok || current.ID != a.ID || current.MustChangePassword || g.closed || g.workspaces[a.ID] != s {
		webhookError(w, 401)
		return
	}
	if !s.purgeMu.TryLock() {
		webhookError(w, 503)
		return
	}
	defer s.purgeMu.Unlock()
	manageWebhook(w, r, g.root.store, s, origin, a.ID, id, rotate, expected)
}
func (s *Server) webhookManage(w http.ResponseWriter, r *http.Request, id string, rotate bool) {
	if !s.webhookStandalone {
		webhookError(w, 401)
		return
	}
	if !webhookTransport(s.ownerAuth, r) {
		webhookError(w, 403)
		return
	}
	expected, valid := webhookManagementInput(w, r, rotate)
	if !valid {
		return
	}
	buffer := &webhookResponse{header: make(http.Header)}
	defer buffer.flush(w)
	w = buffer
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.purgeMu.TryLock() {
		webhookError(w, 503)
		return
	}
	defer s.purgeMu.Unlock()
	if !webhookLock(ctx, &s.ownerAuth.mu) {
		webhookError(w, 503)
		return
	}
	defer s.ownerAuth.mu.Unlock()
	if !webhookOwnerSession(ctx, s.ownerAuth, r) {
		webhookError(w, 401)
		return
	}
	origin, _ := s.webhookOrigin()
	manageWebhook(w, r, s.store, s, origin, "standalone-owner", id, rotate, expected)
}
