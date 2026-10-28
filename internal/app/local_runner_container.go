package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The local MCP Runner runs in its own container, outside every computer, with
// one namespace per account (docs/decisions/0071-runner-container.md). This file
// is the account side of it: which Runner an account talks to, and the one-time
// copy of the state an older release kept inside the guest.

const (
	// guestRunnerDir is where earlier releases kept the Runner inside the guest.
	guestRunnerDir = "/workspace/shared/.tofi/runner"
	// Per-file and per-listing limits for the copy; a connection's data is small.
	guestCopyMaxFile    = 4 << 20
	guestCopyMaxEntries = 400
	guestCopyMaxDepth   = 8

	runnerMigrationAttempt = 3 * time.Minute
)

// runnerMigrationInterval is how long the copier waits after a failed attempt.
var runnerMigrationInterval = 10 * time.Minute

var errNoContainerRunner = errors.New("local MCP Runner is not configured")

// localRunnerState is the account's record of which Runner serves it. Absent
// means the guest Runner (or nothing) until the copy has been verified.
type localRunnerState struct {
	State    string   `json:"state"` // "container"
	At       int64    `json:"at"`
	Plugins  []string `json:"plugins,omitempty"`
	Kept     []string `json:"kept_existing,omitempty"`
	Attached bool     `json:"attached"`
	// GuestDigests is what the computer's Runner held (manifest and every file
	// copied, as SHA-256 digests, no content) when the account switched. A later
	// start compares the computer against it: a rollback to an older release
	// resumes the guest Runner, which then diverges from the container's copy.
	GuestDigests map[string]string `json:"guest_digests,omitempty"`
	// Conflict lists plugins whose copy on the computer differs from the one the
	// container was seeded with. Both copies are kept; nothing is overwritten.
	Conflict   []string `json:"conflict,omitempty"`
	ConflictAt int64    `json:"conflict_at,omitempty"`
}

type localRunnerMigration struct {
	mu       sync.Mutex
	path     string
	state    localRunnerState
	switched atomic.Bool
	cancel   context.CancelFunc
	done     chan struct{}
	// guest is replaced in tests; nil means the account's computer.
	guest guestRunnerFiles
}

// initLocalRunner is called once from NewServer with the account's Config.
func (s *Server) initLocalRunner(c Config) {
	s.localRunnerTokenFn = c.LocalRunnerTokenFunc
	if c.DataDir == "" {
		return
	}
	m := &s.runnerMigration
	m.path = filepath.Join(c.DataDir, "local-runner.json")
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var st localRunnerState
	if json.Unmarshal(data, &st) == nil && st.State == "container" {
		m.state = st
		m.switched.Store(true)
	}
}

func (m *localRunnerMigration) persistLocked() error {
	if m.path == "" {
		return nil
	}
	data, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// containerRunnerConfigured reports whether the gateway gave this account a
// namespace on the Runner container.
func (s *Server) containerRunnerConfigured() bool {
	return s.isolatedWorkspace && s.localRunnerURL != "" && s.localRunnerTokenFn != nil
}

// containerRunnerActive reports whether this account's local connections are
// served by the Runner container. With no computer there is no other Runner.
// With a computer, the guest Runner stays in use until its state has been
// copied out and verified.
func (s *Server) containerRunnerActive() bool {
	return s.containerRunnerConfigured() && (s.microVM == nil || s.runnerMigration.switched.Load())
}

// containerRunnerConfig returns the base URL (already inside the account's
// namespace) and the account's bearer token.
func (s *Server) containerRunnerConfig() (string, string, error) {
	if !s.containerRunnerConfigured() {
		return "", "", errNoContainerRunner
	}
	base := strings.TrimRight(s.localRunnerURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || !strings.Contains(u.Path, mcprunner.AccountPathPrefix) {
		return "", "", errors.New("invalid local MCP Runner URL")
	}
	token, err := s.localRunnerTokenFn()
	if err != nil || strings.TrimSpace(token) == "" {
		return "", "", errors.New("local MCP Runner is not ready")
	}
	return base, strings.TrimSpace(token), nil
}

// ---- reading the guest ----------------------------------------------------

type guestDirEntry struct {
	Name      string
	Directory bool
}

// guestRunnerFiles reads files of the old in-guest Runner. The production
// implementation goes through the computer's file actions; tests use a map.
type guestRunnerFiles interface {
	// ReadFile returns a whole regular file; errGuestMissing if it does not exist.
	ReadFile(ctx context.Context, path string, limit int64) ([]byte, error)
	// ListDir lists one directory; errGuestMissing if it does not exist.
	ListDir(ctx context.Context, dir string) ([]guestDirEntry, error)
}

var errGuestMissing = errors.New("not found on the computer")

type computerGuestFiles struct{ client *computer.Client }

func (g computerGuestFiles) action(ctx context.Context, name string, args any) (json.RawMessage, error) {
	raw, _ := json.Marshal(args)
	res, err := g.client.Action(ctx, computer.Action{BotID: preferenceSyncBotID, RunID: "runner-migration", Source: "human", Name: name, Args: raw})
	if err != nil {
		var coded *tooloutcome.Error
		if errors.As(err, &coded) && coded.Outcome.Code == tooloutcome.FileNotFound {
			return nil, errGuestMissing
		}
		if strings.Contains(err.Error(), "no such file or directory") || strings.Contains(err.Error(), "file_not_found") {
			return nil, errGuestMissing
		}
		return nil, err
	}
	return res.Result, nil
}

func (g computerGuestFiles) ListDir(ctx context.Context, dir string) ([]guestDirEntry, error) {
	out, err := g.action(ctx, "files.list", map[string]string{"path": dir})
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name      string `json:"name"`
		Directory bool   `json:"directory"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	list := make([]guestDirEntry, 0, len(entries))
	for _, e := range entries {
		list = append(list, guestDirEntry{Name: e.Name, Directory: e.Directory})
	}
	return list, nil
}

func (g computerGuestFiles) ReadFile(ctx context.Context, file string, limit int64) ([]byte, error) {
	var data []byte
	var version string
	var offset int64
	for {
		out, err := g.action(ctx, "files.export_chunk", map[string]any{"path": file, "offset": offset, "limit": 256 << 10, "version": version})
		if err != nil {
			return nil, err
		}
		var chunk struct {
			Data    string `json:"data_base64"`
			Next    int64  `json:"next_offset"`
			Size    int64  `json:"size"`
			EOF     bool   `json:"eof"`
			Version string `json:"version"`
		}
		if json.Unmarshal(out, &chunk) != nil || chunk.Size > limit || chunk.Next < offset {
			return nil, errors.New("invalid or oversized computer file")
		}
		b, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || int64(len(b)) != chunk.Next-offset {
			return nil, errors.New("invalid computer file chunk")
		}
		data = append(data, b...)
		version, offset = chunk.Version, chunk.Next
		if chunk.EOF {
			return data, nil
		}
	}
}

func (s *Server) guestRunnerFiles() guestRunnerFiles {
	if s.runnerMigration.guest != nil {
		return s.runnerMigration.guest
	}
	return computerGuestFiles{client: s.microVM}
}

// ---- the snapshot -----------------------------------------------------------

type runnerSnapshot struct {
	plugins []mcprunner.ImportPlugin
	// digests lets two snapshots be compared: a guest that changed while we
	// copied is copied again.
	digests map[string]string
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func importFileOf(rel string, data []byte) mcprunner.ImportFile {
	return mcprunner.ImportFile{Path: rel, SHA256: digestOf(data), DataBase64: base64.StdEncoding.EncodeToString(data)}
}

// snapshotGuestRunner reads the old Runner's manifest and the data of every
// plugin in it. found is false when the guest has no Runner state at all.
func snapshotGuestRunner(ctx context.Context, g guestRunnerFiles) (snap runnerSnapshot, found bool, err error) {
	manifest, err := g.ReadFile(ctx, guestRunnerDir+"/manifest.json", 1<<20)
	if errors.Is(err, errGuestMissing) {
		return runnerSnapshot{}, false, nil
	}
	if err != nil {
		return runnerSnapshot{}, false, err
	}
	records, err := mcprunner.ParseRecords(manifest)
	if err != nil {
		return runnerSnapshot{}, false, fmt.Errorf("the computer's Runner manifest is unreadable: %w", err)
	}
	snap.digests = map[string]string{"manifest": digestOf(manifest)}
	sort.Slice(records, func(i, j int) bool { return records[i].Spec.ID < records[j].Spec.ID })
	for _, rec := range records {
		id := rec.Spec.ID
		if !localMCPID(id) || rec.Request.ID != id || (rec.Request.Kind != "builtin_gog" && rec.Request.Kind != "npm" && rec.Request.Kind != "pypi") {
			return runnerSnapshot{}, false, fmt.Errorf("the computer's Runner lists an unsupported plugin %q", id)
		}
		dir := guestRunnerDir + "/plugins/" + id
		plugin := mcprunner.ImportPlugin{Request: rec.Request}
		plugin.Request.Env = nil
		add := func(rel string, data []byte) {
			plugin.Files = append(plugin.Files, importFileOf(rel, data))
			snap.digests[id+"/"+rel] = digestOf(data)
		}
		if rec.Request.Kind == "builtin_gog" {
			for _, name := range []string{"keyring-password", "gog-account.json"} {
				data, err := g.ReadFile(ctx, dir+"/"+name, guestCopyMaxFile)
				if errors.Is(err, errGuestMissing) {
					continue // a Google account that was never connected has no account file
				}
				if err != nil {
					return runnerSnapshot{}, false, err
				}
				add(name, data)
			}
		} else {
			// The environment a person gave the package. The manifest says where each value is
			// kept; only a file inside this plugin's own secrets folder is read.
			keys := make([]string, 0, len(rec.Spec.SecretEnv))
			for key := range rec.Spec.SecretEnv {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				file := rec.Spec.SecretEnv[key]
				if path.Dir(path.Clean(file)) != dir+"/secrets" || path.Base(file) != key {
					return runnerSnapshot{}, false, fmt.Errorf("plugin %q keeps a secret outside its own folder", id)
				}
				data, err := g.ReadFile(ctx, file, 16<<10)
				if err != nil {
					return runnerSnapshot{}, false, err
				}
				if plugin.Request.Env == nil {
					plugin.Request.Env = map[string]string{}
				}
				plugin.Request.Env[key] = string(data)
				snap.digests[id+"/env/"+key] = digestOf(data)
			}
		}
		budget := guestCopyMaxEntries
		if err := collectGuestTree(ctx, g, dir+"/home", "home", 0, &budget, add); err != nil {
			return runnerSnapshot{}, false, err
		}
		if rec.Request.Kind == "builtin_gog" {
			// The stored Google token is encrypted under the keyring password. Data without
			// its password is useless in the container and the guest's copy is the only
			// one that could still be repaired, so the account does not switch.
			hasKeyring, hasHome := false, false
			for _, f := range plugin.Files {
				hasKeyring = hasKeyring || f.Path == "keyring-password"
				hasHome = hasHome || strings.HasPrefix(f.Path, "home/")
			}
			if hasHome && !hasKeyring {
				return runnerSnapshot{}, false, fmt.Errorf("plugin %q has Google data on the computer but no keyring password; not switching", id)
			}
		}
		snap.plugins = append(snap.plugins, plugin)
	}
	return snap, true, nil
}

func collectGuestTree(ctx context.Context, g guestRunnerFiles, dir, rel string, depth int, budget *int, add func(string, []byte)) error {
	if depth > guestCopyMaxDepth {
		return errors.New("plugin data is nested too deeply to copy")
	}
	entries, err := g.ListDir(ctx, dir)
	if errors.Is(err, errGuestMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		if *budget--; *budget < 0 {
			return errors.New("plugin data has too many files to copy")
		}
		if e.Name == "" || strings.ContainsAny(e.Name, "/\x00") || e.Name == "." || e.Name == ".." {
			return errors.New("plugin data has an unusable file name")
		}
		if e.Directory {
			if err := collectGuestTree(ctx, g, dir+"/"+e.Name, rel+"/"+e.Name, depth+1, budget, add); err != nil {
				return err
			}
			continue
		}
		data, err := g.ReadFile(ctx, dir+"/"+e.Name, guestCopyMaxFile)
		if err != nil {
			return err
		}
		add(rel+"/"+e.Name, data)
	}
	return nil
}

func sameDigests(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ---- the copy ---------------------------------------------------------------

type runnerMigrationReport struct {
	// Copied are plugins now present in the container with the guest's data.
	Copied []string
	// Kept are plugins the container already had with different data; the container's copy was not overwritten.
	Kept []string
	// Nothing is true when the guest had no Runner state to copy.
	Nothing bool
}

func (s *Server) postRunnerImport(ctx context.Context, base, token string, plugins []mcprunner.ImportPlugin) ([]mcprunner.ImportResult, error) {
	data, status, err := s.runnerRequestTo(ctx, base, token, http.MethodPost, "/v1/import", mcprunner.ImportRequest{Plugins: plugins})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("the Runner container refused the copy (%d)", status)
	}
	var out struct {
		Results []mcprunner.ImportResult `json:"results"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil, errors.New("invalid Runner container response")
	}
	return out.Results, nil
}

func (s *Server) runnerContainerHas(ctx context.Context, base, token string, ids []string) error {
	data, status, err := s.runnerRequestTo(ctx, base, token, http.MethodGet, "/v1/plugins", nil)
	if err != nil || status != http.StatusOK {
		return errors.New("the Runner container cannot list its plugins")
	}
	var out struct {
		Plugins []mcprunner.Status `json:"plugins"`
	}
	if json.Unmarshal(data, &out) != nil {
		return errors.New("invalid Runner container response")
	}
	have := map[string]bool{}
	for _, p := range out.Plugins {
		have[p.ID] = true
	}
	for _, id := range ids {
		if !have[id] {
			return fmt.Errorf("plugin %q is missing from the Runner container after the copy", id)
		}
	}
	return nil
}

// migrateGuestRunner copies the in-guest Runner's plugins and their data into
// this account's namespace on the Runner container, verifies the copy, and only
// then switches the account over. It is idempotent and never deletes anything on
// the computer.
func (s *Server) migrateGuestRunner(ctx context.Context) (runnerMigrationReport, error) {
	var report runnerMigrationReport
	g := s.guestRunnerFiles()
	first, found, err := snapshotGuestRunner(ctx, g)
	if err != nil {
		return report, err
	}
	if !found || len(first.plugins) == 0 {
		report.Nothing = true
		return report, s.switchToContainerRunner(ctx, nil, nil, first.digests)
	}
	base, token, err := s.containerRunnerConfig()
	if err != nil {
		return report, err
	}
	results, err := s.postRunnerImport(ctx, base, token, first.plugins)
	if err != nil {
		return report, err
	}
	if len(results) != len(first.plugins) {
		return report, errors.New("the Runner container answered for a different set of plugins")
	}
	for i, res := range results {
		if res.ID != first.plugins[i].Request.ID {
			return report, errors.New("the Runner container answered for a different set of plugins")
		}
		switch {
		case res.State == mcprunner.ImportInstalled && res.Identical:
			report.Copied = append(report.Copied, res.ID)
		case res.State == mcprunner.ImportPresent && res.Identical:
			report.Copied = append(report.Copied, res.ID)
		case res.State == mcprunner.ImportPresent:
			report.Kept = append(report.Kept, res.ID)
		default:
			return report, fmt.Errorf("plugin %q could not be copied: %s", res.ID, res.Error)
		}
	}
	ids := append(append([]string(nil), report.Copied...), report.Kept...)
	if err := s.runnerContainerHas(ctx, base, token, ids); err != nil {
		return report, err
	}
	// The computer kept running while we copied. Read it again: if anything
	// changed, this pass proves nothing and the next one starts over.
	second, _, err := snapshotGuestRunner(ctx, g)
	if err != nil {
		return report, err
	}
	if !sameDigests(first.digests, second.digests) {
		return report, errors.New("the computer's Runner state changed while it was copied")
	}
	return report, s.switchToContainerRunner(ctx, ids, report.Kept, first.digests)
}

// switchToContainerRunner records the verified copy, points the account at the
// container, and re-attaches every local connection to it.
func (s *Server) switchToContainerRunner(ctx context.Context, ids, kept []string, guestDigests map[string]string) error {
	m := &s.runnerMigration
	m.mu.Lock()
	m.state = localRunnerState{State: "container", At: time.Now().Unix(), Plugins: ids, Kept: kept, GuestDigests: guestDigests}
	err := m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("could not record the Runner switch: %w", err)
	}
	m.switched.Store(true)
	s.gogStatus.mu.Lock()
	s.gogStatus.checked = time.Time{}
	s.gogStatus.mu.Unlock()
	return s.attachMigratedConnections(ctx)
}

// checkGuestDrift compares the computer's Runner with what it held when the
// account switched. A difference means the guest Runner was used after the
// switch (typically after rolling back to a release that predates the container
// and upgrading again). Nothing is imported and nothing on either side is
// changed: the container's copy is the one in use, the computer's copy stays
// where it is, and the plugins that differ are recorded as a conflict for the
// owner to resolve. It reads only.
func (s *Server) checkGuestDrift(ctx context.Context) ([]string, error) {
	m := &s.runnerMigration
	m.mu.Lock()
	recorded := m.state.GuestDigests
	m.mu.Unlock()
	if len(recorded) == 0 {
		return nil, nil // switched by a release that did not record digests
	}
	snap, found, err := snapshotGuestRunner(ctx, s.guestRunnerFiles())
	if err != nil {
		return nil, err
	}
	var differing []string
	if found {
		seen := map[string]bool{}
		for key, digest := range snap.digests {
			if recorded[key] != digest {
				seen[strings.SplitN(key, "/", 2)[0]] = true
			}
		}
		for key := range recorded {
			if _, ok := snap.digests[key]; !ok {
				seen[strings.SplitN(key, "/", 2)[0]] = true
			}
		}
		for id := range seen {
			differing = append(differing, id)
		}
		sort.Strings(differing)
	}
	m.mu.Lock()
	changed := strings.Join(m.state.Conflict, ",") != strings.Join(differing, ",")
	m.state.Conflict = differing
	if len(differing) > 0 && changed {
		m.state.ConflictAt = time.Now().Unix()
	}
	if len(differing) == 0 {
		m.state.ConflictAt = 0
	}
	var perr error
	if changed {
		perr = m.persistLocked()
	}
	m.mu.Unlock()
	if len(differing) > 0 {
		log.Printf("[runner] the computer's Runner changed after the switch (%s): both copies are kept and the container's copy stays in use", strings.Join(differing, ", "))
	}
	return differing, perr
}

// RunnerConflicts lists the plugins whose copy on the computer differs from the
// one the container was seeded with (both are kept).
func (s *Server) RunnerConflicts() []string {
	m := &s.runnerMigration
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.state.Conflict...)
}

// attachMigratedConnections points every existing local_<id> connection at the
// container. A connection the person never attached stays unattached.
func (s *Server) attachMigratedConnections(ctx context.Context) error {
	m := &s.runnerMigration
	m.mu.Lock()
	ids := append([]string(nil), m.state.Plugins...)
	attached := m.state.Attached
	m.mu.Unlock()
	if attached {
		return nil
	}
	views, err := s.extensions.ListMCP()
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, v := range views {
		existing[v.Name] = true
	}
	var failed []string
	for _, id := range ids {
		if !existing["local_"+id] {
			continue
		}
		if err := s.attachLocalMCP(id); err != nil {
			failed = append(failed, id)
			log.Printf("[runner] could not attach %s to the Runner container: %v", id, err)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not re-attach %s to the Runner container", strings.Join(failed, ", "))
	}
	m.mu.Lock()
	m.state.Attached = true
	err = m.persistLocked()
	m.mu.Unlock()
	if err == nil {
		_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
	}
	return err
}

// referencesGuestRunner reports whether a saved connection still points at the
// Runner inside the computer.
func (s *Server) referencesGuestRunner() bool {
	if s.extensions == nil {
		return false
	}
	views, err := s.extensions.ListMCP()
	if err != nil {
		return false
	}
	for _, v := range views {
		if strings.HasPrefix(v.Name, "local_") && strings.HasPrefix(v.URL, computer.RunnerOrigin) {
			return true
		}
	}
	return false
}

// runnerMigrationDue decides whether to touch the computer now. A person with a
// saved connection gets the copy at once (that may wake the computer). Others
// are checked only while the computer is already running, so the copier never
// keeps a sleeping computer awake.
func (s *Server) runnerMigrationDue(ctx context.Context) bool {
	if s.runnerMigration.guest != nil || s.referencesGuestRunner() {
		return true
	}
	info, err, _ := s.cachedMicroVMInfo(ctx)
	return err == nil && info.State == "ready"
}

func (s *Server) startLocalRunnerMigration() {
	m := &s.runnerMigration
	if !s.containerRunnerConfigured() || s.microVM == nil && m.guest == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.done = cancel, make(chan struct{})
	go func() {
		defer close(m.done)
		wait := 5 * time.Second
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = runnerMigrationInterval
			if m.switched.Load() {
				// A switch whose re-attach failed is finished here.
				m.mu.Lock()
				pending := !m.state.Attached
				m.mu.Unlock()
				if pending {
					attemptCtx, done := context.WithTimeout(ctx, runnerMigrationAttempt)
					err := s.attachMigratedConnections(attemptCtx)
					done()
					if err != nil {
						continue
					}
				}
				// Once per start: has the computer's Runner moved on since the switch?
				// Only while the computer is already up; this never wakes it.
				if !s.runnerMigrationDue(ctx) {
					continue
				}
				attemptCtx, done := context.WithTimeout(ctx, runnerMigrationAttempt)
				_, err := s.checkGuestDrift(attemptCtx)
				done()
				if err != nil {
					log.Printf("[runner] comparing the computer's Runner with the container's copy will be retried: %v", err)
					continue
				}
				return
			}
			if !s.runnerMigrationDue(ctx) {
				continue
			}
			attemptCtx, done := context.WithTimeout(ctx, runnerMigrationAttempt)
			report, err := s.migrateGuestRunner(attemptCtx)
			done()
			if err != nil {
				log.Printf("[runner] copying the computer's Runner state will be retried: %v", err)
				continue
			}
			log.Printf("[runner] account Runner moved to the container: %d copied, %d kept, nothing=%v", len(report.Copied), len(report.Kept), report.Nothing)
			return
		}
	}()
}

func (s *Server) stopLocalRunnerMigration() {
	m := &s.runnerMigration
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel = nil
	}
}
