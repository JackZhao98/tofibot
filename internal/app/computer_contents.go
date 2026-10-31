package app

import (
	"net/http"
	"sort"
)

// What lives only on the computer, as far as the server can tell
// (docs/design/computer-decoupling.md section 7). The server never reads the
// computer's disk for this: it names what Settings saved for the computer and
// counts the files that still wait for their copy. Browser sign-ins, installed
// tools and work files are described in words by the UI; the server cannot list
// them.

// computerConfiguration lists, by name, the environment variables and SSH keys
// saved in Settings for the computer. Values never leave the vault.
func (s *Server) computerConfiguration() (env, keys []string) {
	env, keys = []string{}, []string{}
	if s.secretVault == nil {
		return env, keys
	}
	v := s.secretVault
	v.mu.Lock()
	for _, record := range v.records {
		if record.RunID != "" || record.Target == "" {
			continue
		}
		switch record.Kind {
		case "env":
			env = append(env, record.Target)
		case "ssh":
			keys = append(keys, record.Target)
		}
	}
	v.mu.Unlock()
	sort.Strings(env)
	sort.Strings(keys)
	return env, keys
}

// freshComputerStatus is the run-context fact of a computer nothing was saved
// for: the Bot asks instead of assuming a key or variable exists.
const freshComputerStatus = "No environment variables or SSH keys have been added in Settings for this Bot computer. If a task needs a key, token or login, ask the person for it; never assume one is set."

// freshComputerNote returns freshComputerStatus while the computer has nothing
// saved for it, and nothing once the person added any. It makes no call to the
// computer, so a sleeping computer is not woken for a prompt line.
func (s *Server) freshComputerNote() string {
	if s.computer() == nil || s.secretVault == nil {
		return ""
	}
	if env, keys := s.computerConfiguration(); len(env)+len(keys) > 0 {
		return ""
	}
	return freshComputerStatus
}

// handleComputerContents serves GET /api/computer/contents: the names and counts
// the "What is on this Bot computer" rows and the wipe and swap confirmations list.
// Without a computer there is nothing to list (404 not_configured, the same code
// the other computer routes use), and the Web app never asks.
func (s *Server) handleComputerContents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method_not_allowed", "use GET")
		return
	}
	if s.computer() == nil {
		writeErr(w, 404, "not_configured", "computer VM is not configured")
		return
	}
	env, keys := s.computerConfiguration()
	pending, err := s.store.pendingGuestCopies()
	if err != nil {
		writeErr(w, 500, "storage", "could not count files waiting for their copy")
		return
	}
	writeJSON(w, 200, map[string]any{"environment": env, "ssh_keys": keys, "pending_file_copies": pending})
}
