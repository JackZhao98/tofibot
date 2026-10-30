package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	_ "modernc.org/sqlite"
)

// fakeComputer is a Provider with no network. An action named in blockOn waits
// until its context ends or release is closed; the rest answer at once.
type fakeComputer struct {
	id      string
	kind    string
	blockOn map[string]bool
	release chan struct{}

	mu       sync.Mutex
	seen     time.Time
	calls    atomic.Int32
	inFlight atomic.Int32
	order    []string // BotID of every call, in the order the computer received them
}

func newFakeComputer(id string) *fakeComputer {
	return &fakeComputer{id: id, kind: "fake", blockOn: map[string]bool{}, release: make(chan struct{}), seen: time.Now()}
}

func (f *fakeComputer) ID() string   { return f.id }
func (f *fakeComputer) Kind() string { return f.kind }
func (f *fakeComputer) Info(context.Context) (computer.Info, error) {
	return computer.Info{Kind: f.kind, State: "ready"}, nil
}
func (f *fakeComputer) Action(ctx context.Context, a computer.Action) (computer.ActionResult, error) {
	return f.ActionWithin(ctx, a, 0)
}
func (f *fakeComputer) ActionWithin(ctx context.Context, a computer.Action, _ time.Duration) (computer.ActionResult, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.order = append(f.order, a.BotID)
	f.mu.Unlock()
	if f.blockOn[a.Name] {
		f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		select {
		case <-ctx.Done():
			return computer.ActionResult{}, ctx.Err()
		case <-f.release:
		}
	}
	return computer.ActionResult{OK: true, Result: json.RawMessage(`{"ran_on":"` + f.id + `"}`)}, nil
}
func (f *fakeComputer) Retry(context.Context) error { return nil }
func (f *fakeComputer) Purge(context.Context) (computer.Info, error) {
	return computer.Info{}, nil
}
func (f *fakeComputer) LastSeen() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen
}
func (f *fakeComputer) setSeen(t time.Time) {
	f.mu.Lock()
	f.seen = t
	f.mu.Unlock()
}

func newRegistryServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func shellArgs() json.RawMessage { return json.RawMessage(`{"command":"true"}`) }

func toolCode(err error) string {
	if o, ok := tooloutcome.FromError(err); ok {
		return o.Code
	}
	return ""
}

// The provider interface is what routes use: a VM whose id is not "firecracker"
// is served under its own id, and the old id answers "not configured".
func TestRoutesFollowTheProviderID(t *testing.T) {
	s := newRegistryServer(t)
	client, err := computer.New(computer.Config{ID: "vm-7", Socket: "/tmp/tofi-provider-id.sock", Client: &http.Client{Transport: reviewTransport(func(*http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteString(`{"kind":"firecracker","state":"ready"}`)
		return rec.Result(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	get := func(path string) int {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://tofi.local"+path, nil))
		return rec.Code
	}
	if code := get("/api/computers/vm-7/info"); code != 200 {
		t.Fatalf("own id: %d", code)
	}
	if code := get("/api/computers/firecracker/info"); code == 200 {
		t.Fatal("the fixed id still answers although the provider has another id")
	}
	var _ computer.Provider = client
}

// The computers table no longer pins platform to darwin or kind to mac, and an
// old table with the CHECKs is rebuilt without losing a paired computer.
func TestComputersTableRebuildKeepsPairedRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE runs(id TEXT PRIMARY KEY);
CREATE TABLE computers(
 id TEXT PRIMARY KEY, name TEXT NOT NULL, platform TEXT NOT NULL CHECK(platform='darwin'),
 kind TEXT NOT NULL DEFAULT 'mac' CHECK(kind='mac'), token_hash BLOB NOT NULL,
 capabilities TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, revoked_at TEXT
);
INSERT INTO computers(id,name,platform,kind,token_hash,capabilities,last_seen,created_at) VALUES('old-mac','Studio','darwin','mac',x'0102','["files.list"]','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if err = migrateComputerRows(db); err != nil {
			t.Fatal(err)
		}
	}
	var kind, status, labels string
	var protocol int
	if err = db.QueryRow(`SELECT kind,status,labels,protocol_version FROM computers WHERE id='old-mac'`).Scan(&kind, &status, &labels, &protocol); err != nil {
		t.Fatal(err)
	}
	if kind != "personal" || status != "attached" || labels != "{}" || protocol != 0 {
		t.Fatalf("migrated row: %s %s %s %d", kind, status, labels, protocol)
	}
	if _, err = db.Exec(`INSERT INTO computers(id,name,platform,kind,token_hash,capabilities,last_seen,created_at) VALUES('linux-box','Box','linux','vm',x'','[]','t','t')`); err != nil {
		t.Fatalf("a linux vm row is refused: %v", err)
	}
	if _, err = db.Exec(`SELECT run_id,computer_id,region,compute_ms FROM run_computers LIMIT 1`); err != nil {
		t.Fatalf("run_computers: %v", err)
	}
}

func TestPersonalComputerLimit(t *testing.T) {
	s := newRegistryServer(t)
	s.store.maxPersonalComputers = 2
	pair := func() error {
		code, _, err := s.store.createComputerPairing()
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.store.pairComputer(code, "Laptop", "linux", []string{"files.list"})
		return err
	}
	for i := 0; i < 2; i++ {
		if err := pair(); err != nil {
			t.Fatalf("pairing %d: %v", i, err)
		}
	}
	if err := pair(); err != errTooManyComputers {
		t.Fatalf("third pairing: %v", err)
	}
	if n, _ := s.store.countPersonalComputers(); n != 2 {
		t.Fatalf("rows=%d", n)
	}
}

// One lock per computer: a second Bot waits on the computer the first one is
// operating, and uses a different computer at the same time. The lock is a lease
// per call: it frees when the call ends, not when the run does.
func TestOneLockPerComputer(t *testing.T) {
	s := newRegistryServer(t)
	a, b := newFakeComputer("mac-a"), newFakeComputer("mac-b")
	a.blockOn["shell.exec"] = true
	for _, c := range []*fakeComputer{a, b} {
		if err := s.RegisterComputer(c, c.id, ""); err != nil {
			t.Fatal(err)
		}
	}
	run1, run2 := Run{ID: "run-1", BotID: "bot-1"}, Run{ID: "run-2", BotID: "bot-2"}
	first := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), run1, "mac-a", "shell.exec", shellArgs())
		first <- err
	}()
	for a.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	// Another Bot, same computer: refused while the first Bot's call is running.
	short, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	_, err := s.computerActionOn(short, run2, "mac-a", "files.list", shellArgs())
	cancel()
	if toolCode(err) != codeComputerBusy {
		t.Fatalf("second Bot on the same computer: %v", err)
	}
	if a.calls.Load() != 1 {
		t.Fatalf("the refused call reached the computer: %d", a.calls.Load())
	}
	// Another Bot, another computer: runs at once, while bot-1 is still on A.
	if out, err := s.computerActionOn(context.Background(), run2, "mac-b", "shell.exec", shellArgs()); err != nil || !strings.Contains(out, "mac-b") {
		t.Fatalf("second Bot on another computer: %q %v", out, err)
	}
	// The first call ends. The run goes on, but the computer is free for the other Bot.
	close(a.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if slot := s.slotFor("mac-a"); slot.holder() != "" {
		t.Fatalf("the lock outlived the call: held by %s", slot.holder())
	}
	if _, err := s.computerActionOn(context.Background(), run2, "mac-a", "files.list", shellArgs()); err != nil {
		t.Fatalf("after the holder's call ended: %v", err)
	}
	if _, err := s.computerActionOn(context.Background(), run1, "no-such", "shell.exec", shellArgs()); toolCode(err) != codeComputerUnknown {
		t.Fatalf("unknown computer: %v", err)
	}
}

func queued(slot *computerSlot) int {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return len(slot.waiters)
}

// Bots alternate: a Bot that finishes a call and calls again queues behind the
// ones already waiting, in arrival order, and never holds the computer between calls.
func TestComputerLeaseIsFairAcrossBots(t *testing.T) {
	s := newRegistryServer(t)
	mac := newFakeComputer("mac-a")
	mac.blockOn["shell.exec"] = true
	if err := s.RegisterComputer(mac, "Mac", ""); err != nil {
		t.Fatal(err)
	}
	slot := s.slotFor("mac-a")
	runs := []Run{{ID: "run-1", BotID: "bot-1"}, {ID: "run-2", BotID: "bot-2"}, {ID: "run-3", BotID: "bot-3"}}
	var wg sync.WaitGroup
	call := func(r Run, action string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.computerActionOn(context.Background(), r, "mac-a", action, shellArgs()); err != nil {
				t.Errorf("%s %s: %v", r.BotID, action, err)
			}
		}()
	}
	call(runs[0], "shell.exec") // holds the computer, blocked in the call
	for mac.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	call(runs[1], "files.list")
	for queued(slot) != 1 {
		time.Sleep(2 * time.Millisecond)
	}
	call(runs[2], "files.list")
	for queued(slot) != 2 {
		time.Sleep(2 * time.Millisecond)
	}
	close(mac.release) // bot-1's call ends
	wg.Wait()
	// bot-1 asks again straight away, after the other two have had their turn.
	if _, err := s.computerActionOn(context.Background(), runs[0], "mac-a", "files.list", shellArgs()); err != nil {
		t.Fatal(err)
	}
	mac.mu.Lock()
	order := strings.Join(mac.order, ",")
	mac.mu.Unlock()
	if order != "bot-1,bot-2,bot-3,bot-1" {
		t.Fatalf("calls reached the computer in order %s", order)
	}
	if slot.holder() != "" || queued(slot) != 0 {
		t.Fatalf("lock leaked: holder=%q queued=%d", slot.holder(), queued(slot))
	}
}

// A Bot that gives up waiting leaves the queue and the lock still passes on.
func TestComputerLeaseSkipsAWaiterThatGaveUp(t *testing.T) {
	s := newRegistryServer(t)
	mac := newFakeComputer("mac-a")
	mac.blockOn["shell.exec"] = true
	if err := s.RegisterComputer(mac, "Mac", ""); err != nil {
		t.Fatal(err)
	}
	slot := s.slotFor("mac-a")
	done := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), Run{ID: "run-1", BotID: "bot-1"}, "mac-a", "shell.exec", shellArgs())
		done <- err
	}()
	for mac.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	quitter, cancel := context.WithCancel(context.Background())
	gone := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(quitter, Run{ID: "run-2", BotID: "bot-2"}, "mac-a", "files.list", shellArgs())
		gone <- err
	}()
	for queued(slot) != 1 {
		time.Sleep(2 * time.Millisecond)
	}
	late := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), Run{ID: "run-3", BotID: "bot-3"}, "mac-a", "files.list", shellArgs())
		late <- err
	}()
	for queued(slot) != 2 {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if err := <-gone; toolCode(err) != codeComputerBusy {
		t.Fatalf("the Bot that gave up: %v", err)
	}
	close(mac.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-late; err != nil {
		t.Fatalf("the next Bot in line was not served: %v", err)
	}
	if slot.holder() != "" || queued(slot) != 0 {
		t.Fatalf("lock leaked: holder=%q queued=%d", slot.holder(), queued(slot))
	}
}

// Parallel calls of one run share the lease; it frees when the last one ends.
func TestComputerLeaseIsSharedByParallelCallsOfOneRun(t *testing.T) {
	s := newRegistryServer(t)
	mac := newFakeComputer("mac-a")
	mac.blockOn["shell.exec"] = true
	if err := s.RegisterComputer(mac, "Mac", ""); err != nil {
		t.Fatal(err)
	}
	slot := s.slotFor("mac-a")
	run := Run{ID: "run-1", BotID: "bot-1"}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.computerActionOn(context.Background(), run, "mac-a", "shell.exec", shellArgs()); err != nil {
				t.Error(err)
			}
		}()
	}
	for mac.inFlight.Load() != 2 {
		time.Sleep(2 * time.Millisecond)
	}
	if slot.holder() != "bot-1" {
		t.Fatalf("holder %q", slot.holder())
	}
	close(mac.release)
	wg.Wait()
	if slot.holder() != "" {
		t.Fatalf("lock held after both calls ended: %q", slot.holder())
	}
}

// An unclean loss: no heartbeat in time. The lock is released, a call in flight
// ends with an ordinary tool error, and the computer comes back when it answers.
func TestUncleanLossReleasesTheLock(t *testing.T) {
	s := newRegistryServer(t)
	old := computerLostAfter
	computerLostAfter = time.Minute
	t.Cleanup(func() { computerLostAfter = old })
	mac := newFakeComputer("mac-a")
	mac.blockOn["shell.exec"] = true
	if err := s.RegisterComputer(mac, "Mac", ""); err != nil {
		t.Fatal(err)
	}
	holder := Run{ID: "run-1", BotID: "bot-1"}
	result := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), holder, "mac-a", "shell.exec", shellArgs())
		result <- err
	}()
	for mac.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	mac.setSeen(time.Now().Add(-2 * time.Minute))
	s.sweepComputerHeartbeats(time.Now())
	select {
	case err := <-result:
		if toolCode(err) != codeComputerLost {
			t.Fatalf("in-flight call ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call in flight hung after the computer was lost")
	}
	// New calls fail fast and the lock is free.
	if _, err := s.computerActionOn(context.Background(), Run{ID: "run-2", BotID: "bot-2"}, "mac-a", "files.list", shellArgs()); toolCode(err) != codeComputerOffline {
		t.Fatalf("call on a lost computer: %v", err)
	}
	if slot := s.slotFor("mac-a"); slot.holder() != "" {
		t.Fatalf("lock still held by %s", slot.holder())
	}
	// The Bot is told, in run context, that it is offline.
	if line := s.computersRunContext(context.Background()); !strings.Contains(line, "offline") {
		t.Fatalf("run context: %q", line)
	}
	mac.blockOn = map[string]bool{}
	mac.setSeen(time.Now())
	s.sweepComputerHeartbeats(time.Now())
	if _, err := s.computerActionOn(context.Background(), Run{ID: "run-2", BotID: "bot-2"}, "mac-a", "files.list", shellArgs()); err != nil {
		t.Fatalf("after the computer answered again: %v", err)
	}
}

// Detach without a restart: tools and capabilities follow, the row is stored,
// and a restart keeps the VM detached until an administrator attaches it.
func TestHotAttachWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	vm := newFakeComputer("firecracker")
	factory := func() (computer.Provider, error) { return vm, nil }
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s.computerFactory = factory
	if s.computerCapabilities().Available {
		t.Fatal("a server with no socket starts with a computer")
	}
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "hello", "hot-attach")
	if err != nil {
		t.Fatal(err)
	}
	toolNames := func() map[string]bool {
		names := map[string]bool{}
		for _, tool := range s.tools(conv, run) {
			names[tool.Name] = true
		}
		return names
	}
	if toolNames()["computer_shell"] || toolNames()["publish_file"] {
		t.Fatal("computer tools are offered with no computer")
	}
	if err = s.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.computerCapabilities().Available || !toolNames()["computer_shell"] || !toolNames()["publish_file"] {
		t.Fatal("tools did not follow the attach")
	}
	if err = s.AttachVM(context.Background()); err != errComputerAttached {
		t.Fatalf("second attach: %v", err)
	}
	var status string
	if s.store.db.QueryRow(`SELECT status FROM computers WHERE kind='vm'`).Scan(&status) != nil || status != computerRowAttached {
		t.Fatalf("vm row status %q", status)
	}
	report, err := s.DetachVM(context.Background(), time.Second)
	if err != nil || !report.Drained || report.Interrupted != 0 {
		t.Fatalf("detach: %+v %v", report, err)
	}
	if s.computerCapabilities().Available || toolNames()["computer_shell"] {
		t.Fatal("tools did not follow the detach")
	}
	if s.store.db.QueryRow(`SELECT status FROM computers WHERE kind='vm'`).Scan(&status) != nil || status != computerRowDetached {
		t.Fatalf("vm row status after detach %q", status)
	}
	if s.store.legacyBlobs() != nil {
		t.Fatal("legacy file reader still points at the detached computer")
	}
	if _, err = s.DetachVM(context.Background(), time.Second); err != errComputerNone {
		t.Fatalf("detach twice: %v", err)
	}
	// A restart does not bring a detached VM back by itself.
	s.Close()
	s2, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.computer() != nil {
		t.Fatal("restart attached a detached VM")
	}
	s2.computerFactory = factory
	if err = s2.AttachVM(context.Background()); err != nil || s2.computer() == nil {
		t.Fatalf("attach after restart: %v", err)
	}
}

// Detach during a call: the call ends with a tool error instead of hanging,
// and the computer is gone afterwards.
func TestDetachMidCallReturnsComputerDetached(t *testing.T) {
	s := newRegistryServer(t)
	vm := newFakeComputer("firecracker")
	vm.blockOn["shell.exec"] = true
	s.microVM = vm
	run := Run{ID: "run-1", BotID: "bot-1"}
	result := make(chan error, 1)
	go func() {
		_, err := s.microVMAction(context.Background(), run, "shell.exec", shellArgs())
		result <- err
	}()
	for vm.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	start := time.Now()
	report, err := s.DetachVM(context.Background(), 150*time.Millisecond)
	if err != nil || report.Drained || report.Interrupted != 1 {
		t.Fatalf("detach: %+v %v", report, err)
	}
	select {
	case err := <-result:
		if toolCode(err) != codeComputerDetached {
			t.Fatalf("call ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call hung after the detach")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("detach waited far beyond its timeout")
	}
	if _, err = s.microVMAction(context.Background(), run, "shell.exec", shellArgs()); err == nil {
		t.Fatal("a call reached a detached computer")
	}
}

// A detach waits for a running call that finishes in time, and does not interrupt it.
func TestDetachWaitsForIdle(t *testing.T) {
	s := newRegistryServer(t)
	vm := newFakeComputer("firecracker")
	vm.blockOn["shell.exec"] = true
	s.microVM = vm
	done := make(chan error, 1)
	go func() {
		_, err := s.microVMAction(context.Background(), Run{ID: "run-1", BotID: "bot-1"}, "shell.exec", shellArgs())
		done <- err
	}()
	for vm.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(vm.release)
	}()
	report, err := s.DetachVM(context.Background(), 5*time.Second)
	if err != nil || !report.Drained || report.Interrupted != 0 {
		t.Fatalf("detach: %+v %v", report, err)
	}
	if err = <-done; err != nil {
		t.Fatalf("the call that finished in time failed: %v", err)
	}
}

// The administrator's view is the VM and the server. A person's own computers,
// and the fact that any exist, are not in it.
func TestAdminSeesOnlyTheVM(t *testing.T) {
	s := newRegistryServer(t)
	s.microVM = newFakeComputer("firecracker")
	code, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.pairComputer(code, "Maria's private laptop", "linux", []string{"files.list"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterComputer(newFakeComputer("laptop-1"), "Maria's private laptop", "eu"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://tofi.local/api/admin/computer/state", nil))
	if rec.Code != 200 {
		t.Fatalf("state: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{"Maria", "laptop-1", "personal", "eu"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the administrator's view mentions %q: %s", leak, body)
		}
	}
	var view adminVMSummary
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Kind != "vm" || !view.Attached {
		t.Fatalf("view: %s", body)
	}
	// The same rule holds for what the account's own storage reports to admin routes.
	if err = s.store.upsertVMComputer("firecracker", "Bot workspace", "linux", "", kvmCapabilities, 1, computerRowAttached); err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.listComputerRows()
	if err != nil || len(rows) != 2 || rows[0].Kind != "vm" {
		t.Fatalf("rows (vm first): %+v %v", rows, err)
	}
}

// The Bot lists its computers: name, kind, online, capabilities. With only the
// VM nothing new is added to its tools or its context.
func TestBotListsItsComputers(t *testing.T) {
	s := newRegistryServer(t)
	s.microVM = newFakeComputer("firecracker")
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, _ := s.store.AddUserRun(conv.ID, bot.ID, "hi", "list-computers")
	has := func(name string) bool {
		for _, tool := range s.tools(conv, run) {
			if tool.Name == name {
				return true
			}
		}
		return false
	}
	if has("list_computers") || s.computersRunContext(context.Background()) != "" {
		t.Fatal("a VM-only workspace gained a computer list")
	}
	code, _, _ := s.store.createComputerPairing()
	if _, _, err := s.store.pairComputer(code, "Studio Mac", "darwin", []string{"files.list"}); err != nil {
		t.Fatal(err)
	}
	if !has("list_computers") || !has("computer_action") {
		t.Fatal("list_computers missing after a personal computer was added")
	}
	var list Tool
	for _, tool := range s.computerTools(run) {
		if tool.Name == "list_computers" {
			list = tool
		}
	}
	out, err := list.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Computers []Computer `json:"computers"`
	}
	if json.Unmarshal([]byte(out), &parsed) != nil {
		t.Fatal(out)
	}
	kinds := map[string]string{}
	for _, c := range parsed.Computers {
		kinds[c.Name] = c.Kind
	}
	if kinds["Studio Mac"] != "personal" || kinds["Bot workspace"] != "vm" {
		t.Fatalf("kinds: %v in %s", kinds, out)
	}
}

// A run's computer use is recorded for later metering.
func TestRunRecordsComputerForMetering(t *testing.T) {
	s := newRegistryServer(t)
	s.microVM = newFakeComputer("firecracker")
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "hi", "metering")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM run_computers WHERE run_id=?`, run.ID).Scan(&n)
	if n != 0 {
		t.Fatal("a run that used no computer already has metering rows")
	}
	for i := 0; i < 2; i++ {
		if _, err = s.microVMAction(context.Background(), run, "shell.exec", shellArgs()); err != nil {
			t.Fatal(err)
		}
	}
	var id string
	var region sql.NullString
	var ms sql.NullInt64
	if s.store.db.QueryRow(`SELECT computer_id,region,compute_ms FROM run_computers WHERE run_id=?`, run.ID).Scan(&id, &region, &ms) != nil || id != "firecracker" || !ms.Valid || region.Valid {
		t.Fatalf("metering: %q %+v %+v", id, region, ms)
	}
	s.store.db.QueryRow(`SELECT COUNT(*) FROM run_computers WHERE run_id=?`, run.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("two calls on one computer made %d rows", n)
	}
}

// A Bot queued for a computer that is then lost is refused at once, not left waiting.
func TestQueuedBotIsRefusedWhenTheComputerIsLost(t *testing.T) {
	s := newRegistryServer(t)
	old := computerLostAfter
	computerLostAfter = time.Minute
	t.Cleanup(func() { computerLostAfter = old })
	mac := newFakeComputer("mac-a")
	mac.blockOn["shell.exec"] = true
	if err := s.RegisterComputer(mac, "Mac", ""); err != nil {
		t.Fatal(err)
	}
	slot := s.slotFor("mac-a")
	holder := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), Run{ID: "run-1", BotID: "bot-1"}, "mac-a", "shell.exec", shellArgs())
		holder <- err
	}()
	for mac.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	waiter := make(chan error, 1)
	go func() {
		_, err := s.computerActionOn(context.Background(), Run{ID: "run-2", BotID: "bot-2"}, "mac-a", "files.list", shellArgs())
		waiter <- err
	}()
	for queued(slot) != 1 {
		time.Sleep(2 * time.Millisecond)
	}
	mac.setSeen(time.Now().Add(-2 * time.Minute))
	s.sweepComputerHeartbeats(time.Now())
	for name, ch := range map[string]chan error{"holder": holder, "waiter": waiter} {
		select {
		case err := <-ch:
			// The call in flight ends as computer_lost (effect unknown); the Bot
			// that never started is refused as offline (nothing ran).
			want := codeComputerOffline
			if name == "holder" {
				want = codeComputerLost
			}
			if toolCode(err) != want {
				t.Fatalf("%s ended with %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s hung after the computer was lost", name)
		}
	}
	if slot.holder() != "" || queued(slot) != 0 {
		t.Fatalf("lock leaked: holder=%q queued=%d", slot.holder(), queued(slot))
	}
}
