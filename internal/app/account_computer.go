package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

func (g *AccountGateway) computerRequest(ctx context.Context, op, id string) (string, error) {
	if g.config.AccountProvisionerSocket == "" {
		return "", nil
	}
	if !filepath.IsAbs(g.config.AccountComputerSocketRoot) {
		return "", errors.New("absolute account computer paths required")
	}
	body := map[string]any{"op": op, "account_id": id}
	if op == "reserve" {
		body["quota_gib"] = g.config.AccountComputerDiskGiB
	}
	data, err := g.computerControl(ctx, body)
	if err != nil {
		return "", err
	}
	var result struct {
		AccountID string `json:"account_id"`
		Socket    string `json:"socket"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if op != "ensure" {
		return "", nil
	}
	expected := filepath.Join(g.config.AccountComputerSocketRoot, id, "control.sock")
	if result.AccountID != id || result.Socket != expected {
		return "", errors.New("account computer identity mismatch")
	}
	return expected, nil
}

func (g *AccountGateway) computerControl(ctx context.Context, body any) ([]byte, error) {
	if !filepath.IsAbs(g.config.AccountProvisionerSocket) {
		return nil, errors.New("account provisioner is not configured")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", g.config.AccountProvisionerSocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 75 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://account-provisioner/v1/accounts", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("account computer control rejected (%d)", resp.StatusCode)
	}
	const limit = 256 << 10
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && len(data) > limit {
		err = errors.New("account computer response too large")
	}
	return data, err
}
