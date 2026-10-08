package guest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type desktop struct {
	streamActive       bool // legacy compatibility; protected by Service.mu
	streamViewers      int  // shared desktop stream viewers; protected by Service.mu
	tabCap             *tabCapTracker
	shared             bool // one workspace session, rather than a legacy fixture desktop
	botID              string
	botDir             string
	terminalMarker     string
	display            string
	profile            string
	remotePort         int
	xvfb               *exec.Cmd
	fluxbox            *exec.Cmd
	panel              *exec.Cmd
	clockPanel         *exec.Cmd
	launcherPanel      *exec.Cmd
	chrome             *exec.Cmd
	log                *limitedBuffer
	chromeLog          *limitedBuffer
	runtimeDir         string
	ready              chan struct{}
	startErr           error
	readyClosed        bool
	cancel             context.CancelFunc
	captureW           int
	captureH           int
	lastActivity       time.Time
	inFlight           int
	stopping           bool
	stopOnce           sync.Once
	stopDone           chan struct{}
	stopErr            error
	stopRequestMu      sync.Mutex
	stopRequestRunning bool
	stopRequestDone    chan struct{}
	stopRequestErr     error
}

type shellArgs struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec"`
}

type fileArgs struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	Append         bool   `json:"append"`
	Offset         int    `json:"offset"`
	Limit          int    `json:"limit"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

const exportChunkBytes = 512 << 10

type exportArgs struct {
	Path    string `json:"path"`
	Offset  int64  `json:"offset"`
	Limit   int    `json:"limit"`
	Version string `json:"version"`
}

func (s *Service) dispatchAction(ctx context.Context, req ActionRequest) (any, error) {
	switch req.Action {
	case "timezone.get":
		return s.timezoneGet()
	case "timezone.set":
		args, err := decodeTimezoneArgs(req.Args)
		if err != nil {
			return nil, err
		}
		return s.timezoneSet(args)
	case "terminal.open":
		var a terminalOpenArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		return s.terminalOpen(ctx, req.BotID, req.RunID, a)
	case "terminal.list":
		return map[string]any{"terminals": s.terminalList(req.BotID)}, nil
	case "terminal.read":
		var a terminalReadArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		return s.terminalRead(req.BotID, a)
	case "terminal.write":
		var a terminalWriteArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		return s.terminalWrite(req.BotID, req.RunID, req.Source, a)
	case "terminal.resize":
		var a terminalResizeArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		return s.terminalResize(req.BotID, a)
	case "terminal.close":
		var a terminalIDArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		return s.terminalClose(req.BotID, a)
	case "terminal.cancel_run":
		if len(req.Args) > 0 && string(req.Args) != "{}" && string(req.Args) != "null" {
			var empty struct{}
			if err := decodeArgs(req.Args, &empty); err != nil {
				return nil, err
			}
		}
		return s.terminalCancelRun(req.BotID, req.RunID)
	case "shell.exec":
		var args shellArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.shell(ctx, req.BotID, args)
	case "files.list":
		var args fileArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.listFiles(ctx, req.BotID, args.Path)
	case "files.identity":
		var args struct {
			Path string `json:"path"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.fileIdentity(req.BotID, args.Path)
	case "files.read":
		var args fileArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.readFile(ctx, req.BotID, args)
	case "files.export_chunk":
		var args exportArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.exportFileChunk(ctx, req.BotID, args)
	case "files.write":
		var args fileArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if req.WriteIdentity != nil {
			return s.guardedWriteFile(ctx, req.BotID, args, *req.WriteIdentity)
		}
		return s.writeFile(ctx, req.BotID, args)
	case "desktop.start":
		return s.startDesktop(ctx, req.BotID)
	case "desktop.stop":
		return s.stopDesktop(ctx, req.BotID)
	case "desktop.capture":
		return s.desktopCapture(ctx, req.BotID)
	case "desktop.click":
		var args struct {
			X                int  `json:"x"`
			Y                int  `json:"y"`
			Button           int  `json:"button"`
			ScreenshotWidth  *int `json:"screenshot_width"`
			ScreenshotHeight *int `json:"screenshot_height"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if (args.ScreenshotWidth == nil) != (args.ScreenshotHeight == nil) {
			return nil, errors.New("screenshot_width and screenshot_height must be provided together")
		}
		width, height := 0, 0
		if args.ScreenshotWidth != nil {
			width, height = *args.ScreenshotWidth, *args.ScreenshotHeight
			if width < 1 || width > 1280 || height < 1 || height > 800 {
				return nil, errors.New("screenshot dimensions must be within 1..1280 by 1..800")
			}
		}
		return s.desktopClick(ctx, req.BotID, args.X, args.Y, args.Button, width, height)
	case "desktop.scroll":
		var args struct {
			Direction string `json:"direction"`
			Amount    int    `json:"amount"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.desktopScroll(ctx, req.BotID, args.Direction, args.Amount)
	case "desktop.type":
		var args struct {
			Text string `json:"text"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.desktopType(ctx, req.BotID, args.Text)
	case "desktop.key":
		var args struct {
			Key       string   `json:"key"`
			Modifiers []string `json:"modifiers"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.desktopKey(ctx, req.BotID, args.Key, args.Modifiers)
	case "browser.type_private":
		var args privateBrowserInput
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, errors.New("invalid private browser input")
		}
		return s.browserTypePrivate(ctx, req.BotID, args)
	case "browser.navigate":
		var args struct {
			URL      string `json:"url"`
			TargetID string `json:"target_id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.browserControl(ctx, req.BotID, browserCommand{Action: "navigate", URL: args.URL, TargetID: args.TargetID})
	case "browser.snapshot":
		var args struct {
			TargetID string `json:"target_id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.browserControl(ctx, req.BotID, browserCommand{Action: "snapshot", TargetID: args.TargetID})
	case "browser.action":
		var args struct {
			Action   string `json:"action"`
			URL      string `json:"url"`
			TargetID string `json:"target_id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return s.browserControl(ctx, req.BotID, browserCommand{Action: args.Action, URL: args.URL, TargetID: args.TargetID})
	default:
		return nil, fmt.Errorf("%w: %s", errInvalidAction, req.Action)
	}
}

func fileVersion(info os.FileInfo) string {
	st, _ := info.Sys().(*syscall.Stat_t)
	if st == nil {
		return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	return fmt.Sprintf("%d:%d:%d:%d", st.Dev, st.Ino, info.Size(), info.ModTime().UnixNano())
}

func (s *Service) exportFileChunk(ctx context.Context, botID string, args exportArgs) (map[string]any, error) {
	p, err := s.resolvePath(botID, args.Path, false)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(s.root, p)
	if err != nil || !within(s.root, p) {
		return nil, errors.New("path escapes guest workspace")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("only regular files can be published")
	}
	version := fileVersion(before)
	if args.Version != "" && args.Version != version {
		return nil, errors.New("file changed while publishing")
	}
	if args.Offset < 0 || args.Offset > before.Size() {
		return nil, errors.New("invalid file offset")
	}
	if args.Limit <= 0 || args.Limit > exportChunkBytes {
		args.Limit = exportChunkBytes
	}
	buf := make([]byte, args.Limit)
	n, readErr := f.ReadAt(buf, args.Offset)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	check, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("file changed while publishing")
	}
	after, statErr := check.Stat()
	_ = check.Close()
	if statErr != nil || fileVersion(after) != version {
		return nil, errors.New("file changed while publishing")
	}
	next := args.Offset + int64(n)
	return map[string]any{"data_base64": base64.StdEncoding.EncodeToString(buf[:n]), "next_offset": next,
		"size": before.Size(), "eof": next == before.Size(), "version": version, "name": filepath.Base(p)}, nil
}

func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
		return errors.New("args must be valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func (s *Service) botDir(botID string) (string, error) {
	if !botIDPattern.MatchString(botID) {
		return "", errors.New("bot_id must be a canonical UUID")
	}
	p := filepath.Join(s.root, "bots", botID)
	if err := os.MkdirAll(filepath.Join(p, "tmp"), 0770); err != nil {
		return "", fmt.Errorf("create bot workspace: %w", err)
	}
	return p, nil
}

func (s *Service) resolvePath(botID, name string, write bool) (string, error) {
	bot, err := s.botDir(botID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		name = "."
	}
	var p string
	if filepath.IsAbs(name) {
		p = filepath.Clean(name)
		if _, err := filepath.Rel(s.root, p); err != nil {
			return "", err
		}
	} else {
		p = filepath.Join(bot, filepath.Clean(name))
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "", err
	}
	check := p
	if write {
		check = filepath.Dir(p)
	}
	for {
		if _, e := os.Lstat(check); e == nil {
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	resolved, err := filepath.EvalSymlinks(check)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil && !within(root, resolved) {
		return "", errors.New("path escapes guest workspace")
	}
	if target, e := filepath.EvalSymlinks(p); e == nil && !within(root, target) {
		return "", errors.New("path escapes guest workspace")
	}
	if !within(root, p) {
		return "", errors.New("path escapes guest workspace")
	}
	if !write {
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
	}
	return p, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func (s *Service) shell(parent context.Context, botID string, args shellArgs) (map[string]any, error) {
	if strings.TrimSpace(args.Command) == "" {
		return nil, errors.New("command is required")
	}
	if parent == nil {
		parent = context.Background()
	}
	bot, err := s.botDir(botID)
	if err != nil {
		return nil, err
	}
	cwd := bot
	if alias, ok := s.workspaceAlias(botID); ok {
		cwd = alias
	}
	timeout := args.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", args.Command)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = setEnv(cleanEnv(s.root, bot), "PWD", cwd)
	cmd.Env = setEnv(cmd.Env, "TMPDIR", filepath.Join(cwd, "tmp"))
	// Shell commands may launch grandchildren that inherit os/exec's output
	// pipes. Bound the post-exit drain so cancellation cannot wait forever on a
	// descendant that ignored the process-group signal.
	cmd.WaitDelay = 1500 * time.Millisecond
	stdout := &limitedBuffer{limit: MaxOutput}
	stderr := &limitedBuffer{limit: MaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start shell: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		select {
		case waitErr = <-done:
		case <-time.After(2 * time.Second):
			waitErr = errors.New("shell process did not exit after SIGKILL")
		}
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	result := map[string]any{
		"stdout": stdout.String(), "stderr": stderr.String(), "timed_out": timedOut,
		"duration_ms": time.Since(started).Milliseconds(), "truncated": stdout.truncated || stderr.truncated,
	}
	if timedOut || ctx.Err() != nil {
		result["exit_code"] = 137
	} else {
		result["exit_code"] = exitCode(waitErr)
	}
	return result, nil
}

func cleanEnv(root, bot string) []string {
	home := filepath.Join(root, "home")
	env := []string{
		"HOME=" + home,
		"PATH=" + filepath.Join(home, ".local", "bin") + ":" + filepath.Join(root, "shared", "bin") + ":/usr/local/bin:/usr/bin:/bin",
		// Keep user-level package managers pointed at the persistent shared
		// workspace. These values are intentionally shared by every Bot in the
		// VM; the system image remains read-only and no runtime is installed here.
		"NPM_CONFIG_PREFIX=" + filepath.Join(home, ".local"),
		"GOBIN=" + filepath.Join(home, ".local", "bin"),
		"CARGO_INSTALL_ROOT=" + filepath.Join(home, ".local"),
		"PWD=" + bot,
		"TMPDIR=" + filepath.Join(bot, "tmp"),
		"LANG=C.UTF-8", "TERM=dumb", "TOFI_WORKSPACE=" + bot,
	}
	if b, err := os.ReadFile(filepath.Join(root, guestTimezoneFile)); err == nil {
		if zone := strings.TrimSpace(string(b)); zone != "" {
			if _, loadErr := time.LoadLocation(zone); loadErr == nil {
				env = append(env, "TZ="+zone)
			}
		}
	}
	// User-configured values live in the shared VM and apply to newly started
	// processes only. Never inherit credentials from the host process.
	entries, _ := os.ReadDir(filepath.Join(root, ".tofi-env"))
	for _, entry := range entries {
		name := entry.Name()
		if !configuredEnvName(name) || !entry.Type().IsRegular() {
			continue
		}
		value, err := os.ReadFile(filepath.Join(root, ".tofi-env", name))
		if err != nil || len(value) > 65536 || strings.ContainsRune(string(value), 0) {
			continue
		}
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], name+"=") {
				env = append(env[:i], env[i+1:]...)
			}
		}
		env = append(env, name+"="+string(value))
	}
	sort.Strings(env)
	return env
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var x *exec.ExitError
	if errors.As(err, &x) {
		if ws, ok := x.Sys().(syscall.WaitStatus); ok {
			return ws.ExitStatus()
		}
	}
	return 1
}

func (s *Service) listFiles(ctx context.Context, botID, name string) ([]map[string]any, error) {
	p, err := s.resolvePath(botID, name, false)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	if len(entries) > MaxListEntries {
		entries = entries[:MaxListEntries]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"name": entry.Name(), "path": filepath.Join(name, entry.Name()), "directory": entry.IsDir(), "size": info.Size()})
	}
	return out, nil
}

func (s *Service) readFile(ctx context.Context, botID string, args fileArgs) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p, err := s.resolvePath(botID, args.Path, false)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data, err := readFileBytes(ctx, f, MaxFileBytes+1)
	if err != nil {
		return nil, err
	}
	fileHash := ""
	if info.Mode().IsRegular() && len(data) <= MaxFileBytes {
		sum := sha256.Sum256(data)
		fileHash = hex.EncodeToString(sum[:])
	}
	if args.Offset < 1 {
		args.Offset = 1
	}
	if args.Limit <= 0 || args.Limit > MaxFileBytes {
		args.Limit = MaxFileBytes
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), MaxFileBytes)
	line := 0
	var b strings.Builder
	truncated := false
	for scanner.Scan() {
		line++
		if line < args.Offset {
			continue
		}
		if line >= args.Offset+args.Limit {
			truncated = true
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if b.Len()+len(scanner.Bytes()) > MaxFileBytes {
			truncated = true
			break
		}
		b.Write(scanner.Bytes())
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	result := map[string]any{"path": args.Path, "content": b.String(), "bytes": b.Len(), "truncated": truncated}
	if fileHash != "" {
		result["sha256"] = fileHash
	} else {
		result["sha256_unavailable"] = true
	}
	return result, nil
}

func readFileBytes(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("invalid read limit")
	}
	buf := make([]byte, 0, minInt64(limit, 32*1024))
	chunk := make([]byte, 32*1024)
	for int64(len(buf)) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want := int64(len(chunk))
		if remain := limit - int64(len(buf)); remain < want {
			want = remain
		}
		n, err := r.Read(chunk[:want])
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func minInt64(a, b int64) int {
	if a < b {
		return int(a)
	}
	return int(b)
}

func (s *Service) writeFile(ctx context.Context, botID string, args fileArgs) (map[string]any, error) {
	if len(args.Content) > MaxFileBytes {
		return nil, errors.New("content exceeds file size limit")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	expected := strings.TrimSpace(args.ExpectedSHA256)
	if expected != "" {
		decoded, err := hex.DecodeString(expected)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("expected_sha256 must be a 64-character hexadecimal SHA-256")
		}
	}
	p, err := s.resolvePath(botID, args.Path, true)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0770); err != nil {
		return nil, err
	}
	target, atomic, err := resolveWriteTarget(s.root, p)
	if err != nil {
		return nil, err
	}
	lock := s.fileWriteLock(target)
	if err := lockFileWrite(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, exists, err := sha256IfRegular(ctx, target)
	if err != nil {
		return nil, err
	}
	if expected != "" && (!exists || !strings.EqualFold(expected, current)) {
		return nil, fmt.Errorf("%w: expected %s, current %s", errFileConflict, expected, current)
	}
	if args.Append {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
		if err != nil {
			return nil, err
		}
		n, writeErr := f.WriteString(args.Content)
		if writeErr == nil {
			writeErr = ctx.Err()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		newHash, _, err := sha256IfRegular(ctx, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": args.Path, "bytes": n, "sha256": newHash}, nil
	}
	if !atomic {
		// A dangling symlink cannot be atomically replaced without replacing the
		// link itself. Preserve the existing follow-symlink behavior under the
		// same path lock; existing targets use the atomic branch below.
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
		if err != nil {
			return nil, err
		}
		n, writeErr := f.WriteString(args.Content)
		if writeErr == nil {
			writeErr = ctx.Err()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		newHash, _, err := sha256IfRegular(ctx, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": args.Path, "bytes": n, "sha256": newHash}, nil
	}
	mode := os.FileMode(0660)
	if info, statErr := os.Stat(target); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("only regular files can be written")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tofi-write-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	n, writeErr := tmp.WriteString(args.Content)
	if writeErr == nil {
		writeErr = ctx.Err()
	}
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(tmpName, target); err != nil {
		return nil, err
	}
	newHash := sha256.Sum256([]byte(args.Content))
	return map[string]any{"path": args.Path, "bytes": n, "sha256": hex.EncodeToString(newHash[:])}, nil
}

func (s *Service) fileWriteLock(path string) *sync.Mutex {
	s.fileWriteMu.Lock()
	defer s.fileWriteMu.Unlock()
	if s.fileWriteLocks == nil {
		s.fileWriteLocks = make(map[string]*sync.Mutex)
	}
	if s.fileWriteLocks[path] == nil {
		s.fileWriteLocks[path] = &sync.Mutex{}
	}
	return s.fileWriteLocks[path]
}

func lockFileWrite(ctx context.Context, lock *sync.Mutex) error {
	if lock.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if lock.TryLock() {
				return nil
			}
		}
	}
}

func resolveWriteTarget(root, p string) (string, bool, error) {
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return p, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return p, true, nil
	}
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		// Keep follow-symlink behavior for a dangling link, but cannot use
		// rename without replacing the link itself.
		return p, false, nil
	}
	if !within(root, target) {
		return "", false, errors.New("path escapes guest workspace")
	}
	return target, true, nil
}

func sha256IfRegular(ctx context.Context, path string) (string, bool, error) {
	info, err := os.Stat(path) // follows an in-workspace symlink target
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, errors.New("only regular files can be written")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if readErr == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), true, nil
		}
		if readErr != nil {
			return "", false, readErr
		}
	}
}

func (s *Service) currentDesktop(ctx context.Context, botID string) (*desktop, error) {
	if !botIDPattern.MatchString(botID) {
		return nil, errors.New("bot_id must be a canonical UUID")
	}
	s.mu.Lock()
	d := s.desktopLocked(botID)
	if d != nil && d.ready != nil && !d.readyClosed {
		ready := d.ready
		s.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	if d == nil {
		return nil, errors.New("desktop is not running; call desktop.start first")
	}
	if d.startErr != nil {
		return nil, d.startErr
	}
	return d, nil
}

// desktopLocked resolves a logical Bot to the workspace graphical session.
// Callers must hold Service.mu. The map fallback keeps isolated/unit fixtures
// that construct a per-Bot desktop directly compatible.
func (s *Service) desktopLocked(botID string) *desktop {
	if s.sharedDesktop != nil {
		return s.sharedDesktop
	}
	return s.desktops[botID]
}

func (s *Service) removeDesktopAliasesLocked(d *desktop) {
	for botID, candidate := range s.desktops {
		if candidate == d {
			delete(s.desktops, botID)
		}
	}
	if s.sharedDesktop == d {
		s.sharedDesktop = nil
	}
}

func (s *Service) setCaptureSize(d *desktop, width, height int) {
	s.mu.Lock()
	d.captureW, d.captureH = width, height
	s.mu.Unlock()
}

func (s *Service) captureSize(d *desktop) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.captureW, d.captureH
}

func scaleClickCoordinates(x, y, captureW, captureH int) (int, int) {
	if captureW <= 0 || captureH <= 0 {
		return x, y
	}
	return x * 1280 / captureW, y * 800 / captureH
}

func (s *Service) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.closeRunner()
		s.closeOAuth()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.reaperStop)
		<-s.reaperDone
		s.mu.Lock()
		terms := make([]*terminal, 0, len(s.terminals))
		for _, t := range s.terminals {
			terms = append(terms, t)
		}
		all := make([]*desktop, 0, 1)
		cancels := make(map[*desktop]context.CancelFunc, 1)
		seen := make(map[*desktop]bool)
		for _, d := range s.desktops {
			if seen[d] {
				continue
			}
			seen[d] = true
			d.stopping = true
			cancels[d] = d.cancel
			all = append(all, d)
		}
		s.mu.Unlock()
		for _, t := range terms {
			t.mu.Lock()
			if !t.closed {
				t.closed = true
				t.closeReason = "vm_shutdown"
				t.closedAt = time.Now()
				if t.cmd != nil && t.cmd.Process != nil {
					_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
				}
				if t.pty != nil {
					_ = t.pty.Close()
				}
			}
			t.mu.Unlock()
		}
		var wg sync.WaitGroup
		results := make(chan error, len(all))
		for _, d := range all {
			if cancel := cancels[d]; cancel != nil {
				cancel()
			}
			wg.Add(1)
			go func(d *desktop) {
				defer wg.Done()
				// Shutdown owns its cleanup deadline; a disconnected HTTP caller
				// must not leave Chrome with an unflushed profile.
				results <- s.requestDesktopStop(context.Background(), d)
			}(d)
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil && s.closeErr == nil {
				s.closeErr = err
			}
		}
	})
	return s.closeErr
}

type limitedBuffer struct {
	mu        sync.Mutex
	b         bytes.Buffer
	limit     int
	truncated bool
	tail      bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.limit = MaxOutput
	}
	if b.tail {
		if len(p) >= b.limit {
			b.b.Reset()
			_, _ = b.b.Write(p[len(p)-b.limit:])
			b.truncated = true
			return len(p), nil
		}
		if overflow := b.b.Len() + len(p) - b.limit; overflow > 0 {
			current := b.b.Bytes()
			b.b.Reset()
			_, _ = b.b.Write(current[overflow:])
			b.truncated = true
		}
		return b.b.Write(p)
	}
	left := b.limit - b.b.Len()
	if left <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > left {
		_, _ = b.b.Write(p[:left])
		b.truncated = true
		return len(p), nil
	}
	return b.b.Write(p)
}
func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// freePort asks the kernel for a loopback port used only by this guest's
// browser CDP endpoint. It is not exposed through the vsock listener.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("browser URL must be an http or https URL")
	}
	return u, nil
}

func chromeHTTP(ctx context.Context, d *desktop, method, path string) ([]byte, error) {
	if d.remotePort <= 0 {
		return nil, errors.New("browser debugging endpoint is unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", d.remotePort, path), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("browser returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func asDataPNG(p []byte) (map[string]any, error) {
	if len(p) == 0 || len(p) > MaxScreenshot {
		return nil, errors.New("screenshot exceeds 1 MiB result limit")
	}
	encoded := base64.StdEncoding.EncodeToString(p)
	imageURL := "data:image/png;base64," + encoded
	if len(imageURL) > 1<<20 {
		return nil, errors.New("screenshot exceeds 1 MiB result limit")
	}
	config, err := png.DecodeConfig(bytes.NewReader(p))
	if err != nil {
		return nil, fmt.Errorf("invalid PNG screenshot: %w", err)
	}
	return map[string]any{"image_url": imageURL, "width": config.Width, "height": config.Height}, nil
}

var _ io.Writer = (*limitedBuffer)(nil)

func configuredEnvName(name string) bool {
	if len(name) == 0 || len(name) > 128 || (name[0] != '_' && (name[0] < 'A' || name[0] > 'Z')) || !validEnvKey(name) || strings.HasPrefix(name, "LD_") {
		return false
	}
	switch name {
	case "PATH", "HOME", "BASH_ENV", "ENV", "DISPLAY", "TOFI_WORKSPACE", "PWD", "TMPDIR":
		return false
	}
	return true
}
