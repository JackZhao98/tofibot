package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

// Terminal control shares the existing computer owner: a viewer never claims
// ownership, and keyboard input cannot race a model or desktop controller.
func (s *Server) handleTerminalAction(w http.ResponseWriter, r *http.Request, botID, action string, raw json.RawMessage) {
	if !s.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeErr(w, 403, "csrf", "origin rejected")
		return
	}
	var in struct {
		ControlID  string `json:"control_id"`
		Seq        uint64 `json:"seq"`
		TerminalID string `json:"terminal_id"`
		Data       string `json:"data"`
		Command    string `json:"command"`
		Cols       int    `json:"cols"`
		Rows       int    `json:"rows"`
		Cursor     uint64 `json:"cursor"`
		MaxBytes   int    `json:"max_bytes"`
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		writeErr(w, 400, "invalid_request", "invalid terminal arguments")
		return
	}
	if action == "terminal.list" || action == "terminal.read" {
		args := map[string]any{}
		if action == "terminal.read" {
			args = map[string]any{"terminal_id": in.TerminalID, "cursor": in.Cursor, "max_bytes": in.MaxBytes}
		}
		b, _ := json.Marshal(args)
		result, err := s.microVM.Action(r.Context(), computer.Action{BotID: botID, RunID: "viewer-" + uuid.NewString(), Name: action, Args: b, Source: "viewer"})
		if err != nil {
			writeErr(w, 502, "terminal_unavailable", err.Error())
			return
		}
		var value map[string]any
		if json.Unmarshal(result.Result, &value) != nil {
			writeErr(w, 502, "terminal_unavailable", "invalid terminal response")
			return
		}
		if action == "terminal.list" {
			normalizeTerminalCommands(value)
			ids := []string{}
			rows, err := s.store.db.Query(`SELECT id FROM runs WHERE bot_id=? AND status IN ('queued','running')`, botID)
			if err != nil {
				writeErr(w, 500, "storage", "cannot inspect active tasks")
				return
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				writeErr(w, 500, "storage", "cannot inspect active tasks")
				return
			}
			value["active_run_ids"] = ids
		}
		writeJSON(w, 200, map[string]any{"ok": true, "result": value})
		return
	}
	if action == "terminal.control.acquire" {
		if in.ControlID != "" {
			writeErr(w, 400, "invalid_request", "acquire does not accept control_id")
			return
		}
		lease := s.terminalLease(botID)
		if !lease.TryLock() {
			writeErr(w, 409, "computer_busy", "电脑正在执行操作，请稍后重试")
			return
		}
		defer lease.Unlock()
		if s.botHasActiveRun(botID) {
			writeErr(w, 409, "computer_busy", "Bot 正在工作，接管前需要停止当前任务")
			return
		}
		c := &computerControl{id: uuid.NewString(), botID: botID, terminal: true}
		if !s.claimTerminalOwner(botID, "human-control:"+c.id) {
			writeErr(w, 409, "computer_busy", "电脑已被其他窗口或任务接管")
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		s.computerOwnerMu.Lock()
		if s.computerControls == nil {
			s.computerControls = map[string]*computerControl{}
		}
		s.computerControls["terminal:"+botID] = c
		s.computerOwnerMu.Unlock()
		s.renewControlTimer(c)
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"control_id": c.id, "lease_ms": 15000}})
		return
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls["terminal:"+botID]
	s.computerOwnerMu.Unlock()
	if c == nil || c.id != in.ControlID || !c.terminal {
		writeErr(w, 409, "control_expired", "控制已释放，请重新接管")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if action == "terminal.control.release" {
		s.releaseControl(c)
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]bool{"released": true}})
		return
	}
	if c.released || !c.expires.After(time.Now()) {
		s.releaseControl(c)
		writeErr(w, 409, "control_expired", "控制已过期，请重新接管")
		return
	}
	if action == "terminal.control.renew" {
		s.renewControlTimer(c)
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]bool{"renewed": true}})
		return
	}
	args := map[string]any{"terminal_id": in.TerminalID}
	switch action {
	case "terminal.open":
		if strings.TrimSpace(in.Command) == "" {
			in.Command = s.defaultTerminalCommand(botID)
		}
		args = map[string]any{"command": in.Command, "cols": in.Cols, "rows": in.Rows}
	case "terminal.write":
		args["data"] = in.Data
	case "terminal.resize":
		args["cols"] = in.Cols
		args["rows"] = in.Rows
	case "terminal.close":
	default:
		writeErr(w, 400, "invalid_request", "unsupported terminal action")
		return
	}
	if in.Seq != c.seq+1 {
		writeErr(w, 409, "input_sequence", "输入顺序已失效，请重新接管")
		return
	}
	c.seq = in.Seq
	lease := s.terminalLease(botID)
	if err := lockComputerLease(r.Context(), lease); err != nil {
		writeControlError(w, err)
		return
	}
	defer lease.Unlock()
	result, err := s.controlGuest(r.Context(), c, action, args)
	if err != nil {
		writeErr(w, 502, "terminal_action_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) terminalTool(r Run) Tool {
	return Tool{Name: "computer_terminal", Description: "Open and operate a persistent PTY terminal in this Bot's VM workspace. Tabs and live output are visible to the human in Terminal. Use for interactive or long-running commands. open starts a shell when command is omitted; read returns incremental text and a next_cursor; write sends text/control characters. Sessions are bounded and end on VM restart; working files persist. User control blocks model actions. Close finished sessions.", Parameters: objectSchema(map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"open", "list", "read", "write", "resize", "close"}},
		"terminal_id": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"}, "data": map[string]any{"type": "string"}, "cursor": map[string]any{"type": "integer", "minimum": 0}, "cols": map[string]any{"type": "integer"}, "rows": map[string]any{"type": "integer"},
	}, []string{"action"}), Identity: func(raw json.RawMessage) tooloutcome.Identity {
		var in map[string]json.RawMessage
		_ = json.Unmarshal(raw, &in)
		var action string
		_ = json.Unmarshal(in["action"], &action)
		delete(in, "action")
		if action == "open" {
			var command string
			_ = json.Unmarshal(in["command"], &command)
			if strings.TrimSpace(command) == "" {
				in["command"], _ = json.Marshal(s.defaultTerminalCommand(r.BotID))
			}
		}
		args, _ := json.Marshal(in)
		return computerRecoveryIdentity(r.BotID, microVMComputerID, "terminal."+action, args)
	}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in map[string]json.RawMessage
		if json.Unmarshal(raw, &in) != nil {
			return "", errors.New("invalid terminal input")
		}
		var action string
		if json.Unmarshal(in["action"], &action) != nil || !microVMActions["terminal."+action] {
			return "", errors.New("invalid terminal action")
		}
		delete(in, "action")
		if action == "open" {
			var command string
			_ = json.Unmarshal(in["command"], &command)
			if strings.TrimSpace(command) == "" {
				in["command"], _ = json.Marshal(s.defaultTerminalCommand(r.BotID))
			}
		}
		args, _ := json.Marshal(in)
		result, err := s.microVMAction(ctx, r, "terminal."+action, args)
		if err != nil || action != "read" {
			return result, err
		}
		var output map[string]any
		if json.Unmarshal([]byte(result), &output) != nil {
			return result, nil
		}
		if encoded, ok := output["data_base64"].(string); ok {
			decoded, e := base64.StdEncoding.DecodeString(encoded)
			if e == nil {
				delete(output, "data_base64")
				output["data"] = strings.ToValidUTF8(string(decoded), "�")
				b, _ := json.Marshal(output)
				return string(b), nil
			}
		}
		return result, nil
	}}
}

// Keep names and paths in variables: PS1 expands them as data, never as shell
// source. Capture the guest-assigned working directory without changing HOME.
func (s *Server) defaultTerminalCommand(botID string) string {
	name := "Bot"
	if bot, err := s.store.GetBot(botID); err == nil {
		name = bot.Name
	}
	return botTerminalCommand(name)
}

func botTerminalCommand(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	const rc = `[[ -f ~/.bashrc ]] && source ~/.bashrc
__tofi_prompt() {
 case "$PWD" in
 "$TOFI_PROMPT_ROOT") TOFI_PROMPT_PATH="$TOFI_PROMPT_ROOT" ;;
 "$TOFI_PROMPT_ROOT"/*) TOFI_PROMPT_PATH="$TOFI_PROMPT_ROOT${PWD#"$TOFI_PROMPT_ROOT"}" ;;
 *) TOFI_PROMPT_PATH="$PWD" ;;
 esac
 PS1='${TOFI_PROMPT_LABEL} › ${TOFI_PROMPT_PATH} $ '
}
shopt -s promptvars
if (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1) )); then
 PROMPT_COMMAND+=(__tofi_prompt)
else
 PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }__tofi_prompt"
fi
`
	return "# tofi-default-terminal\nexport TOFI_PROMPT_LABEL=" + quote(name) + " TOFI_PROMPT_ROOT=\"$PWD\"; exec bash --rcfile <(printf '%s' " + quote(rc) + ") -i"
}

func normalizeTerminalCommands(value map[string]any) {
	for _, key := range []string{"terminals", "sessions"} {
		rows, _ := value[key].([]any)
		for _, row := range rows {
			entry, _ := row.(map[string]any)
			command, _ := entry["command"].(string)
			if strings.HasPrefix(command, "# tofi-default-terminal\n") {
				entry["command"] = "exec bash -i"
			}
		}
	}
}
