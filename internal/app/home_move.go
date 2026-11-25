package app

// Moving the home directory between Bot computers (owner 2026-11-24: an account has one Bot computer; switching
// between the built-in VM and E2B moves /workspace/home; installed software and browser sign-ins do not move).
//
// No guest change: the old computer packs home into a tarball split into parts under the 20 MiB import limit,
// the server streams each part page by page (files.export_chunk -> files.import_chunk), the new computer checks the
// parts' sha256 and unpacks them. Each move has its own folder, so a retry never reuses a stale part. Packing and
// unpacking run detached on the guest (setsid) and leave "done" or "failed:<rc>" in a state file the server polls,
// because one shell.exec call ends after 120 s.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/e2b"
)

const (
	homeMoveMaxBytes   int64 = 4 << 30 // a larger home is refused with home_too_large
	homeMoveSpare      int64 = 64 << 20
	homeMovePartBytes        = "19m" // under the guest's 20 MiB import limit
	homeMoveDeadline         = 30 * time.Minute
	homeMovePoll             = 2 * time.Second
	homeMoveActionTime       = 3 * time.Minute
)

// homeMoveError carries the named reason the page shows (environments.error.<code>).
type homeMoveError struct{ code string }

func (e homeMoveError) Error() string { return "home move: " + e.code }

func homeMoveCode(err error) string {
	var named homeMoveError
	if errors.As(err, &named) {
		return named.code
	}
	// E2B's own refusal (a revoked key, the account's limit) is the reason the person can act on.
	var upstream *e2b.Error
	if errors.As(err, &upstream) && upstream.Code != "" {
		return upstream.Code
	}
	return "move_failed"
}

// homeMoveState is the switch in progress, read by the environments view.
type homeMoveState struct {
	mu        sync.Mutex
	phase     string // "" | moving | failed
	target    string
	errorCode string
}

type homeMoveView struct {
	Phase     string `json:"phase"`
	Target    string `json:"target,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func (m *homeMoveState) view() *homeMoveView {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phase == "" {
		return nil
	}
	return &homeMoveView{Phase: m.phase, Target: m.target, ErrorCode: m.errorCode}
}

// begin claims the one move slot; false while a move runs.
func (m *homeMoveState) begin(target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phase == "moving" {
		return false
	}
	m.phase, m.target, m.errorCode = "moving", target, ""
	return true
}

func (m *homeMoveState) end(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.phase, m.target, m.errorCode = "", "", ""
		return
	}
	m.phase, m.errorCode = "failed", homeMoveCode(err)
}

// guestCall runs one action on one computer and returns its raw result.
type guestCall func(ctx context.Context, name string, args any) (json.RawMessage, error)

// attachedGuest calls the attached Bot computer through the slot gate (a detach waits for the call or ends it).
func (s *Server) attachedGuest(botID, moveID string) guestCall {
	return func(ctx context.Context, name string, args any) (json.RawMessage, error) {
		raw, _ := json.Marshal(args)
		out, err := s.microVMActionFromSource(ctx, Run{ID: "home-move:" + moveID, BotID: botID}, name, raw, "model")
		return json.RawMessage(out), err
	}
}

// standbyGuest calls a computer that is not attached (the E2B computer beside the VM): no slot, a deadline per call.
func standbyGuest(p computer.Provider, botID, moveID string) guestCall {
	return func(ctx context.Context, name string, args any) (json.RawMessage, error) {
		raw, _ := json.Marshal(args)
		res, err := p.ActionWithin(ctx, computer.Action{BotID: botID, RunID: "home-move:" + moveID, Name: name, Args: raw, Source: "model"}, homeMoveActionTime)
		if err != nil {
			return nil, err
		}
		return res.Result, nil
	}
}

type guestShellResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func guestShell(ctx context.Context, call guestCall, command string, timeout int) (guestShellResult, error) {
	raw, err := call(ctx, "shell.exec", map[string]any{"command": command, "timeout_sec": timeout})
	if err != nil {
		return guestShellResult{}, err
	}
	var out guestShellResult
	if json.Unmarshal(raw, &out) != nil {
		return guestShellResult{}, errors.New("unreadable shell result")
	}
	return out, nil
}

// waitGuestState polls a state file until it says done or failed:<rc>.
func waitGuestState(ctx context.Context, call guestCall, file string) error {
	deadline := time.Now().Add(homeMoveDeadline)
	for time.Now().Before(deadline) {
		out, err := guestShell(ctx, call, "cat "+file+" 2>/dev/null || true", 20)
		if err != nil {
			return err
		}
		switch state := strings.TrimSpace(out.Stdout); {
		case state == "done":
			return nil
		case strings.HasPrefix(state, "failed"):
			return fmt.Errorf("guest step %s", state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(homeMovePoll):
		}
	}
	return errors.New("guest step timed out")
}

func homeMoveDir(moveID string) string { return "/workspace/home/uploads/.tofi-move-" + moveID }

// packHome packs /workspace/home on a computer into parts under its move folder and returns the part names (the
// sums file lists them) and the size of home. tar exits 1 when a file changed while it was read (a Bot still
// working): the archive is still complete for every other file, so that counts as done.
func packHome(ctx context.Context, from guestCall, moveID string) ([]string, int64, error) {
	dir := homeMoveDir(moveID)
	size, err := guestShell(ctx, from, "du -sb --exclude='uploads/.tofi-move-*' /workspace/home | cut -f1", 60)
	if err != nil {
		return nil, 0, guestStepError("could not measure home", size, err)
	}
	used, err := strconv.ParseInt(strings.TrimSpace(size.Stdout), 10, 64)
	if err != nil {
		return nil, 0, errors.New("could not measure home")
	}
	if used > homeMoveMaxBytes {
		return nil, used, homeMoveError{"home_too_large"}
	}
	pack := fmt.Sprintf(`mkdir -p %[1]s && setsid nohup sh -c 'cd /workspace && tar --exclude="home/uploads/.tofi-move-*" -czf %[1]s/home.tgz home; rc=$?; if [ $rc -le 1 ]; then cd %[1]s && split -b %[2]s home.tgz part. && rm home.tgz && sha256sum part.* > sums && echo done > state; else echo failed:$rc > state; fi' >/dev/null 2>&1 &`, dir, homeMovePartBytes)
	if out, err := guestShell(ctx, from, pack, 30); err != nil || out.ExitCode != 0 {
		return nil, used, guestStepError("could not start packing", out, err)
	}
	if err := waitGuestState(ctx, from, dir+"/state"); err != nil {
		return nil, used, err
	}
	sums, err := guestShell(ctx, from, "cat "+dir+"/sums", 30)
	if err != nil || sums.ExitCode != 0 {
		return nil, used, errors.New("could not read the part list")
	}
	var parts []string
	for _, line := range strings.Split(strings.TrimSpace(sums.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[1], "part.") {
			parts = append(parts, fields[1])
		}
	}
	if len(parts) == 0 {
		return nil, used, errors.New("no parts")
	}
	return parts, used, nil
}

// checkHomeSpace refuses a target that cannot hold the parts and the unpacked home at once.
func checkHomeSpace(ctx context.Context, to guestCall, used int64) error {
	free, err := guestShell(ctx, to, "df -B1 --output=avail /workspace | tail -1", 30)
	if err != nil {
		return err
	}
	if avail, e := strconv.ParseInt(strings.TrimSpace(free.Stdout), 10, 64); e != nil || avail < 2*used+homeMoveSpare {
		return homeMoveError{"target_disk_full"}
	}
	return nil
}

// unpackHome checks the parts' sha256 on the target and unpacks them over /workspace. Files keep the target's
// own owner (--no-same-owner).
func unpackHome(ctx context.Context, to guestCall, moveID string) error {
	dir := homeMoveDir(moveID)
	check, err := guestShell(ctx, to, "cd "+dir+" && sha256sum -c --quiet sums", 110)
	if err != nil || check.ExitCode != 0 {
		return errors.New("parts did not match")
	}
	unpack := fmt.Sprintf(`setsid nohup sh -c 'cd %[1]s && cat part.* | tar --no-same-owner -xzf - -C /workspace; rc=$?; if [ $rc -eq 0 ]; then echo done > unpacked; else echo failed:$rc > unpacked; fi' >/dev/null 2>&1 &`, dir)
	if out, err := guestShell(ctx, to, unpack, 30); err != nil || out.ExitCode != 0 {
		return guestStepError("could not start unpacking", out, err)
	}
	return waitGuestState(ctx, to, dir+"/unpacked")
}

func cleanHomeMove(call guestCall, moveID string) {
	c, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _ = guestShell(c, call, "rm -rf "+homeMoveDir(moveID), 30)
}

// moveHome copies /workspace/home from one computer to another. It never deletes anything outside its own folder.
func moveHome(ctx context.Context, from, to guestCall, moveID string) error {
	defer cleanHomeMove(from, moveID)
	defer cleanHomeMove(to, moveID)
	parts, used, err := packHome(ctx, from, moveID)
	if err != nil {
		return err
	}
	if err := checkHomeSpace(ctx, to, used); err != nil {
		return err
	}
	dir := homeMoveDir(moveID)
	if _, err := guestShell(ctx, to, "rm -rf /workspace/home/uploads/.tofi-move-* && mkdir -p "+dir, 30); err != nil {
		return err
	}
	for _, name := range append(parts, "sums") {
		if err := copyGuestFile(ctx, from, to, dir+"/"+name); err != nil {
			return err
		}
	}
	return unpackHome(ctx, to, moveID)
}

// copyGuestFile streams one file from one computer to the other in 512 KiB pages.
func copyGuestFile(ctx context.Context, from, to guestCall, path string) error {
	var offset int64
	version := ""
	for {
		raw, err := from(ctx, "files.export_chunk", map[string]any{"path": path, "offset": offset, "limit": 512 << 10, "version": version})
		if err != nil {
			return err
		}
		var w guestWireChunk
		if json.Unmarshal(raw, &w) != nil || w.Next < offset {
			return errors.New("invalid chunk")
		}
		if offset == 0 {
			version = w.Version
		}
		res, err := to(ctx, "files.import_chunk", map[string]any{"path": path, "offset": offset, "total_size": w.Size, "data_base64": w.Data})
		if err != nil {
			return err
		}
		var status struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(res, &status)
		if status.Status == "exists" {
			return errors.New("stale part on the target")
		}
		offset = w.Next
		if w.EOF || offset >= w.Size {
			return nil
		}
	}
}

// homeMoveBot is the Bot whose identity the guest checks for the move's calls: any active Bot of the account.
func (s *Server) homeMoveBot() string {
	if s.store == nil {
		return ""
	}
	var id string
	_ = s.store.db.QueryRow(`SELECT id FROM bots WHERE archived=0 ORDER BY created_at LIMIT 1`).Scan(&id)
	return id
}

func newHomeMoveID() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:12] }

// moveHomeFunc is moveHome, or a test's stand-in (fake computers cannot run tar).
func (s *Server) moveHomeFunc() func(ctx context.Context, from, to guestCall, moveID string) error {
	if s.homeMover != nil {
		return s.homeMover
	}
	return moveHome
}

// guestStepError keeps what the guest said (the call error, the exit code and the first line of stderr) for the
// server log; the page shows only the move's named reason.
func guestStepError(step string, out guestShellResult, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}
	line := strings.TrimSpace(out.Stderr)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return fmt.Errorf("%s: exit %d: %s", step, out.ExitCode, line)
}

// clear forgets a finished or failed move, so a stale failure does not stay on the page after the person fixed
// its cause (a new key, a removed computer, a switch that went through).
func (m *homeMoveState) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phase != "moving" {
		m.phase, m.target, m.errorCode = "", "", ""
	}
}
