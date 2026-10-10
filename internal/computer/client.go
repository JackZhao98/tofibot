// Package computer contains the small control-plane client used to talk to a
// Bot's fixed computer.  The control socket is deliberately configured by the
// service, rather than selected by a request, so callers cannot route a Bot to
// an arbitrary VM.
package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const DefaultSocket = "/run/tofi-computer/control.sock"

const controlInfoTimeout = 2 * time.Second
const oauthStartTimeout = 60 * time.Second
const oauthSessionTimeout = 10 * time.Second
const purgeTimeout = 180 * time.Second

type Config struct {
	Socket string
	Client *http.Client
	Ensure func(context.Context) error
}

type Info struct {
	VCPUs              int    `json:"vcpus,omitempty"`
	MemoryMiB          int    `json:"memory_mib,omitempty"`
	DiskGiB            int    `json:"disk_gib,omitempty"`
	DesktopIdleSeconds *int   `json:"desktop_idle_seconds,omitempty"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	Phase              string `json:"phase,omitempty"`
	WorkspaceRoot      string `json:"workspace_root"`
	Browser            string `json:"browser,omitempty"`
	Error              string `json:"error,omitempty"`
	ID                 string `json:"id,omitempty"`
	// Hibernation reports whether an idle computer is snapshotted to disk
	// instead of left running; HibernatedAt is set while it is.
	Hibernation  bool      `json:"hibernation,omitempty"`
	HibernatedAt string    `json:"hibernated_at,omitempty"`
	LastWake     *WakeInfo `json:"last_wake,omitempty"`
}

// Computer states beside ready/starting/stopped/error. A hibernated computer
// is available: the manager restores it on the next guest-bound request.
const (
	StateHibernating = "hibernating"
	StateHibernated  = "hibernated"
	StateResuming    = "resuming"
)

// Available reports a computer that serves actions now or after an automatic
// restore from hibernation.
func Available(state string) bool {
	switch state {
	case "ready", StateHibernating, StateHibernated, StateResuming:
		return true
	}
	return false
}

// WakeInfo describes the computer's most recent start: a snapshot restore or
// a cold boot (with the reason when a snapshot was rejected).
type WakeInfo struct {
	Kind           string  `json:"kind"`
	Seconds        float64 `json:"seconds"`
	FallbackReason string  `json:"fallback_reason,omitempty"`
	// StorageSeconds is the jail and disk preparation part of the wake;
	// ImageAttach says how each release image reached the jail ("link",
	// "bind" or the 5 GiB "copy" fallback). ClockSynced is set on a restore.
	StorageSeconds *float64          `json:"storage_seconds,omitempty"`
	ImageAttach    map[string]string `json:"image_attach,omitempty"`
	ClockSynced    *bool             `json:"clock_synced,omitempty"`
}

type Action struct {
	Source        string                `json:"source,omitempty"`
	BotID         string                `json:"bot_id"`
	BotName       string                `json:"bot_name,omitempty"`
	RunID         string                `json:"run_id,omitempty"`
	Name          string                `json:"action"`
	Args          json.RawMessage       `json:"args,omitempty"`
	WriteIdentity *tooloutcome.Identity `json:"write_identity,omitempty"`
}

type ActionResult struct {
	OK      bool                 `json:"ok"`
	Result  json.RawMessage      `json:"result,omitempty"`
	Error   string               `json:"error,omitempty"`
	Outcome *tooloutcome.Outcome `json:"outcome,omitempty"`
}

// OAuthStartResult is the guest-owned authorization session created by the
// private vsock bridge. RedirectURI is safe for the caller to open in its UI.
type OAuthStartResult struct {
	SessionID   string `json:"session_id"`
	RedirectURI string `json:"redirect_uri"`
}

type OAuthArmResult struct {
	OK bool `json:"ok"`
}

type OAuthPollResult struct {
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	State  string `json:"state,omitempty"`
	Error  string `json:"error,omitempty"`
}

type OAuthCancelResult struct {
	OK bool `json:"ok"`
}

type OAuthSessionRequest struct {
	SessionID string `json:"session_id"`
}

type Client struct {
	socket string
	http   *http.Client
	ensure func(context.Context) error
}

func New(cfg Config) (*Client, error) {
	socket := strings.TrimSpace(cfg.Socket)
	if socket == "" {
		socket = DefaultSocket
	}
	if len(socket) > 108 || !strings.HasPrefix(socket, "/") {
		return nil, errors.New("computer socket must be an absolute Unix socket path")
	}
	client := cfg.Client
	if client == nil {
		client = NewUnixHTTPClient(socket, 150*time.Second)
	}
	return &Client{socket: socket, http: client, ensure: cfg.Ensure}, nil
}

func (c *Client) Socket() string { return c.socket }

func (c *Client) request(ctx context.Context, method, endpoint string, body any, out any) error {
	return c.do(ctx, method, endpoint, body, out, true, 0)
}

// ActionDeadlineError reports a guest action that did not answer within its
// own deadline, measured after admission and cold-boot readiness.
type ActionDeadlineError struct{ Limit time.Duration }

func (e *ActionDeadlineError) Error() string {
	return fmt.Sprintf("computer action did not answer within %s", e.Limit)
}

// do sends one control request. Observational probes pass admit=false: they
// must never start (or keep alive) an account computer that is not running.
// A positive limit bounds only the request itself, not admission or boot.
func (c *Client) do(ctx context.Context, method, endpoint string, body any, out any, admit bool, limit time.Duration) error {
	if admit && c.ensure != nil {
		if err := c.ensure(ctx); err != nil {
			return fmt.Errorf("computer admission: %w", err)
		}
		if method == http.MethodPost && endpoint == "/v1/action" {
			// Admission starts the manager asynchronously. Wait on the fixed
			// socket before sending a guest action, which must never be replayed.
			if err := c.waitForBlobGuest(ctx); err != nil {
				return fmt.Errorf("computer guest readiness: %w", err)
			}
		}
	}
	if limit > 0 {
		parent := ctx
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
		err := c.send(ctx, method, endpoint, body, out)
		if err != nil && parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ActionDeadlineError{Limit: limit}
		}
		return err
	}
	return c.send(ctx, method, endpoint, body, out)
}

func (c *Client) send(ctx context.Context, method, endpoint string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://tofi-computer"+endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("computer control socket: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		// A guest refusal that carries a structured not-executed outcome proves
		// the action was never performed. Anything else stays unclassified.
		if _, ok := body.(Action); ok {
			var rejected ActionResult
			if json.Unmarshal(message, &rejected) == nil && rejected.Outcome != nil {
				o := *rejected.Outcome
				if o.Version == 1 && o.Status == tooloutcome.Validation && o.Certainty == "not_executed" && o.NextAction != "" {
					return o.Err()
				}
			}
		}
		return fmt.Errorf("computer control returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return fmt.Errorf("invalid computer control response: %w", err)
	}
	return nil
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	infoCtx, cancel := context.WithTimeout(ctx, controlInfoTimeout)
	defer cancel()
	var out Info
	if err := c.request(infoCtx, http.MethodGet, "/v1/info", nil, &out); err != nil {
		return out, err
	}
	if out.Kind == "" {
		out.Kind = "firecracker"
	}
	return out, nil
}

func (c *Client) Action(ctx context.Context, action Action) (ActionResult, error) {
	return c.ActionWithin(ctx, action, 0)
}

// ActionWithin is Action with a deadline for the guest's answer. Admission and
// cold-boot readiness stay bounded by ctx alone; a slow boot is not a hung guest.
func (c *Client) ActionWithin(ctx context.Context, action Action, limit time.Duration) (ActionResult, error) {
	if strings.TrimSpace(action.BotID) == "" {
		return ActionResult{}, errors.New("bot_id is required")
	}
	if strings.TrimSpace(action.Name) == "" {
		return ActionResult{}, errors.New("action is required")
	}
	if len(action.Args) == 0 {
		action.Args = json.RawMessage(`{}`)
	}
	var out ActionResult
	if err := c.do(ctx, http.MethodPost, "/v1/action", action, &out, true, limit); err != nil {
		return out, err
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "computer action failed"
		}
		return out, errors.New(out.Error)
	}
	if len(out.Result) == 0 {
		out.Result = json.RawMessage(`null`)
	}
	return out, nil
}

func (c *Client) Retry(ctx context.Context) error {
	retryCtx, cancel := context.WithTimeout(ctx, controlInfoTimeout)
	defer cancel()
	return c.request(retryCtx, http.MethodPost, "/v1/retry", map[string]any{}, nil)
}

// Purge clears the persistent workspace through the fixed, root-owned VM
// manager. It deliberately does not use a guest shell action: the manager
// stops the VM, checkpoints the disk, replaces it with a fresh filesystem, and
// boots the same configured VM again.
func (c *Client) Purge(ctx context.Context) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, purgeTimeout)
	defer cancel()
	var out Info
	err := c.request(ctx, http.MethodPost, "/v1/purge", map[string]bool{"confirm": true}, &out)
	return out, err
}

// OAuthStart asks the existing guest VM to create an authorization session.
func (c *Client) OAuthStart(ctx context.Context) (OAuthStartResult, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthStartTimeout)
	defer cancel()
	var out OAuthStartResult
	err := c.request(ctx, http.MethodPost, "/v1/oauth/start", map[string]any{}, &out)
	if err == nil && (out.SessionID == "" || out.RedirectURI == "") {
		err = errors.New("invalid OAuth start response")
	}
	return out, err
}

func (c *Client) OAuthArm(ctx context.Context, sessionID, state string) (OAuthArmResult, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthSessionTimeout)
	defer cancel()
	var out OAuthArmResult
	err := c.request(ctx, http.MethodPost, "/v1/oauth/arm", struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}{sessionID, state}, &out)
	if err == nil && !out.OK {
		err = errors.New("OAuth arm failed")
	}
	return out, err
}

func (c *Client) OAuthPoll(ctx context.Context, sessionID string) (OAuthPollResult, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthSessionTimeout)
	defer cancel()
	var out OAuthPollResult
	err := c.request(ctx, http.MethodPost, "/v1/oauth/poll", OAuthSessionRequest{SessionID: sessionID}, &out)
	if err == nil {
		switch out.Status {
		case "pending", "complete", "denied", "expired":
		default:
			err = errors.New("invalid OAuth poll response")
		}
	}
	return out, err
}

func (c *Client) OAuthCancel(ctx context.Context, sessionID string) (OAuthCancelResult, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthSessionTimeout)
	defer cancel()
	var out OAuthCancelResult
	err := c.request(ctx, http.MethodPost, "/v1/oauth/cancel", OAuthSessionRequest{SessionID: sessionID}, &out)
	if err == nil && !out.OK {
		err = errors.New("OAuth cancel failed")
	}
	return out, err
}

// NewUnixHTTPClient is exported for tests and for callers that need a custom
// timeout while retaining the fixed socket routing.
func NewUnixHTTPClient(socket string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 150 * time.Second
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}}
}

func ConfiguredSocket() string {
	return strings.TrimSpace(os.Getenv("TOFI_COMPUTER_SOCKET"))
}

// Resources describes one user's shared computer, not a per-Bot quota.
type ResourceAllocation struct {
	VCPUs     int `json:"vcpus"`
	MemoryMiB int `json:"memory_mib"`
	DiskGiB   int `json:"disk_gib"`
}
type Resources struct {
	Error       string             `json:"error,omitempty"`
	Scope       string             `json:"scope"`
	State       string             `json:"state"`
	Current     ResourceAllocation `json:"current"`
	Desired     ResourceAllocation `json:"desired"`
	Pending     bool               `json:"pending"`
	ApplyPolicy string             `json:"apply_policy"`
	Host        map[string]int     `json:"host"`
	Limits      map[string]int     `json:"limits"`
	Memory      *MemoryUsage       `json:"memory,omitempty"`
}

// MemoryUsage is observational: the Firecracker process RSS on the host and,
// with the virtio balloon, the guest driver's own statistics. Guest numbers
// are an indication only and never an admission input.
type MemoryUsage struct {
	BalloonEnabled bool           `json:"balloon_enabled"`
	HostRSSMiB     *int           `json:"host_rss_mib"`
	Balloon        map[string]int `json:"balloon"`
}

func (c *Client) Resources(ctx context.Context) (Resources, error) {
	ctx, cancel := context.WithTimeout(ctx, controlInfoTimeout)
	defer cancel()
	var out Resources
	err := c.request(ctx, http.MethodGet, "/v1/resources", nil, &out)
	return out, err
}
func (c *Client) ConfigureResources(ctx context.Context, value ResourceAllocation) (Resources, error) {
	ctx, cancel := context.WithTimeout(ctx, controlInfoTimeout)
	defer cancel()
	var out Resources
	err := c.request(ctx, http.MethodPost, "/v1/resources", value, &out)
	return out, err
}

// ApplyResources restarts the shared VM only after explicit user confirmation.
func (c *Client) ApplyResources(ctx context.Context) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, controlInfoTimeout)
	defer cancel()
	var out Info
	err := c.request(ctx, http.MethodPost, "/v1/resources/apply", map[string]bool{"confirm": true}, &out)
	return out, err
}

// GuestHealth is the manager's direct probe of the guest agent over vsock. It
// distinguishes a VM whose process is alive ("ready") but whose guest no longer
// answers, which /v1/info cannot see.
type GuestHealth struct {
	State string `json:"state"`
	// Guest is "ok", "unresponsive" or "not_ready" (the VM is not running).
	Guest string `json:"guest"`
	Error string `json:"error,omitempty"`
}

// ErrHealthUnsupported means the manager predates /v1/health; callers must not
// treat it as an unhealthy guest.
var ErrHealthUnsupported = errors.New("computer manager does not support health probes")

const healthTimeout = 12 * time.Second
const recoverTimeout = 45 * time.Second // the manager re-probes the guest before restarting

func (c *Client) Health(ctx context.Context) (GuestHealth, error) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	var out GuestHealth
	err := c.do(ctx, http.MethodGet, "/v1/health", nil, &out, false, 0)
	if err != nil && strings.Contains(err.Error(), "404 Not Found") {
		return out, ErrHealthUnsupported
	}
	if err == nil && out.Guest == "" {
		err = errors.New("invalid computer health response")
	}
	return out, err
}

// Recover asks the manager to restart this computer because its guest stopped
// answering. The manager re-probes the guest itself and refuses (409) to
// restart one that is responsive, so the App cannot restart a healthy VM.
func (c *Client) Recover(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, recoverTimeout)
	defer cancel()
	err := c.do(ctx, http.MethodPost, "/v1/recover", map[string]bool{"confirm": true}, nil, false, 0)
	if err != nil && strings.Contains(err.Error(), "guest is responsive") {
		return ErrGuestResponsive
	}
	return err
}

// ErrGuestResponsive means the manager's own probe found the guest answering.
var ErrGuestResponsive = errors.New("computer guest is responsive")
