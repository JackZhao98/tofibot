// Package e2b implements a computer.Provider that runs the Bot computer in an
// E2B cloud sandbox built from the TOFI Guest image. The guest listens on TCP
// inside the sandbox and is reached through E2B's port proxy.
package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Named failure codes shared with the environments API.
const (
	CodeInvalidKey          = "invalid_key"
	CodeUnreachable         = "e2b_unreachable"
	CodeLimit               = "e2b_limit"
	CodeImageMissing        = "image_missing"
	CodeTemplateBuildFailed = "template_build_failed"
	CodeSandboxFailed       = "sandbox_failed"
	CodeGuestUnreachable    = "guest_unreachable"
	// CodeSandboxGone: the stored sandbox no longer exists on E2B. Nothing is
	// recreated behind the person's back (its disk is gone); Retry creates a new one.
	CodeSandboxGone = "sandbox_gone"
	// CodeTokenUnreadable: the guest cannot read the token file the server wrote.
	CodeTokenUnreadable = "token_unreadable"
	// CodeNotReady is returned by an action while the template is not ready.
	CodeNotReady = "not_ready"
)

const (
	defaultAPIBase = "https://api.e2b.app"
	defaultDomain  = "e2b.app"
	restTimeout    = 30 * time.Second
	maxMessage     = 300
)

// Error is a named E2B failure. Message is E2B's own short text and never
// carries a credential.
type Error struct {
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "e2b: " + e.Code
	}
	return "e2b: " + e.Code + ": " + e.Message
}

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

func validDomain(d string) bool { return len(d) <= 253 && hostPattern.MatchString(d) }

// e2bDomain reports a domain of E2B's own cloud. With the default API base the
// sandbox URLs (which receive the access tokens) must be on it.
func e2bDomain(d string) bool {
	d = strings.ToLower(d)
	for _, root := range []string{"e2b.app", "e2b.dev"} {
		if d == root || strings.HasSuffix(d, "."+root) {
			return true
		}
	}
	return false
}

// api is the REST client for api.e2b.app.
type api struct {
	hc      *http.Client
	base    string
	key     string
	secrets func() []string // extra values to scrub from messages
}

type sandboxInfo struct {
	SandboxID          string  `json:"sandboxID"`
	TemplateID         string  `json:"templateID"`
	EnvdVersion        string  `json:"envdVersion"`
	EnvdAccessToken    string  `json:"envdAccessToken"`
	TrafficAccessToken *string `json:"trafficAccessToken"`
	Domain             *string `json:"domain"`
	State              string  `json:"state"`
}

func (s sandboxInfo) traffic() string {
	if s.TrafficAccessToken == nil {
		return ""
	}
	return *s.TrafficAccessToken
}

func (s sandboxInfo) domain() string {
	if s.Domain == nil || *s.Domain == "" {
		return defaultDomain
	}
	return *s.Domain
}

func statusCode(status int, fallback string) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return CodeInvalidKey
	case status == http.StatusTooManyRequests:
		return CodeLimit
	case status >= 500:
		return CodeUnreachable
	}
	return fallback
}

func (a *api) scrub(s string) string {
	s = strings.TrimSpace(s)
	secrets := []string{a.key}
	if a.secrets != nil {
		secrets = append(secrets, a.secrets()...)
	}
	for _, secret := range secrets {
		if len(secret) >= 6 {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	if len(s) > maxMessage {
		s = s[:maxMessage] + "..."
	}
	return s
}

// call sends one JSON request. okStatus lists accepted statuses; the matched
// status is returned. Any other status becomes an *Error whose code is derived
// from the status, or fallback for other 4xx.
func (a *api) call(ctx context.Context, method, path string, body, out any, headers map[string]string, fallback string, okStatus ...int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return 0, &Error{Code: CodeUnreachable, Message: "bad E2B request"}
	}
	req.Header.Set("X-API-Key", a.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return 0, ctx.Err()
		}
		return 0, &Error{Code: CodeUnreachable, Message: "E2B did not answer"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, ok := range okStatus {
		if resp.StatusCode == ok {
			if out != nil && len(bytes.TrimSpace(raw)) > 0 {
				if err := json.Unmarshal(raw, out); err != nil {
					return resp.StatusCode, &Error{Code: CodeUnreachable, Status: resp.StatusCode, Message: "E2B sent an unreadable answer"}
				}
			}
			return resp.StatusCode, nil
		}
	}
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return resp.StatusCode, &Error{Code: statusCode(resp.StatusCode, fallback), Status: resp.StatusCode, Message: a.scrub(msg)}
}

// CheckKey validates an API key with a cheap list call. It returns nil, or an
// *Error with code invalid_key, e2b_limit or e2b_unreachable.
func CheckKey(ctx context.Context, hc *http.Client, apiBase, key string) error {
	if hc == nil {
		hc = defaultHTTP()
	}
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	a := &api{hc: hc, base: strings.TrimRight(apiBase, "/"), key: key}
	_, err := a.call(ctx, http.MethodGet, "/v2/sandboxes?paginationLimit=1", nil, nil, nil, CodeUnreachable, http.StatusOK)
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case CodeInvalidKey, CodeLimit, CodeUnreachable:
		default:
			e.Code = CodeUnreachable
		}
	}
	return err
}

func (a *api) createSandbox(ctx context.Context, templateID string, timeout time.Duration, idem string) (sandboxInfo, error) {
	var out sandboxInfo
	body := map[string]any{
		"templateID": templateID,
		"timeout":    int(timeout.Seconds()),
		"autoPause":  true,
		"network":    map[string]any{"allowPublicTraffic": false},
		"metadata":   map[string]string{"app": "tofi"},
	}
	_, err := a.call(ctx, http.MethodPost, "/v2/sandboxes", body, &out, map[string]string{"Idempotency-Key": idem}, CodeSandboxFailed, http.StatusCreated)
	return out, err
}

func (a *api) getSandbox(ctx context.Context, id string) (sandboxInfo, error) {
	var out sandboxInfo
	_, err := a.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, &out, nil, CodeSandboxFailed, http.StatusOK)
	return out, err
}

func (a *api) connect(ctx context.Context, id string, timeout time.Duration) (sandboxInfo, error) {
	var out sandboxInfo
	_, err := a.call(ctx, http.MethodPost, "/v2/sandboxes/"+url.PathEscape(id)+"/connect", map[string]int{"timeout": int(timeout.Seconds())}, &out, nil, CodeSandboxFailed, http.StatusOK, http.StatusCreated)
	return out, err
}

func (a *api) setTimeout(ctx context.Context, id string, timeout time.Duration) error {
	_, err := a.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/timeout", map[string]int{"timeout": int(timeout.Seconds())}, nil, nil, CodeSandboxFailed, http.StatusNoContent)
	return err
}

func (a *api) pause(ctx context.Context, id string) error {
	_, err := a.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/pause", nil, nil, nil, CodeSandboxFailed, http.StatusNoContent)
	return err
}

func (a *api) kill(ctx context.Context, id string) error {
	_, err := a.call(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil, nil, CodeSandboxFailed, http.StatusNoContent)
	return err
}

type templateInfo struct {
	TemplateID string `json:"templateID"`
	BuildID    string `json:"buildID"`
}

func (a *api) createTemplate(ctx context.Context, name string) (templateInfo, error) {
	var out templateInfo
	// The Guest runs Chrome, Xvfb and office tools: E2B's template default is too small.
	body := map[string]any{"name": name, "cpuCount": 2, "memoryMB": 4096, "minFreeDiskMb": 4096}
	_, err := a.call(ctx, http.MethodPost, "/v3/templates", body, &out, nil, CodeTemplateBuildFailed, http.StatusAccepted, http.StatusCreated, http.StatusOK)
	return out, err
}

// templateByName finds a template the team already has under this name (a
// second account on the same E2B team, or a removed and re-added computer).
func (a *api) templateByName(ctx context.Context, name string) (string, error) {
	var out struct {
		TemplateID string `json:"templateID"`
	}
	_, err := a.call(ctx, http.MethodGet, "/templates/aliases/"+url.PathEscape(name), nil, &out, nil, CodeTemplateBuildFailed, http.StatusOK)
	return out.TemplateID, err
}

type templateBuildRow struct {
	BuildID   string `json:"buildID"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// templateBuilds lists a template's builds, newest last as E2B returns them.
func (a *api) templateBuilds(ctx context.Context, templateID string) ([]templateBuildRow, error) {
	var out struct {
		Builds []templateBuildRow `json:"builds"`
	}
	_, err := a.call(ctx, http.MethodGet, "/templates/"+url.PathEscape(templateID), nil, &out, nil, CodeTemplateBuildFailed, http.StatusOK)
	return out.Builds, err
}

func (a *api) startBuild(ctx context.Context, templateID, buildID, image string) error {
	body := map[string]any{
		"fromImage": image,
		"steps":     []map[string]any{{"type": "USER", "args": []string{"root"}}},
		"startCmd":  "/usr/local/bin/tofi-e2b-start",
		"readyCmd":  "curl -fsS http://127.0.0.1:8331/health",
	}
	_, err := a.call(ctx, http.MethodPost, "/v2/templates/"+url.PathEscape(templateID)+"/builds/"+url.PathEscape(buildID), body, nil, nil, CodeTemplateBuildFailed, http.StatusAccepted, http.StatusOK, http.StatusNoContent)
	return err
}

type buildStatus struct {
	Status string `json:"status"`
	Reason *struct {
		Message string `json:"message"`
		Step    string `json:"step"`
	} `json:"reason"`
}

func (a *api) buildStatus(ctx context.Context, templateID, buildID string) (buildStatus, error) {
	var out buildStatus
	_, err := a.call(ctx, http.MethodGet, "/templates/"+url.PathEscape(templateID)+"/builds/"+url.PathEscape(buildID)+"/status", nil, &out, nil, CodeTemplateBuildFailed, http.StatusOK)
	return out, err
}

func isNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func defaultHTTP() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 0
	t.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{Transport: t}
}

func describe(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return fmt.Sprint("request failed")
}
