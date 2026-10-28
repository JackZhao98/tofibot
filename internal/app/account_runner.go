package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

// runnerMasterKey is the key the Runner container derives every account's token
// from. The gateway reads it from a file the Runner (or the installer) created;
// an account runtime only ever receives a function that yields its own token.
type runnerMasterKey struct {
	mu  sync.Mutex
	key []byte
}

func (g *AccountGateway) runnerMaster() ([]byte, error) {
	m := &g.runnerKey
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.key) >= mcprunner.MinMasterKeyBytes {
		return m.key, nil
	}
	data, err := os.ReadFile(g.config.RunnerMasterKeyFile)
	if err != nil {
		return nil, errors.New("local MCP Runner is not ready")
	}
	key := []byte(strings.TrimSpace(string(data)))
	if len(key) < mcprunner.MinMasterKeyBytes {
		return nil, errors.New("local MCP Runner key is not valid")
	}
	m.key = key
	return key, nil
}

// runnerEpochTTL is how long a token epoch read from the Runner is trusted. A
// revocation therefore takes effect for the gateway within this time.
const runnerEpochTTL = 15 * time.Second

type runnerEpochCache struct {
	mu sync.Mutex
	m  map[string]runnerEpoch
}

type runnerEpoch struct {
	epoch int
	at    time.Time
}

// runnerEpoch is the account's token epoch on the Runner (0 until a revocation).
// The epoch is not secret. When the Runner cannot be asked, the last known value
// is used, or 0 if there is none; the Runner then answers 401 and the next call
// asks again.
func (g *AccountGateway) runnerEpoch(accountID string) int {
	c := &g.runnerEpochs
	c.mu.Lock()
	entry, ok := c.m[accountID]
	c.mu.Unlock()
	if ok && time.Since(entry.at) < runnerEpochTTL {
		return entry.epoch
	}
	epoch := entry.epoch
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.config.RunnerGatewayURL, "/")+mcprunner.AccountPathPrefix+accountID+"/epoch", nil); err == nil {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if resp, err := client.Do(req); err == nil {
			var out struct {
				Epoch int `json:"epoch"`
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<10)).Decode(&out) == nil && out.Epoch >= 0 {
				epoch = out.Epoch
			}
			resp.Body.Close()
		}
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]runnerEpoch{}
	}
	c.m[accountID] = runnerEpoch{epoch: epoch, at: time.Now()}
	c.mu.Unlock()
	return epoch
}

// runnerTokenFunc issues the bearer token of one account at its current epoch.
func (g *AccountGateway) runnerTokenFunc(accountID string) func() (string, error) {
	return func() (string, error) {
		key, err := g.runnerMaster()
		if err != nil {
			return "", err
		}
		return mcprunner.AccountTokenAt(key, accountID, g.runnerEpoch(accountID)), nil
	}
}

// configureAccountRunner gives a workspace its namespace on the Runner
// container. It replaces any other Runner setting of the workspace.
func (g *AccountGateway) configureAccountRunner(c *Config, accountID string) {
	if g.config.RunnerGatewayURL == "" || !mcprunner.ValidAccountID(accountID) {
		return
	}
	c.LocalRunnerURL = strings.TrimRight(g.config.RunnerGatewayURL, "/") + mcprunner.AccountPathPrefix + accountID
	c.LocalRunnerTokenFile = ""
	c.LocalRunnerTokenFunc = g.runnerTokenFunc(accountID)
}

// deleteRunnerState removes the account's namespace on the Runner container:
// its processes, plugins and tokens. It is idempotent.
func (g *AccountGateway) deleteRunnerState(ctx context.Context, accountID string) error {
	if g.config.RunnerGatewayURL == "" {
		return nil
	}
	token, err := g.runnerTokenFunc(accountID)()
	if err != nil {
		return &accountDeleteError{code: "runner_unavailable", message: "local connection data could not be removed"}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(g.config.RunnerGatewayURL, "/")+mcprunner.AccountPathPrefix+accountID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return &accountDeleteError{code: "runner_unavailable", message: "local connection data could not be removed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &accountDeleteError{code: "runner_unavailable", message: "local connection data could not be removed"}
	}
	return nil
}
