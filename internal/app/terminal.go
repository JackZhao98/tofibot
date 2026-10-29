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
		result, err := s.computer().Action(r.Context(), computer.Action{BotID: botID, RunID: "viewer-" + uuid.NewString(), Name: action, Args: b, Source: "viewer"})
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
			// Terminals whose command end wakes the Bot: the Terminal tab marks them.
			notify, err := waitIDsTx(s.store.db, `SELECT target FROM waits WHERE bot_id=? AND kind='command_exit' AND status='pending'`, botID)
			if err != nil {
				writeErr(w, 500, "storage", "cannot inspect command waits")
				return
			}
			if notify == nil {
				notify = []string{}
			}
			value["notify_terminal_ids"] = notify
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
			writeErr(w, 409, "computer_busy", "The computer is busy. Try again shortly.")
			return
		}
		defer lease.Unlock()
		if s.botHasActiveRun(botID) {
			writeErr(w, 409, "computer_busy", "The Bot is working. Stop its current task before taking over.")
			return
		}
		c := &computerControl{id: uuid.NewString(), botID: botID, terminal: true}
		if !s.claimTerminalOwner(botID, "human-control:"+c.id) {
			writeErr(w, 409, "computer_busy", "The computer is already controlled by another window or task.")
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
		writeErr(w, 409, "control_expired", "Control was released. Take over again.")
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
		writeErr(w, 409, "control_expired", "Control expired. Take over again.")
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
		writeErr(w, 409, "input_sequence", "The input sequence is stale. Take over again.")
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
	return Tool{Name: "computer_terminal", Description: terminalToolDescription, Parameters: objectSchema(map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"open", "list", "read", "write", "resize", "close"}},
		"terminal_id": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"}, "data": map[string]any{"type": "string"}, "cursor": map[string]any{"type": "integer", "minimum": 0}, "cols": map[string]any{"type": "integer"}, "rows": map[string]any{"type": "integer"},
		"notify_on_exit": map[string]any{"type": "boolean", "description": "With open (and a command) or write: register a wait on the end of the command. The result has the exit code and the last output. A restart of the computer interrupts the command and the result says so; decide whether to run it again. In a shell terminal the end is the return of the shell to its prompt, with no exit code. End your turn or talk with the person; read the output when the result arrives."},
	}, []string{"action"}), Identity: func(raw json.RawMessage) tooloutcome.Identity {
		var in map[string]json.RawMessage
		_ = json.Unmarshal(raw, &in)
		var action string
		_ = json.Unmarshal(in["action"], &action)
		delete(in, "action")
		delete(in, "notify_on_exit")
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
			return "", tooloutcome.InvalidArguments("invalid terminal input")
		}
		var action string
		if json.Unmarshal(in["action"], &action) != nil || !microVMActions["terminal."+action] {
			return "", tooloutcome.InvalidArguments("invalid terminal action")
		}
		delete(in, "action")
		var notify bool
		if raw, ok := in["notify_on_exit"]; ok {
			if json.Unmarshal(raw, &notify) != nil {
				return "", tooloutcome.InvalidArguments("notify_on_exit must be true or false")
			}
			delete(in, "notify_on_exit")
			if notify && action != "open" && action != "write" {
				return "", tooloutcome.InvalidArguments("notify_on_exit applies to open and write only")
			}
		}
		if action == "open" {
			var command string
			_ = json.Unmarshal(in["command"], &command)
			if strings.TrimSpace(command) == "" {
				in["command"], _ = json.Marshal(s.defaultTerminalCommand(r.BotID))
			}
		}
		args, _ := json.Marshal(in)
		result, err := s.microVMAction(ctx, r, "terminal."+action, args)
		if err == nil && notify {
			result = s.attachCommandWait(ctx, r, action, in, result)
		}
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

const terminalToolDescription = "Open and operate a persistent PTY terminal in this Bot's VM workspace. The human sees tabs and live output in Terminal. Use for interactive or long-running commands. open starts a shell when command is omitted; read returns incremental text and a next_cursor; write sends text and control characters. A terminal keeps running after your turn ends and through hibernation. A restart of the computer ends every terminal; working files persist. User control blocks model actions. Close finished terminals. " +
	"Run a long command in a terminal of its own with notify_on_exit: the system wakes you with the result when it ends, so do not poll: after the receipt, tell the person what they need and end your turn. A timed check is added to the wait; when the command ends first, end the check with cancel_wait."

// attachCommandWait registers the wait that notify_on_exit asks for, after the
// action has succeeded. The action cannot be undone, so a refusal is reported
// inside the result rather than as a failed call.
func (s *Server) attachCommandWait(ctx context.Context, r Run, action string, in map[string]json.RawMessage, result string) string {
	var terminalID string
	if action == "write" {
		_ = json.Unmarshal(in["terminal_id"], &terminalID)
	} else {
		var opened struct {
			ID         string `json:"id"`
			TerminalID string `json:"terminal_id"`
		}
		_ = json.Unmarshal([]byte(result), &opened)
		terminalID = opened.TerminalID
		if terminalID == "" {
			terminalID = opened.ID
		}
	}
	out := map[string]any{}
	if json.Unmarshal([]byte(result), &out) != nil {
		out = map[string]any{"result": result}
	}
	receipt, err := s.registerCommandExitFor(ctx, r, terminalID)
	if err != nil {
		out["notify_on_exit"] = map[string]any{"registered": false, "error": err.Error()}
	} else {
		receipt["registered"] = true
		out["notify_on_exit"] = receipt
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (s *Server) registerCommandExitFor(ctx context.Context, r Run, terminalID string) (map[string]any, error) {
	if strings.TrimSpace(terminalID) == "" {
		return nil, waitRefusal("terminal_not_found", "The call gave no terminal id to watch.")
	}
	if s.store == nil {
		return nil, errors.New("store unavailable")
	}
	return s.registerCommandExit(ctx, commandExitRequest{Run: r, ConversationID: r.ConversationID, TerminalID: terminalID})
}
