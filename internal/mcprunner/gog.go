package mcprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// boundGogMCPArgs permits only the pinned gogcli v0.40.0 McpCmd options.
// All RootFlags, aliases, short clusters and option terminators stay Runner-owned;
// a new account spelling therefore cannot bypass a selector-specific denylist.
func boundGogMCPArgs(mailbox string, args []string) ([]string, error) {
	errArgs := errors.New("built-in Gmail arguments must use mcp and supported MCP options; root options are Runner managed")
	if mailbox == "" || len(args) == 0 || args[0] != "mcp" {
		return nil, errArgs
	}
	for i := 1; i < len(args); i++ {
		flag, value, attached := strings.Cut(args[i], "=")
		switch flag {
		case "--allow-tool", "--tool", "--timeout-seconds", "--max-output-bytes":
			if !attached {
				i++
				if i == len(args) {
					return nil, errArgs
				}
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "-") {
				return nil, errArgs
			}
			if flag == "--timeout-seconds" || flag == "--max-output-bytes" {
				if n, err := strconv.Atoi(value); err != nil || n <= 0 {
					return nil, errArgs
				}
			}
		case "--allow-write", "--list-tools":
			if attached {
				if _, err := strconv.ParseBool(value); err != nil {
					return nil, errArgs
				}
			}
		default:
			return nil, errArgs
		}
	}
	return append([]string{"--account", mailbox}, args...), nil
}

type GogStartRequest struct {
	Email           string          `json:"email"`
	CredentialsJSON json.RawMessage `json:"credentials_json"`
	RedirectURI     string          `json:"redirect_uri"`
	Scope           string          `json:"scope,omitempty"` // readonly or read-send
}

type GogStartResult struct {
	AuthorizationURL string `json:"authorization_url"`
}
type GogFinishRequest struct {
	RedirectedURL string `json:"redirected_url"`
}
type gogSession struct {
	Email       string    `json:"email"`
	Scope       string    `json:"scope"`
	RedirectURI string    `json:"redirect_uri"`
	State       string    `json:"state"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type gogAccount struct {
	Email       string    `json:"email"`
	Scope       string    `json:"scope,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
}

func (r *Runner) gogSpec(id string) (Spec, error) {
	p, err := r.get(id)
	if err != nil {
		return Spec{}, err
	}
	if p.spec.Kind != "builtin_gog" {
		return Spec{}, errors.New("plugin is not gogcli")
	}
	return p.spec, nil
}

func validateGogStart(input GogStartRequest) error {
	if input.Scope != "" && input.Scope != "readonly" && input.Scope != "read-send" {
		return errors.New("unsupported Gmail authorization scope")
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email || len(input.Email) > 254 {
		return errors.New("valid Google account email required")
	}
	u, err := url.Parse(input.RedirectURI)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/api/extensions/local-mcp/gog/oauth/callback") {
		return errors.New("valid HTTPS callback URL required")
	}
	var credentials struct {
		Web struct {
			ClientID     string   `json:"client_id"`
			ClientSecret string   `json:"client_secret"`
			RedirectURIs []string `json:"redirect_uris"`
		} `json:"web"`
	}
	if len(input.CredentialsJSON) > 32<<10 || json.Unmarshal(input.CredentialsJSON, &credentials) != nil || credentials.Web.ClientID == "" || credentials.Web.ClientSecret == "" {
		return errors.New("Google Web OAuth client JSON required")
	}
	registered := false
	for _, redirect := range credentials.Web.RedirectURIs {
		if redirect == input.RedirectURI {
			registered = true
			break
		}
	}
	if !registered {
		return errors.New("Google OAuth client must register the shown callback URL")
	}
	return nil
}

func gogEnv(spec Spec) ([]string, error) {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TMPDIR=/tmp"}
	for key, value := range spec.Env {
		env = append(env, key+"="+value)
	}
	for key, path := range spec.SecretEnv {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		env = append(env, key+"="+strings.TrimSpace(string(data)))
	}
	return env, nil
}

func runGog(ctx context.Context, spec Spec, input []byte, args ...string) ([]byte, error) {
	env, err := gogEnv(spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, spec.Command, args...)
	cmd.Dir = spec.WorkDir
	cmd.Env = env
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("gogcli command failed: %w", err)
	}
	if stdout.Len() > 64<<10 || stderr.Len() > 64<<10 {
		return nil, errors.New("gogcli response too large")
	}
	return stdout.Bytes(), nil
}

func (r *Runner) GogStart(ctx context.Context, id string, input GogStartRequest) (GogStartResult, error) {
	p, err := r.get(id)
	if err != nil {
		return GogStartResult{}, err
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	spec, err := r.gogSpec(id)
	if err != nil {
		return GogStartResult{}, err
	}
	if err := validateGogStart(input); err != nil {
		return GogStartResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := runGog(ctx, spec, input.CredentialsJSON, "auth", "credentials", "set", "-"); err != nil {
		return GogStartResult{}, errors.New("could not save Google OAuth client")
	}
	scope := input.Scope
	if scope == "" {
		scope = "readonly"
	}
	flags := []string{"--json", "--no-input"}
	if scope == "readonly" {
		flags = append(flags, "--readonly")
	}
	flags = append(flags, "auth", "add", input.Email, "--services", "gmail", "--gmail-scope", scope, "--remote", "--step", "1", "--redirect-uri", input.RedirectURI)
	output, err := runGog(ctx, spec, nil, flags...)
	if err != nil {
		return GogStartResult{}, errors.New("could not start Google authorization")
	}
	var result struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return GogStartResult{}, errors.New("invalid gogcli authorization response")
	}
	authURL, err := url.Parse(result.AuthURL)
	if err != nil || authURL.Scheme != "https" || authURL.Host != "accounts.google.com" {
		return GogStartResult{}, errors.New("invalid Google authorization URL")
	}
	state := authURL.Query().Get("state")
	if state == "" {
		return GogStartResult{}, errors.New("Google authorization state missing")
	}
	session := gogSession{Email: input.Email, Scope: scope, RedirectURI: input.RedirectURI, State: state, ExpiresAt: time.Now().Add(10 * time.Minute)}
	data, err := json.Marshal(session)
	if err != nil {
		return GogStartResult{}, err
	}
	if err := writeAtomic0600(filepath.Join(spec.WorkDir, "gog-oauth-session.json"), data); err != nil {
		return GogStartResult{}, err
	}
	return GogStartResult{AuthorizationURL: result.AuthURL}, nil
}

func (r *Runner) GogFinish(ctx context.Context, id string, input GogFinishRequest) error {
	p, err := r.get(id)
	if err != nil {
		return err
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	spec, err := r.gogSpec(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(spec.WorkDir, "gog-oauth-session.json"))
	if err != nil {
		return errors.New("Google authorization session missing")
	}
	var session gogSession
	if json.Unmarshal(data, &session) != nil || time.Now().After(session.ExpiresAt) {
		return errors.New("Google authorization session expired")
	}
	if session.Scope == "" {
		session.Scope = "readonly"
	}
	u, err := url.Parse(input.RedirectedURL)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != session.RedirectURI || u.Query().Get("state") != session.State || u.Query().Get("code") == "" || u.Query().Get("error") != "" {
		return errors.New("Google authorization callback does not match")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	flags := []string{"--json", "--no-input"}
	if session.Scope == "" || session.Scope == "readonly" {
		flags = append(flags, "--readonly")
	}
	flags = append(flags, "auth", "add", session.Email, "--services", "gmail", "--gmail-scope", session.Scope, "--remote", "--step", "2", "--auth-url", input.RedirectedURL, "--redirect-uri", session.RedirectURI)
	if _, err := runGog(ctx, spec, nil, flags...); err != nil {
		return errors.New("Google authorization could not be completed")
	}
	account, _ := json.Marshal(gogAccount{Email: session.Email, Scope: session.Scope, ConnectedAt: time.Now()})
	if err := writeAtomic0600(filepath.Join(spec.WorkDir, "gog-account.json"), account); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(spec.WorkDir, "gog-oauth-session.json"))
	return nil
}

func (r *Runner) GogStatus(id string) (gogAccount, error) {
	spec, err := r.gogSpec(id)
	if err != nil {
		return gogAccount{}, err
	}
	data, err := os.ReadFile(filepath.Join(spec.WorkDir, "gog-account.json"))
	if errors.Is(err, os.ErrNotExist) {
		return gogAccount{}, nil
	}
	if err != nil {
		return gogAccount{}, err
	}
	var account gogAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return gogAccount{}, err
	}
	return account, nil
}

// GogCheck performs a bounded read-only Gmail request with the stored token.
func (r *Runner) GogCheck(ctx context.Context, id string) error {
	spec, err := r.gogSpec(id)
	if err != nil {
		return err
	}
	account, err := r.GogStatus(id)
	if err != nil || account.Email == "" {
		return errors.New("Google account is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = runGog(ctx, spec, nil, "--json", "--no-input", "--readonly", "--account", account.Email, "gmail", "search", "newer_than:1d", "--max", "1")
	if err != nil {
		return errors.New("Gmail read verification failed")
	}
	return nil
}

func (r *Runner) GogDisconnect(ctx context.Context, id string) error {
	p, err := r.get(id)
	if err != nil {
		return err
	}
	p.gogMu.Lock()
	defer p.gogMu.Unlock()
	spec, err := r.gogSpec(id)
	if err != nil {
		return err
	}
	account, err := r.GogStatus(id)
	if err != nil {
		return err
	}
	if account.Email == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := runGog(ctx, spec, nil, "--no-input", "auth", "remove", account.Email); err != nil {
		return errors.New("could not remove stored Google token")
	}
	return os.Remove(filepath.Join(spec.WorkDir, "gog-account.json"))
}
