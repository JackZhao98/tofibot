package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bootstrapBody(secret, username string) string {
	data, _ := json.Marshal(map[string]string{"bootstrap_secret": secret, "username": username, "email": username + "@example.test", "password": "SyntheticPassword123!"})
	return string(data)
}

// The synthetic broker can only count/reject reservations; it provisions no
// Worker or guest and receives no bootstrap secret or password.
func bootstrapBroker(t *testing.T, g *AccountGateway) (*atomic.Int32, *atomic.Bool) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tofi-bootstrap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var reserves atomic.Int32
	var reject atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["op"] != "reserve" || len(body) != 3 {
			t.Error("unexpected synthetic broker request")
			w.WriteHeader(400)
			return
		}
		reserves.Add(1)
		if reject.Load() {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"account_id": body["account_id"]})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	g.config.AccountProvisionerSocket = socket
	g.config.AccountComputerSocketRoot = dir
	return &reserves, &reject
}

func bootstrapState(t *testing.T, g *AccountGateway, wantAccounts, wantConsumed int, wantHash bool) {
	t.Helper()
	var count, consumed, hashPresent, owners int
	err := g.root.store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM accounts), (SELECT consumed FROM account_bootstrap WHERE id=1), (SELECT bootstrap_hash IS NOT NULL FROM owner_auth_settings WHERE id=1), (SELECT COUNT(*) FROM workspace_owner)`).Scan(&count, &consumed, &hashPresent, &owners)
	if err != nil || count != wantAccounts || consumed != wantConsumed || (hashPresent == 1) != wantHash || owners != 0 {
		t.Fatalf("bootstrap state: accounts=%d consumed=%d hash_present=%d owners=%d err=%v", count, consumed, hashPresent, owners, err)
	}
}

func bootstrapRestart(t *testing.T, g *AccountGateway) *AccountGateway {
	t.Helper()
	config := g.config
	config.OwnerAuth = false         // Persisted authority must remain enabled on restart.
	config.AccountMaintenance = true // No workspace background jobs in this fixture.
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := NewAccountGateway(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	return next
}

func TestAccountBootstrapRequiresDeploymentSecret(t *testing.T) {
	g := accountFixture(t)
	w := accountRequest(g, "POST", "/api/auth/setup", `{"username":"synthetic-admin","email":"admin@example.test","password":"SyntheticPassword123!"}`, nil)
	if w.Code != 401 {
		t.Fatalf("missing bootstrap secret: status=%d, want 401", w.Code)
	}
}

func TestAccountBootstrapRejectsBeforeHashAndReservation(t *testing.T) {
	g := accountFixture(t)
	reserves, _ := bootstrapBroker(t, g)
	secret := accountCreationSecret(t, g, true)
	for i := 0; i < cap(g.auth.hashing); i++ {
		g.auth.hashing <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(g.auth.hashing); i++ {
			<-g.auth.hashing
		}
	}()
	for _, invalid := range []string{"", "wrong-synthetic-secret", " " + secret, secret + "="} {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		r := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(bootstrapBody(invalid, "synthetic-admin"))).WithContext(ctx)
		r.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		if ctx.Err() != nil || w.Code != 401 || !strings.Contains(w.Body.String(), `"invalid_bootstrap"`) {
			t.Errorf("invalid secret reached hashing or wrong error: status=%d context=%v", w.Code, ctx.Err())
		}
		cancel()
		if strings.Contains(w.Body.String(), secret) || len(w.Result().Cookies()) != 0 {
			t.Fatal("bootstrap response disclosed authority")
		}
	}
	if reserves.Load() != 0 {
		t.Fatal("invalid secret reserved a Worker")
	}
	bootstrapState(t, g, 0, 0, true)
}

func TestAccountBootstrapAuthorizedRaceReplayAndRestart(t *testing.T) {
	g := accountFixture(t)
	reserves, _ := bootstrapBroker(t, g)
	secret := accountCreationSecret(t, g, true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i, candidate := range []string{secret, secret, "wrong-synthetic-secret"} {
		wg.Add(1)
		go func(i int, candidate string) {
			defer wg.Done()
			<-start
			w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(candidate, []string{"alpha-admin", "bravo-admin", "wrong-admin"}[i]), nil)
			if w.Code == 200 {
				if candidate != secret {
					t.Error("invalid competitor won setup")
				}
				if !strings.Contains(w.Body.String(), `"role":"admin"`) || len(w.Result().Cookies()) != 1 {
					t.Error("missing Admin session")
				}
				successes.Add(1)
			} else if w.Code != 401 {
				t.Errorf("race rejection status=%d", w.Code)
			}
		}(i, candidate)
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 || reserves.Load() != 1 {
		t.Fatalf("winners=%d reserves=%d", successes.Load(), reserves.Load())
	}
	bootstrapState(t, g, 1, 1, false)
	if _, err := os.Stat(g.auth.bootstrapPath); !os.IsNotExist(err) {
		t.Fatal("committed bootstrap file retained")
	}
	g = bootstrapRestart(t, g)
	bootstrapState(t, g, 1, 1, false)
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "replay-admin"), nil); w.Code != 503 {
		t.Fatal("maintenance no longer fenced")
	}
	g.config.AccountMaintenance = false
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "replay-admin"), nil); w.Code != 401 {
		t.Fatal("consumed bootstrap replayed after restart")
	}
	var username string
	if err := g.root.store.db.QueryRow(`SELECT username FROM accounts`).Scan(&username); err != nil {
		t.Fatal(err)
	}
	loginBody, _ := json.Marshal(map[string]string{"identifier": username, "password": "SyntheticPassword123!"})
	if w := accountRequest(g, "POST", "/api/auth/login", string(loginBody), nil); w.Code != 200 {
		t.Fatal("initialized Admin could not log in after restart")
	}
	if _, err := g.root.store.db.Exec(`DELETE FROM account_sessions; DELETE FROM accounts`); err != nil {
		t.Fatal(err)
	}
	g = bootstrapRestart(t, g)
	g.config.AccountMaintenance = false
	bootstrapState(t, g, 0, 1, false)
	if w := accountRequest(g, "GET", "/api/auth/session", "", nil); strings.Contains(w.Body.String(), `"setup_required":true`) {
		t.Fatal("empty consumed installation advertised setup")
	}
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "reopened-admin"), nil); w.Code != 401 {
		t.Fatal("empty consumed installation reopened setup")
	}
	if reserves.Load() != 1 {
		t.Fatal("replay reserved a Worker")
	}
}

func TestAccountBootstrapReservationFailureRollsBackAuthority(t *testing.T) {
	g := accountFixture(t)
	reserves, reject := bootstrapBroker(t, g)
	secret := accountCreationSecret(t, g, true)
	before := sha256.Sum256([]byte(secret))
	reject.Store(true)
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 409 || !strings.Contains(w.Body.String(), `"setup_unavailable"`) || len(w.Result().Cookies()) != 0 {
		t.Fatal("reservation rejection semantics changed")
	}
	bootstrapState(t, g, 0, 0, true)
	var saved []byte
	if err := g.root.store.db.QueryRow(`SELECT bootstrap_hash FROM owner_auth_settings WHERE id=1`).Scan(&saved); err != nil || string(saved) != string(before[:]) {
		t.Fatal("reservation failure consumed authority")
	}
	if accountCreationSecret(t, g, true) != secret {
		t.Fatal("reservation failure removed or rotated secret")
	}
	reject.Store(false)
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 200 {
		t.Fatal("authorized retry failed")
	}
	bootstrapState(t, g, 1, 1, false)
	if reserves.Load() != 2 {
		t.Fatal("wrong reservation attempts")
	}
}

func TestAccountBootstrapOlderAccountsCloseWithoutFile(t *testing.T) {
	g := accountFixture(t)
	secret := accountCreationSecret(t, g, true)
	if _, err := g.create(context.Background(), "older-admin", "older@example.test", "SyntheticPassword123!", true, secret); err != nil {
		t.Fatal(err)
	}
	// Simulate the old setup transaction: account exists, both bootstrap markers
	// remain unconsumed, and the private file was removed separately.
	hash := sha256.Sum256([]byte(secret))
	if _, err := g.root.store.db.Exec(`UPDATE account_bootstrap SET consumed=0; UPDATE owner_auth_settings SET bootstrap_hash=? WHERE id=1`, hash[:]); err != nil {
		t.Fatal(err)
	}
	g = bootstrapRestart(t, g)
	bootstrapState(t, g, 1, 1, false)
	g.config.AccountMaintenance = false
	if w := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"older-admin","password":"SyntheticPassword123!"}`, nil); w.Code != 200 {
		t.Fatal("older account login regressed")
	}
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "replay-admin"), nil); w.Code != 401 {
		t.Fatal("older installation reopened setup")
	}
}

func TestAccountBootstrapMalformedStateFailsClosed(t *testing.T) {
	for name, query := range map[string]string{
		"missing owner state":     `DELETE FROM owner_auth_settings`,
		"malformed hash":          `UPDATE owner_auth_settings SET bootstrap_hash=x'01'`,
		"missing account state":   `DELETE FROM account_bootstrap`,
		"malformed account state": `UPDATE account_bootstrap SET consumed=2`,
	} {
		t.Run(name, func(t *testing.T) {
			g := accountFixture(t)
			secret := accountCreationSecret(t, g, true)
			if _, err := g.root.store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 401 {
				t.Fatal("malformed bootstrap accepted")
			}
			if _, err := initializeOwnerAuth(g.root.store, g.config); err == nil {
				t.Fatal("malformed bootstrap startup accepted")
			}
		})
	}
	for _, staleFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumed NULL without file", true: "consumed NULL with stale file"}[staleFile], func(t *testing.T) {
			g := accountFixture(t)
			secret := accountCreationSecret(t, g, true)
			if _, err := g.root.store.db.Exec(`UPDATE owner_auth_settings SET bootstrap_hash=NULL`); err != nil {
				t.Fatal(err)
			}
			if !staleFile {
				if err := os.Remove(g.auth.bootstrapPath); err != nil {
					t.Fatal(err)
				}
			}
			g = bootstrapRestart(t, g)
			g.config.AccountMaintenance = false
			bootstrapState(t, g, 0, 1, false)
			if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 401 {
				t.Fatal("NULL authority reopened")
			}
			if _, err := os.Stat(g.auth.bootstrapPath); !os.IsNotExist(err) {
				t.Fatal("consumed secret file recreated or retained")
			}
		})
	}
}

func TestAccountBootstrapPendingRestartAndImplicitAuth(t *testing.T) {
	g, err := NewAccountGateway(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	secret := accountCreationSecret(t, g, true)
	g = bootstrapRestart(t, g)
	if accountCreationSecret(t, g, true) != secret {
		t.Fatal("pending bootstrap rotated on restart")
	}
	g.config.AccountMaintenance = false
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 200 {
		t.Fatal("implicit multi-account auth setup failed")
	}
	bootstrapState(t, g, 1, 1, false)
}
