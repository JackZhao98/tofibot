package app

// Bot secrets (docs/design/bot-secrets.md): environment-style secrets the
// person chose to keep for one Bot. They live in the same sealed vault as every
// other credential, as records of kind botSecretKind scoped by BotID. A Bot
// can only name them on a command; the host injects the value into that one
// command's environment and masks it in the output. No Bot tool reads, lists
// values of, or deletes a secret.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

// botSecretKind is not "env": the Settings list, its duplicate rule and the
// portable export all select kind "env" and must never see a Bot's secret.
const botSecretKind = "bot_env"

const (
	// maxBotSecrets bounds one Bot's saved names; maxShellSecrets bounds one command.
	maxBotSecrets   = 32
	maxShellSecrets = 8
	// secretMask replaces an injected value wherever it shows in command output.
	secretMask = "[secret masked]"
	// Awareness: names only, within this many runes of context.
	botSecretContextRunes = 400
)

// botSecretNamesLocked returns the Bot's saved names, sorted. The caller holds v.mu.
func (v *secretVault) botSecretNamesLocked(botID string) []string {
	names := []string{}
	for _, r := range v.records {
		if r.Kind == botSecretKind && r.BotID == botID && r.RunID == "" {
			names = append(names, r.Target)
		}
	}
	sort.Strings(names)
	return names
}

// findBotSecretLocked finds one Bot's record by name. Another Bot's record is
// never returned: the match requires the same BotID.
func (v *secretVault) findBotSecretLocked(botID, name string) (secretRecord, bool) {
	if botID == "" {
		return secretRecord{}, false
	}
	for _, r := range v.records {
		if r.Kind == botSecretKind && r.BotID == botID && r.RunID == "" && r.Target == name {
			return r, true
		}
	}
	return secretRecord{}, false
}

func (v *secretVault) findGlobalEnvLocked(name string) (secretRecord, bool) {
	for _, r := range v.records {
		if r.Kind == "env" && r.RunID == "" && r.BotID == "" && r.Target == name && r.Status == "stored" {
			return r, true
		}
	}
	return secretRecord{}, false
}

// storeBotSecretLocked seals value under the Bot and name, replacing a record
// of the same name. It returns the new id and the record it replaced. The
// caller holds v.mu and calls saveLocked.
func (v *secretVault) storeBotSecretLocked(botID, name, label, value string) (string, *secretRecord, error) {
	if !allowedSecretEnv(name) || !validSecretValue(value) {
		return "", nil, errors.New("invalid secret name or value")
	}
	var replaced *secretRecord
	if old, ok := v.findBotSecretLocked(botID, name); ok {
		replaced = &old
	} else if len(v.botSecretNamesLocked(botID)) >= maxBotSecrets || len(v.records) >= 256 {
		return "", nil, errors.New("secret limit reached for this Bot")
	}
	record := secretRecord{ID: uuid.NewString(), Name: label, Kind: botSecretKind, Target: name, BotID: botID, Status: "stored", CreatedAt: now()}
	var err error
	if record.Ciphertext, err = v.seal(record.ID, value); err != nil {
		return "", nil, errors.New("could not protect secret")
	}
	if replaced != nil {
		delete(v.records, replaced.ID)
	}
	v.records[record.ID] = record
	return record.ID, replaced, nil
}

// purgeBotSecrets removes every secret of a deleted Bot.
func (v *secretVault) purgeBotSecrets(botID string) {
	if v == nil || botID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	changed := false
	for id, r := range v.records {
		if r.Kind == botSecretKind && r.BotID == botID {
			delete(v.records, id)
			changed = true
		}
	}
	if changed {
		_ = v.saveLocked()
	}
}

// shellSecretNames reads the names a command asks for. Order and duplicates
// are normalised; the list is bounded.
func shellSecretNames(raw json.RawMessage) ([]string, error) {
	var in struct {
		Secrets []string `json:"secrets"`
	}
	_ = json.Unmarshal(raw, &in)
	if len(in.Secrets) > maxShellSecrets {
		return nil, tooloutcome.InvalidArguments(fmt.Sprintf("a command can use at most %d secrets", maxShellSecrets))
	}
	seen := map[string]bool{}
	names := []string{}
	for _, n := range in.Secrets {
		if !allowedSecretEnv(n) {
			return nil, tooloutcome.InvalidArguments("secret names are environment variable names")
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names, nil
}

// resolveShellSecrets returns the environment for one command: the Bot's own
// secret wins; the global Settings credential of the same name is used only if
// the Bot has none. It stamps last_used_at on Bot-scoped records.
func (s *Server) resolveShellSecrets(botID string, names []string) (map[string]string, error) {
	v := s.secretVault
	if v == nil {
		return nil, tooloutcome.InvalidArguments("secret storage is unavailable")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	env := map[string]string{}
	stamped := map[string]secretRecord{}
	for _, name := range names {
		rec, ok := v.findBotSecretLocked(botID, name)
		bot := ok
		if !ok {
			rec, ok = v.findGlobalEnvLocked(name)
		}
		if !ok {
			available := v.botSecretNamesLocked(botID)
			for _, r := range v.records {
				if r.Kind == "env" && r.RunID == "" && r.BotID == "" && r.Status == "stored" {
					available = append(available, r.Target)
				}
			}
			sort.Strings(available)
			list := strings.Join(available, ", ")
			if list == "" {
				list = "none"
			}
			return nil, tooloutcome.InvalidArguments("unknown secret " + name + "; available names: " + list)
		}
		value, err := v.reveal(rec)
		if err != nil {
			return nil, tooloutcome.New(tooloutcome.Permanent, "secret_unavailable", "not_executed", "secret unavailable", "explain_blocker").Err()
		}
		env[name] = value
		if bot {
			rec.LastUsedAt = now()
			stamped[rec.ID] = rec
		}
	}
	if len(stamped) > 0 {
		previous := map[string]secretRecord{}
		for id, rec := range stamped {
			previous[id] = v.records[id]
			v.records[id] = rec
		}
		if err := v.saveLocked(); err != nil {
			for id, rec := range previous {
				v.records[id] = rec
			}
		}
	}
	return env, nil
}

// maskSecretValues replaces each value, in raw and JSON-escaped form, with the
// fixed mask. Longer values go first so a value that contains another is
// covered whole.
func maskSecretValues(text string, values []string) string {
	sorted := append([]string(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		if v == "" {
			continue
		}
		text = strings.ReplaceAll(text, v, secretMask)
		if enc, err := json.Marshal(v); err == nil && len(enc) > 2 {
			text = strings.ReplaceAll(text, string(enc[1:len(enc)-1]), secretMask)
		}
	}
	return text
}

// shellSecretMask builds the output filter for one command's injected values.
func shellSecretMask(env map[string]string) func(string) string {
	values := make([]string, 0, len(env))
	for _, v := range env {
		values = append(values, v)
	}
	return func(s string) string { return maskSecretValues(s, values) }
}

// withShellEnv adds the injected environment to the command's guest arguments.
func withShellEnv(args json.RawMessage, env map[string]string) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return nil, err
	}
	m["env"] = env
	return json.Marshal(m)
}

// botSecretContext lists the Bot's saved names, never values, within a fixed
// budget. It returns "" when the Bot has none.
func (s *Server) botSecretContext(botID string) string {
	v := s.secretVault
	if v == nil {
		return ""
	}
	v.mu.Lock()
	names := v.botSecretNamesLocked(botID)
	v.mu.Unlock()
	if len(names) == 0 {
		return ""
	}
	head := "[saved secrets]\nNames only. Pass a name in the secrets argument of computer_shell to use it.\n"
	tail := "[/saved secrets]"
	body := ""
	for i, n := range names {
		if len([]rune(head+body+n))+len([]rune(tail))+24 > botSecretContextRunes {
			body += fmt.Sprintf("and %d more\n", len(names)-i)
			break
		}
		body += n + "\n"
	}
	return head + body + tail
}

type botSecretView struct {
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// handleBotSecrets serves the owner's management routes. Values are never
// returned; replace takes a new value, delete removes the record.
func (s *Server) handleBotSecrets(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/bots/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "secrets" || parts[0] == "" {
		return false
	}
	botID := parts[0]
	fail := func(code int, msg string) { writeErr(w, code, "bot_secret", msg) }
	if s.secretVault == nil {
		fail(503, "Secret storage unavailable")
		return true
	}
	if _, err := s.store.GetBot(botID); err != nil {
		fail(404, "Bot not found")
		return true
	}
	v := s.secretVault
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			fail(405, "Method not allowed")
			return true
		}
		v.mu.Lock()
		list := []botSecretView{}
		for _, rec := range v.records {
			if rec.Kind == botSecretKind && rec.BotID == botID && rec.RunID == "" {
				list = append(list, botSecretView{Name: rec.Target, CreatedAt: rec.CreatedAt, LastUsedAt: rec.LastUsedAt})
			}
		}
		v.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		writeJSON(w, 200, map[string]any{"secrets": list})
		return true
	}
	name := parts[2]
	if !allowedSecretEnv(name) {
		fail(404, "Not found")
		return true
	}
	switch r.Method {
	case http.MethodPut:
		var in struct {
			Value string `json:"value"`
		}
		if decode(r, &in) != nil || !validSecretValue(in.Value) {
			fail(400, "Invalid secret value")
			return true
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		old, exists := v.findBotSecretLocked(botID, name)
		if !exists {
			fail(404, "Not found")
			return true
		}
		id, replaced, err := v.storeBotSecretLocked(botID, name, old.Name, in.Value)
		if err == nil {
			// A replaced value is a new record; keep the history of when it was used.
			rec := v.records[id]
			rec.LastUsedAt = old.LastUsedAt
			v.records[id] = rec
			err = v.saveLocked()
		}
		if err != nil {
			delete(v.records, id)
			if replaced != nil {
				v.records[replaced.ID] = *replaced
			}
			fail(500, "Could not save secret")
			return true
		}
		writeJSON(w, 200, botSecretView{Name: name, CreatedAt: v.records[id].CreatedAt, LastUsedAt: old.LastUsedAt})
	case http.MethodDelete:
		v.mu.Lock()
		defer v.mu.Unlock()
		old, exists := v.findBotSecretLocked(botID, name)
		if !exists {
			fail(404, "Not found")
			return true
		}
		delete(v.records, old.ID)
		if err := v.saveLocked(); err != nil {
			v.records[old.ID] = old
			fail(500, "Could not remove secret")
			return true
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		fail(405, "Method not allowed")
	}
	return true
}

// injectedSecrets holds the values injected during one run so later computer
// tool results of that run are masked too.
type injectedSecrets struct {
	mu     sync.Mutex
	values []string
}

func (i *injectedSecrets) add(env map[string]string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, v := range env {
		i.values = append(i.values, v)
	}
}

func (i *injectedSecrets) mask(text string) string {
	i.mu.Lock()
	values := append([]string(nil), i.values...)
	i.mu.Unlock()
	if len(values) == 0 {
		return text
	}
	return maskSecretValues(text, values)
}
