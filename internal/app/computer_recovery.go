package app

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Both computer_action and convenience wrappers use this identity. It discards
// fields that the guest ignores for the selected operation, and normalizes
// effective defaults. It performs no remote observation or execution.
func computerRecoveryIdentity(bot, computerID, action string, raw json.RawMessage) tooloutcome.Identity {
	args := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &args)
	if computerID != microVMComputerID {
		i := tooloutcome.OperationIdentity("computer/"+computerID, action, raw)
		if action == "files.read" || action == "files.list" || action == "screen.capture" || action == "host.info" {
			i.Risk = tooloutcome.Observation
		}
		return i
	}
	if action == "browser.action" {
		var command string
		_ = json.Unmarshal(args["action"], &command)
		if command != "" {
			action = "browser." + command
			delete(args, "action")
		}
	}
	fields := map[string]string{
		"shell.exec": "command timeout_sec",
		"files.list": "path", "files.read": "path offset limit", "files.write": "path content append expected_sha256",
		"desktop.start": "", "desktop.stop": "", "desktop.capture": "",
		"desktop.click": "x y button screenshot_width screenshot_height", "desktop.type": "text", "desktop.key": "key modifiers", "desktop.scroll": "direction amount",
		"terminal.open": "command cols rows", "terminal.list": "", "terminal.read": "terminal_id cursor max_bytes", "terminal.write": "terminal_id data data_base64", "terminal.resize": "terminal_id cols rows", "terminal.close": "terminal_id",
		"browser.navigate": "url target_id", "browser.snapshot": "target_id", "browser.read": "find max_chars", "browser.new": "url", "browser.switch": "target_id", "browser.close": "target_id",
	}
	if keys, known := fields[action]; known {
		effective := map[string]json.RawMessage{}
		for _, key := range strings.Fields(keys) {
			if value, ok := args[key]; ok {
				effective[key] = value
			}
		}
		args = effective
	}
	// The guest treats omitted/empty values identically for these fields.
	for key, value := range args {
		if string(value) == "null" || string(value) == "false" || string(value) == "0" || string(value) == `""` || string(value) == "[]" {
			delete(args, key)
		}
	}
	if action == "shell.exec" && string(args["timeout_sec"]) == "60" {
		delete(args, "timeout_sec")
	}
	if action == "desktop.click" && string(args["button"]) == "1" {
		delete(args, "button")
	}
	if action == "desktop.scroll" && string(args["amount"]) == "3" {
		delete(args, "amount")
	}

	if strings.HasPrefix(action, "files.") {
		var name string
		_ = json.Unmarshal(args["path"], &name)
		if strings.TrimSpace(name) == "" {
			name = "."
		}
		if !path.IsAbs(name) {
			name = path.Join("/workspace/bots", bot, name)
		}
		args["path"], _ = json.Marshal(path.Clean(name))
		if value, ok := args["expected_sha256"]; ok {
			var hash string
			if json.Unmarshal(value, &hash) == nil {
				hash = strings.TrimSpace(hash)
				if hash == "" {
					delete(args, "expected_sha256")
				} else {
					args["expected_sha256"], _ = json.Marshal(hash)
				}
			}
		}
	}
	encoded, _ := json.Marshal(args)
	i := tooloutcome.OperationIdentity("computer/"+computerID+"/bot/"+bot, action, encoded)
	if isReadOnlyMicroVMAction(action) || action == "browser.read" || action == "terminal.list" || action == "terminal.read" {
		i.Risk = tooloutcome.Observation
	}
	if action == "files.write" {
		i.Risk = tooloutcome.TargetMutation
		i.ResolutionRequired = true
	}
	return i
}

func (s *Server) resolveComputerRecovery(ctx context.Context, r Run, computerID, action string, args json.RawMessage) (tooloutcome.Identity, error) {
	i := computerRecoveryIdentity(r.BotID, computerID, action, args)
	if !i.ResolutionRequired {
		return i, nil
	}
	if s.microVM == nil {
		return i, tooloutcome.New(tooloutcome.Permanent, "computer_unavailable", "not_executed", "Computer VM is not configured.", "explain_blocker").Err()
	}
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &in) != nil {
		return i, tooloutcome.InvalidArguments("Invalid file operation arguments.")
	}
	raw, _ := json.Marshal(in)
	lookupCtx, cancelLookup := context.WithTimeout(ctx, 5*time.Second)
	defer cancelLookup()
	result, err := s.microVM.Action(lookupCtx, computer.Action{BotID: r.BotID, RunID: r.ID, Name: "files.identity", Args: raw, Source: "model"})
	if err != nil {
		if ctx.Err() != nil {
			return i, ctx.Err()
		}
		// Older/offline backends may lack this lookup. Preserve ordinary first-call
		// behavior, but keep unknown targets opaque: the boundary cannot authorize
		// another mutation while an uncertain effect is unresolved.
		i.Risk, i.ResolutionRequired = tooloutcome.OpaqueEffect, false
		return i, nil
	}
	var identity struct {
		Target       string `json:"target"`
		Object       string `json:"object"`
		Parent       string `json:"parent"`
		ParentObject string `json:"parent_object"`
		GuardVersion int    `json:"guard_version"`
	}
	if json.Unmarshal(result.Result, &identity) != nil || identity.Target == "" || identity.Parent == "" || identity.ParentObject == "" || identity.GuardVersion != 1 {
		i.Risk, i.ResolutionRequired = tooloutcome.OpaqueEffect, false
		return i, nil
	}
	i.Target, i.Object, i.ResolutionRequired = identity.Target, identity.Object, false
	i.Parent, i.ParentObject, i.GuardVersion = identity.Parent, identity.ParentObject, identity.GuardVersion
	return i, nil
}
