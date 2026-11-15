package app

// computer_files and computer_shell on a person's Mac (design: docs/design/mac-folders.md).
//
// One tool per job, as with computer_desktop: computer_id picks the place. Without
// it the Bot computer serves the call, as before. With the id of a Mac on which
// the person granted this Bot folders, the call becomes a folders.* job on that
// Mac. The Mac runs every file operation and every command under one sandbox
// policy built from this Bot's grants, so the server never decides a path.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const macFilesNote = " On a Mac (computer_id of a Mac from the run context): paths are absolute paths inside the folders the person granted you there, such as /Users/name/project/src/app.ts; anything outside them is refused. Actions there: files.list, files.read (offset and limit in lines, numbered lines, at most 2000 lines or 64 KB per call; next_offset when there is more), files.write (whole file, at most 512 KB) and files.edit (old_string must match exactly once, or set replace_all; read the file first). files.view, append and expected_sha256 are Bot computer only. The person's grant covers writes and edits there; they change the person's real files, so keep them to what was asked."

const macShellNote = " On a Mac (computer_id of a Mac from the run context): the command runs with bash in cwd, an absolute path inside a folder the person granted you (default: your first folder there), in a sandbox that sees only your folders and the developer tools on the person's PATH. effect is required there: none when the command only reads (git status, git diff, git log, ls, grep, rg, cat; it then runs read-only with no network and is not reviewed), change when it creates or edits files in your folders (build, test, format, npm install, git add, git commit), delete when it removes work git cannot bring back (rm -rf, git reset --hard, git clean), publish or send when it pushes or uploads (git push, deploy). Anything but none is reviewed and may need the person's approval. Network reaches package registries and code hosts only; no SSH keys, Keychain or saved git credentials, so pushing to a private remote fails: ask the person to push. Servers and tests that listen on localhost cannot bind. timeout_sec is 1 to 240 (default 120); nothing keeps running after the call returns. secrets is Bot computer only."

// withMacFolders lets computer_files and computer_shell reach this Bot's Mac
// folders. Without a Bot computer it adds Mac-only tools of the same names.
func (s *Server) withMacFolders(r Run, tools []Tool) []Tool {
	if len(s.macFoldersForBot(r.BotID)) == 0 {
		return tools
	}
	found := map[string]bool{}
	for i, t := range tools {
		if t.Name == "computer_files" || t.Name == "computer_shell" {
			tools[i] = s.macFolderTool(r, t)
			found[t.Name] = true
		}
	}
	for _, name := range []string{"computer_files", "computer_shell"} {
		if !found[name] {
			tools = append(tools, s.macFolderTool(r, Tool{Name: name}))
		}
	}
	return tools
}

func (s *Server) macFolderTool(r Run, vm Tool) Tool {
	vmExecute := vm.Execute
	files := vm.Name == "computer_files"
	t := vm
	props := map[string]any{}
	required := []string{}
	if vm.Parameters != nil {
		if p, ok := vm.Parameters["properties"].(map[string]any); ok {
			props = maps.Clone(p)
		}
		if req, ok := vm.Parameters["required"].([]string); ok {
			required = append(required, req...)
		}
	}
	props["computer_id"] = map[string]any{"type": "string", "description": "Omit for the Bot computer. The id of a Mac from the run context to work in your folders on that Mac."}
	if files {
		props["action"] = map[string]any{"type": "string", "enum": []string{"files.list", "files.read", "files.write", "files.edit", "files.view"}, "description": "files.list, files.read, files.write, files.edit (Mac only) or files.view (Bot computer only)"}
		props["path"] = map[string]any{"type": "string", "description": "On the Bot computer: relative to this Bot profile, or absolute under /workspace. On a Mac: an absolute path inside your folders there."}
		props["old_string"] = map[string]any{"type": "string", "description": "files.edit: the exact text to replace"}
		props["new_string"] = map[string]any{"type": "string", "description": "files.edit: the replacement text"}
		props["replace_all"] = map[string]any{"type": "boolean", "description": "files.edit: replace every occurrence instead of exactly one"}
		if _, ok := props["content"]; !ok {
			props["content"] = map[string]any{"type": "string"}
		}
		if _, ok := props["offset"]; !ok {
			props["offset"] = map[string]any{"type": "integer", "minimum": 1}
			props["limit"] = map[string]any{"type": "integer", "minimum": 1}
		}
		if len(required) == 0 {
			required = []string{"action"}
		}
	} else {
		props["cwd"] = map[string]any{"type": "string", "description": "Mac only: an absolute folder path inside your folders there. Default: your first folder."}
		props["effect"] = map[string]any{"type": "string", "enum": []string{"none", "change", "delete", "publish", "send", "other_external"}, "description": "Mac only, required there: what the command changes. none = it only reads."}
		if _, ok := props["command"]; !ok {
			props["command"] = map[string]any{"type": "string", "description": "bash command"}
			props["timeout_sec"] = map[string]any{"type": "integer", "minimum": 1, "maximum": folderExecMax}
		}
		if len(required) == 0 {
			required = []string{"command"}
		}
	}
	t.Parameters = objectSchema(props, required)
	if vmExecute == nil {
		// No Bot computer: the tool serves Macs only.
		if files {
			t.Description = "List, read, write or edit files in the folders the person granted you on their Mac." + macFilesNote
		} else {
			t.Description = "Run a bounded shell command in a folder the person granted you on their Mac." + macShellNote
		}
		t.Timeout = computerToolTimeout
	} else if files {
		t.Description = vm.Description + macFilesNote
	} else {
		t.Description = vm.Description + macShellNote
	}
	if !files {
		// A command may run up to folderExecMax on the Mac; the tool waits for its answer.
		t.Timeout = max(t.Timeout, computerDispatchTimeoutFor("folders.exec", json.RawMessage(fmt.Sprintf(`{"timeout_sec":%d}`, folderExecMax)))+computerShellMargin)
	}
	mac := func(raw json.RawMessage) (string, bool) {
		var in struct {
			ComputerID string `json:"computer_id"`
		}
		_ = json.Unmarshal(raw, &in)
		id := strings.TrimSpace(in.ComputerID)
		if id == "" || id == microVMComputerID {
			if vmExecute == nil {
				if macs := s.macFoldersForBot(r.BotID); len(macs) == 1 {
					return macs[0].ComputerID, true
				}
				return "", true
			}
			return "", false
		}
		if vm := s.computer(); vm != nil && vm.ID() == id {
			return "", false
		}
		return id, true
	}
	identity, resolve := vm.Identity, vm.ResolveIdentity
	t.Identity = func(raw json.RawMessage) tooloutcome.Identity {
		if id, onMac := mac(raw); onMac {
			action, args, err := macFolderCall(files, raw)
			if err != nil {
				return tooloutcome.OperationIdentity("computer/"+id, t.Name, raw)
			}
			return macFolderIdentity(r.BotID, id, action, args)
		}
		if identity != nil {
			return identity(withoutMacFields(files, raw))
		}
		return tooloutcome.OperationIdentity(t.Name, t.Name, raw)
	}
	t.ResolveIdentity = func(ctx context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
		if _, onMac := mac(raw); onMac || resolve == nil {
			return t.Identity(raw), nil
		}
		return resolve(ctx, withoutMacFields(files, raw))
	}
	t.Execute = func(ctx context.Context, raw json.RawMessage) (string, error) {
		id, onMac := mac(raw)
		if !onMac {
			if vmExecute == nil {
				return "", tooloutcome.InvalidArguments("There is no Bot computer; pass computer_id of a Mac from the run context.")
			}
			var in struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(raw, &in)
			if files && in.Action == "files.edit" {
				return "", tooloutcome.InvalidArguments("files.edit works on a Mac only. On the Bot computer read the file and write it whole.")
			}
			return vmExecute(ctx, withoutMacFields(files, raw))
		}
		if id == "" {
			return "", tooloutcome.InvalidArguments("You have folders on more than one Mac: pass computer_id of the Mac from the run context.")
		}
		action, args, err := macFolderCall(files, raw)
		if err != nil {
			return "", err
		}
		if e := s.macFolderReady(r, id); e != nil {
			return "", e
		}
		out, err := s.computerActionOn(ctx, r, id, action, args)
		return s.identifyResult(id, false, out, err, nil)
	}
	return t
}

// macFolderCall turns the tool input into a folders.* job.
func macFolderCall(files bool, raw json.RawMessage) (string, json.RawMessage, error) {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if files {
		var in struct {
			ComputerID string  `json:"computer_id"`
			Action     string  `json:"action"`
			Path       string  `json:"path"`
			Content    *string `json:"content"`
			Offset     int     `json:"offset"`
			Limit      int     `json:"limit"`
			OldString  *string `json:"old_string"`
			NewString  *string `json:"new_string"`
			ReplaceAll bool    `json:"replace_all"`
			Append     bool    `json:"append"`
			Expected   *string `json:"expected_sha256"`
		}
		if err := d.Decode(&in); err != nil {
			return "", nil, tooloutcome.InvalidArguments("computer_files on a Mac takes action, path and the fields of that action")
		}
		if in.Append || in.Expected != nil {
			return "", nil, tooloutcome.InvalidArguments("append and expected_sha256 work on the Bot computer only. On a Mac write the whole file or use files.edit.")
		}
		var args any
		switch in.Action {
		case "files.list":
			args = map[string]any{"path": in.Path}
		case "files.read":
			m := map[string]any{"path": in.Path}
			if in.Offset > 0 {
				m["offset"] = in.Offset
			}
			if in.Limit > 0 {
				m["limit"] = in.Limit
			}
			args = m
		case "files.write":
			if in.Content == nil {
				return "", nil, tooloutcome.InvalidArguments("files.write needs content")
			}
			args = map[string]any{"path": in.Path, "content": *in.Content}
		case "files.edit":
			if in.OldString == nil || in.NewString == nil {
				return "", nil, tooloutcome.InvalidArguments("files.edit needs old_string and new_string")
			}
			args = map[string]any{"path": in.Path, "old_string": *in.OldString, "new_string": *in.NewString, "replace_all": in.ReplaceAll}
		case "files.view":
			return "", nil, tooloutcome.InvalidArguments("files.view works on the Bot computer only. On a Mac read the file, or describe what to look at to the person.")
		default:
			return "", nil, tooloutcome.InvalidArguments("computer_files on a Mac takes files.list, files.read, files.write or files.edit")
		}
		b, _ := json.Marshal(args)
		action := "folders." + strings.TrimPrefix(in.Action, "files.")
		if err := validateFolderArgs(action, b); err != nil {
			return "", nil, tooloutcome.InvalidArguments(err.Error())
		}
		return action, b, nil
	}
	var in struct {
		ComputerID string   `json:"computer_id"`
		Command    string   `json:"command"`
		Cwd        string   `json:"cwd"`
		Effect     string   `json:"effect"`
		TimeoutSec int      `json:"timeout_sec"`
		Secrets    []string `json:"secrets"`
	}
	if err := d.Decode(&in); err != nil {
		return "", nil, tooloutcome.InvalidArguments("computer_shell on a Mac takes command, cwd, effect and timeout_sec")
	}
	if len(in.Secrets) > 0 {
		return "", nil, tooloutcome.InvalidArguments("secrets work on the Bot computer only; a command on a Mac gets none.")
	}
	m := map[string]any{"command": in.Command, "effect": strings.TrimSpace(strings.ToLower(in.Effect))}
	if in.Cwd != "" {
		m["cwd"] = in.Cwd
	}
	if in.TimeoutSec != 0 {
		m["timeout_sec"] = in.TimeoutSec
	}
	b, _ := json.Marshal(m)
	if err := validateFolderArgs("folders.exec", b); err != nil {
		return "", nil, tooloutcome.InvalidArguments(err.Error())
	}
	return "folders.exec", b, nil
}

// withoutMacFields drops the Mac-only fields before the Bot computer's own
// decoder sees them (it refuses unknown fields).
func withoutMacFields(files bool, raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return raw
	}
	keys := []string{"computer_id", "cwd", "effect"}
	if files {
		keys = []string{"computer_id", "old_string", "new_string", "replace_all"}
	}
	changed := false
	for _, k := range keys {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	b, _ := json.Marshal(m)
	return b
}

// macFolderIdentity is the recovery identity of a folders.* job: reads and
// commands that only read are observations; everything else changes the Mac.
func macFolderIdentity(bot, computerID, action string, args json.RawMessage) tooloutcome.Identity {
	i := tooloutcome.OperationIdentity("computer/"+computerID+"/bot/"+bot, action, args)
	switch action {
	case "folders.list", "folders.read":
		i.Risk = tooloutcome.Observation
	case "folders.exec":
		var v struct {
			Effect string `json:"effect"`
		}
		_ = json.Unmarshal(args, &v)
		if !consequentialEffect(v.Effect) {
			i.Risk = tooloutcome.Observation
		}
	}
	return i
}

// macFolderReady answers at once, in the server's words, when the Mac cannot
// take the call: not a Mac of this account, no folders for this Bot, offline.
func (s *Server) macFolderReady(r Run, id string) error {
	row, err := s.store.computerRow(id)
	if err != nil || row.Kind != computer.RowKindPersonal {
		return tooloutcome.InvalidArguments("computer_id " + id + " is not a Mac of this account; use an id from the run context.")
	}
	for _, m := range s.macFoldersForBot(r.BotID) {
		if m.ComputerID != id {
			continue
		}
		if !m.Online {
			seen := lastSeenAgo(m.LastSeen)
			if seen != "" {
				seen = " (last seen " + seen + ")"
			}
			return tooloutcome.New(tooloutcome.Transient, "computer_offline", "not_executed", row.Name+" is offline"+seen+": the TOFI app is not open there or the Mac is asleep, so its folders cannot be reached now. Nothing was done. Tell the person; do not retry until they say the app is open.", "explain_blocker").Err()
		}
		return nil
	}
	return folderNotGrantedOutcome(row.Name).Err()
}

func folderNotGrantedOutcome(mac string) tooloutcome.Outcome {
	if mac == "" {
		mac = "this Mac"
	}
	return tooloutcome.New(tooloutcome.Denied, "folder_not_granted", "not_executed", "The person has not granted you this folder on "+mac+", so nothing was done. Work only in the folders listed in the run context. To use another folder, ask the person to add it for you in the TOFI Mac app: Settings > Environments > Folders on this Mac.", "explain_blocker")
}

// macFolderError words a Mac's refusal of a folder job. The Mac sends only a code.
func macFolderError(code string) (tooloutcome.Outcome, bool) {
	words := map[string][2]string{
		"outside_grant":         {tooloutcome.Denied, "The path is outside the folders the person granted you on this Mac (a link or a parent path may lead out of them), so nothing was done. Use a path inside your folders."},
		"folder_missing":        {tooloutcome.Permanent, "This granted folder is no longer on the Mac (it was moved, renamed or deleted), so nothing was done. Tell the person; they can remove it and add it again in the TOFI Mac app."},
		"folder_moved":          {tooloutcome.Permanent, "This granted folder was replaced by another folder at the same path, so the grant no longer applies and nothing was done. Tell the person; they can add it again in the TOFI Mac app."},
		"mac_busy":              {tooloutcome.Transient, "The Mac is still running your previous command or several folder steps at once, so this step was not started. Wait for the running command to finish, then try again."},
		"not_found":             {tooloutcome.Permanent, "No file or folder exists at this path on the Mac. Nothing was changed. List the folder to find the right name."},
		"not_a_file":            {tooloutcome.Permanent, "This path is a folder, not a file. Nothing was changed."},
		"not_a_directory":       {tooloutcome.Permanent, "This path is a file, not a folder. Nothing was changed."},
		"binary_file":           {tooloutcome.Permanent, "This file is not text, so it was not read. Nothing was changed."},
		"old_string_not_found":  {tooloutcome.Permanent, "old_string does not occur in the file, so nothing was changed. Read the file again and copy the exact text, including spaces and line breaks."},
		"old_string_not_unique": {tooloutcome.Permanent, "old_string occurs more than once, so nothing was changed. Add surrounding lines to make it unique, or set replace_all."},
		"file_changed":          {tooloutcome.Transient, "The file changed on the Mac while it was being edited, so nothing was changed. Read it again and repeat the edit."},
		"write_denied":          {tooloutcome.Denied, "The sandbox does not let you write this path (git hooks and config, editor settings and shell startup files are protected), so nothing was changed. Ask the person if that file must change."},
		"symlink":               {tooloutcome.Denied, "The path is a symbolic link; writes do not follow links. Nothing was changed. Write the file the link points to, if it is inside your folders."},
		"too_large":             {tooloutcome.Permanent, "The file is too large for one read or write here. Nothing was changed. Read it in parts with offset and limit, or use a command."},
		"runtime_unavailable":   {tooloutcome.Permanent, "Commands are not available on this Mac (its TOFI app has no working command sandbox), so nothing was run. Files still work. Tell the person."},
		"sandbox_fault":         {tooloutcome.Permanent, "The Mac could not confirm that your previous command stopped, so it does not start new commands for you until the TOFI app restarts. Nothing was run. Tell the person."},
	}
	w, ok := words[code]
	if !ok {
		return tooloutcome.Outcome{}, false
	}
	next := "explain_blocker"
	switch code {
	case "not_found", "not_a_file", "not_a_directory", "old_string_not_found", "old_string_not_unique", "too_large", "outside_grant", "symlink":
		next = "repair_arguments"
	case "mac_busy", "file_changed":
		next = "retry_later"
	}
	return tooloutcome.New(w[0], code, "not_executed", w[1], next), true
}
