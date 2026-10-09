package computer

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHibernatedComputerActionGoesToTheManagerWithoutPreparing(t *testing.T) {
	var prepares, actions atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "firecracker", "state": "hibernated", "hibernation": true,
				"hibernated_at": "2026-10-08T00:00:00Z", "last_wake": map[string]any{"kind": "restore", "seconds": 1.6, "clock_synced": true,
					"storage_seconds": 0.04, "image_attach": map[string]any{"rootfs.ext4": "bind", "vmlinux": "bind"}}})
		case "/v1/prepare", "/v1/retry":
			prepares.Add(1)
		case "/v1/action":
			// The manager restores the computer inside this request.
			actions.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": "done"})
		}
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}}}
	c := &Client{socket: "/fixture", http: client, ensure: func(context.Context) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Action(ctx, Action{BotID: "b", Name: "shell.exec"}); err != nil {
		t.Fatalf("action on hibernated computer: %v", err)
	}
	if actions.Load() != 1 || prepares.Load() != 0 {
		t.Fatalf("actions=%d prepares=%d", actions.Load(), prepares.Load())
	}
	info, err := c.Info(ctx)
	if err != nil || !info.Hibernation || info.HibernatedAt == "" || info.LastWake == nil || info.LastWake.Kind != "restore" {
		t.Fatalf("info = %+v %v", info, err)
	}
	// The manager's wake diagnostics reach the App's info API unchanged.
	wake := info.LastWake
	if wake.StorageSeconds == nil || *wake.StorageSeconds != 0.04 || wake.ClockSynced == nil || !*wake.ClockSynced ||
		wake.ImageAttach["rootfs.ext4"] != "bind" || wake.ImageAttach["vmlinux"] != "bind" {
		t.Fatalf("last_wake = %+v", wake)
	}
}
