package mcprunner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

// Per-account isolation primitives for the multi-account Runner. See
// docs/decisions/D80-runner-container.md for the threat model.

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
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("tofi-mcp-runner/v1/account\x00"))
	mac.Write([]byte(accountID))
	return hex.EncodeToString(mac.Sum(nil))
}

// MinMasterKeyBytes is the shortest master key the Runner accepts.
const MinMasterKeyBytes = 32

var lchown = os.Lchown

// chownTo gives one path to the account's uid. A nil credential (single-tenant
// Runner, tests) leaves ownership alone.
func chownTo(cred *syscall.Credential, path string) error {
	if cred == nil {
		return nil
	}
	return lchown(path, int(cred.Uid), int(cred.Gid))
}

// chownTree gives a directory and everything below it to the account's uid. It
// never follows a symlink: the link itself changes owner.
func chownTree(cred *syscall.Credential, root string) error {
	if cred == nil {
		return nil
	}
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return chownTo(cred, path)
	})
}

// uidRegistry hands every account its own numeric uid and never reuses one, so a
// deleted account's leftovers can never belong to a new account.
type uidRegistry struct {
	mu   sync.Mutex
	path string
	base int
	next int
	ids  map[string]int
}

type uidFile struct {
	Next int            `json:"next"`
	IDs  map[string]int `json:"ids"`
}

func loadUIDRegistry(path string, base int) (*uidRegistry, error) {
	r := &uidRegistry{path: path, base: base, next: base, ids: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var f uidFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("uid registry: %w", err)
	}
	seen := map[int]bool{}
	for id, uid := range f.IDs {
		if !ValidAccountID(id) || uid < base || uid >= f.Next || seen[uid] {
			return nil, errors.New("uid registry is inconsistent")
		}
		seen[uid] = true
	}
	r.next, r.ids = f.Next, f.IDs
	if r.ids == nil {
		r.ids = map[string]int{}
	}
	if r.next < base {
		r.next = base
	}
	return r, nil
}

// uid returns the account's uid, allocating and persisting one on first use.
func (r *uidRegistry) uid(id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if uid, ok := r.ids[id]; ok {
		return uid, nil
	}
	if r.next >= r.base+60000 {
		return 0, errors.New("no uid left for another account")
	}
	uid := r.next
	data, err := json.Marshal(uidFile{Next: uid + 1, IDs: withEntry(r.ids, id, uid)})
	if err != nil {
		return 0, err
	}
	if err := writeAtomic0600(r.path, data); err != nil {
		return 0, err
	}
	r.ids[id] = uid
	r.next = uid + 1
	return uid, nil
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
		if filepath.IsAbs(value) && !within(plugin, value) && !strings.HasPrefix(value, "/usr/") {
			// HOME, GOG_HOME and friends are the only absolute values we write.
			return fmt.Errorf("plugin %q environment path is outside the account state", spec.ID)
		}
	}
	return nil
}
