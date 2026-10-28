package app

import (
	"context"
	"errors"
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

// runnerTokenFunc issues the bearer token of one account.
func (g *AccountGateway) runnerTokenFunc(accountID string) func() (string, error) {
	return func() (string, error) {
		key, err := g.runnerMaster()
		if err != nil {
			return "", err
		}
		return mcprunner.AccountToken(key, accountID), nil
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
