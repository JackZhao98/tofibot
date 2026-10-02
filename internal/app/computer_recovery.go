package app

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Both computer_action and convenience wrappers use this identity. It discards
// fields that the guest ignores for the selected operation, and normalizes
// effective defaults. It performs no remote observation or execution.
func computerRecoveryIdentity(bot, computerID, action string, raw json.RawMessage) tooloutcome.Identity {
	args := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &args)
	if computerID != microVMComputerID {
		return tooloutcome.OperationIdentity("computer/"+computerID, action, raw)
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
		"browser.navigate": "url target_id", "browser.snapshot": "target_id", "browser.new": "url", "browser.switch": "target_id", "browser.close": "target_id",
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
	return tooloutcome.OperationIdentity("computer/"+computerID+"/bot/"+bot, action, encoded)
}
