package app

// Folders on a person's Mac that the person granted to a Bot (design: docs/design/mac-folders.md).
//
// The Mac is the source of truth. The person adds a folder for a Bot in the TOFI
// Mac app through the native folder panel; the app keeps the grants and enforces
// them on every job. It reports the list here (PUT /api/computers/{id}/folders,
// device token) so the server can show the Bot its folders and tools, check a
// path before it queues a job, and list the grants in Settings in any browser.
// A list the server holds never widens access: the Mac checks each job again.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	maxMacFolders         = 64
	maxMacFolderBots      = 32
	maxMacFolderPathBytes = 1024
	// macFoldersVersion is the server-info value (local_folders.version) a Mac app
	// reads before it advertises folders.* capabilities. An older server rejects
	// unknown capability names, so the app must not send them there.
	macFoldersVersion = 1
)

var (
	macFolderIDPattern = regexp.MustCompile(`^fld_[A-Za-z0-9_-]{8,48}$`)
	macBotIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// macFolder is one granted folder as the Mac reports it.
type macFolder struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Path string   `json:"path"`
	Bots []string `json:"bots"`
}

func localFoldersServerInfo() map[string]any {
	return map[string]any{"version": macFoldersVersion}
}

// cleanMacPath accepts an absolute, already clean POSIX path without control characters.
func cleanMacPath(p string) bool {
	if p == "" || len(p) > maxMacFolderPathBytes || !utf8.ValidString(p) || !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// normalizeMacFolders checks a reported list. A bad list is refused whole: the
// Mac sends the full list each time, so a partial store would hide a revoke.
func normalizeMacFolders(in []macFolder) ([]macFolder, error) {
	if len(in) > maxMacFolders {
		return nil, errors.New("too many folders")
	}
	out := make([]macFolder, 0, len(in))
	seen := map[string]bool{}
	for _, f := range in {
		if !macFolderIDPattern.MatchString(f.ID) || seen[f.ID] {
			return nil, errors.New("invalid or repeated folder id")
		}
		seen[f.ID] = true
		if !cleanMacPath(f.Path) {
			return nil, errors.New("folder path must be absolute and clean")
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = path.Base(f.Path)
		}
		if utf8.RuneCountInString(name) > 255 || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, errors.New("invalid folder name")
		}
		if len(f.Bots) == 0 || len(f.Bots) > maxMacFolderBots {
			return nil, errors.New("a folder needs one to 32 Bots")
		}
		bots := make([]string, 0, len(f.Bots))
		botSeen := map[string]bool{}
		for _, b := range f.Bots {
			if !macBotIDPattern.MatchString(b) {
				return nil, errors.New("invalid Bot id")
			}
			if !botSeen[b] {
				botSeen[b] = true
				bots = append(bots, b)
			}
		}
		sort.Strings(bots)
		out = append(out, macFolder{ID: f.ID, Name: name, Path: f.Path, Bots: bots})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// updateComputerFolders stores the Mac's list. Open folder jobs of a Bot that
// lost the folder are cancelled: the Mac refuses them too, but a pending job
// should not wait for its answer.
func (s *Store) updateComputerFolders(id, token string, folders []macFolder) ([]macFolder, error) {
	list, err := normalizeMacFolders(folders)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = authenticateDevice(tx, id, token); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(list)
	t := now()
	if _, err = tx.Exec(`UPDATE computers SET folders=?,last_seen=? WHERE id=? AND revoked_at IS NULL`, string(raw), t, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT id,bot_id,args FROM computer_jobs WHERE device_id=? AND status IN ('pending','running') AND action LIKE 'folders.%'`, id)
	if err != nil {
		return nil, err
	}
	var stale []string
	for rows.Next() {
		var jobID, botID, args string
		if rows.Scan(&jobID, &botID, &args) != nil {
			continue
		}
		var a struct {
			Path string `json:"path"`
			Cwd  string `json:"cwd"`
		}
		_ = json.Unmarshal([]byte(args), &a)
		target := a.Path
		if target == "" {
			target = a.Cwd
		}
		if target == "" {
			// A command without cwd runs in the Bot's first folder: it stays while the Bot has any.
			if !slices.ContainsFunc(list, func(f macFolder) bool { return containsString(f.Bots, botID) }) {
				stale = append(stale, jobID)
			}
			continue
		}
		if _, ok := macFolderFor(list, botID, target); !ok {
			stale = append(stale, jobID)
		}
	}
	rows.Close()
	for _, jobID := range stale {
		if _, err = tx.Exec(`UPDATE computer_jobs SET status='cancelled',error='folder_not_granted',completed_at=? WHERE id=? AND status IN ('pending','running')`, t, jobID); err != nil {
			return nil, err
		}
	}
	return list, tx.Commit()
}

func (s *Store) computerFolders(id string) []macFolder {
	var raw string
	if s.db.QueryRow(`SELECT folders FROM computers WHERE id=? AND revoked_at IS NULL`, id).Scan(&raw) != nil {
		return nil
	}
	var list []macFolder
	_ = json.Unmarshal([]byte(raw), &list)
	return list
}

// macFolderFor finds the folder of a Bot's grant that holds target (equal to or
// below the folder, by whole path segments). The comparison is on clean paths;
// the Mac compares real paths again, so this is a pre-check and never a grant.
func macFolderFor(list []macFolder, botID, target string) (macFolder, bool) {
	if target == "" || !strings.HasPrefix(target, "/") {
		return macFolder{}, false
	}
	target = path.Clean(target)
	best := macFolder{}
	found := false
	for _, f := range list {
		if !containsString(f.Bots, botID) {
			continue
		}
		if target == f.Path || strings.HasPrefix(target, f.Path+"/") {
			if !found || len(f.Path) > len(best.Path) {
				best, found = f, true
			}
		}
	}
	return best, found
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// botMacFolders is one Mac with the folders a Bot may use there.
type botMacFolders struct {
	ComputerID string      `json:"computer_id"`
	Name       string      `json:"name"`
	Online     bool        `json:"online"`
	LastSeen   string      `json:"last_seen,omitempty"`
	Exec       bool        `json:"exec"`
	Folders    []macFolder `json:"folders"`
}

// macFoldersForBot lists the Macs on which a Bot has at least one folder, online ones first.
func (s *Server) macFoldersForBot(botID string) []botMacFolders {
	if s.store == nil || botID == "" {
		return nil
	}
	rows, err := s.store.db.Query(`SELECT id,name,capabilities,last_seen,status,folders FROM computers WHERE kind=? AND revoked_at IS NULL AND folders<>'[]' ORDER BY created_at,id`, computer.RowKindPersonal)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []botMacFolders
	for rows.Next() {
		var id, name, capsRaw, lastSeen, status, raw string
		if rows.Scan(&id, &name, &capsRaw, &lastSeen, &status, &raw) != nil {
			continue
		}
		var caps []string
		_ = json.Unmarshal([]byte(capsRaw), &caps)
		if !containsString(caps, "folders.read") {
			continue
		}
		var list []macFolder
		_ = json.Unmarshal([]byte(raw), &list)
		mine := []macFolder{}
		for _, f := range list {
			if containsString(f.Bots, botID) {
				mine = append(mine, f)
			}
		}
		if len(mine) == 0 {
			continue
		}
		online := status == computerRowAttached && computerRecentlySeen(lastSeen)
		if slot := s.slotFor(id); slot != nil {
			online = online && slot.currentState() == slotAttached
		}
		out = append(out, botMacFolders{ComputerID: id, Name: name, Online: online, LastSeen: lastSeen, Exec: containsString(caps, "folders.exec"), Folders: mine})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Online && !out[j].Online })
	return out
}

// routeComputerFolders answers PUT /api/computers/{id}/folders from the Mac.
func (s *Server) routeComputerFolders(w http.ResponseWriter, r *http.Request, id string) {
	var v struct {
		Folders []macFolder `json:"folders"`
	}
	if err := decodeStrict(r, 256<<10, &v); err != nil || v.Folders == nil {
		writeErr(w, 400, "invalid_request", "invalid folders")
		return
	}
	list, err := s.store.updateComputerFolders(id, bearerToken(r), v.Folders)
	if err != nil {
		if err.Error() == "unauthorized" {
			s.writeDeviceAuthErr(w, id, err)
		} else {
			writeErr(w, 400, "folders_rejected", err.Error())
		}
		return
	}
	s.deviceSeen(id)
	writeJSON(w, 200, map[string]any{"folders": list})
}

// lastSeenAgo words a last-seen stamp for a Bot ("3 minutes ago"); empty when unknown.
func lastSeenAgo(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || t.IsZero() {
		return ""
	}
	d := time.Since(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return t.UTC().Format("2006-01-02 15:04 UTC")
	}
	return t.UTC().Format("2006-01-02")
}

const (
	// maxFolderCommandBytes keeps a command short enough for the reviewer to read whole.
	maxFolderCommandBytes = 2 << 10
	maxFolderWriteBytes   = 512 << 10
	maxFolderEditBytes    = 256 << 10
	// folderExecDefault and folderExecMax are timeout_sec of a command on a Mac.
	folderExecDefault = 120
	folderExecMax     = 240
)

// folderEffects is the effect vocabulary of a command on a Mac: actionEffects
// plus change (creates or edits files in the granted folders).
func folderEffect(e string) bool {
	if e == "change" {
		return true
	}
	return slices.Contains(actionEffects, e)
}

// validateFolderArgs checks the args of a folders.* job before it is queued.
func validateFolderArgs(action string, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxFolderWriteBytes+(64<<10) || !json.Valid(raw) {
		return errors.New("args must be a JSON object")
	}
	strict := func(v any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	absPath := func(p string) error {
		if !cleanMacPath(p) {
			return errors.New("path must be an absolute path inside one of your folders on the Mac, such as /Users/name/project/README.md")
		}
		return nil
	}
	switch action {
	case "folders.list":
		var v struct {
			Path string `json:"path"`
		}
		if err := strict(&v); err != nil {
			return errors.New("files.list on a Mac takes path")
		}
		return absPath(v.Path)
	case "folders.read":
		var v struct {
			Path   string `json:"path"`
			Offset *int   `json:"offset"`
			Limit  *int   `json:"limit"`
		}
		if err := strict(&v); err != nil || (v.Offset != nil && *v.Offset < 1) || (v.Limit != nil && (*v.Limit < 1 || *v.Limit > 5000)) {
			return errors.New("files.read on a Mac takes path, optional offset (line, from 1) and limit (1 to 5000 lines)")
		}
		return absPath(v.Path)
	case "folders.write":
		var v struct {
			Path    string  `json:"path"`
			Content *string `json:"content"`
		}
		if err := strict(&v); err != nil || v.Content == nil || len(*v.Content) > maxFolderWriteBytes {
			return errors.New("files.write on a Mac takes path and content (at most 512 KiB)")
		}
		return absPath(v.Path)
	case "folders.edit":
		var v struct {
			Path       string  `json:"path"`
			OldString  *string `json:"old_string"`
			NewString  *string `json:"new_string"`
			ReplaceAll bool    `json:"replace_all"`
		}
		if err := strict(&v); err != nil || v.OldString == nil || v.NewString == nil || *v.OldString == "" || len(*v.OldString) > maxFolderEditBytes || len(*v.NewString) > maxFolderEditBytes || *v.OldString == *v.NewString {
			return errors.New("files.edit on a Mac takes path, old_string (not empty) and a different new_string, each at most 256 KiB, and optional replace_all")
		}
		return absPath(v.Path)
	case "folders.exec":
		var v struct {
			Command    string `json:"command"`
			Cwd        string `json:"cwd"`
			Effect     string `json:"effect"`
			TimeoutSec *int   `json:"timeout_sec"`
		}
		if err := strict(&v); err != nil || strings.TrimSpace(v.Command) == "" || len(v.Command) > maxFolderCommandBytes || strings.IndexByte(v.Command, 0) >= 0 {
			return errors.New("computer_shell on a Mac takes command (at most 2 KiB), cwd, effect and optional timeout_sec")
		}
		if !folderEffect(v.Effect) {
			return errors.New("computer_shell on a Mac needs effect: none (the command only reads), change (it creates or edits files in your folders), delete, publish, send or other_external")
		}
		if v.TimeoutSec != nil && (*v.TimeoutSec < 1 || *v.TimeoutSec > folderExecMax) {
			return errors.New("timeout_sec on a Mac is 1 to 240")
		}
		if v.Cwd != "" {
			return absPath(v.Cwd)
		}
		return nil
	}
	return errors.New("unsupported action")
}

// folderExecTimeout is the time a command may run on the Mac.
func folderExecTimeout(raw json.RawMessage) time.Duration {
	var v struct {
		TimeoutSec int `json:"timeout_sec"`
	}
	_ = json.Unmarshal(raw, &v)
	if v.TimeoutSec < 1 || v.TimeoutSec > folderExecMax {
		v.TimeoutSec = folderExecDefault
	}
	return time.Duration(v.TimeoutSec) * time.Second
}

const (
	// maxOpenFolderJobs is how many folder jobs one Mac holds at once (it runs them side by side).
	maxOpenFolderJobs = 4
	// folderExecLeaseMS is the lease a running command gets per status read. The Mac reads every 2 s;
	// a slow or failed read does not stop a long build before the lease runs out.
	folderExecLeaseMS = 10000
)

// jobLeaseCap is the longest lease one status read gives a running job.
func jobLeaseCap(action string) int64 {
	if action == "folders.exec" {
		return folderExecLeaseMS
	}
	return 1000
}

// folderQueueRoom refuses a folder job the Mac would not start: the Mac claims
// jobs one by one and a claimed job is never handed out again, so the caps are
// kept here and the Mac's own mac_busy answer is only a backstop.
func folderQueueRoom(tx *sql.Tx, deviceID, botID, action string) error {
	if botID == "" {
		return tooloutcome.New(tooloutcome.Denied, "folder_not_granted", "not_executed", "Folders on a Mac are granted to a Bot; this call has no Bot.", "explain_blocker").Err()
	}
	var open, execs int
	if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN action='folders.exec' AND bot_id=? THEN 1 ELSE 0 END),0) FROM computer_jobs WHERE device_id=? AND status IN ('pending','running') AND action LIKE 'folders.%'`, botID, deviceID).Scan(&open, &execs); err != nil {
		return err
	}
	if open >= maxOpenFolderJobs || (action == "folders.exec" && execs > 0) {
		return macBusyOutcome().Err()
	}
	return nil
}

func macBusyOutcome() tooloutcome.Outcome {
	return tooloutcome.New(tooloutcome.Transient, "mac_busy", "not_executed", "The Mac is still running your previous command or several folder steps at once, so this step was not started. Wait for the running command to finish, then try again.", "retry_later")
}

const (
	macFolderReviewCommands = "commands" // commands that change something; file writes and edits are covered by the grant
	macFolderReviewChanges  = "changes"  // also every file write and edit
)

// folderCommandLabel is the approval label of a command: the command itself,
// or for a long one its start and a digest, so an approval never covers a
// different command that only shares the first words.
func folderCommandLabel(cmd string) string {
	text := approvalLabelText(cmd)
	if utf8.RuneCountInString(text) <= 240 && text == strings.Join(strings.Fields(cmd), " ") {
		return text
	}
	sum := sha256.Sum256([]byte(cmd))
	runes := []rune(text)
	if len(runes) > 220 {
		runes = runes[:220]
	}
	return strings.TrimSpace(string(runes)) + " … sha256:" + hex.EncodeToString(sum[:4])
}

// folderChangeLabel is the label and the reviewer's text of a file write or edit: the path and a digest of the change.
func folderChangeLabel(action string, args []byte) (label, text string) {
	var v struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	_ = json.Unmarshal(args, &v)
	change := v.Content
	text = "new content: " + v.Content
	if action == "folders.edit" {
		change = v.OldString + "\x00" + v.NewString
		text = "replace: " + v.OldString + "\nwith: " + v.NewString
	}
	sum := sha256.Sum256([]byte(change))
	return v.Path + " · sha256:" + hex.EncodeToString(sum[:4]), text
}

// folderEffectWords names a declared command effect for the reviewer.
func folderEffectWords(effect string) string {
	switch strings.TrimSpace(strings.ToLower(effect)) {
	case "change":
		return "change: creates or edits files in a folder the person granted to this Bot on their Mac"
	case "delete":
		return "delete: removes files or history in a granted folder on the person's Mac"
	}
	return effect
}

// maxContextFolders bounds the folders the run context lists per Mac.
const maxContextFolders = 20

// macFoldersRunContext tells the Bot where it may work on the person's Macs.
// Only this Bot's folders are listed; other Bots' grants stay out of its context.
func (s *Server) macFoldersRunContext(r Run) string {
	macs := s.macFoldersForBot(r.BotID)
	if len(macs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Your folders on the person's Mac (granted to you in the TOFI Mac app; nothing else on that Mac is reachable):\n")
	for _, m := range macs {
		state := "connected"
		if !m.Online {
			state = "offline"
			if seen := lastSeenAgo(m.LastSeen); seen != "" {
				state += ", last seen " + seen
			}
		}
		fmt.Fprintf(&b, "- %s (Mac, id=%s, %s):", m.Name, m.ComputerID, state)
		for i, f := range m.Folders {
			if i == maxContextFolders {
				fmt.Fprintf(&b, " and %d more", len(m.Folders)-i)
				break
			}
			b.WriteString(" " + f.Path)
			if i < len(m.Folders)-1 && i < maxContextFolders-1 {
				b.WriteByte(',')
			}
		}
		if !m.Exec {
			b.WriteString(". Commands are not available on this Mac, files are")
		}
		b.WriteString(".\n")
	}
	b.WriteString("Work there with computer_files and computer_shell and pass computer_id of that Mac. An offline Mac cannot be reached until the person opens the TOFI app on it.")
	return b.String()
}

// folderArgKeys are the exact (lower-case) keys of each folders.* action.
var folderArgKeys = map[string][]string{
	"folders.list":  {"path"},
	"folders.read":  {"path", "offset", "limit"},
	"folders.write": {"path", "content"},
	"folders.edit":  {"path", "old_string", "new_string", "replace_all"},
	"folders.exec":  {"command", "cwd", "effect", "timeout_sec"},
}

// canonicalFolderArgs is the one form of a folders.* job that the guard, the
// queue and the Mac all read. Go matches JSON keys without regard to case and the
// Mac does not, so {"command":..,"Command":..} could show the reviewer one command
// and run another: a key that is not exactly one of the action's keys is refused,
// and the args are written out again from the parsed values.
func canonicalFolderArgs(action string, raw json.RawMessage) (json.RawMessage, error) {
	keys, ok := folderArgKeys[action]
	if !ok {
		return nil, errors.New("unsupported action")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("args must be a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil {
		return nil, errors.New("args must be a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, errors.New("args must be a JSON object")
		}
		key, _ := t.(string)
		if !slices.Contains(keys, key) || seen[key] {
			return nil, fmt.Errorf("%s takes only %s", action, strings.Join(keys, ", "))
		}
		seen[key] = true
		var skip json.RawMessage
		if err = dec.Decode(&skip); err != nil {
			return nil, errors.New("args must be a JSON object")
		}
	}
	out, _ := json.Marshal(m)
	return out, validateFolderArgs(action, out)
}
