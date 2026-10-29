package app

// Host control (computer-decoupling S6, `tofi computer add|remove`): the root
// `tofi` command on the server attaches and detaches every account's computer
// without restarting the App.
//
// Trust model. The route is /api/host/computer/{state|attach|detach}. It answers
// only to a bearer token that sits in a file the host creates (root-owned content,
// readable by the App's uid): TOFI_HOST_CONTROL_TOKEN_FILE. Without that file the
// route does not exist (404), so an install that never ran the new `tofi` has no
// such door. A request that came through a proxy (any forwarding header) is
// refused even with the right token, and the Caddyfile does not proxy the path
// at all: the host reaches it on the App's loopback port. The App runs in a
// container with that port published, so the peer address it sees for the host's
// own call is its loopback or its network's gateway (the Docker bridge address);
// a request from any other address, such as a LAN machine reaching a
// TOFI_BIND=0.0.0.0 port, gets 404 before the token is looked at. The token grants what
// the administrator's per-account endpoints grant, for all accounts, and nothing
// else.

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// hostControlTokenMax bounds the token file we are willing to read.
const hostControlTokenMax = 256

// hostControlRoute is the prefix of the host-only routes.
const hostControlRoute = "/api/host/computer/"

// forwardingHeaders mark a request that came through a proxy.
var forwardingHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Via"}

// hostGateways lists the container's default gateways (the address a connection
// from the host's own loopback arrives from, through a published port). Tests
// replace it.
var hostGateways = func() []net.IP {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []net.IP
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		var raw uint32
		if _, err := fmt.Sscanf(fields[2], "%x", &raw); err != nil {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, raw)
		out = append(out, ip)
	}
	return out
}

// hostSourceAllowed reports that the peer is the host itself: loopback or the
// container's gateway. The peer address is the TCP source, which a client cannot
// choose; forwarding headers are checked separately.
func hostSourceAllowed(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return true
	}
	for _, gw := range hostGateways() {
		if gw.Equal(ip) {
			return true
		}
	}
	return false
}

// hostAuthorized checks the bearer token against the token file. A missing or
// short file never authorizes.
func hostAuthorized(r *http.Request, tokenFile string) bool {
	if tokenFile == "" {
		return false
	}
	for _, h := range forwardingHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	f, err := os.Open(tokenFile)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, hostControlTokenMax+1)
	n, _ := f.Read(buf)
	if n == 0 || n > hostControlTokenMax {
		return false
	}
	want := strings.TrimSpace(string(buf[:n]))
	if len(want) < 32 {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// hostControlPresent reports that the token file exists (the route is on).
func hostControlPresent(tokenFile string) bool {
	if tokenFile == "" {
		return false
	}
	_, err := os.Stat(tokenFile)
	return err == nil
}

// provisioner is the Worker's provisioning socket while the computer is on, and
// empty while the host has detached the computers: a server without a computer
// reserves, ensures and reclaims nothing.
func (g *AccountGateway) provisioner() string {
	if g.computerOff.Load() {
		return ""
	}
	return g.config.AccountProvisionerSocket
}

// serveHost answers /api/host/computer/*. It returns false for any other path.
func (g *AccountGateway) serveHost(w http.ResponseWriter, r *http.Request) bool {
	op, ok := strings.CutPrefix(r.URL.Path, hostControlRoute)
	if !ok || strings.Contains(op, "/") {
		return false
	}
	if !hostControlPresent(g.config.HostControlTokenFile) {
		return false // the door does not exist on a server whose host never made the token
	}
	if !hostSourceAllowed(r) {
		writeErr(w, 404, "not_found", "not found") // not the host: the door does not exist from here
		return true
	}
	if !hostAuthorized(r, g.config.HostControlTokenFile) {
		writeErr(w, 401, "unauthorized", "host token required")
		return true
	}
	switch op {
	case "state":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "use GET")
			return true
		}
		attached, total := g.hostCountComputers()
		writeJSON(w, 200, map[string]any{"backend_available": g.config.AccountProvisionerSocket != "", "enabled": !g.computerOff.Load(), "accounts": total, "attached": attached})
	case "attach":
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return true
		}
		g.hostAttachAll(w, r)
	case "detach":
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return true
		}
		g.hostDetachAll(w, r)
	default:
		writeErr(w, 404, "not_found", "unknown computer operation")
	}
	return true
}

// hostCountComputers counts the accounts that have a computer attached, among
// the workspaces that are loaded.
func (g *AccountGateway) hostCountComputers() (attached, total int) {
	g.mu.Lock()
	workspaces := make([]*Server, 0, len(g.workspaces))
	for _, s := range g.workspaces {
		workspaces = append(workspaces, s)
	}
	g.mu.Unlock()
	for _, s := range workspaces {
		total++
		if s.computer() != nil {
			attached++
		}
	}
	return attached, total
}

// hostAccounts lists the accounts that can have a computer.
func (g *AccountGateway) hostAccounts(ctx context.Context) ([]Account, error) {
	rows, err := g.root.store.db.QueryContext(ctx, `SELECT id,username,email,role,disabled,must_change_password,legacy FROM accounts WHERE disabled=0 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Disabled, &a.MustChangePassword, &a.Legacy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// hostAttachAll turns the computers on and attaches every account's. The App
// must have been started with the Worker's sockets (a server that started with
// the backend off cannot build a computer client and answers 409: the host then
// restarts the App once, which is what it always did).
func (g *AccountGateway) hostAttachAll(w http.ResponseWriter, r *http.Request) {
	if g.config.AccountProvisionerSocket == "" {
		writeErr(w, 409, "computer_backend_unavailable", "this server was started without a computer backend")
		return
	}
	g.computerOff.Store(false)
	accounts, err := g.hostAccounts(r.Context())
	if err != nil {
		writeErr(w, 500, "internal", "accounts unavailable")
		return
	}
	attached, already := 0, 0
	var failed []string
	for _, a := range accounts {
		ws, err := g.workspace(a)
		if err != nil {
			failed = append(failed, a.ID)
			continue
		}
		switch err := ws.AttachVM(r.Context()); {
		case err == nil:
			attached++
		case errors.Is(err, errComputerAttached):
			already++
		default:
			log.Printf("[computer] host attach: account %s: %v", a.ID, err)
			failed = append(failed, a.ID)
		}
	}
	status := 200
	if len(failed) > 0 {
		status = 502
	}
	writeJSON(w, status, map[string]any{"attached": attached, "already_attached": already, "failed": failed})
}

// hostDetachAll turns the computers off, then detaches every loaded account's
// (drain, copy the outbox, cancel the rest). From then on no account reserves or
// ensures a computer, and a workspace opened later starts detached.
func (g *AccountGateway) hostDetachAll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WaitSeconds int `json:"wait_seconds"`
	}
	if r.ContentLength != 0 && decodeStrict(r, 1024, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid detach request")
		return
	}
	if in.WaitSeconds < 0 || in.WaitSeconds > 600 {
		writeErr(w, 400, "invalid_request", "wait_seconds must be 0 to 600")
		return
	}
	g.computerOff.Store(true)
	g.mu.Lock()
	workspaces := make([]*Server, 0, len(g.workspaces))
	for _, s := range g.workspaces {
		workspaces = append(workspaces, s)
	}
	g.mu.Unlock()
	detached, interrupted, synced := 0, 0, 0
	failed := 0
	for _, s := range workspaces {
		report, err := s.DetachVM(r.Context(), time.Duration(in.WaitSeconds)*time.Second)
		switch {
		case err == nil:
			detached++
			interrupted += report.Interrupted
			synced += report.Outbox.Synced
		case errors.Is(err, errComputerNone):
		default:
			log.Printf("[computer] host detach: %v", err)
			failed++
		}
	}
	status := 200
	if failed > 0 {
		status = 500
	}
	writeJSON(w, status, map[string]any{"detached": detached, "interrupted": interrupted, "outbox_synced": synced, "failed": failed})
}
