package mcprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/googleapi"
)

type GogStartRequest struct {
	Email           string          `json:"email"`
	CredentialsJSON json.RawMessage `json:"credentials_json"`
	RedirectURI     string          `json:"redirect_uri"`
	Scope           string          `json:"scope,omitempty"` // Gmail scope: readonly or read-send
	// Services to authorise on the account: gmail (default), calendar, drive. A second service is added to the
	// account's existing grant (incremental authorisation), never replacing it.
	Services []string `json:"services,omitempty"`
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
	Services    []string  `json:"services,omitempty"`
	RedirectURI string    `json:"redirect_uri"`
	State       string    `json:"state"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type gogAccount struct {
	Email       string    `json:"email"`
	Scope       string    `json:"scope,omitempty"`
	Services    []string  `json:"services,omitempty"`
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

var gogServices = map[string]bool{"gmail": true, "calendar": true, "drive": true}

// gogServiceList validates the requested services, adds the ones the account already has, and returns them in a
// stable order. An empty request means Gmail, the connector TOFI ships.
func gogServiceList(requested, granted []string) ([]string, error) {
	want := map[string]bool{}
	for _, name := range requested {
		if !gogServices[name] {
			return nil, errors.New("unsupported Google service")
		}
		want[name] = true
	}
	if len(want) == 0 {
		want["gmail"] = true
	}
	for _, name := range granted {
		if gogServices[name] {
			want[name] = true
		}
	}
	out := []string{}
	for _, name := range []string{"gmail", "calendar", "drive"} {
		if want[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// gogAuthAddFlags is the `gog auth add` command line for the services and Gmail scope chosen. Read-only unless the
// person chose to let Gmail send; even then Drive stays read-only, and Calendar has no narrower mode in gog.
func gogAuthAddFlags(email, scope string, services []string) []string {
	flags := []string{"--json", "--no-input"}
	hasGmail := len(services) > 0 && services[0] == "gmail"
	if scope == "" || scope == "readonly" || !hasGmail {
		flags = append(flags, "--readonly")
	}
	flags = append(flags, "auth", "add", email, "--services", strings.Join(services, ","))
	if hasGmail && scope != "" {
		flags = append(flags, "--gmail-scope", scope)
	}
	if scope == "read-send" && hasGmail {
		for _, service := range services {
			if service == "drive" {
				flags = append(flags, "--drive-scope", "readonly")
			}
		}
	}
	return flags
}

func (r *Runner) grantedGogScope(id string) string {
	account, err := r.GogStatus(id)
	if err != nil {
		return ""
	}
	return account.Scope
}

// gogToolSelectors are the `gog mcp --allow-tool` selectors for authorised services. They are gog v0.40.0's own
// service names (`gog mcp --list-tools`: gmail_search, gmail_get_message, gmail_get_thread, calendar_events,
// drive_search, drive_get, all read-only); a service the account has not authorised contributes none.
func gogToolSelectors(services []string) string {
	return strings.Join(authorisedGogServices(services), ",")
}

// authorisedGogServices keeps the recognised services of an account record in the stable order; an account recorded
// before services were tracked (or with none) is Gmail only.
func authorisedGogServices(granted []string) []string {
	out := []string{}
	for _, name := range []string{"gmail", "calendar", "drive"} {
		for _, have := range granted {
			if have == name {
				out = append(out, name)
				break
			}
		}
	}
	if len(out) == 0 {
		out = []string{"gmail"}
	}
	return out
}

// gogMCPArgs is the argument list the MCP child starts with. The installed default (`mcp --allow-tool <services>`)
// follows the services the account has authorised, read from the account record at every start; arguments someone
// set by hand are left alone.
func gogMCPArgs(spec Spec) []string {
	if spec.Kind != "builtin_gog" || !isDefaultGogArgs(spec.Args) {
		return spec.Args
	}
	var account gogAccount
	if data, err := os.ReadFile(filepath.Join(spec.WorkDir, "gog-account.json")); err == nil {
		_ = json.Unmarshal(data, &account)
	}
	return []string{"mcp", "--allow-tool", gogToolSelectors(account.Services)}
}

func isDefaultGogArgs(args []string) bool {
	if len(args) != 3 || args[0] != "mcp" || args[1] != "--allow-tool" {
		return false
	}
	for _, name := range strings.Split(args[2], ",") {
		if !gogServices[name] {
			return false
		}
	}
	return true
}

// recycle stops an idle MCP child so the next call starts it with the tools of the current account. A child busy with
// a call is left to finish and is closed by the release of its last lease (releaseLease), not at the idle reap.
func (p *plugin) recycle() {
	p.mu.Lock()
	if p.cli == nil || p.active > 0 || p.ready != nil {
		p.restartWhenIdle = p.cli != nil
		p.mu.Unlock()
		return
	}
	cli, stop, group := p.cli, p.stop, p.processGroup
	p.cli, p.stop, p.tools = nil, nil, nil
	p.mu.Unlock()
	shutdown(cli, stop, group)
}

// withIncrementalAuth asks Google to keep what the account already granted and add the new scopes to it.
func withIncrementalAuth(authURL string) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil || u.Scheme != "https" || u.Host != "accounts.google.com" {
		return "", errors.New("invalid Google authorization URL")
	}
	q := u.Query()
	q.Set("include_granted_scopes", "true")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func validateGogStart(input GogStartRequest) error {
	if _, err := gogServiceList(input.Services, nil); err != nil {
		return err
	}
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

// gogCommandError keeps gogcli's own output for classification only (see APIDisabled); it is never shown or logged.
type gogCommandError struct {
	err    error
	output string
}

func (e *gogCommandError) Error() string { return "gogcli command failed: " + e.err.Error() }
func (e *gogCommandError) Unwrap() error { return e.err }

func boundedTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// APIDisabled is returned when Google's structured error says an API is not enabled in the OAuth client's project.
// The API and project are the validated values from that error, never its text or links.
type APIDisabled struct {
	API     string `json:"api"`
	Project string `json:"project"`
}

func (e *APIDisabled) Error() string { return "Google API is not enabled for the project" }

func classifyGogFailure(err error, fallbackAPI string) error {
	var failed *gogCommandError
	if errors.As(err, &failed) {
		if f, ok := googleapi.ParseFailure(failed.output); ok {
			api := f.API
			if api == "" {
				api = fallbackAPI
			}
			return &APIDisabled{API: api, Project: f.Project}
		}
	}
	return nil
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
		return nil, &gogCommandError{err: err, output: boundedTail(stdout.String()+"\n"+stderr.String(), 8<<10)}
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
		// Adding a service must not quietly narrow what the account already granted for Gmail.
		if scope = r.grantedGogScope(id); scope == "" {
			scope = "readonly"
		}
	}
	granted, _ := r.GogStatus(id)
	services, err := gogServiceList(input.Services, granted.Services)
	if err != nil {
		return GogStartResult{}, err
	}
	flags := gogAuthAddFlags(input.Email, scope, services)
	flags = append(flags, "--remote", "--step", "1", "--redirect-uri", input.RedirectURI)
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
	incremental, err := withIncrementalAuth(result.AuthURL)
	if err != nil {
		return GogStartResult{}, err
	}
	result.AuthURL = incremental
	state := authURL.Query().Get("state")
	if state == "" {
		return GogStartResult{}, errors.New("Google authorization state missing")
	}
	session := gogSession{Email: input.Email, Scope: scope, Services: services, RedirectURI: input.RedirectURI, State: state, ExpiresAt: time.Now().Add(10 * time.Minute)}
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
	services, err := gogServiceList(session.Services, nil)
	if err != nil {
		return err
	}
	flags := gogAuthAddFlags(session.Email, session.Scope, services)
	flags = append(flags, "--remote", "--step", "2", "--auth-url", input.RedirectedURL, "--redirect-uri", session.RedirectURI)
	if _, err := runGog(ctx, spec, nil, flags...); err != nil {
		return errors.New("Google authorization could not be completed")
	}
	account, _ := json.Marshal(gogAccount{Email: session.Email, Scope: session.Scope, Services: services, ConnectedAt: time.Now()})
	if err := writeAtomic0600(filepath.Join(spec.WorkDir, "gog-account.json"), account); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(spec.WorkDir, "gog-oauth-session.json"))
	// The MCP child was started with the tools of the services granted then; the next call starts it with these.
	p.recycle()
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

// gogChecks are the bounded read-only requests that prove each service answers with the stored token.
var gogChecks = map[string]struct {
	args []string
	name string
}{
	"gmail":    {[]string{"gmail", "search", "newer_than:1d", "--max", "1"}, "Gmail"},
	"calendar": {[]string{"calendar", "events", "--today", "--max", "1"}, "Calendar"},
	"drive":    {[]string{"drive", "search", "--max", "1", "--raw-query", "--", "trashed = false"}, "Drive"},
}

// GogCheck performs one bounded read-only request per authorised service. The first service that fails decides the
// error, so an API that is not enabled names that service's API.
func (r *Runner) GogCheck(ctx context.Context, id string) error {
	spec, err := r.gogSpec(id)
	if err != nil {
		return err
	}
	account, err := r.GogStatus(id)
	if err != nil || account.Email == "" {
		return errors.New("Google account is not connected")
	}
	services := authorisedGogServices(account.Services)
	for _, service := range services {
		check, ok := gogChecks[service]
		if !ok {
			continue
		}
		serviceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		args := append([]string{"--json", "--no-input", "--readonly", "--account", account.Email}, check.args...)
		_, err = runGog(serviceCtx, spec, nil, args...)
		cancel()
		if err != nil {
			if disabled := classifyGogFailure(err, googleapi.ServiceAPI[service]); disabled != nil {
				return disabled
			}
			return errors.New(check.name + " read verification failed")
		}
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
	err = os.Remove(filepath.Join(spec.WorkDir, "gog-account.json"))
	p.recycle()
	return err
}

// authorisedServicesOf is the services to report for a status: none without an account, otherwise the recorded ones
// (Gmail for an account recorded before services were tracked).
func authorisedServicesOf(account gogAccount) []string {
	if account.Email == "" {
		return []string{}
	}
	return authorisedGogServices(account.Services)
}
