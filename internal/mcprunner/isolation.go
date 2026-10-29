package mcprunner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Per-account isolation primitives for the multi-account Runner. See
// docs/decisions/0071-runner-container.md for the threat model.

// AccountPathPrefix is the URL prefix of one account's namespace. Everything an
// account can reach lives below /a/<account id>/.
const AccountPathPrefix = "/a/"

var accountIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// ValidAccountID accepts the identifiers the gateway uses (a canonical UUID or
// the fixed legacy owner id) and nothing that could name a path element.
func ValidAccountID(id string) bool { return accountIDPattern.MatchString(id) }

// AccountToken is the bearer token of one account. The gateway and the Runner
// derive it from the shared master key, so a token proves one account only:
// knowing the token of account A reveals nothing about the token of account B
// (HMAC under a key neither holds), and the Runner stores no per-account secret.
func AccountToken(master []byte, accountID string) string {
	return AccountTokenAt(master, accountID, 0)
}

// AccountTokenAt is the token of one account at a token epoch. Epoch 0 is the
// original derivation; a higher epoch mixes the number into the MAC, so raising
// one account's epoch in the Runner's registry revokes every token issued for it
// before, and no other account's. The epoch is not secret.
func AccountTokenAt(master []byte, accountID string, epoch int) string {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("tofi-mcp-runner/v1/account\x00"))
	mac.Write([]byte(accountID))
	if epoch > 0 {
		mac.Write([]byte("\x00epoch\x00" + strconv.Itoa(epoch)))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// MinMasterKeyBytes is the shortest master key the Runner accepts.
const MinMasterKeyBytes = 32

// MaxUID is the first uid the Runner never hands out. 65534 is "nobody", and a
// Docker userns-remap or rootless daemon maps only 65536 uids (0..65535) into the
// container, so an account uid above that range could not be set at all.
const MaxUID = 65534

// uidRegistry hands every account its own numeric uid and never reuses one, so a
// deleted account's leftovers can never belong to a new account.
//
// The registry file is a cache of what the directories already say. The owner of
// an account's tmp/ directory is authoritative: on start the registry has to
// agree with it for every account that exists on disk, and an account where it
// does not (a lost, restored-from-backup or edited uids.json) is refused rather
// than repaired, because guessing would hand one account another's files.
type uidRegistry struct {
	mu       sync.Mutex
	path     string
	accounts string // <root>/accounts, scanned for directory owners
	base     int
	limit    int
	next     int
	ids      map[string]int
	purged   map[string]int64 // tombstones: account id -> unix time of the purge
	epochs   map[string]int   // token epochs; absent means 0
	refused  map[string]string
}

type uidFile struct {
	Next   int              `json:"next"`
	IDs    map[string]int   `json:"ids"`
	Purged map[string]int64 `json:"purged,omitempty"`
	Epochs map[string]int   `json:"epochs,omitempty"`
}

// ownerOf reports the uid that owns a path (without following a link). Tests
// replace it where chown is faked.
var ownerOf = func(path string) (uid int, ok bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// accountOwner is the uid an account directory belongs to: the owner of its
// tmp/ directory (always the account uid), or of state/ in a layout that gave
// that to the account. Root-owned or absent means nothing was ever assigned.
func accountOwner(dir string) (int, bool) {
	for _, name := range []string{"tmp", "state"} {
		if uid, ok := ownerOf(filepath.Join(dir, name)); ok && uid != 0 {
			return uid, true
		}
	}
	return 0, false
}

// scanOwners lists the uid of every account directory below accounts.
func scanOwners(accounts string) map[string]int {
	out := map[string]int{}
	entries, err := os.ReadDir(accounts)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !ValidAccountID(e.Name()) || !e.IsDir() {
			continue
		}
		if uid, ok := accountOwner(filepath.Join(accounts, e.Name())); ok {
			out[e.Name()] = uid
		}
	}
	return out
}

// loadUIDRegistry reads the registry and reconciles it with the account
// directories. A registry that is missing, unreadable or inconsistent while
// accounts exist is treated as lost: every account on disk is refused and new
// uids start above anything a directory already carries.
func loadUIDRegistry(path string, base int, isolate bool) (*uidRegistry, error) {
	r := &uidRegistry{path: path, accounts: filepath.Join(filepath.Dir(path), "accounts"), base: base, limit: MaxUID, next: base, ids: map[string]int{}, purged: map[string]int64{}, epochs: map[string]int{}, refused: map[string]string{}}
	lost := ""
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		lost = "the uid registry is missing"
	case err != nil:
		return nil, err
	default:
		var f uidFile
		if err := json.Unmarshal(data, &f); err != nil {
			lost = "the uid registry cannot be parsed"
		} else if !consistent(f, base) {
			lost = "the uid registry is inconsistent"
		} else {
			r.next, r.ids = f.Next, f.IDs
			if r.ids == nil {
				r.ids = map[string]int{}
			}
			for id, e := range f.Epochs {
				if ValidAccountID(id) && e > 0 {
					r.epochs[id] = e
				}
			}
			for id, at := range f.Purged {
				if ValidAccountID(id) {
					r.purged[id] = at
				}
			}
			if r.next < base {
				r.next = base
			}
		}
		if lost != "" {
			// Keep the evidence; the next allocation writes a fresh file.
			_ = os.Rename(path, path+".unusable")
		}
	}
	if !isolate {
		return r, nil // directories keep the Runner's own uid: nothing to reconcile
	}
	owners := scanOwners(r.accounts)
	byUID := map[int][]string{}
	for id, uid := range owners {
		byUID[uid] = append(byUID[uid], id)
		if uid >= r.next {
			r.next = uid + 1
		}
	}
	for id, uid := range owners {
		switch registered, ok := r.ids[id]; {
		case len(byUID[uid]) > 1:
			r.refuse(id, fmt.Sprintf("uid %d owns more than one account directory", uid))
		case lost != "":
			r.refuse(id, lost+", and the directory is owned by uid "+fmt.Sprint(uid))
		case !ok:
			r.refuse(id, fmt.Sprintf("the directory is owned by uid %d but the registry has no entry", uid))
		case registered != uid:
			r.refuse(id, fmt.Sprintf("the registry says uid %d but the directory is owned by uid %d", registered, uid))
		}
	}
	return r, nil
}

func consistent(f uidFile, base int) bool {
	seen := map[int]bool{}
	for id, uid := range f.IDs {
		if !ValidAccountID(id) || uid < base || uid >= f.Next || seen[uid] {
			return false
		}
		seen[uid] = true
	}
	return true
}

func (r *uidRegistry) refuse(id, reason string) {
	r.refused[id] = reason
	log.Printf("hub: SECURITY: account %s is refused: %s; restore uids.json from backup (nothing is repaired automatically)", id, reason)
}

// check is the cheap per-open agreement test: the registry entry, the directory
// owner and the refusal list must all say the same.
func (r *uidRegistry) check(id, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reason, bad := r.refused[id]; bad {
		return errors.New("account refused: " + reason)
	}
	if uid, on := accountOwner(dir); on {
		if registered, ok := r.ids[id]; !ok || registered != uid {
			reason := fmt.Sprintf("directory owner uid %d disagrees with the registry (%v)", uid, r.ids[id])
			r.refuse(id, reason)
			return errors.New("account refused: " + reason)
		}
	}
	return nil
}

// uid returns the account's uid, allocating and persisting one on first use. A
// uid that already owns any directory below accounts/ is skipped.
func (r *uidRegistry) uid(id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reason, bad := r.refused[id]; bad {
		return 0, errors.New("account refused: " + reason)
	}
	if uid, ok := r.ids[id]; ok {
		return uid, nil
	}
	taken := map[int]bool{}
	for _, uid := range scanOwners(r.accounts) {
		taken[uid] = true
	}
	uid := r.next
	for taken[uid] {
		uid++
	}
	if uid >= r.limit {
		return 0, errors.New("no uid left for another account")
	}
	ids, next := withEntry(r.ids, id, uid), uid+1
	if err := r.persistLocked(ids, next, r.purged, r.epochs); err != nil {
		return 0, err
	}
	r.ids, r.next = ids, next
	return uid, nil
}

func (r *uidRegistry) persistLocked(ids map[string]int, next int, purged map[string]int64, epochs map[string]int) error {
	data, err := json.Marshal(uidFile{Next: next, IDs: ids, Purged: purged, Epochs: epochs})
	if err != nil {
		return err
	}
	return writeAtomic0600(r.path, data)
}

// isPurged reports whether the account was deleted. A purged id is never
// served again; its uid stays reserved.
func (r *uidRegistry) isPurged(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.purged[id]
	return ok
}

// markPurged records the tombstone durably before the account's state is removed.
func (r *uidRegistry) markPurged(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.purged[id]; ok {
		return nil
	}
	purged := make(map[string]int64, len(r.purged)+1)
	for k, v := range r.purged {
		purged[k] = v
	}
	purged[id] = time.Now().Unix()
	if err := r.persistLocked(r.ids, r.next, purged, r.epochs); err != nil {
		return err
	}
	r.purged = purged
	return nil
}

// uidOfAccount returns the registered uid without allocating one.
func (r *uidRegistry) uidOfAccount(id string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	uid, ok := r.ids[id]
	return uid, ok
}

func withEntry(m map[string]int, id string, uid int) map[string]int {
	out := make(map[string]int, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[id] = uid
	return out
}

// within reports whether path is dir or lies below it, after cleaning. The
// callers pass paths the Runner built or read from its own manifest.
func within(dir, path string) bool {
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// validateSpecWithin refuses a manifest entry that points outside the
// account's own state. The manifest is written by the Runner, but a restored
// backup or a hand edit must still never make one account run another's code.
func validateSpecWithin(spec Spec, stateDir string) error {
	plugin := filepath.Join(stateDir, "plugins", spec.ID)
	if !within(plugin, spec.WorkDir) {
		return fmt.Errorf("plugin %q work directory is outside the account state", spec.ID)
	}
	if spec.Kind == "builtin_gog" {
		if spec.Command != gogBinary {
			return fmt.Errorf("plugin %q command is not the bundled gogcli", spec.ID)
		}
	} else if !within(plugin, spec.Command) {
		return fmt.Errorf("plugin %q command is outside the account state", spec.ID)
	}
	for key, path := range spec.SecretEnv {
		if !within(plugin, path) {
			return fmt.Errorf("plugin %q secret %q is outside the account state", spec.ID, key)
		}
	}
	for _, value := range spec.Env {
		// Clean first: "/usr/../runner/accounts/<other>" is not below /usr/.
		value = filepath.Clean(value)
		if filepath.IsAbs(value) && !within(plugin, value) && !strings.HasPrefix(value, "/usr/") {
			// HOME, GOG_HOME and friends are the only absolute values we write.
			return fmt.Errorf("plugin %q environment path is outside the account state", spec.ID)
		}
	}
	return nil
}

// epoch is the account's token epoch.
func (r *uidRegistry) epoch(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epochs[id]
}

// bumpEpoch revokes every token issued for the account so far and returns the new epoch.
func (r *uidRegistry) bumpEpoch(id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	epochs := make(map[string]int, len(r.epochs)+1)
	for k, v := range r.epochs {
		epochs[k] = v
	}
	epochs[id]++
	if err := r.persistLocked(r.ids, r.next, r.purged, epochs); err != nil {
		return 0, err
	}
	r.epochs = epochs
	return epochs[id], nil
}
