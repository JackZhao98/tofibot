package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A synthetic Mac: it speaks only the device routes, with its own token.
type testMac struct {
	t     *testing.T
	s     *Server
	id    string
	token string
}

func (m *testMac) call(method, path, body string, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://tofi.local"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	m.s.Handler().ServeHTTP(w, r)
	return w
}

// enrollMac runs the whole claim: Settings issues a code, the Mac claims it.
func enrollMac(t *testing.T, s *Server, name string, caps ...string) *testMac {
	t.Helper()
	s.personalComputers = true
	m := &testMac{t: t, s: s}
	w := m.call("POST", "/api/computers/pairings", "", "")
	if w.Code != 201 {
		t.Fatalf("issue code: %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &issued)
	body, _ := json.Marshal(map[string]any{"code": issued.Code, "name": name, "platform": "darwin", "capabilities": caps})
	w = m.call("POST", "/api/computers/pair", string(body), "")
	if w.Code != 201 {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	var got struct{ ID, Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	m.id, m.token = got.ID, got.Token
	return m
}

func (m *testMac) poll(wait int) *ComputerJob {
	m.t.Helper()
	w := m.call("GET", "/api/computers/"+m.id+"/jobs?wait="+strconv.Itoa(wait), "", m.token)
	if w.Code != 200 {
		m.t.Fatalf("poll: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Job *ComputerJob `json:"job"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Job
}

func (m *testMac) answer(job *ComputerJob, result string) {
	m.t.Helper()
	body, _ := json.Marshal(map[string]any{"result": json.RawMessage(result)})
	if w := m.call("POST", "/api/computers/"+m.id+"/jobs/"+job.ID+"/result", string(body), m.token); w.Code != 200 {
		m.t.Fatalf("answer: %d %s", w.Code, w.Body.String())
	}
}

func computerActionTool(s *Server, r Run) Tool {
	for _, tool := range s.computerTools(r) {
		if tool.Name == "computer_action" {
			return tool
		}
	}
	panic("no computer_action")
}

func runCall(tool Tool, id, action, args string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tool.Execute(ctx, json.RawMessage(`{"computer_id":"`+id+`","action":"`+action+`","args":`+args+`}`))
}

// The claim: a code is shown once, the Mac claims it once, and what it gets is a
// computer row of kind personal with its own slot.
func TestPersonalClaimIsOneUseAndMakesASlot(t *testing.T) {
	s := computerTestServer(t)
	m := enrollMac(t, s, "Studio Mac", "files.list", "sandbox.exec")
	row, err := s.store.computerRow(m.id)
	if err != nil || row.Kind != "personal" || row.Name != "Studio Mac" {
		t.Fatalf("row: %+v %v", row, err)
	}
	if slot := s.slotFor(m.id); slot == nil || slot.rowKind != "personal" {
		t.Fatal("a claimed Mac has no runtime slot")
	}
	// The same code never claims twice.
	code, _, _ := s.store.createComputerPairing()
	body, _ := json.Marshal(map[string]any{"code": code, "name": "A", "platform": "darwin", "capabilities": []string{"files.list"}})
	if w := m.call("POST", "/api/computers/pair", string(body), ""); w.Code != 201 {
		t.Fatalf("first claim: %d", w.Code)
	}
	if w := m.call("POST", "/api/computers/pair", string(body), ""); w.Code != 400 {
		t.Fatalf("second claim of one code: %d", w.Code)
	}
}

// The cap is Config.MaxPersonalComputers.
func TestPersonalClaimHonoursTheComputerCap(t *testing.T) {
	s := computerTestServer(t)
	s.store.maxPersonalComputers = 2
	enrollMacNoFail := func(name string) int {
		s.personalComputers = true
		m := &testMac{t: t, s: s}
		w := m.call("POST", "/api/computers/pairings", "", "")
		var issued struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &issued)
		body, _ := json.Marshal(map[string]any{"code": issued.Code, "name": name, "platform": "darwin", "capabilities": []string{"files.list"}})
		return m.call("POST", "/api/computers/pair", string(body), "").Code
	}
	if enrollMacNoFail("a") != 201 || enrollMacNoFail("b") != 201 || enrollMacNoFail("c") != 400 {
		t.Fatal("the third Mac was not refused at a cap of two")
	}
}

// A device token reaches only its own computer's device routes, and the owner-only
// routes stay behind the session gate.
func TestPersonalTokenScope(t *testing.T) {
	s := computerTestServer(t)
	a := enrollMac(t, s, "Mac A", "files.list")
	b := enrollMac(t, s, "Mac B", "files.list")
	for _, tc := range []struct {
		name, method, path, token string
		want                      int
	}{
		{"own heartbeat", "POST", "/api/computers/" + a.id + "/heartbeat", a.token, 200},
		{"own poll", "GET", "/api/computers/" + a.id + "/jobs", a.token, 200},
		{"other's heartbeat", "POST", "/api/computers/" + b.id + "/heartbeat", a.token, 401},
		{"other's poll", "GET", "/api/computers/" + b.id + "/jobs", a.token, 401},
		{"no token", "POST", "/api/computers/" + a.id + "/heartbeat", "", 401},
		{"other's capabilities", "PATCH", "/api/computers/" + b.id + "/capabilities", a.token, 401},
	} {
		body := ""
		if tc.method == "PATCH" {
			body = `{"capabilities":["files.list"]}`
		}
		if w := a.call(tc.method, tc.path, body, tc.token); w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	// With owner sign-in on, only the device routes skip the session gate.
	for _, tc := range []struct {
		method, path string
		independent  bool
	}{
		{"POST", "/api/computers/x/heartbeat", true},
		{"GET", "/api/computers/x/jobs", true},
		{"POST", "/api/computers/x/jobs/j/result", true},
		{"POST", "/api/computers/pair", true},
		{"POST", "/api/computers/pairings", false},
		{"DELETE", "/api/computers/x", false},
		{"POST", "/api/computers/x/actions", false},
		{"GET", "/api/computers", false},
		{"GET", "/api/bots", false},
		{"GET", "/api/conversations", false},
	} {
		got := ownerIndependentRoute(httptest.NewRequest(tc.method, tc.path, nil))
		if got != tc.independent {
			t.Fatalf("%s %s independent=%v, want %v", tc.method, tc.path, got, tc.independent)
		}
	}
}

// computer_action reaches the chosen Mac through the queue; shell is the VM's name
// for sandbox.exec; the job names the Bot for the Mac's own consent window.
func TestPersonalRoutingAndShellAlias(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Studio Mac", "files.read", "sandbox.exec")
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)

	type res struct {
		out string
		err error
	}
	done := make(chan res, 1)
	go func() {
		out, err := runCall(tool, mac.id, "shell.exec", `{"command":"echo hi"}`)
		done <- res{out, err}
	}()
	job := mac.poll(5)
	if job == nil || job.Action != "sandbox.exec" || job.BotID != run.BotID || job.RunID != run.ID {
		t.Fatalf("job: %+v", job)
	}
	w := mac.call("GET", "/api/computers/"+mac.id+"/jobs?wait=0", "", mac.token)
	if strings.Contains(w.Body.String(), `"job":{`) {
		t.Fatal("a claimed job was handed out twice")
	}
	mac.answer(job, `{"exitCode":0,"stdout":"hi\n"}`)
	got := <-done
	if got.err != nil || !strings.Contains(got.out, `"stdout":"hi`) {
		t.Fatalf("result: %q %v", got.out, got.err)
	}
	// The claim carries the Bot's name.
	go func() { _, _ = runCall(tool, mac.id, "files.read", `{"path":"a"}`) }()
	next := mac.poll(5)
	if next == nil {
		t.Fatal("no second job")
	}
	raw, _ := json.Marshal(s.withBotName(next))
	if !strings.Contains(string(raw), `"bot_name":"bot"`) {
		t.Fatalf("claim without bot_name: %s", raw)
	}
	mac.answer(next, `{"path":"a","content":"x"}`)
	// Actions the Mac did not advertise are refused before they are queued.
	if _, err := runCall(tool, mac.id, "files.write", `{"path":"a","content":"x"}`); toolCode(err) != "capability_not_granted" {
		t.Fatalf("unadvertised action: %v", err)
	}
	// A name that is neither the VM's nor the Mac's is refused as invalid.
	if _, err := runCall(tool, mac.id, "browser.navigate", `{"url":"https://example.com"}`); err == nil {
		t.Fatal("a VM-only action reached a Mac")
	}
}

// One lock per computer, a lease per call: two Bots take turns on one Mac, and
// a second Mac is free meanwhile.
func TestPersonalLeaseIsPerComputerAndPerCall(t *testing.T) {
	s := computerTestServer(t)
	m1 := enrollMac(t, s, "Mac One", "files.list")
	m2 := enrollMac(t, s, "Mac Two", "files.list")
	r1 := activeComputerRun(t, s)
	r2 := activeComputerRun(t, s)
	t1, t2 := computerActionTool(s, r1), computerActionTool(s, r2)

	var wg sync.WaitGroup
	order := make(chan string, 4)
	call := func(tool Tool, id, label string) {
		defer wg.Done()
		if _, err := runCall(tool, id, "files.list", `{}`); err != nil {
			t.Errorf("%s: %v", label, err)
		}
		order <- label
	}
	wg.Add(2)
	go call(t1, m1.id, "bot1-first")
	first := m1.poll(5)
	if first == nil {
		t.Fatal("no first job")
	}
	// Bot 2 wants the same Mac: it queues behind the lease, and its job is not
	// even queued until the lease frees.
	go call(t2, m1.id, "bot2-second")
	time.Sleep(150 * time.Millisecond)
	if w := m1.call("GET", "/api/computers/"+m1.id+"/jobs?wait=0", "", m1.token); strings.Contains(w.Body.String(), `"job":{`) {
		t.Fatal("a second Bot's job reached the Mac while the first held the lease")
	}
	if slot := s.slotFor(m1.id); slot.holder() != r1.BotID {
		t.Fatalf("holder %q, want %q", slot.holder(), r1.BotID)
	}
	// Another Mac is not blocked by the first one's lock.
	wg.Add(1)
	go call(t2, m2.id, "bot2-other-mac")
	if j := m2.poll(5); j == nil {
		t.Fatal("the other Mac got no job while the first was busy")
	} else {
		m2.answer(j, `{"entries":[]}`)
	}
	m1.answer(first, `{"entries":[]}`)
	second := m1.poll(5)
	if second == nil || second.BotID != r2.BotID {
		t.Fatalf("the queued Bot's job: %+v", second)
	}
	m1.answer(second, `{"entries":[]}`)
	wg.Wait()
	close(order)
	if slot := s.slotFor(m1.id); slot.holder() != "" {
		t.Fatalf("lease leaked: %q", slot.holder())
	}
}

// Unplug the Mac mid-call: the call ends as computer_lost within the silence
// limit, the row says lost, the lock is free, and the Mac coming back attaches it.
func TestPersonalComputerLostMidCall(t *testing.T) {
	old := personalLostAfter
	personalLostAfter = 200 * time.Millisecond
	t.Cleanup(func() { personalLostAfter = old })
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Laptop", "sandbox.exec")
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)

	done := make(chan error, 1)
	go func() {
		_, err := runCall(tool, mac.id, "shell.exec", `{"command":"sleep 100"}`)
		done <- err
	}()
	job := mac.poll(5)
	if job == nil {
		t.Fatal("no job")
	}
	// The Mac now goes silent: no heartbeat, no result.
	select {
	case err := <-done:
		if toolCode(err) != codeComputerLost {
			t.Fatalf("ended with %v (code %q)", err, toolCode(err))
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the call hung after the Mac went silent")
	}
	row, _ := s.store.computerRow(mac.id)
	if row.Status != computerRowLost {
		t.Fatalf("row status %q", row.Status)
	}
	if s.slotFor(mac.id).holder() != "" {
		t.Fatal("the lost Mac still holds a lock")
	}
	// A new call is refused at once, as offline.
	if _, err := runCall(tool, mac.id, "shell.exec", `{"command":"true"}`); toolCode(err) != codeComputerOffline {
		t.Fatalf("call on a lost Mac: %v", err)
	}
	// The job that was in flight is not replayed to the returning Mac.
	if w := mac.call("POST", "/api/computers/"+mac.id+"/heartbeat", "", mac.token); w.Code != 200 {
		t.Fatalf("heartbeat: %d", w.Code)
	}
	if got := mac.poll(0); got != nil {
		t.Fatalf("an uncertain job was reissued: %+v", got)
	}
	row, _ = s.store.computerRow(mac.id)
	if row.Status != computerRowAttached || s.slotFor(mac.id).currentState() != slotAttached {
		t.Fatalf("the returning Mac is not attached: %q", row.Status)
	}
}

// The sweep finds a silent Mac with no call in flight.
func TestPersonalSweepMarksSilentMacLost(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Quiet", "files.list")
	if _, err := s.store.db.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano), mac.id); err != nil {
		t.Fatal(err)
	}
	s.sweepComputerHeartbeats(time.Now())
	if s.slotFor(mac.id).currentState() != slotLost {
		t.Fatal("silent Mac not marked lost")
	}
	// The sweep must not stamp a lost Mac as seen.
	row, _ := s.store.computerRow(mac.id)
	if row.Status != computerRowLost {
		t.Fatalf("status %q", row.Status)
	}
}

// Revoke: the token stops working with its own code, a call in flight ends, the
// slot and the listing forget the Mac.
func TestPersonalRevoke(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Old Mac", "sandbox.exec")
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)
	done := make(chan error, 1)
	go func() {
		_, err := runCall(tool, mac.id, "shell.exec", `{"command":"sleep 100"}`)
		done <- err
	}()
	if mac.poll(5) == nil {
		t.Fatal("no job")
	}
	if w := mac.call("DELETE", "/api/computers/"+mac.id, "", ""); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a revoked Mac answered")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the call hung after revoke")
	}
	w := mac.call("POST", "/api/computers/"+mac.id+"/heartbeat", "", mac.token)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "device_revoked") {
		t.Fatalf("heartbeat after revoke: %d %s", w.Code, w.Body.String())
	}
	if w = mac.call("GET", "/api/computers/"+mac.id+"/jobs", "", mac.token); w.Code != 401 {
		t.Fatalf("poll after revoke: %d", w.Code)
	}
	if s.slotFor(mac.id) != nil {
		t.Fatal("slot survived revoke")
	}
	list, _ := s.listComputers(context.Background())
	for _, c := range list {
		if c.ID == mac.id {
			t.Fatal("revoked Mac still listed")
		}
	}
	// The cap frees up.
	if n, _ := s.store.countPersonalComputers(); n != 0 {
		t.Fatalf("count %d", n)
	}
}

// list_computers: the VM is the default, a Mac shows its name, state and what it
// lent; a Mac another Bot is using is marked in use.
func TestListComputersShowsDefaultAndUse(t *testing.T) {
	s := computerTestServer(t)
	s.microVM = newFakeComputer("firecracker")
	mac := enrollMac(t, s, "Studio Mac", "files.list")
	run := activeComputerRun(t, s)
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
	_ = json.Unmarshal([]byte(out), &parsed)
	byName := map[string]Computer{}
	for _, c := range parsed.Computers {
		byName[c.Name] = c
	}
	if !byName["Bot workspace"].Default || byName["Studio Mac"].Default || !byName["Studio Mac"].Online || byName["Studio Mac"].Kind != "personal" {
		t.Fatalf("list: %s", out)
	}
	slot := s.slotFor(mac.id)
	release, _ := slot.acquire(context.Background(), "someone-else", "run-x")
	defer release()
	out, _ = list.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(out, `"in_use":true`) {
		t.Fatalf("a held Mac is not marked in use: %s", out)
	}
}

// computer_action is an effect tool: shell and file writes on a Mac get the same
// review as on the VM; only reads are observations.
func TestPersonalActionsAreReviewedLikeTheVM(t *testing.T) {
	for _, action := range []string{"shell.exec", "sandbox.exec", "files.write"} {
		a := ToolActivity{Name: "computer_action", Arguments: `{"computer_id":"c","action":"` + action + `","args":{}}`}
		if !mcpUncertainEffectActivityWith(a, nil) {
			t.Fatalf("%s on a Mac is not treated as an effect", action)
		}
	}
	for _, action := range []string{"files.read", "files.list"} {
		a := ToolActivity{Name: "computer_action", Arguments: `{"computer_id":"c","action":"` + action + `","args":{}}`}
		if mcpUncertainEffectActivityWith(a, nil) {
			t.Fatalf("%s on a Mac is treated as an effect", action)
		}
	}
}

// The administrator sees the VM and the server, and no sign of a Mac: not its
// name, id, count or state, in any admin answer, with Macs enrolled through the
// real claim path.
func TestAdminCannotSeePersonalComputers(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "mac-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "mac-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	ac := accountCookie(t, g, admin)
	workspace, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	m1 := enrollMac(t, workspace, "Maria's MacBook Pro", "files.list")
	m2 := enrollMac(t, workspace, "Maria's Mac mini", "files.list")
	run := activeComputerRun(t, workspace)
	go func() { _, _ = runCall(computerActionTool(workspace, run), m1.id, "files.list", `{}`) }()
	if m1.poll(5) == nil {
		t.Fatal("no job")
	}
	secret := []string{"Maria", "MacBook", "Mac mini", m1.id, m2.id, "personal"}
	for _, path := range []string{
		"/api/admin/accounts", "/api/admin/accounts/" + user.ID, "/api/admin/accounts/" + user.ID + "/computer/state",
		"/api/admin/accounts/" + user.ID + "/resources", "/api/admin/capacity", "/api/admin/storage",
		"/api/admin/deleted-exports",
	} {
		w := accountRequest(g, "GET", path, "", ac)
		for _, leak := range secret {
			if strings.Contains(w.Body.String(), leak) {
				t.Fatalf("GET %s mentions %q: %s", path, leak, w.Body.String())
			}
		}
	}
	// The administrator cannot read the computer routes of a person's workspace either.
	for _, path := range []string{"/api/computers", "/api/computers/" + m1.id + "/actions/x"} {
		w := accountRequest(g, "GET", path, "", ac)
		for _, leak := range secret {
			if strings.Contains(w.Body.String(), leak) {
				t.Fatalf("admin GET %s reached a person's computers: %s", path, w.Body.String())
			}
		}
	}
}

// In an account gateway a Mac has no session. Its code and token carry the
// account that issued them, so the claim and every poll reach that account's
// workspace and no other.
func TestGatewayRoutesDeviceRequestsByTag(t *testing.T) {
	g := accountFixture(t)
	g.config.PersonalComputers = true
	admin, err := g.create(context.Background(), "tag-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "tag-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	uc, ac := accountCookie(t, g, user), accountCookie(t, g, admin)
	issue := func(c *http.Cookie) string {
		w := accountRequest(g, "POST", "/api/computers/pairings", "", c)
		if w.Code != 201 {
			t.Fatalf("issue: %d %s", w.Code, w.Body.String())
		}
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Code
	}
	claim := func(code string) (id, token string, status int) {
		body, _ := json.Marshal(map[string]any{"code": code, "name": "Mac", "platform": "darwin", "capabilities": []string{"files.list"}})
		w := accountRequest(g, "POST", "/api/computers/pair", string(body), nil)
		var out struct{ ID, Token string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.ID, out.Token, w.Code
	}
	deviceCall := func(method, path, token string) int {
		r := httptest.NewRequest(method, path, nil)
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w.Code
	}
	userCode, adminCode := issue(uc), issue(ac)
	if !strings.HasPrefix(userCode, user.ID+".") {
		t.Fatal("a code does not name its account")
	}
	uid, utoken, st := claim(userCode)
	if st != 201 {
		t.Fatalf("user claim: %d", st)
	}
	aid, atoken, st := claim(adminCode)
	if st != 201 {
		t.Fatalf("admin claim: %d", st)
	}
	userWS, _ := g.workspace(user)
	adminWS, _ := g.workspace(admin)
	if _, err = userWS.store.computerRow(uid); err != nil {
		t.Fatalf("the Mac is not in its account: %v", err)
	}
	if _, err = adminWS.store.computerRow(uid); err == nil {
		t.Fatal("a Mac landed in another account")
	}
	if c := deviceCall("POST", "/api/computers/"+uid+"/heartbeat", utoken); c != 200 {
		t.Fatalf("own heartbeat: %d", c)
	}
	if c := deviceCall("GET", "/api/computers/"+uid+"/jobs", utoken); c != 200 {
		t.Fatalf("own poll: %d", c)
	}
	// One account's token never reaches another's computer, tagged or not.
	if c := deviceCall("POST", "/api/computers/"+uid+"/heartbeat", atoken); c != 401 {
		t.Fatalf("other account's token: %d", c)
	}
	if c := deviceCall("POST", "/api/computers/"+aid+"/heartbeat", utoken); c != 401 {
		t.Fatalf("user's token on the admin's computer: %d", c)
	}
	if c := deviceCall("POST", "/api/computers/"+uid+"/heartbeat", strings.SplitN(utoken, ".", 2)[1]); c != 401 {
		t.Fatalf("untagged token: %d", c)
	}
	if c := deviceCall("POST", "/api/computers/"+uid+"/heartbeat", "no-such-account."+strings.SplitN(utoken, ".", 2)[1]); c != 401 {
		t.Fatalf("token with a forged tag: %d", c)
	}
	// Owner-only routes still need the session.
	if c := deviceCall("DELETE", "/api/computers/"+uid, utoken); c != 401 {
		t.Fatalf("a device token revoked a computer: %d", c)
	}
	if w := accountRequest(g, "POST", "/api/computers/pairings", "", nil); w.Code != 401 {
		t.Fatalf("anonymous code issue: %d", w.Code)
	}
}

// The owner's test endpoint can attribute a job to one of their Bots, which is
// what the Mac's per-Bot consent keys on; a Bot that does not exist is refused.
func TestOwnerActionCanNameABot(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Studio Mac", "files.list")
	bot, err := s.store.CreateBot("Ada", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"action":"files.list","args":{},"bot_id":"` + bot.ID + `"}`
	if w := mac.call("POST", "/api/computers/"+mac.id+"/actions", body, ""); w.Code != 201 {
		t.Fatalf("queue: %d %s", w.Code, w.Body.String())
	}
	job := mac.poll(2)
	if job == nil || job.BotID != bot.ID {
		t.Fatalf("job: %+v", job)
	}
	if w := mac.call("POST", "/api/computers/"+mac.id+"/actions", `{"action":"files.list","args":{},"bot_id":"no-such-bot"}`, ""); w.Code != 404 {
		t.Fatalf("unknown bot: %d", w.Code)
	}
}
