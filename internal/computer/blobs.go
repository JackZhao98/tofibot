package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const MaxBlobBytes int64 = 20 << 20

func (c *Client) blob(ctx context.Context, method, id string, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return nil, errors.New("invalid blob identity")
	}
	if int64(len(data)) > MaxBlobBytes {
		return nil, errors.New("blob exceeds upload limit")
	}
	if c.ensure != nil {
		if err := c.ensure(ctx); err != nil {
			return nil, err
		}
	}
	if err := c.waitForBlobGuest(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://tofi-computer/v1/blobs/"+id, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("guest blob storage returned %d", response.StatusCode)
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, MaxBlobBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > MaxBlobBytes {
		return nil, errors.New("guest blob exceeds download limit")
	}
	return result, nil
}
func (c *Client) PutBlob(ctx context.Context, id string, data []byte) error {
	_, err := c.blob(ctx, "PUT", id, data)
	return err
}
func (c *Client) GetBlob(ctx context.Context, id string) ([]byte, error) {
	return c.blob(ctx, "GET", id, nil)
}
func (c *Client) DeleteBlob(ctx context.Context, id string) error {
	_, err := c.blob(ctx, "DELETE", id, nil)
	return err
}

// Preparation is scoped to this fixed account socket; no host disk mounts or
// alternate storage fallback. The caller deadline bounds cold boot and transfer.
func (c *Client) waitForBlobGuest(ctx context.Context) error {
	prepared := false
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, _ := http.NewRequestWithContext(probeCtx, "GET", "http://tofi-computer/v1/info", nil)
		response, err := c.http.Do(req)
		var info Info
		if err == nil {
			if response.StatusCode == 200 {
				json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&info)
			}
			response.Body.Close()
		}
		cancel()
		if info.State == "ready" {
			return nil
		}
		if info.State == "error" {
			return errors.New("account guest file storage unavailable")
		}
		if info.State == "stopped" && !prepared {
			prepared = true
			req, _ := http.NewRequestWithContext(ctx, "POST", "http://tofi-computer/v1/prepare", bytes.NewReader([]byte("{}")))
			req.Header.Set("Content-Type", "application/json")
			response, err := c.http.Do(req)
			if err != nil {
				return err
			}
			response.Body.Close()
			if response.StatusCode != 202 && response.StatusCode != 409 {
				return errors.New("account guest preparation rejected")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
