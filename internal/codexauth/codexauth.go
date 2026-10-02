// Package codexauth owns the local owner's Codex OAuth device flow.
// Credentials are private local state and are never included in status values
// or error messages. The file is intentionally only permission protected (it
// is not presented as encrypted storage); deployments should protect the data
// directory as they protect other owner credentials.
package codexauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	clientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	issuer         = "https://auth.openai.com"
	deviceURL      = issuer + "/codex/device"
	userCodeURL    = issuer + "/api/accounts/deviceauth/usercode"
	deviceTokenURL = issuer + "/api/accounts/deviceauth/token"
	tokenURL       = issuer + "/oauth/token"
	userAgent      = "opencode/1.0"
	fileName       = "codex-credentials.json"
	requestTimeout = 30 * time.Second
	maxBodyBytes   = 1 << 20
)

type DeviceSession struct {
	SessionID       string `json:"session_id"`
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
	ExpiresAt       int64  `json:"expires_at"`
	Interval        int    `json:"interval"`
}

type Status struct {
	Connected bool  `json:"connected"`
	ExpiresAt int64 `json:"expires_at,omitempty"`
	Pending   bool  `json:"pending,omitempty"`
}

type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id,omitempty"`
	AccessOnly   bool   `json:"access_only,omitempty"`
}

type pendingSession struct {
	deviceAuthID string
	userCode     string
	interval     time.Duration
	expiresAt    time.Time
	lastPoll     time.Time
	generation   uint64
}

type endpoints struct {
	userCode string
	device   string
	token    string
	issuer   string
}

type Manager struct {
	dataDir        string
	credentialPath string
	client         *http.Client
	endpoints      endpoints

	mu         sync.Mutex
	pending    map[string]pendingSession
	generation uint64
	refresh    sync.Mutex
}

func New(dataDir string) (*Manager, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("Codex auth data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create Codex auth data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("protect Codex auth data directory: %w", err)
	}
	return newManager(dataDir, &http.Client{Timeout: requestTimeout}, endpoints{
		userCode: userCodeURL,
		device:   deviceTokenURL,
		token:    tokenURL,
		issuer:   issuer,
	})
}

func newManager(dataDir string, client *http.Client, ep endpoints) (*Manager, error) {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	if ep.issuer == "" {
		ep.issuer = issuer
	}
	return &Manager{
		dataDir:        dataDir,
		credentialPath: filepath.Join(dataDir, fileName),
		client:         client,
		endpoints:      ep,
		pending:        make(map[string]pendingSession),
	}, nil
}

func (m *Manager) Start(ctx context.Context) (DeviceSession, error) {
	if ctx == nil {
		return DeviceSession{}, errors.New("Codex login context is required")
	}
	if err := ctx.Err(); err != nil {
		return DeviceSession{}, err
	}
	m.mu.Lock()
	startGeneration := m.generation
	m.cleanupPendingLocked(time.Now())
	m.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoints.userCode, strings.NewReader(`{"client_id":"`+clientID+`"}`))
	if err != nil {
		return DeviceSession{}, errors.New("create Codex login request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("originator", "opencode")
	resp, err := m.client.Do(req)
	if err != nil {
		return DeviceSession{}, fmt.Errorf("start Codex login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DeviceSession{}, errors.New("Codex device login unavailable")
	}
	var body struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     string `json:"interval"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := decodeBody(resp.Body, &body); err != nil || body.DeviceAuthID == "" || body.UserCode == "" {
		return DeviceSession{}, errors.New("invalid Codex device login response")
	}
	interval, _ := strconv.Atoi(body.Interval)
	if interval < 1 {
		interval = 5
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 10 * 60
	}
	// The API returns seconds for the device interval/expiry. Session IDs are
	// random handles and never encode device credentials.
	sessionID, err := randomID()
	if err != nil {
		return DeviceSession{}, errors.New("create Codex login session failed")
	}
	expires := time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	m.mu.Lock()
	if m.generation != startGeneration {
		m.mu.Unlock()
		return DeviceSession{}, errors.New("Codex login session was cancelled")
	}
	m.cleanupPendingLocked(time.Now())
	m.pending[sessionID] = pendingSession{deviceAuthID: body.DeviceAuthID, userCode: body.UserCode, interval: time.Duration(interval) * time.Second, expiresAt: expires, generation: m.generation}
	m.mu.Unlock()
	return DeviceSession{SessionID: sessionID, VerificationURL: deviceURL, UserCode: body.UserCode, ExpiresAt: expires.UnixMilli(), Interval: interval}, nil
}

func (m *Manager) Poll(ctx context.Context, id string) (Status, error) {
	if ctx == nil {
		return Status{}, errors.New("Codex poll context is required")
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	pending, ok := m.pending[id]
	if !ok || time.Now().After(pending.expiresAt) {
		delete(m.pending, id)
		m.mu.Unlock()
		return Status{}, errors.New("Codex login session expired")
	}
	generation := pending.generation
	if !pending.lastPoll.IsZero() && time.Since(pending.lastPoll) < pending.interval {
		m.mu.Unlock()
		return Status{Pending: true}, nil
	}
	pending.lastPoll = time.Now()
	m.pending[id] = pending
	m.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"device_auth_id": pending.deviceAuthID, "user_code": pending.userCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoints.device, strings.NewReader(string(body)))
	if err != nil {
		return Status{}, errors.New("create Codex login poll failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("originator", "opencode")
	resp, err := m.client.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("check Codex login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return Status{Pending: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Status{}, errors.New("Codex device login failed")
	}
	var auth struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	if err := decodeBody(resp.Body, &auth); err != nil || auth.Code == "" || auth.Verifier == "" {
		return Status{}, errors.New("invalid Codex authorization response")
	}
	newToken, err := m.exchange(ctx, auth.Code, auth.Verifier)
	if err != nil {
		return Status{}, err
	}
	// The network exchange is deliberately outside the lock. Recheck the
	// session generation before the short atomic write so Disconnect cannot be
	// undone by a late device response.
	m.refresh.Lock()
	defer m.refresh.Unlock()
	m.mu.Lock()
	current, stillPending := m.pending[id]
	validSession := stillPending && current.generation == generation && current.expiresAt.After(time.Now())
	m.mu.Unlock()
	if !validSession {
		return Status{}, errors.New("Codex login session expired")
	}
	if err := m.save(newToken); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
	return Status{Connected: true, ExpiresAt: newToken.ExpiresAt}, nil
}

func (m *Manager) Status() Status {
	t, err := m.load()
	m.mu.Lock()
	m.cleanupPendingLocked(time.Now())
	pending := len(m.pending) > 0
	now := time.Now()
	m.mu.Unlock()
	if err != nil || t.AccessToken == "" || (t.AccessOnly && t.ExpiresAt <= now.Add(30*time.Second).UnixMilli()) || (!t.AccessOnly && t.RefreshToken == "") {
		return Status{Pending: pending}
	}
	return Status{Connected: true, ExpiresAt: t.ExpiresAt, Pending: pending}
}

func (m *Manager) Credential(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("Codex credential context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.refresh.Lock()
	defer m.refresh.Unlock()
	t, err := m.load()
	if err != nil || t.AccessToken == "" || (!t.AccessOnly && t.RefreshToken == "") {
		return "", errors.New("Codex is not connected")
	}
	if t.AccessOnly {
		if t.ExpiresAt > time.Now().Add(30*time.Second).UnixMilli() {
			return credential(t), nil
		}
		return "", errors.New("Codex access snapshot expired; reconnect your ChatGPT account")
	}
	if t.ExpiresAt > time.Now().Add(30*time.Second).UnixMilli() {
		return credential(t), nil
	}
	if t.RefreshToken == "" {
		return "", errors.New("Codex login expired; reconnect your ChatGPT account")
	}
	return m.refreshToken(ctx, t)
}

// CredentialReadOnly returns the currently stored, unexpired access token
// without refreshing or writing the credential file. Callers that only need
// provider metadata should use this method so opening settings cannot mutate
// the owner's OAuth state.
func (m *Manager) CredentialReadOnly(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("Codex credential context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t, err := m.load()
	if err != nil || t.AccessToken == "" {
		return "", errors.New("Codex is not connected")
	}
	if t.ExpiresAt <= time.Now().Add(30*time.Second).UnixMilli() {
		return "", errors.New("Codex access snapshot expired; reconnect your ChatGPT account")
	}
	return credential(t), nil
}

// SaveAccessOnlyCredential stores a current access-token snapshot without a
// refresh token. It is intended for importing an owner-approved credential
// snapshot from another local installation; it never enables token refresh.
func (m *Manager) SaveAccessOnlyCredential(accessToken, accountID string, expiresAt int64) error {
	if strings.TrimSpace(accessToken) == "" || expiresAt <= 0 {
		return errors.New("Codex access snapshot is incomplete")
	}
	m.refresh.Lock()
	defer m.refresh.Unlock()
	// An imported snapshot is a new credential generation. Invalidate any
	// in-flight device session before writing so its late exchange cannot win.
	m.mu.Lock()
	m.generation++
	m.pending = make(map[string]pendingSession)
	m.mu.Unlock()
	return m.save(token{AccessToken: accessToken, AccountID: accountID, ExpiresAt: expiresAt, AccessOnly: true})
}

func (m *Manager) Disconnect() error {
	m.refresh.Lock()
	defer m.refresh.Unlock()
	if err := os.Remove(m.credentialPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disconnect Codex: %w", err)
	}
	m.mu.Lock()
	m.generation++
	m.pending = make(map[string]pendingSession)
	m.mu.Unlock()
	return nil
}

func (m *Manager) cleanupPendingLocked(now time.Time) {
	for id, p := range m.pending {
		if !now.Before(p.expiresAt) {
			delete(m.pending, id)
		}
	}
}

func (m *Manager) exchange(ctx context.Context, code, verifier string) (token, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {m.endpoints.issuer + "/deviceauth/callback"}, "client_id": {clientID}, "code_verifier": {verifier}}
	return m.postToken(ctx, form, "Codex token exchange failed")
}

func (m *Manager) refreshToken(ctx context.Context, old token) (string, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {clientID}}
	newToken, err := m.postToken(ctx, form, "Codex login expired; reconnect your ChatGPT account")
	if err != nil {
		return "", err
	}
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = old.RefreshToken
	}
	if newToken.AccountID == "" {
		newToken.AccountID = old.AccountID
	}
	if err := m.save(newToken); err != nil {
		return "", err
	}
	return credential(newToken), nil
}

func (m *Manager) postToken(ctx context.Context, form url.Values, failure string) (token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoints.token, strings.NewReader(form.Encode()))
	if err != nil {
		return token{}, errors.New("create Codex token request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return token{}, fmt.Errorf("Codex token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return token{}, errors.New(failure)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := decodeBody(resp.Body, &body); err != nil || body.AccessToken == "" {
		return token{}, errors.New("invalid Codex token response")
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 3600
	}
	account := accountID(body.IDToken)
	if account == "" {
		account = accountID(body.AccessToken)
	}
	return token{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(body.ExpiresIn) * time.Second).UnixMilli(), AccountID: account, AccessOnly: false}, nil
}

func (m *Manager) load() (token, error) {
	data, err := os.ReadFile(m.credentialPath)
	if err != nil {
		return token{}, err
	}
	var t token
	if err := json.Unmarshal(data, &t); err != nil {
		return token{}, errors.New("stored Codex credentials are invalid")
	}
	return t, nil
}

func (m *Manager) save(t token) error {
	data, err := json.Marshal(t)
	if err != nil {
		return errors.New("save Codex credentials failed")
	}
	if err := os.MkdirAll(m.dataDir, 0700); err != nil {
		return fmt.Errorf("save Codex credentials: %w", err)
	}
	tmp, err := os.CreateTemp(m.dataDir, ".codex-credentials-*")
	if err != nil {
		return fmt.Errorf("save Codex credentials: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect Codex credentials: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("save Codex credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("save Codex credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save Codex credentials: %w", err)
	}
	if err := os.Rename(tmpName, m.credentialPath); err != nil {
		return fmt.Errorf("activate Codex credentials: %w", err)
	}
	return nil
}

func credential(t token) string { return t.AccessToken + "\x00" + t.AccountID }

func randomID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func accountID(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		AccountID string `json:"chatgpt_account_id"`
		Auth      struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if claims.AccountID != "" {
		return claims.AccountID
	}
	return claims.Auth.AccountID
}

func decodeBody(r io.Reader, dst any) error {
	return json.NewDecoder(io.LimitReader(r, maxBodyBytes)).Decode(dst)
}
