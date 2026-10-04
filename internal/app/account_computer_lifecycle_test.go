package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/google/uuid"
)

type syntheticLifecycleBroker struct {
	mu             sync.Mutex
	states         map[string]accountComputerStatus
	calls          map[string]int
	failDelete     bool
	lostRecreate   bool
	rejectRecreate bool
	onDelete       func()
	wrongIdentity  bool
}

func lifecycleFixture(t *testing.T) (*AccountGateway, Account, Account, *http.Cookie, *http.Cookie, *syntheticLifecycleBroker) {
	t.Helper()
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "delete-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "delete-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	user.MustChangePassword = false
	b := &syntheticLifecycleBroker{states: map[string]accountComputerStatus{}, calls: map[string]int{}}
	for i, a := range []Account{admin, user} {
		b.states[a.ID] = accountComputerStatus{AccountID: a.ID, Generation: uuid.NewString(), State: "active", Supported: true, Slot: i + 1, QuotaBytes: 8 << 30}
	}
	dir, err := os.MkdirTemp("/tmp", "tofi-del-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "broker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Op         string `json:"op"`
			ID         string `json:"account_id"`
			Generation string `json:"generation"`
			Operation  string `json:"operation_id"`
			Quota      int    `json:"quota_gib"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.calls[in.Op]++
		out, ok := b.states[in.ID]
		if !ok {
			w.WriteHeader(409)
			return
		}
		switch in.Op {
		case "computer_status":
		case "delete":
			if in.Generation != out.Generation {
				w.WriteHeader(409)
				return
			}
			if b.onDelete != nil {
				b.onDelete()
			}
			out.OperationID = in.Operation
			out.State = "deleted"
			out.ResourcesReleased = true
			out.Slot = 0
			out.QuotaBytes = 0
			out.Phase = "complete"
			if b.failDelete {
				out.State = "cleanup_failed"
				out.ResourcesReleased = false
				out.Slot = 2
				out.QuotaBytes = 8 << 30
				out.Error = "synthetic stop failure"
				b.states[in.ID] = out
				w.WriteHeader(409)
				return
			}
			b.states[in.ID] = out
		case "recreate":
			if b.rejectRecreate {
				w.WriteHeader(503)
				return
			}
			if out.State == "active" && out.OperationID == in.Operation {
				break
			}
			if out.State != "deleted" || out.Generation != in.Generation {
				w.WriteHeader(409)
				return
			}
			out.Generation = uuid.NewString()
			out.OperationID = in.Operation
			out.State = "active"
			out.ResourcesReleased = false
			out.Slot = 2
			out.QuotaBytes = int64(in.Quota) << 30
			out.Phase = "reserved"
			out.Error = ""
			b.states[in.ID] = out
			if b.lostRecreate {
				w.WriteHeader(503)
				return
			}
		case "ensure":
			if out.State != "active" {
				w.WriteHeader(409)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"account_id": in.ID, "socket": filepath.Join(dir, "sockets", in.ID, "control.sock")})
			return
		default:
			t.Errorf("unexpected broker mutation %s", in.Op)
			w.WriteHeader(400)
			return
		}
		if b.wrongIdentity {
			out.AccountID = admin.ID
		}
		json.NewEncoder(w).Encode(out)
	})}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	g.config.AccountProvisionerSocket = filepath.Join(dir, "broker.sock")
	g.config.AccountComputerSocketRoot = filepath.Join(dir, "sockets")
	return g, admin, user, accountCookie(t, g, admin), accountCookie(t, g, user), b
}

func lifecycleBody(t *testing.T, b *syntheticLifecycleBroker, a Account, op string, quota int) string {
	t.Helper()
	b.mu.Lock()
	status := b.states[a.ID]
	b.mu.Unlock()
	in := map[string]any{"operation_id": op, "expected_generation": status.Generation, "confirm_computer_id": a.ID, "confirm_account_id": a.ID, "confirm_username": a.Username, "acknowledge_data_loss": true}
	if quota != 0 {
		in["quota_gib"] = quota
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAccountComputerDeletionAuthorizationAndConfirmation(t *testing.T) {
	g, admin, user, ac, uc, b := lifecycleFixture(t)
	path := "/api/admin/accounts/" + user.ID + "/computer"
	body := lifecycleBody(t, b, user, uuid.NewString(), 0)
	for _, test := range []struct {
		cookie *http.Cookie
		body   string
		want   int
	}{
		{nil, body, 401}, {uc, body, 403}, {ac, `{}`, 400},
		{ac, strings.Replace(body, `"acknowledge_data_loss":true`, `"acknowledge_data_loss":false`, 1), 400},
		{ac, strings.Replace(body, `"confirm_account_id":"`+user.ID+`"`, `"confirm_account_id":"`+admin.ID+`"`, 1), 400},
		{ac, strings.Replace(body, `"confirm_computer_id":"`+user.ID+`"`, `"confirm_computer_id":"`+admin.ID+`"`, 1), 400},
		{ac, strings.TrimSuffix(body, "}") + `,"path":"/retained"}`, 400},
	} {
		if w := accountRequest(g, "DELETE", path, test.body, test.cookie); w.Code != test.want {
			t.Fatalf("got %d want %d: %s", w.Code, test.want, w.Body.String())
		}
	}
	r := httptest.NewRequest("DELETE", path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(ac)
	r.Header.Set("Origin", "https://foreign.example.test")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("CSRF %d", w.Code)
	}
	if _, err := g.root.store.db.Exec(`UPDATE accounts SET must_change_password=1 WHERE id=?`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 403 {
		t.Fatalf("initial password %d", w.Code)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls["delete"] != 0 {
		t.Fatal("rejected request reached deletion")
	}
}

func TestAccountComputerDeletionRetainsLoginChatsCredentialsAndFencesPolling(t *testing.T) {
	g, _, user, ac, uc, b := lifecycleFixture(t)
	s, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := s.store.CreateBot("retained synthetic bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	attachmentID := uuid.NewString()
	if _, err = s.store.db.Exec(`INSERT INTO attachments VALUES(?,?,?,?,?,?,?)`, attachmentID, bot.DMConversationID, "retained-name.txt", "text/plain", 5, "vm:"+attachmentID, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(g.config.DataDir, "accounts", user.ID, "retained-provider-marker")
	if err = os.WriteFile(marker, []byte("synthetic server credential marker"), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/accounts/" + user.ID + "/computer"
	body := lifecycleBody(t, b, user, uuid.NewString(), 0)
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if !s.closing {
		t.Fatal("workspace background workers not closed")
	}
	if w := accountRequest(g, "GET", "/api/auth/session", "", uc); w.Code != 200 || !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatal("login session removed")
	}
	if w := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"delete-user","password":"SyntheticPassword123!"}`, nil); w.Code != 200 {
		t.Fatalf("login after deletion %d", w.Code)
	}
	for i := 0; i < 4; i++ {
		if w := accountRequest(g, "GET", "/api/computers/firecracker/info", "", uc); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"deleted"`) {
			t.Fatalf("poll %d %s", w.Code, w.Body.String())
		}
	}
	if w := accountRequest(g, "GET", "/api/conversations", "", uc); w.Code != 200 || !strings.Contains(w.Body.String(), bot.DMConversationID) {
		t.Fatalf("history %d %s", w.Code, w.Body.String())
	}
	s, err = g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	if s.scheduler != nil {
		t.Fatal("deleted computer restarted background work")
	}
	a, _, err := s.store.Attachment(attachmentID)
	if err != nil || !a.Unavailable || a.Name != "retained-name.txt" {
		t.Fatalf("attachment metadata %v %+v", err, a)
	}
	if w := accountRequest(g, "GET", "/api/attachments/"+attachmentID, "", uc); w.Code != 410 {
		t.Fatalf("deleted attachment %d %s", w.Code, w.Body.String())
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "synthetic server credential marker" {
		t.Fatal("server credential marker changed")
	}
	if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID+"/quota", `{"quota_gib":16}`, ac); w.Code != 409 {
		t.Fatalf("quota resurrection %d", w.Code)
	}
	if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID, `{"disabled":true}`, ac); w.Code != 200 {
		t.Fatalf("disable %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID, `{"disabled":false}`, ac); w.Code != 200 {
		t.Fatalf("restore %d %s", w.Code, w.Body.String())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls["ensure"] != 0 || b.calls["restore"] != 0 {
		t.Fatalf("implicit restart: %+v", b.calls)
	}
}

func TestAccountComputerPartialFailureRestartSameOperationAndStaleDelete(t *testing.T) {
	g, _, user, ac, uc, b := lifecycleFixture(t)
	path := "/api/admin/accounts/" + user.ID + "/computer"
	operation := uuid.NewString()
	body := lifecycleBody(t, b, user, operation, 0)
	b.failDelete = true
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 503 {
		t.Fatalf("failure %d %s", w.Code, w.Body.String())
	}
	if !g.computerFenced(user.ID) {
		t.Fatal("failed delete unfenced")
	}
	cfg := g.config
	g.Close()
	var err error
	g, err = NewAccountGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if w := accountRequest(g, "GET", path, "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"cleanup_failed"`) || strings.Contains(w.Body.String(), `"resources_released":true`) {
		t.Fatalf("pending status %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "GET", "/api/computers/firecracker/info", "", uc); w.Code != 200 || !strings.Contains(w.Body.String(), "cleanup_failed") {
		t.Fatal("restart lost fence")
	}
	b.failDelete = false
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 200 {
		t.Fatalf("retry %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 200 {
		t.Fatalf("idempotent retry %d %s", w.Code, w.Body.String())
	}
	recreate := lifecycleBody(t, b, user, uuid.NewString(), 16)
	if w := accountRequest(g, "POST", path+"/recreate", recreate, ac); w.Code != 200 {
		t.Fatalf("recreate %d %s", w.Code, w.Body.String())
	}
	if g.computerFenced(user.ID) {
		t.Fatal("verified recreation remained fenced")
	}
	if w := accountRequest(g, "DELETE", path, body, ac); w.Code != 409 {
		t.Fatalf("stale delete %d %s", w.Code, w.Body.String())
	}
	var auditState, actor string
	if g.root.store.db.QueryRow(`SELECT state,actor_id FROM account_computer_audit WHERE operation_id=?`, operation).Scan(&auditState, &actor) != nil || auditState != "deleted" || actor == "" {
		t.Fatal("durable audit missing")
	}
}

func TestAccountComputerLostRecreateResponseRetriesAdmissionAndQuota(t *testing.T) {
	g, _, user, ac, _, b := lifecycleFixture(t)
	path := "/api/admin/accounts/" + user.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := lifecycleBody(t, b, user, uuid.NewString(), 16)
	b.lostRecreate = true
	if w := accountRequest(g, "POST", path+"/recreate", body, ac); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountRequest(g, "GET", path, "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"recreating"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	b.lostRecreate = false
	if w := accountRequest(g, "POST", path+"/recreate", strings.Replace(body, `"quota_gib":16`, `"quota_gib":8`, 1), ac); w.Code != 409 {
		t.Fatal("recreate operation quota changed", w.Code)
	}
	if w := accountRequest(g, "POST", path+"/recreate", body, ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAccountComputerDeletionDrainsActiveRequestsAndAllowsSelfComputer(t *testing.T) {
	g, admin, user, ac, uc, b := lifecycleFixture(t)
	r := httptest.NewRequest("GET", "/api/synthetic-guest-call", nil)
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(uc)
	registered, _, cleanup, ok := g.register(r)
	if !ok {
		t.Fatal("request registration")
	}
	var drained atomic.Bool
	go func() { <-registered.Context().Done(); drained.Store(true); cleanup() }()
	b.onDelete = func() {
		if !drained.Load() {
			t.Error("Worker deletion preceded request drain")
		}
	}
	path := "/api/admin/accounts/" + user.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	b.onDelete = nil
	path = "/api/admin/accounts/" + admin.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, b, admin, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatalf("self computer %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "GET", "/api/admin/accounts", "", ac); w.Code != 200 {
		t.Fatal("self computer deletion lost admin login")
	}
}

type accountComputerPauseKey struct{}

// Pause one authenticated HTTP request immediately before the real gate wait.
// The production handlers, cancellation, drain and Worker transport still run.
type pausedAccountComputerTransition struct {
	accountComputerTransition
	paused chan context.Context
	resume <-chan struct{}
}

func (transition *pausedAccountComputerTransition) acquire(ctx context.Context) error {
	if ctx.Value(accountComputerPauseKey{}) == true {
		transition.paused <- ctx
		if transition.resume == nil {
			<-ctx.Done()
		} else {
			select {
			case <-transition.resume:
			case <-ctx.Done():
			}
		}
	}
	return transition.accountComputerTransition.acquire(ctx)
}

func TestAccountComputerSelfDeletionDrainsCanceledTransitionWaiters(t *testing.T) {
	for _, operation := range []string{"quota", "duplicate_delete"} {
		t.Run(operation, func(t *testing.T) {
			g, admin, _, ac, _, b := lifecycleFixture(t)
			transition := &pausedAccountComputerTransition{accountComputerTransition: accountComputerGate(make(chan struct{}, 1)), paused: make(chan context.Context, 1)}
			g.computerTransitions.Store(admin.ID, transition)
			path := "/api/admin/accounts/" + admin.ID
			body := lifecycleBody(t, b, admin, uuid.NewString(), 0)
			method, waiterPath, waiterBody := "PATCH", path+"/quota", `{"quota_gib":8}`
			if operation == "duplicate_delete" {
				method, waiterPath, waiterBody = "DELETE", path+"/computer", body
			}
			request := httptest.NewRequest(method, waiterPath, strings.NewReader(waiterBody))
			request.TLS = &tls.ConnectionState{}
			request.AddCookie(ac)
			ctx, cancel := context.WithCancel(context.WithValue(request.Context(), accountComputerPauseKey{}, true))
			defer cancel()
			waiterDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, request.WithContext(ctx))
				waiterDone <- w
			}()
			var waiterContext context.Context
			select {
			case waiterContext = <-transition.paused:
			case <-time.After(3 * time.Second):
				t.Fatal("authenticated waiter did not reach pre-acquisition breakpoint")
			}
			b.onDelete = func() {
				if waiterContext.Err() == nil {
					t.Error("Worker delete preceded cancellation")
				}
				g.mu.Lock()
				defer g.mu.Unlock()
				if len(g.active[admin.ID]) != 1 {
					t.Error("Worker delete preceded the canceled waiter's real handler cleanup")
				}
			}
			deleteDone := make(chan *httptest.ResponseRecorder, 1)
			go func() { deleteDone <- accountRequest(g, "DELETE", path+"/computer", body, ac) }()
			select {
			case w := <-waiterDone:
				if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"operation_canceled"`) {
					t.Fatalf("canceled waiter %d %s", w.Code, w.Body.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled transition waiter prevented its own drain")
			}
			select {
			case w := <-deleteDone:
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"deleted"`) {
					t.Fatalf("self deletion %d %s", w.Code, w.Body.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("self deletion waited on a transition waiter instead of reaching Worker")
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.calls["delete"] != 1 || b.calls["quota"] != 0 || !b.states[admin.ID].ResourcesReleased {
				t.Fatalf("unserialized or uncompleted deletion: %+v", b.calls)
			}
		})
	}
}

func TestAccountComputerQuotaRechecksAuthorizationAfterWaiting(t *testing.T) {
	for _, revoked := range []string{"role", "disabled", "initial_password", "session"} {
		t.Run(revoked, func(t *testing.T) {
			g, admin, user, ac, _, b := lifecycleFixture(t)
			resume := make(chan struct{})
			transition := &pausedAccountComputerTransition{accountComputerTransition: accountComputerGate(make(chan struct{}, 1)), paused: make(chan context.Context, 1), resume: resume}
			g.computerTransitions.Store(user.ID, transition)
			request := httptest.NewRequest("PATCH", "/api/admin/accounts/"+user.ID+"/quota", strings.NewReader(`{"quota_gib":16}`))
			request.TLS = &tls.ConnectionState{}
			request.AddCookie(ac)
			ctx, cancel := context.WithCancel(context.WithValue(request.Context(), accountComputerPauseKey{}, true))
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, request.WithContext(ctx))
				done <- w
			}()
			select {
			case <-transition.paused:
			case <-time.After(3 * time.Second):
				t.Fatal("quota did not reach pre-acquisition breakpoint")
			}
			query := map[string]string{
				"role":             `UPDATE accounts SET role='user' WHERE id=?`,
				"disabled":         `UPDATE accounts SET disabled=1 WHERE id=?`,
				"initial_password": `UPDATE accounts SET must_change_password=1 WHERE id=?`,
				"session":          `DELETE FROM account_sessions WHERE account_id=?`,
			}[revoked]
			if _, err := g.root.store.db.Exec(query, admin.ID); err != nil {
				t.Fatal(err)
			}
			close(resume)
			select {
			case w := <-done:
				if w.Code != 403 {
					t.Fatalf("stale authorization admitted: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("quota did not finish after its wait")
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.calls["quota"] != 0 || g.computerFenced(user.ID) {
				t.Fatal("revoked waiter mutated the computer")
			}
		})
	}
}

func TestAccountComputerDeletionDoesNotAcceptWrongBrokerIdentity(t *testing.T) {
	g, _, user, ac, _, b := lifecycleFixture(t)
	b.wrongIdentity = true
	if w := accountRequest(g, "GET", "/api/admin/accounts/"+user.ID+"/computer", "", ac); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+user.ID+"/computer", lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if b.calls["delete"] != 0 {
		t.Fatal("wrong identity reached Worker delete")
	}
}

func TestAccountComputerRecreateAdmissionFailureRetainsOriginalRetry(t *testing.T) {
	g, _, user, ac, _, b := lifecycleFixture(t)
	path := "/api/admin/accounts/" + user.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := lifecycleBody(t, b, user, uuid.NewString(), 16)
	b.rejectRecreate = true
	if w := accountRequest(g, "POST", path+"/recreate", body, ac); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out accountComputerStatus
	w := accountRequest(g, "GET", path, "", ac)
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.State != "recreating" || out.QuotaBytes != 16<<30 || out.ResourcesReleased {
		t.Fatalf("pending recreation %d %s", w.Code, w.Body.String())
	}
	b.rejectRecreate = false
	if w := accountRequest(g, "POST", path+"/recreate", body, ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAccountComputerDisabledTargetDeletionAndLegacyRejection(t *testing.T) {
	g, _, user, ac, _, b := lifecycleFixture(t)
	if _, err := g.root.store.db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/accounts/" + user.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var disabled bool
	if g.root.store.db.QueryRow(`SELECT disabled FROM accounts WHERE id=?`, user.ID).Scan(&disabled) != nil || !disabled {
		t.Fatal("deletion changed disabled account")
	}
	if w := accountRequest(g, "POST", path+"/recreate", lifecycleBody(t, b, user, uuid.NewString(), 8), ac); w.Code != 409 {
		t.Fatal("disabled recreation admitted", w.Code)
	}
	if _, err := g.root.store.db.Exec(`INSERT INTO accounts(id,username,email,role,salt,password_hash,legacy,created_at) SELECT 'legacy-owner','synthetic-legacy','legacy@example.test','admin',salt,password_hash,1,created_at FROM accounts WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if w := accountRequest(g, "GET", "/api/admin/accounts/legacy-owner/computer", "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"supported":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/legacy-owner/computer", `{}`, ac); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAccountComputerUnresolvedResizeDoesNotPersistDeletionIntent(t *testing.T) {
	g, _, user, ac, _, b := lifecycleFixture(t)
	status := b.states[user.ID]
	status.Supported = false
	status.Error = "synthetic unresolved resize"
	b.states[user.ID] = status
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+user.ID+"/computer", lifecycleBody(t, b, user, uuid.NewString(), 0), ac); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if g.computerFenced(user.ID) || b.calls["delete"] != 0 {
		t.Fatal("preflight rejection persisted a destructive intent")
	}
}

// Composition fixtures use the real fixed-socket computer client and lifecycle
// Ensure guard, with an in-memory HTTP transport. No Guest or provider is used.
type computerIntegrationGuest struct {
	mu            sync.Mutex
	data          map[string][]byte
	pauseMethod   string
	pauseAction   bool
	lostPut       bool
	cleanup       bool
	started       chan context.Context
	canceled      chan struct{}
	resume        chan struct{}
	once          sync.Once
	released      atomic.Bool
	lateTransport atomic.Int32
	gets          atomic.Int32
}

func computerIntegrationResponse(status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"X-Tofi-Blob-Durability": {"1"}}, Body: io.NopCloser(bytes.NewReader(data))}
}

func (f *computerIntegrationGuest) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.released.Load() {
		f.lateTransport.Add(1)
		return nil, errors.New("synthetic Guest already deleted")
	}
	if r.URL.Path == "/v1/info" {
		return computerIntegrationResponse(200, []byte(`{"kind":"firecracker","state":"ready"}`)), nil
	}
	blob := strings.HasPrefix(r.URL.Path, "/v1/blobs/")
	if !blob && r.URL.Path != "/v1/action" {
		return nil, errors.New("unexpected synthetic Guest route")
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
	if blob && r.Method == http.MethodPut {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.data[id] = append([]byte(nil), data...)
		f.mu.Unlock()
	}
	if blob && r.Method == http.MethodGet {
		f.gets.Add(1)
	}
	if f.started != nil && ((blob && r.Method == f.pauseMethod) || (!blob && f.pauseAction)) {
		f.once.Do(func() {
			f.started <- r.Context()
			if !f.cleanup {
				<-r.Context().Done()
				close(f.canceled)
			}
			<-f.resume // Actual handler completion stays pending after cancellation.
		})
		if f.cleanup {
			return computerIntegrationResponse(503, nil), nil
		}
		return nil, r.Context().Err()
	}
	if blob && r.Method == http.MethodPut && f.lostPut {
		return computerIntegrationResponse(503, nil), nil
	}
	if blob {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if data, ok := f.data[id]; ok {
				return computerIntegrationResponse(200, data), nil
			}
			return computerIntegrationResponse(404, nil), nil
		case http.MethodDelete:
			delete(f.data, id)
		}
		return computerIntegrationResponse(200, nil), nil
	}
	return computerIntegrationResponse(200, []byte(`{"ok":true,"result":{"exit_code":0}}`)), nil
}

func computerIntegrationWorkspace(t *testing.T, g *AccountGateway, a Account, guest *computerIntegrationGuest) (*Server, Config) {
	t.Helper()
	var captured Config
	g.runtimeFactory = func(c Config) (*Server, error) {
		captured = c
		return NewServer(c)
	}
	s, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	client, err := computer.New(computer.Config{Socket: captured.ComputerSocket, Ensure: captured.ComputerEnsure, Client: &http.Client{Transport: guest}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM, s.store.guestBlobs = client, client
	return s, captured
}

func computerIntegrationFence(t *testing.T, g *AccountGateway, a Account, state string) {
	t.Helper()
	// This mirrors the journal commit lock; no runtime or request registration is
	// replaced. Used to exercise all durable states without physical deletion.
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := g.root.store.db.Exec(`INSERT INTO account_computer_lifecycle VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET state=excluded.state`, a.ID, a.ID, uuid.NewString(), uuid.NewString(), state, a.ID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
}

func computerIntegrationWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(what)
	}
}

func TestAccountComputerIntegrationDrainsWorkspaceIO(t *testing.T) {
	for _, operation := range []string{"export", "import_put", "import_verify", "import_cleanup", "credential", "upload", "download"} {
		t.Run(operation, func(t *testing.T) {
			webhookSyntheticEnvironment(t)
			g, _, user, ac, uc, broker := lifecycleFixture(t)
			guest := &computerIntegrationGuest{data: map[string][]byte{}, started: make(chan context.Context, 1), canceled: make(chan struct{}), resume: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(guest.resume) }) }
			defer release()
			s, _ := computerIntegrationWorkspace(t, g, user, guest)
			bot, err := s.store.CreateBot("Synthetic drain bot", "", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			method, path, body := "POST", "/api/portability/export", `{"kind":"account"}`
			contentType := "application/json"
			if operation == "export" || operation == "download" {
				id := uuid.NewString()
				guest.data[id] = []byte("synthetic drain bytes")
				webhookExec(t, s.store, `INSERT INTO attachments VALUES(?,?,?,?,?,?,?)`, id, bot.DMConversationID, "synthetic-drain.txt", "text/plain", len(guest.data[id]), "vm:"+id, now())
				guest.pauseMethod = "GET"
				if operation == "download" {
					method, path, body = "GET", "/api/attachments/"+id, ""
				}
			} else if strings.HasPrefix(operation, "import_") {
				_, bundle, _ := portableAssetFixture(t)
				raw, _ := json.Marshal(bundle)
				in, _ := json.Marshal(portableImportRequest{Bundle: raw})
				w := accountRequest(g, "POST", "/api/portability/preview", string(in), uc)
				var preview portablePreview
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.ID == "" {
					t.Fatalf("synthetic preview %d", w.Code)
				}
				in, _ = json.Marshal(portableImportRequest{Bundle: raw, PreviewID: preview.ID})
				path, body = "/api/portability/apply", string(in)
				guest.pauseMethod = "PUT"
				if operation == "import_verify" {
					guest.pauseMethod = "GET"
				}
				if operation == "import_cleanup" {
					guest.pauseMethod, guest.cleanup, guest.lostPut = "DELETE", true, true
				}
			} else if operation == "credential" {
				id := portableSyntheticEnvironment(t, s, "SYNTHETIC_DRAIN_TOKEN", "Synthetic disposable value")
				path, body = "/api/computer/credentials/"+id+"/apply", `{"bot_id":"`+bot.ID+`"}`
				guest.pauseAction = true
			} else if operation == "upload" {
				var data bytes.Buffer
				form := multipart.NewWriter(&data)
				part, err := form.CreateFormFile("file", "synthetic-upload.txt")
				if err != nil {
					t.Fatal(err)
				}
				part.Write([]byte("Synthetic upload bytes"))
				form.Close()
				path, body, contentType = "/api/conversations/"+bot.DMConversationID+"/attachments", data.String(), form.FormDataContentType()
				guest.pauseMethod = "PUT"
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.TLS = &tls.ConnectionState{}
			r.AddCookie(uc)
			r.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			handlerDone := make(chan struct{})
			go func() { g.Handler().ServeHTTP(response, r); close(handlerDone) }()
			var paused context.Context
			select {
			case paused = <-guest.started:
			case <-handlerDone:
				t.Fatalf("handler did not reach Guest: %d", response.Code)
			case <-time.After(3 * time.Second):
				t.Fatal("Guest call did not pause")
			}
			var workerReached atomic.Bool
			broker.onDelete = func() {
				g.mu.Lock()
				active := len(g.active[user.ID])
				g.mu.Unlock()
				if active != 0 {
					t.Error("Worker delete preceded real HTTP request completion")
				}
				guest.released.Store(true)
				workerReached.Store(true)
			}
			deleteDone := make(chan *httptest.ResponseRecorder, 1)
			deleteBody := lifecycleBody(t, broker, user, uuid.NewString(), 0)
			go func() {
				deleteDone <- accountRequest(g, "DELETE", "/api/admin/accounts/"+user.ID+"/computer", deleteBody, ac)
			}()
			if !guest.cleanup {
				computerIntegrationWait(t, guest.canceled, "deletion did not cancel Guest request")
				if paused.Err() == nil {
					t.Fatal("Guest transport lost request cancellation")
				}
			} else {
				deadline := time.After(3 * time.Second)
				for !g.computerFenced(user.ID) {
					select {
					case <-deadline:
						t.Fatal("cleanup deletion intent did not persist")
					case <-time.After(time.Millisecond):
					}
				}
				if paused.Err() != nil {
					t.Fatal("bounded cleanup lost its independent completion context")
				}
			}
			if workerReached.Load() {
				t.Fatal("Worker delete skipped paused handler/cleanup drain")
			}
			if w := accountRequest(g, "POST", "/api/portability/export", `{}`, uc); w.Code != 423 {
				t.Fatal("new export passed persisted fence", w.Code)
			}
			release()
			computerIntegrationWait(t, handlerDone, "canceled handler did not finish")
			select {
			case w := <-deleteDone:
				if w.Code != 200 || !workerReached.Load() || (operation != "export" && response.Code < 400) {
					t.Fatalf("delete/drain result %d/%d", w.Code, response.Code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("deletion did not finish after handler exit")
			}
			if guest.lateTransport.Load() != 0 {
				t.Fatal("Guest transport ran after verified deletion")
			}
			if operation == "export" && response.Code == 200 {
				// Existing exports may return an honest missing-asset bundle after
				// a canceled read; they must contain no bytes from that Guest call.
				var canceled portableBundle
				if json.Unmarshal(response.Body.Bytes(), &canceled) != nil || len(canceled.Attachments) != 0 || len(canceled.MissingAttachments) != 1 || canceled.MissingAttachments[0].Reason != "missing" {
					t.Fatal("canceled export returned Guest bytes or lost omission")
				}
			}
			if strings.HasPrefix(operation, "import_") {
				st, err := openStoreWithLimit(filepath.Join(g.config.DataDir, "accounts", user.ID), false, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				var published, staged int
				if st.db.QueryRow(`SELECT COUNT(*) FROM attachments`).Scan(&published) != nil || st.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&staged) != nil || published != 0 || staged == 0 {
					t.Fatal("canceled import published metadata or lost unresolved durable staging journal")
				}
			}
		})
	}
}

func TestAccountComputerIntegrationPortableUnavailable(t *testing.T) {
	webhookSyntheticEnvironment(t)
	g, _, user, ac, uc, broker := lifecycleFixture(t)
	guest := &computerIntegrationGuest{data: map[string][]byte{}}
	s, _ := computerIntegrationWorkspace(t, g, user, guest)
	bot, err := s.store.CreateBot("Synthetic deleted attachment bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := s.store.AddMessage(bot.DMConversationID, "user", "", "", "Synthetic retained attachment binding", "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.store.AddAttachment(bot.DMConversationID, "synthetic-retained.txt", "text/plain", strings.NewReader("Synthetic original bytes"))
	if err != nil || s.store.BindAttachments(bot.DMConversationID, message.ID, []string{old.ID}) != nil {
		t.Fatal("synthetic attachment setup", err)
	}
	path := "/api/admin/accounts/" + user.ID + "/computer"
	if w := accountRequest(g, "DELETE", path, lifecycleBody(t, broker, user, uuid.NewString(), 0), ac); w.Code != 200 {
		t.Fatal("synthetic deletion", w.Code)
	}
	if w := accountRequest(g, "POST", path+"/recreate", lifecycleBody(t, broker, user, uuid.NewString(), 8), ac); w.Code != 200 {
		t.Fatal("synthetic recreation", w.Code)
	}
	// Deliberately plant bytes at the deleted alias. The permanent marker wins.
	guest.data[old.ID] = []byte("Synthetic replacement alias bytes")
	guest.gets.Store(0)
	s, _ = computerIntegrationWorkspace(t, g, user, guest)
	w := accountRequest(g, "POST", "/api/portability/export", `{"kind":"bot","selection":{"categories":["bot_config","chats","attachments"],"bot_ids":["`+bot.ID+`"]}}`, uc)
	var exported portableBundle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &exported) != nil || exported.validate() != nil || guest.gets.Load() != 0 {
		t.Fatal("marked alias was read or export invalid", w.Code)
	}
	if len(exported.Attachments) != 0 || len(exported.MissingAttachments) != 1 || exported.AttachmentCount != 1 {
		t.Fatal("permanent omission count/bytes changed")
	}
	missing := exported.MissingAttachments[0]
	if missing.ID != old.ID || missing.Name != old.Name || missing.Reason != "unavailable" || missing.ConversationID != bot.DMConversationID || missing.Origin.RecordID != old.ID || len(missing.MessageIDs) != 1 || missing.MessageIDs[0] != message.ID {
		t.Fatal("permanent omission lost name/origin/message binding")
	}
	if w := accountRequest(g, "GET", "/api/attachments/"+old.ID, "", uc); w.Code != 410 || guest.gets.Load() != 0 {
		t.Fatal("download revived deleted alias")
	}
	destination, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	destination.guestBlobs = newPortableFixtureBlobs()
	_, result := portableCompositionApply(t, destination, exported)
	round, err := destination.exportPortable(context.Background(), "synthetic-missing-roundtrip", portableSelection{}, "account")
	if err != nil || len(round.MissingAttachments) != 1 || round.MissingAttachments[0].Reason != "unavailable" || round.MissingAttachments[0].Origin.RecordID != old.ID || len(round.MissingAttachments[0].MessageIDs) != 1 || round.MissingAttachments[0].MessageIDs[0] != result.IDMap[message.ID] {
		t.Fatal("missing metadata import/export revived bytes or lost binding", err)
	}
	_, freshBundle, _ := portableAssetFixture(t)
	raw, _ := json.Marshal(freshBundle)
	in, _ := json.Marshal(portableImportRequest{Bundle: raw})
	w = accountRequest(g, "POST", "/api/portability/preview", string(in), uc)
	var preview portablePreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil {
		t.Fatal("fresh import preview", w.Code)
	}
	in, _ = json.Marshal(portableImportRequest{Bundle: raw, PreviewID: preview.ID})
	w = accountRequest(g, "POST", "/api/portability/apply", string(in), uc)
	var applied portableResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &applied) != nil {
		t.Fatal("fresh import after recreation", w.Code)
	}
	for _, asset := range freshBundle.Attachments {
		if applied.IDMap[asset.ID] == "" || applied.IDMap[asset.ID] == asset.ID || applied.IDMap[asset.ID] == old.ID {
			t.Fatal("new imported asset reused a deleted or source identity")
		}
	}
	guest.gets.Store(0)
	fresh, err := s.store.exportPortable(context.Background(), s.instance.ID, portableSelection{}, "account")
	if err != nil || len(fresh.Attachments) != 2 || len(fresh.MissingAttachments) != 1 || guest.gets.Load() != 2 {
		t.Fatal("fresh IDs failed export or old alias was read", err)
	}
	// Marker and metadata are copied in one snapshot; I/O uses that immutable
	// selection after the transaction releases SQLite, independent of later edits.
	tx, err := s.store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotPortableAttachments(context.Background(), tx, &fresh, map[string]portableOrigin{})
	if err != nil || tx.Commit() != nil {
		t.Fatal("attachment snapshot", err)
	}
	webhookExec(t, s.store, `UPDATE attachments SET name='synthetic-later-name.txt' WHERE id=?`, old.ID)
	fresh.Attachments, fresh.MissingAttachments, fresh.AttachmentBindings = nil, nil, nil
	guest.gets.Store(0)
	if err := s.store.exportPortableAttachmentBytes(context.Background(), &fresh, snapshot); err != nil || len(fresh.MissingAttachments) != 1 || fresh.MissingAttachments[0].Name != old.Name || guest.gets.Load() != 2 {
		t.Fatal("snapshot metadata/marker was reread or old alias transported", err)
	}
}

func computerIntegrationHook(t *testing.T, g *AccountGateway, a Account, cookie *http.Cookie) (*Server, webhookMetadata) {
	t.Helper()
	s, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := s.store.CreateBot("Synthetic lifecycle webhook bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	m := webhookMetadataResponse(t, webhookHTTP("POST", "/api/conversations/"+bot.DMConversationID+"/webhook", `{}`, "", cookie, g.Handler(), nil), 201)
	return s, m
}

func TestAccountComputerIntegrationWebhookFence(t *testing.T) {
	for _, state := range []string{"deleting", "cleanup_failed", "deleted", "recreating"} {
		t.Run(state, func(t *testing.T) {
			webhookSyntheticEnvironment(t)
			g, admin, user, ac, uc, _ := lifecycleFixture(t)
			g.root.publicOrigin = "https://synthetic.example.invalid"
			s, hook := computerIntegrationHook(t, g, user, uc)
			other, otherHook := computerIntegrationHook(t, g, admin, ac)
			computerIntegrationFence(t, g, user, state)
			g.mu.Lock()
			delete(g.workspaces, user.ID)
			g.mu.Unlock()
			s.Close()
			s, err := g.workspace(user)
			if err != nil || s.scheduler != nil {
				t.Fatal("metadata runtime reopen", err)
			}
			for _, table := range []string{"webhook_deliveries", "messages", "runs"} {
				if webhookCount(t, s.store, table) != 0 {
					t.Fatal("unexpected initial synthetic ingress rows")
				}
			}
			w := webhookHTTP("POST", "/api/webhooks/"+hook.HookID, `{"event_id":"fenced","content":"Synthetic fenced event"}`, hook.Secret, nil, g.Handler(), nil)
			if w.Code != 401 || strings.Contains(w.Body.String(), state) {
				t.Fatal("fenced ingress was admitted or exposed lifecycle", w.Code)
			}
			for _, table := range []string{"webhook_deliveries", "messages", "runs"} {
				if webhookCount(t, s.store, table) != 0 {
					t.Fatal("fenced ingress persisted", table)
				}
			}
			// Unrelated active account uses only a fixed synthetic engine.
			other.mu.Lock()
			other.engine, other.codexManaged = testEngine{}, false
			other.mu.Unlock()
			w = webhookHTTP("POST", "/api/webhooks/"+otherHook.HookID, `{"event_id":"active","content":"Synthetic unrelated event"}`, otherHook.Secret, nil, g.Handler(), nil)
			if w.Code != 202 || webhookCount(t, other.store, "webhook_deliveries") != 1 || webhookCount(t, s.store, "webhook_deliveries") != 0 {
				t.Fatal("active sibling admission changed", w.Code)
			}
		})
	}
	t.Run("fence_wins_final_admission", func(t *testing.T) {
		webhookSyntheticEnvironment(t)
		g, _, user, _, uc, _ := lifecycleFixture(t)
		g.root.publicOrigin = "https://synthetic.example.invalid"
		s, hook := computerIntegrationHook(t, g, user, uc)
		s.mu.Lock() // The existing readiness lookup sits between the two g.mu locks.
		var unlockOnce sync.Once
		unlock := func() { unlockOnce.Do(s.mu.Unlock) }
		defer unlock()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- webhookHTTP("POST", "/api/webhooks/"+hook.HookID, `{"event_id":"race","content":"Synthetic final admission race"}`, hook.Secret, nil, g.Handler(), nil)
		}()
		// Observe the real readiness barrier instead of relying on a sleep or
		// adding a test hook to public ingress. No values/credentials are logged.
		deadline := time.After(2 * time.Second)
		for {
			stack := make([]byte, 1<<20)
			stack = stack[:runtime.Stack(stack, true)]
			if bytes.Contains(stack, []byte("(*Server).modelConfigured")) {
				break
			}
			select {
			case <-deadline:
				t.Fatal("ingress did not reach readiness barrier")
			case <-time.After(time.Millisecond):
			}
		}
		computerIntegrationFence(t, g, user, "deleting")
		unlock()
		select {
		case w := <-done:
			if w.Code != 401 {
				t.Fatal("final lock missed committed fence", w.Code)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("final admission did not finish")
		}
		for _, table := range []string{"webhook_deliveries", "messages", "runs"} {
			if webhookCount(t, s.store, table) != 0 {
				t.Fatal("post-fence work committed", table)
			}
		}
	})
	t.Run("admission_wins_old_runtime_cannot_execute", func(t *testing.T) {
		webhookSyntheticEnvironment(t)
		g, _, user, ac, uc, broker := lifecycleFixture(t)
		g.root.publicOrigin = "https://synthetic.example.invalid"
		s, hook := computerIntegrationHook(t, g, user, uc)
		e, err := g.root.store.webhookByHook(context.Background(), hook.HookID)
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.store.GetConversation(e.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		var executions atomic.Int32
		s.mu.Lock()
		s.engine, s.codexManaged = computerIntegrationEngine{calls: &executions}, false
		s.mu.Unlock()
		// Force the opposite ordering using the same final lock and real Store
		// admission, then exercise the old-runtime enqueue after actual Close.
		g.mu.Lock()
		_, run, err := s.store.AdmitWebhook(context.Background(), e, webhookEnvelope{EventID: "before-delete", Content: "Synthetic admitted before fence"}, true)
		g.mu.Unlock()
		if err != nil || run.Status != "queued" {
			t.Fatal("pre-fence admission", err)
		}
		if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+user.ID+"/computer", lifecycleBody(t, broker, user, uuid.NewString(), 0), ac); w.Code != 200 {
			t.Fatal("synthetic delete", w.Code)
		}
		s.enqueue(c, run)
		s.mu.Lock()
		closing, workers := s.closing, len(s.queues)
		s.mu.Unlock()
		if !closing || workers != 0 || executions.Load() != 0 {
			t.Fatal("closed runtime executed pre-fence queue")
		}
		if w := accountRequest(g, "POST", "/api/admin/accounts/"+user.ID+"/computer/recreate", lifecycleBody(t, broker, user, uuid.NewString(), 8), ac); w.Code != 200 {
			t.Fatal("synthetic recreate", w.Code)
		}
		s, err = g.workspace(user)
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.engine, s.codexManaged = testEngine{}, false
		s.mu.Unlock()
		w := webhookHTTP("POST", "/api/webhooks/"+hook.HookID, `{"event_id":"after-recreate","content":"Synthetic explicitly recreated admission"}`, hook.Secret, nil, g.Handler(), nil)
		if w.Code != 202 {
			t.Fatal("verified recreation did not restore admission", w.Code)
		}
	})
}

type computerIntegrationEngine struct{ calls *atomic.Int32 }

func (e computerIntegrationEngine) Run(context.Context, Request) (Result, error) {
	e.calls.Add(1)
	return Result{}, errors.New("unexpected synthetic model execution")
}

func TestAccountComputerIntegrationBootstrapAndRuntimeBinding(t *testing.T) {
	for _, state := range []string{"deleting", "cleanup_failed", "deleted", "recreating"} {
		t.Run(state, func(t *testing.T) {
			webhookSyntheticEnvironment(t)
			g, admin, user, ac, uc, broker := lifecycleFixture(t)
			computerIntegrationFence(t, g, user, state)
			guest := &computerIntegrationGuest{data: map[string][]byte{}}
			s, captured := computerIntegrationWorkspace(t, g, user, guest)
			if captured.AccountID != user.ID || s.reviewAccountID() != user.ID || !captured.IsolatedWorkspace || !captured.AccountControlPlane || captured.ComputerSocket != filepath.Join(g.config.AccountComputerSocketRoot, user.ID, "control.sock") {
				t.Fatal("metadata runtime lost retained identity, isolation or socket binding")
			}
			if captured.ComputerEnsure == nil || captured.ComputerEnsure(context.Background()) == nil || s.scheduler != nil {
				t.Fatal("metadata runtime restored computer/startup authority")
			}
			settings, err := s.store.getAutoReviewSettings()
			if err != nil || settings.Mode != "off" || len(s.autoReviewPolicies) != 0 || s.autoReviewProvider != nil {
				t.Fatal("metadata runtime enabled automatic review")
			}
			for _, call := range []struct{ method, path string }{
				{"POST", "/api/portability/export"}, {"POST", "/api/portability/preview"}, {"POST", "/api/portability/apply"},
				{"POST", "/api/computer/credentials/synthetic/apply"}, {"POST", "/api/computer/files/write"}, {"POST", "/api/conversations/synthetic/attachments"},
			} {
				if w := accountRequest(g, call.method, call.path, `{}`, uc); w.Code != 423 {
					t.Fatal("fenced write/export/install passed", call.path, w.Code)
				}
			}
			// Direct use of the real computer client also cannot reach transport.
			if _, err := s.microVM.GetBlob(context.Background(), uuid.NewString()); err == nil {
				t.Fatal("metadata client read passed Ensure")
			}
			if err := s.microVM.PutBlob(context.Background(), uuid.NewString(), []byte("Synthetic fenced write")); err == nil {
				t.Fatal("metadata client write passed Ensure")
			}
			if _, err := s.microVM.Action(context.Background(), computer.Action{Name: "shell.exec"}); err == nil {
				t.Fatal("metadata credential/action passed Ensure")
			}
			for _, path := range []string{"/api/auth/session", "/api/conversations", "/api/computers/firecracker/info"} {
				if w := accountRequest(g, "GET", path, "", uc); w.Code != 200 {
					t.Fatal("retained login/history/status unavailable", path, w.Code)
				}
			}
			if w := accountRequest(g, "POST", "/api/portability/export", `{"kind":"account"}`, ac); w.Code != 200 {
				t.Fatal("unrelated account export fenced", w.Code)
			}
			if w := accountRequest(g, "GET", "/api/admin/accounts", "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), admin.ID) || !strings.Contains(w.Body.String(), user.ID) {
				t.Fatal("retained account identity lost")
			}
			broker.mu.Lock()
			ensures, reserves := broker.calls["ensure"], broker.calls["reserve"]
			broker.mu.Unlock()
			if ensures != 0 || reserves != 0 || guest.gets.Load() != 0 {
				t.Fatal("fenced runtime implicitly ensured/reserved/read Guest")
			}
		})
	}
	t.Run("bootstrap_consumed_replay_after_delete_recreate_restart", func(t *testing.T) {
		webhookSyntheticEnvironment(t)
		g, admin, user, ac, _, broker := lifecycleFixture(t)
		path := "/api/admin/accounts/" + user.ID + "/computer"
		if w := accountRequest(g, "DELETE", path, lifecycleBody(t, broker, user, uuid.NewString(), 0), ac); w.Code != 200 {
			t.Fatal("synthetic delete", w.Code)
		}
		if w := accountRequest(g, "POST", path+"/recreate", lifecycleBody(t, broker, user, uuid.NewString(), 8), ac); w.Code != 200 {
			t.Fatal("synthetic recreation", w.Code)
		}
		var consumed int
		if err := g.root.store.db.QueryRow(`SELECT consumed FROM account_bootstrap WHERE id=1`).Scan(&consumed); err != nil || consumed != 1 {
			t.Fatal("bootstrap authority was revived")
		}
		if _, err := os.Stat(g.auth.bootstrapPath); !os.IsNotExist(err) {
			t.Fatal("consumed bootstrap file revived")
		}
		cfg := g.config
		g.Close()
		reopened, err := NewAccountGateway(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if w := accountRequest(reopened, "GET", "/api/auth/session", "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), admin.ID) || !strings.Contains(w.Body.String(), `"setup_required":false`) {
			t.Fatal("initialized session/setup closure lost on restart", w.Code)
		}
		if w := accountRequest(reopened, "POST", "/api/auth/login", `{"identifier":"delete-admin","password":"SyntheticPassword123!"}`, nil); w.Code != 200 {
			t.Fatal("retained Admin login failed", w.Code)
		}
		if w := accountRequest(reopened, "POST", "/api/auth/setup", `{"username":"synthetic-replay","password":"SyntheticPassword123!","bootstrap_secret":"Synthetic consumed replay"}`, nil); w.Code != 401 {
			t.Fatal("consumed setup accepted replay", w.Code)
		}
	})
}
