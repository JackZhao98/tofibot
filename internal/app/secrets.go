package app

// Secret values bypass conversation history and model tool arguments. This is a
// transport/storage boundary, not isolation from an agent with arbitrary shell
// access to the same user-owned VM.
import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type secretRecord struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Target         string `json:"target,omitempty"`
	Label          string `json:"label,omitempty"`
	Purpose        string `json:"purpose,omitempty"`
	BotID          string `json:"bot_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	Ciphertext     []byte `json:"ciphertext,omitempty"`
	// Model provider keys only: last successful verification and why the
	// provider later rejected the key.
	VerifiedAt string `json:"verified_at,omitempty"`
	Error      string `json:"error,omitempty"`
}
type secretVault struct {
	mu      sync.Mutex
	path    string
	aead    cipher.AEAD
	records map[string]secretRecord
}

func initializeSecretVault(dir string) (*secretVault, error) {
	dir = filepath.Join(dir, "secrets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		var f *os.File
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.Write(key)
			ce := f.Close()
			if err == nil {
				err = ce
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	v := &secretVault{path: filepath.Join(dir, "vault.json"), aead: aead, records: map[string]secretRecord{}}
	data, err := os.ReadFile(v.path)
	if err == nil {
		err = json.Unmarshal(data, &v.records)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("secret vault cannot be loaded")
	}
	for id, r := range v.records {
		if r.RunID != "" {
			delete(v.records, id)
		}
	}
	return v, v.saveLocked()
}
func (v *secretVault) saveLocked() error {
	data, err := json.Marshal(v.records)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(v.path), "vault-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), v.path)
}
func (v *secretVault) seal(id, value string) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, []byte(value), []byte(id)), nil
}
func (v *secretVault) reveal(r secretRecord) (string, error) {
	n := v.aead.NonceSize()
	if len(r.Ciphertext) < n {
		return "", errors.New("secret unavailable")
	}
	b, e := v.aead.Open(nil, r.Ciphertext[:n], r.Ciphertext[n:], []byte(r.ID))
	if e != nil {
		return "", errors.New("secret unavailable")
	}
	return string(b), nil
}
func publicSecret(r secretRecord) secretRecord { r.Ciphertext = nil; return r }

var secretEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
var secretFileName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,79}$`)

func allowedSecretEnv(s string) bool {
	return secretEnvName.MatchString(s) && !strings.HasPrefix(s, "LD_") && s != "PATH" && s != "HOME" && s != "BASH_ENV" && s != "ENV" && s != "DISPLAY" && s != "TOFI_WORKSPACE" && s != "PWD" && s != "TMPDIR"
}
func validSecretValue(value string) bool {
	return len(value) > 0 && len(value) <= 65536 && !strings.ContainsRune(value, 0)
}

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) bool {
	credentials := (r.URL.Path == "/api/computer/credentials" || strings.HasPrefix(r.URL.Path, "/api/computer/credentials/"))
	requests := (r.URL.Path == "/api/secret-inputs" || strings.HasPrefix(r.URL.Path, "/api/secret-inputs/"))
	if !credentials && !requests {
		return false
	}
	if s.secretVault == nil {
		writeErr(w, 503, "unavailable", "Secret storage unavailable")
		return true
	}
	v := s.secretVault
	base := "/api/secret-inputs"
	if credentials {
		base = "/api/computer/credentials"
	}
	suffix := strings.TrimPrefix(r.URL.Path, base)
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	id := parts[0]
	fail := func(code int, msg string) { writeErr(w, code, "secret_input", msg) }
	if r.Method == "GET" && id == "" {
		v.mu.Lock()
		list := []secretRecord{}
		for _, record := range v.records {
			if credentials != (record.RunID == "") || record.Kind == modelProviderKind {
				continue
			}
			if !credentials && (record.ConversationID != r.URL.Query().Get("conversation_id") || record.Status != "pending") {
				continue
			}
			list = append(list, publicSecret(record))
		}
		v.mu.Unlock()
		key := "requests"
		if credentials {
			key = "credentials"
		}
		writeJSON(w, 200, map[string]any{key: list})
		return true
	}
	if credentials && r.Method == "POST" && id == "" {
		var in struct{ Name, Kind, Target, Value string }
		if decode(r, &in) != nil || len(in.Name) > 120 || !validSecretValue(in.Value) || (in.Kind != "env" && in.Kind != "ssh") || (in.Kind == "env" && !allowedSecretEnv(in.Target)) || (in.Kind == "ssh" && (!validSSHKeyName(in.Target) || !strings.Contains(in.Value, "PRIVATE KEY-----"))) {
			fail(400, "Invalid credential fields")
			return true
		}
		record := secretRecord{ID: uuid.NewString(), Name: in.Name, Kind: in.Kind, Target: in.Target, Status: "stored", CreatedAt: now()}
		var err error
		record.Ciphertext, err = v.seal(record.ID, in.Value)
		if err != nil {
			fail(500, "Could not encrypt credential")
			return true
		}
		v.mu.Lock()
		if len(v.records) >= 256 {
			v.mu.Unlock()
			fail(409, "Credential limit reached")
			return true
		}
		for _, old := range v.records {
			if old.RunID == "" && old.Kind == record.Kind && old.Target == record.Target {
				v.mu.Unlock()
				fail(409, "This credential target already exists")
				return true
			}
		}
		v.records[record.ID] = record
		err = v.saveLocked()
		if err != nil {
			delete(v.records, record.ID)
		}
		v.mu.Unlock()
		if err != nil {
			fail(500, "Could not save credential")
		} else {
			writeJSON(w, 201, publicSecret(record))
		}
		return true
	}
	v.mu.Lock()
	record, ok := v.records[id]
	v.mu.Unlock()
	// Model provider keys belong to the server; they are never listed,
	// removed here, or installed on a computer.
	if !ok || credentials != (record.RunID == "") || record.Kind == modelProviderKind {
		fail(404, "Not found")
		return true
	}
	if r.Method == "DELETE" {
		v.mu.Lock()
		previous, existed := v.records[id]
		delete(v.records, id)
		err := v.saveLocked()
		if err != nil && existed {
			v.records[id] = previous
		}
		v.mu.Unlock()
		if err != nil {
			fail(500, "Could not remove credential")
		} else {
			writeJSON(w, 200, map[string]any{"ok": true, "note": "Vault entry removed; an installed VM copy is not removed."})
		}
		return true
	}
	if !credentials && r.Method == "POST" {
		var in struct{ Value string }
		if decode(r, &in) != nil || !validSecretValue(in.Value) {
			fail(400, "A value of 1–65536 bytes is required")
			return true
		}
		v.mu.Lock()
		current, exists := v.records[id]
		created, _ := time.Parse(time.RFC3339Nano, current.CreatedAt)
		if !exists || current.Status != "pending" || time.Since(created) > 10*time.Minute {
			v.mu.Unlock()
			fail(409, "Request no longer pending")
			return true
		}
		var active int
		err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND bot_id=? AND conversation_id=? AND status='running'`, current.RunID, current.BotID, current.ConversationID).Scan(&active)
		if err != nil || active != 1 {
			v.mu.Unlock()
			fail(409, "Run no longer active")
			return true
		}
		current.Ciphertext, err = v.seal(id, in.Value)
		current.Status = "ready"
		if err == nil {
			v.records[id] = current
			err = v.saveLocked()
			if err != nil {
				v.records[id] = record
			}
		}
		v.mu.Unlock()
		if err != nil {
			fail(500, "Could not save secret")
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
		return true
	}
	if credentials && r.Method == "POST" && len(parts) == 2 && parts[1] == "apply" {
		var in struct {
			BotID string `json:"bot_id"`
		}
		if decode(r, &in) != nil {
			fail(400, "Bot required")
			return true
		}
		if _, err := s.store.GetBot(in.BotID); err != nil {
			fail(404, "Bot not found")
			return true
		}
		if s.botHasActiveRun(in.BotID) {
			fail(409, "Wait for this Bot's active task to finish")
			return true
		}
		value, err := v.reveal(record)
		if err != nil {
			fail(500, "Credential unavailable")
			return true
		}
		run := Run{ID: "credential-" + uuid.NewString(), BotID: in.BotID}
		defer s.releaseComputerOwner(run.BotID, run.ID)
		_, err = s.applySecret(r.Context(), run, record.Kind, record.Target, value, "")
		if err != nil {
			fail(409, err.Error())
		} else {
			writeJSON(w, 200, map[string]any{"ok": true, "scope": "shared_computer", "note": "New processes receive environment changes. Existing processes require restart."})
		}
		return true
	}
	fail(405, "Method not allowed")
	return true
}
func secretQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (s *Server) applySecret(ctx context.Context, r Run, action, target, value, command string) (string, error) {
	name := "shell.exec"
	args := map[string]any{}
	switch action {
	case "browser_type":
		name = "desktop.type"
		args["text"] = value
	case "env":
		if !allowedSecretEnv(target) {
			return "", tooloutcome.InvalidArguments("invalid environment variable name")
		}
		path := "/workspace/.tofi-env/" + target
		args["command"] = "umask 077; mkdir -p /workspace/.tofi-env && printf '%s' " + secretQuote(value) + " > " + secretQuote(path)
	case "ssh":
		result, err := s.computerSSHKeys(ctx, r, sshKeyOperation{Action: "import", Name: target, PrivateKey: value})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "shell_exec":
		if !allowedSecretEnv(target) || strings.TrimSpace(command) == "" {
			return "", tooloutcome.InvalidArguments("environment variable name and command required")
		}
		args["command"] = "export " + target + "=" + secretQuote(value) + "; " + command
	default:
		return "", tooloutcome.InvalidArguments("unsupported secret action")
	}
	raw, _ := json.Marshal(args)
	out, err := s.microVMAction(ctx, r, name, raw)
	if err != nil {
		return "", errors.New("Secret operation failed; the computer may be unavailable or controlled by another session")
	}
	if name == "desktop.type" {
		return `{"ok":true}`, nil
	}
	var result struct {
		ExitCode *int `json:"exit_code"`
		TimedOut bool `json:"timed_out"`
	}
	if json.Unmarshal([]byte(out), &result) != nil || result.ExitCode == nil {
		return "", errors.New("Secret operation returned an invalid result")
	}
	if action != "shell_exec" && (*result.ExitCode != 0 || result.TimedOut) {
		return "", errors.New("Secret could not be installed; an SSH target may already exist")
	}
	encoded, _ := json.Marshal(map[string]any{"exit_code": result.ExitCode, "timed_out": result.TimedOut, "output_withheld": true})
	return string(encoded), nil
}
func (s *Server) secretTools(r Run) []Tool {
	if s.secretVault == nil || s.microVM == nil {
		return nil
	}
	return []Tool{
		{Name: "request_secret_input", Description: "Ask the user for a secret through a private input card. The value never enters chat or your tool result. Waits until the user responds or this task times out (normally three minutes; never more than ten minutes), then returns an opaque secret_ref usable only in this run. Explain intended use accurately. Do not ask users to paste secrets into chat.", Parameters: objectSchema(map[string]any{"label": map[string]any{"type": "string"}, "purpose": map[string]any{"type": "string"}}, []string{"label", "purpose"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in struct{ Label, Purpose string }
			if json.Unmarshal(raw, &in) != nil || len(in.Label) > 120 || len(in.Purpose) > 500 || strings.TrimSpace(in.Label) == "" || strings.TrimSpace(in.Purpose) == "" {
				return "", errors.New("label and purpose are required")
			}
			v := s.secretVault
			rec := secretRecord{ID: uuid.NewString(), Label: in.Label, Purpose: in.Purpose, BotID: r.BotID, RunID: r.ID, ConversationID: r.ConversationID, Status: "pending", CreatedAt: now()}
			v.mu.Lock()
			count := 0
			for _, existing := range v.records {
				if existing.RunID == r.ID {
					count++
				}
			}
			if count >= 8 || len(v.records) >= 256 {
				v.mu.Unlock()
				return "", errors.New("secret request limit reached")
			}
			v.records[rec.ID] = rec
			err := v.saveLocked()
			v.mu.Unlock()
			if err != nil {
				v.mu.Lock()
				delete(v.records, rec.ID)
				v.mu.Unlock()
				return "", errors.New("Could not create secret request")
			}
			timer := time.NewTimer(10 * time.Minute)
			defer timer.Stop()
			ticker := time.NewTicker(350 * time.Millisecond)
			defer ticker.Stop()
			success := false
			defer func() {
				if !success {
					v.mu.Lock()
					delete(v.records, rec.ID)
					_ = v.saveLocked()
					v.mu.Unlock()
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-timer.C:
					return "", errors.New("secret request expired")
				case <-ticker.C:
					v.mu.Lock()
					current, exists := v.records[rec.ID]
					v.mu.Unlock()
					if !exists {
						return "", errors.New("secret request cancelled")
					}
					if current.Status == "ready" {
						success = true
						return fmt.Sprintf(`{"secret_ref":%q,"value_hidden":true}`, rec.ID), nil
					}
				}
			}
		}},
		{Name: "use_secret_input", Description: "Use a private input reference from this run without seeing its value. browser_type does not locate or focus a field: first inspect the current screenshot and click the intended password field, then call this tool. Never reveal the value. References from ask_user_form only support visible, focused HTML password inputs on the approved HTTPS origin; wrong focus is rejected. If the site uses a plain text secret field, ask the human to enter it directly; never move the value into ordinary tool arguments. shell_exec exports the value as target environment variable for command; ALL output is withheld. env installs a shared environment variable; ssh installs a private key with target filename without overwriting. Shared VM files can be read by Bots with shell permissions, so only install when the user requested it.", Parameters: objectSchema(map[string]any{"secret_ref": map[string]any{"type": "string"}, "action": map[string]any{"type": "string", "enum": []string{"browser_type", "shell_exec", "env", "ssh"}}, "target": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"}}, []string{"secret_ref", "action"}), Identity: func(raw json.RawMessage) tooloutcome.Identity {
			var in struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(raw, &in)
			return tooloutcome.OperationIdentity("secret_input", in.Action, raw)
		}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				Ref                     string `json:"secret_ref"`
				Action, Target, Command string
			}
			if json.Unmarshal(raw, &in) != nil {
				return "", tooloutcome.InvalidArguments("invalid secret operation")
			}
			v := s.secretVault
			v.mu.Lock()
			rec, ok := v.records[in.Ref]
			v.mu.Unlock()
			created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt)
			if !ok || rec.RunID != r.ID || rec.BotID != r.BotID || rec.ConversationID != r.ConversationID || rec.Status != "ready" || time.Since(created) > 15*time.Minute {
				return "", tooloutcome.New(tooloutcome.Denied, "secret_reference_unavailable", "not_executed", "secret reference unavailable in this run", "explain_blocker").Err()
			}
			if rec.Kind == "browser_form" {
				return s.applyFormSecret(ctx, r, rec, in.Action)
			}
			value, err := v.reveal(rec)
			if err != nil {
				return "", tooloutcome.New(tooloutcome.Permanent, "secret_unavailable", "not_executed", "secret unavailable", "explain_blocker").Err()
			}
			return s.applySecret(ctx, r, in.Action, in.Target, value, in.Command)
		}},
	}
}

// Called when a run terminates, including cancellation. References never outlive
// the run; persistent Settings credentials have no RunID and remain intact.
func (s *Server) clearInactiveRunSecrets() {
	if s.secretVault == nil {
		return
	}
	s.secretVault.mu.Lock()
	runIDs := map[string]bool{}
	for _, record := range s.secretVault.records {
		if record.RunID != "" {
			runIDs[record.RunID] = true
		}
	}
	s.secretVault.mu.Unlock()
	for runID := range runIDs {
		r, err := s.store.GetRun(runID)
		if err == sql.ErrNoRows || (err == nil && r.Status != "queued" && r.Status != "running" && r.Status != runWaiting) {
			s.clearRunSecrets(runID)
		}
	}
}

func (s *Server) clearRunSecrets(runID string) {
	if s.secretVault == nil {
		return
	}
	v := s.secretVault
	v.mu.Lock()
	defer v.mu.Unlock()
	changed := false
	for id, record := range v.records {
		if record.RunID == runID {
			delete(v.records, id)
			changed = true
		}
	}
	if changed {
		_ = v.saveLocked()
	}
}
