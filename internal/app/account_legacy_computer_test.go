package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestAccountLegacyComputerBindingRoutingAndRetainedDataRollback(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tofi-legacy-binding-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	legacy, err := NewServer(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	instance := legacy.instance.ID
	id := uuid.NewSHA1(uuid.MustParse("79d8c04b-3d65-4b99-97bf-f4e8a5630d47"), []byte(instance+":legacy-owner")).String()
	_, err = legacy.store.db.Exec(`INSERT INTO workspace_owner(id,username,email,salt,password_hash,created_at) VALUES(1,'legacy','legacy@example.test',?, ?,1)`, []byte("synthetic-salt"), []byte("synthetic-hash"))
	if err != nil {
		t.Fatal(err)
	}
	bot, err := legacy.store.CreateBot("Preserved owner bot", "synthetic acceptance", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := legacy.store.AddAttachment(bot.DMConversationID, "original.txt", "text/plain", strings.NewReader("preserved original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	cookieRecorder := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.TLS = &tls.ConnectionState{}
	if err = legacy.ownerAuth.issue(cookieRecorder, req); err != nil {
		t.Fatal(err)
	}
	originalCookie := cookieRecorder.Result().Cookies()[0]
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	phase := "worker"
	verified := true
	proofInstance := instance
	var mu sync.Mutex
	var requests []map[string]any
	broker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid broker request")
		}
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, body)
		switch body["op"] {
		case "capacity":
			json.NewEncoder(w).Encode(map[string]any{"total_bytes": int64(100) << 30, "available_bytes": int64(100) << 30, "allocated_bytes": 0, "promised_bytes": int64(16) << 30, "unallocated_promises_bytes": int64(16) << 30, "admission_remaining_bytes": int64(84) << 30, "warning": false, "accounts": []map[string]any{{"account_id": id, "slot": 2, "quota_bytes": int64(16) << 30, "state": "reserved", "logical_bytes": int64(16) << 30, "allocated_bytes": 0}}})
		case "adoption_status":
			if body["account_id"] != id {
				t.Error("wrong installation computer identity")
			}
			json.NewEncoder(w).Encode(map[string]any{"account_id": id, "instance_id": proofInstance, "asset_id": "personal", "phase": phase, "verified": verified})
		case "quota":
			if body["account_id"] != id {
				t.Error("quota did not use adopted identity")
			}
			json.NewEncoder(w).Encode(map[string]any{"account_id": id, "quota_bytes": int64(16) << 30, "applied": true})
		default:
			json.NewEncoder(w).Encode(map[string]any{"account_id": body["account_id"], "socket": filepath.Join(dir, body["account_id"].(string), "control.sock")})
		}
	})}
	go broker.Serve(listener)
	defer broker.Close()
	c := Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance", AccountProvisionerSocket: socket, AccountComputerSocketRoot: dir, AccountComputerDiskGiB: 8, AccountLegacyComputerUUID: id, ComputerSocket: filepath.Join(dir, "personal", "control.sock")}
	g, err := NewAccountGateway(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { g.Close() }()
	owner := Account{ID: "legacy-owner", Legacy: true, Role: "admin"}
	runtime, err := g.workspace(owner)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.isolatedWorkspace || runtime.instance.ID != instance {
		t.Fatal("adoption moved historical data or kept global Runner")
	}
	base, token, err := runtime.localRunnerConfig()
	if err != nil || base != computer.RunnerOrigin || token != "" {
		t.Fatalf("private Runner mapping: %s %v", base, err)
	}
	if got := accountRequest(g, http.MethodGet, "/api/attachments/"+attachment.ID, "", originalCookie); got.Code != 200 || got.Body.String() != "preserved original bytes" {
		t.Fatalf("old session/file: %d %s", got.Code, got.Body.String())
	}
	if _, err = g.computerRequest(context.Background(), "ensure", g.computerIdentity(owner)); err != nil {
		t.Fatal(err)
	}
	quota := accountRequest(g, http.MethodPatch, "/api/admin/accounts/legacy-owner/quota", `{"quota_gib":16}`, originalCookie)
	if quota.Code != 200 || !strings.Contains(quota.Body.String(), `"account_id":"legacy-owner"`) {
		t.Fatalf("adopted quota: %d %s", quota.Code, quota.Body.String())
	}
	capacity := accountRequest(g, http.MethodGet, "/api/admin/capacity", "", originalCookie)
	if capacity.Code != 200 || !strings.Contains(capacity.Body.String(), `"account_id":"legacy-owner"`) || !strings.Contains(capacity.Body.String(), `"computer_id":"`+id+`"`) {
		t.Fatalf("adopted Admin disk projection: %d %s", capacity.Code, capacity.Body.String())
	}
	other, err := g.create(context.Background(), "second-admin", "second@example.test", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET role='admin',must_change_password=0 WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	adminCookie := accountCookie(t, g, other)
	for _, disabled := range []string{"true", "false"} {
		result := accountRequest(g, http.MethodPatch, "/api/admin/accounts/legacy-owner", `{"disabled":`+disabled+`}`, adminCookie)
		if result.Code != 200 {
			t.Fatalf("legacy computer transition: %d %s", result.Code, result.Body.String())
		}
	}
	mu.Lock()
	var sawDisable, sawRestore bool
	for _, body := range requests {
		if body["account_id"] == id {
			sawDisable = sawDisable || body["op"] == "disable"
			sawRestore = sawRestore || body["op"] == "restore"
		}
	}
	mu.Unlock()
	if !sawDisable || !sawRestore {
		t.Fatal("legacy disable/restore bypassed Worker")
	}
	otherRuntime, err := g.workspace(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherRuntime.store == runtime.store || g.computerIdentity(other) != other.ID {
		t.Fatal("new tenant inherited legacy store or computer")
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	// Ownership rollback keeps the compatible account App, its current DB and
	// private Guest Runner, switching only this owner's fixed computer socket.
	mu.Lock()
	phase = "legacy"
	mu.Unlock()
	g, err = NewAccountGateway(c)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = g.workspace(owner)
	if err != nil {
		t.Fatal(err)
	}
	if base, _, err = runtime.localRunnerConfig(); err != nil || base != computer.RunnerOrigin {
		t.Fatal("rollback discarded Guest Runner references")
	}
	var count int
	if err = g.root.store.db.QueryRow(`SELECT count(*) FROM accounts WHERE id=?`, other.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("rollback lost new tenant")
	}
	g.Close()
	missing := c
	missing.AccountLegacyComputerUUID = ""
	if reopened, err := NewAccountGateway(missing); err == nil {
		reopened.Close()
		t.Fatal("persisted mapping silently fell back to external owner")
	}
	mu.Lock()
	verified = false
	mu.Unlock()
	if reopened, err := NewAccountGateway(c); err == nil {
		reopened.Close()
		t.Fatal("accepted unverified ownership")
	}
	mu.Lock()
	verified = true
	proofInstance = uuid.NewString()
	mu.Unlock()
	if reopened, err := NewAccountGateway(c); err == nil {
		reopened.Close()
		t.Fatal("accepted another installation proof")
	}
}
