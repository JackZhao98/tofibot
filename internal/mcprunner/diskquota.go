package mcprunner

import (
	"errors"
	"log"
	"os"
	"sync"
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
