package mcprunner

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Per-account disk quota. The Runner's data filesystem is shared, so one account
// that fills it starves every other account and the App. A plain uid cannot be
// given a size on an ordinary filesystem without quota support the container
// cannot count on, so the Runner measures instead:
//
//   - before an install and before a plugin starts, the account's tree must be
//     below its quota (the measurement may be a few seconds old);
//   - every DiskInterval the Runner measures each open account. An account over
//     its quota loses the contents of its tmp/ first; if it is still over, its
//     processes are killed and nothing of it starts or installs until it is
//     under the quota again (removing a plugin is always possible);
//   - files that were unlinked but are still open count as well: each sweep adds
//     the size of every "(deleted)" file in /proc/<pid>/fd of the account's
//     processes (killing the processes is what frees them);
//   - below the host free-space floor (MinFreeBytes) the sweep kills the processes
//     of the account that uses the most, and starts and installs are refused;
//   - one file cannot grow past RLIMIT_FSIZE (limits.go), and an install is
//     refused when the whole filesystem is nearly full (MinFreeBytes).
//
// The bound is therefore quota + (write rate x DiskInterval), not a hard
// kernel limit. A hard limit needs a quota-capable filesystem or a size-capped
// volume per account; see "Residual risks" in docs/decisions/0071-runner-container.md.

// ErrOverQuota is returned when an account's storage is over its quota.
var ErrOverQuota = errors.New("account storage quota exceeded")

// quotaMeter caches the measured size of one account's tree.
type quotaMeter struct {
	mu    sync.Mutex
	dir   string
	limit int64
	usage int64
	at    time.Time
	over  bool
	// held adds the bytes of files the account's processes hold open after
	// unlinking them; they are in no directory, so a tree walk cannot see them.
	held func() int64
}

// quotaFreshness is how old a measurement may be before an install or start
// measures again.
const quotaFreshness = 5 * time.Second

func newQuotaMeter(dir string, limit uint64) *quotaMeter {
	if limit == 0 {
		return nil
	}
	return &quotaMeter{dir: dir, limit: int64(limit)}
}

// measure walks the tree now.
func (m *quotaMeter) measure() (int64, error) {
	usage, err := treeBytes(m.dir)
	if err != nil {
		return 0, err
	}
	if m.held != nil {
		usage += m.held()
	}
	m.mu.Lock()
	m.usage, m.at, m.over = usage, time.Now(), usage >= m.limit
	m.mu.Unlock()
	return usage, nil
}

// admit is the Runner's Options.Admit for the account.
func (m *quotaMeter) admit() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	fresh := !m.at.IsZero() && time.Since(m.at) < quotaFreshness
	over := m.over
	m.mu.Unlock()
	if !fresh {
		if _, err := m.measure(); err != nil {
			return nil // an unreadable tree is not a reason to refuse; the sweep logs it
		}
		m.mu.Lock()
		over = m.over
		m.mu.Unlock()
	}
	if over {
		return ErrOverQuota
	}
	return nil
}

// sweepQuota measures every open account once and acts on those over quota. It
// returns the ids of the accounts it found over quota.
func (h *Hub) sweepQuota() []string {
	h.mu.Lock()
	tenants := make([]*tenant, 0, len(h.tenants))
	for _, t := range h.tenants {
		tenants = append(tenants, t)
	}
	h.mu.Unlock()
	var over []string
	h.enforceHostFloor(tenants)
	for _, t := range tenants {
		if t.quota == nil {
			continue
		}
		usage, err := t.quota.measure()
		if err != nil {
			log.Printf("hub: quota measurement of account %s failed: %v", t.id, err)
			continue
		}
		if usage < t.quota.limit {
			continue
		}
		over = append(over, t.id)
		log.Printf("hub: account %s uses %d bytes, over its quota of %d: clearing tmp/", t.id, usage, t.quota.limit)
		clearTmp(t.dir)
		if usage, err = t.quota.measure(); err == nil && usage < t.quota.limit {
			continue
		}
		log.Printf("hub: account %s is still over its quota: stopping its processes; installs and starts are refused until it is under", t.id)
		if t.uid > 0 {
			killAllOf(t.uid)
		}
		if t.sandbox != nil {
			t.sandbox.killAll()
		}
	}
	return over
}

// clearTmp empties <account>/tmp through an anchored root, never following a link.
func clearTmp(accountDir string) {
	top, err := openDirRoot(accountDir)
	if err != nil {
		return
	}
	defer top.Close()
	tmp, err := top.OpenRoot("tmp")
	if err != nil {
		return
	}
	defer tmp.Close()
	dir, err := tmp.Open(".")
	if err != nil {
		return
	}
	names, _ := dir.Readdirnames(-1)
	dir.Close()
	for _, name := range names {
		if err := tmp.RemoveAll(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("hub: could not clear tmp/%s: %v", name, err)
		}
	}
}

// quotaLoop runs sweepQuota every DiskInterval.
func (h *Hub) quotaLoop() {
	defer h.stopped.Done()
	ticker := time.NewTicker(h.cfg.Sandbox.DiskInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.sweepQuota()
		}
	}
}

// accountPIDs lists the processes of the account: those of its uid and those in
// its cgroup.
func (t *tenant) accountPIDs() []int {
	seen := map[int]bool{}
	var pids []int
	add := func(pid int) {
		if pid > 1 && !seen[pid] {
			seen[pid] = true
			pids = append(pids, pid)
		}
	}
	if t.uid > 0 {
		for _, pid := range processesOf(t.uid) {
			add(pid)
		}
	}
	if t.sandbox != nil && t.sandbox.cgroupProcs != "" {
		if b, err := os.ReadFile(t.sandbox.cgroupProcs); err == nil {
			for _, f := range strings.Fields(string(b)) {
				if pid, err := strconv.Atoi(f); err == nil {
					add(pid)
				}
			}
		}
	}
	return pids
}

// deletedOpenBytes sums the allocated bytes of the files that the given
// processes hold open although no directory entry names them any more, each
// inode once. A file that is still linked is in the tree walk already.
func deletedOpenBytes(pids []int) int64 {
	var total int64
	seen := map[[2]uint64]bool{}
	for _, pid := range pids {
		fdDir := filepath.Join(procDir, strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			link := filepath.Join(fdDir, e.Name())
			target, err := os.Readlink(link)
			if err != nil || !strings.HasSuffix(target, " (deleted)") {
				continue
			}
			info, err := os.Stat(link) // through the descriptor: the inode itself
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				total += info.Size()
				continue
			}
			key := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			if seen[key] {
				continue
			}
			seen[key] = true
			total += int64(st.Blocks) * 512
		}
	}
	return total
}

// hostLow reports whether the data filesystem is below the free-space floor. A
// failed reading counts as low: an unknown disk is not a reason to admit work.
func (h *Hub) hostLow() bool {
	return checkFreeSpace(h.cfg.Root, h.cfg.MinFreeBytes) != nil
}

// admitHost is part of every account's Options.Admit.
func (h *Hub) admitHost() error {
	if err := checkFreeSpace(h.cfg.Root, h.cfg.MinFreeBytes); err != nil {
		if errors.Is(err, ErrLowDisk) {
			return err
		}
		return ErrLowDisk
	}
	return nil
}

// enforceHostFloor stops the processes of the account that uses the most when
// the data filesystem is below the floor. It returns the id it stopped.
func (h *Hub) enforceHostFloor(tenants []*tenant) string {
	if h.cfg.MinFreeBytes == 0 {
		return ""
	}
	free, err := freeBytes(h.cfg.Root)
	if err != nil || free >= h.cfg.MinFreeBytes {
		return "" // an unreadable disk refuses starts (admitHost) but kills nobody
	}
	var worst *tenant
	var worstUsage int64 = -1
	for _, t := range tenants {
		if t.quota == nil {
			continue
		}
		t.quota.mu.Lock()
		u := t.quota.usage
		t.quota.mu.Unlock()
		if fresh, err := t.quota.measure(); err == nil {
			u = fresh
		}
		if u > worstUsage {
			worst, worstUsage = t, u
		}
	}
	if worst == nil {
		return ""
	}
	log.Printf("hub: only %d bytes are free on the data filesystem (floor %d): stopping the processes of account %s, the largest at %d bytes", free, h.cfg.MinFreeBytes, worst.id, worstUsage)
	if worst.uid > 0 {
		killAllOf(worst.uid)
	}
	if worst.sandbox != nil {
		worst.sandbox.killAll()
	}
	return worst.id
}
