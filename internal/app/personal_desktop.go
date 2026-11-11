package app

// One desktop tool for every kind of computer. computer_desktop acts on the Bot
// computer by default. With computer_id it acts on a person's own paired
// computer: the call is translated to the personal vocabulary (desktop.capture,
// desktop.click, desktop.double_click, ...) and goes through computerActionOn,
// which applies the approval gate for that computer.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const desktopKindsSentence = "computer_id: a person's own Mac."

var (
	desktopComputerIDParam = map[string]any{"type": "string", "description": "Optional. Omit for the Bot computer. Set to the id of a person's own paired computer (list_computers, kind personal) to act on that machine. On it: capture, click, double_click, triple_click, move, drag, scroll, type and key; no start, stop or trace. The Bot computer (no computer_id): start, stop, capture, trace, click, double_click, triple_click, scroll, type, key. Input other than move that does not declare effect needs the person's approval; declare effect none for harmless input such as focusing, scrolling or opening, and the real effect for anything else. capture and move never need approval. button, to_x, to_y and display apply to it only. Files and commands: computer_action with files.* and sandbox.exec."}
	desktopButtonParam     = map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}, "description": "Person's own computer only: mouse button for click and drag, default left."}
	desktopDisplayParam    = map[string]any{"type": "integer", "minimum": 0, "maximum": 15, "description": "Person's own computer only: display id from the displays list of the previous capture; default is the primary display."}
)

func desktopToParam(axis string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "description": "Person's own computer only, drag: end " + axis + " in pixels of the latest capture"}
}

// desktopToolInput is the argument object of computer_desktop.
type desktopToolInput struct {
	Action           string   `json:"action"`
	ComputerID       string   `json:"computer_id,omitempty"`
	Last             int      `json:"last,omitempty"`
	CaptureSeqs      []int    `json:"capture_seqs,omitempty"`
	Columns          int      `json:"columns,omitempty"`
	Detail           string   `json:"detail,omitempty"`
	Share            bool     `json:"share,omitempty"`
	Direction        string   `json:"direction,omitempty"`
	Amount           int      `json:"amount,omitempty"`
	X                *int     `json:"x,omitempty"`
	Y                *int     `json:"y,omitempty"`
	ToX              *int     `json:"to_x,omitempty"`
	ToY              *int     `json:"to_y,omitempty"`
	Button           string   `json:"button,omitempty"`
	Display          *int     `json:"display,omitempty"`
	ScreenshotWidth  int      `json:"screenshot_width,omitempty"`
	ScreenshotHeight int      `json:"screenshot_height,omitempty"`
	Text             string   `json:"text,omitempty"`
	Key              string   `json:"key,omitempty"`
	Modifiers        []string `json:"modifiers,omitempty"`
	Effect           string   `json:"effect,omitempty"`
}

func ptrVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// desktopNeedsComputerID stands for "several Macs and no Bot computer, and the Bot named none".
const desktopNeedsComputerID = "?"

// desktopToolComputer is the computer a computer_desktop call is aimed at: the
// Bot computer unless computer_id names another one. With no Bot computer, the
// only paired Mac with desktop control is the default; with several the Bot
// must name one (desktopNeedsComputerID).
func (s *Server) desktopToolComputer(tool string, raw json.RawMessage) string {
	if tool != "computer_desktop" {
		return microVMComputerID
	}
	var in struct {
		ComputerID string `json:"computer_id"`
	}
	_ = json.Unmarshal(raw, &in)
	id := strings.TrimSpace(in.ComputerID)
	if id == "" && s.computer() == nil {
		if macs := s.desktopMacs(); len(macs) == 1 {
			return macs[0].ID
		}
		return desktopNeedsComputerID
	}
	if id == "" || id == microVMComputerID {
		return microVMComputerID
	}
	if vm := s.computer(); vm != nil && vm.ID() == id {
		return microVMComputerID
	}
	return id
}

// parseDesktopTool turns the tool arguments into an action name and the
// arguments of that action, in the vocabulary of the computer they name.
func (s *Server) parseDesktopTool(raw json.RawMessage) (string, json.RawMessage, error) {
	var in desktopToolInput
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return "", nil, errors.New("computer_desktop requires a desktop action")
	}
	if _, err := normalizeCaptureDetail(in.Detail); err != nil {
		return "", nil, err
	}
	if !strings.HasPrefix(in.Action, "desktop.") {
		in.Action = "desktop." + in.Action // models drop the prefix
	}
	if in.Action == "desktop." {
		return "", nil, errors.New("computer_desktop requires a desktop action")
	}
	if in.Action == "desktop.snapshot" || in.Action == "desktop.screenshot" {
		// Models borrow browser.snapshot's verb; it means a screen capture.
		in.Action = "desktop.capture"
	}
	if in.Share && (in.Action != "desktop.capture" || s.desktopToolComputer("computer_desktop", raw) == microVMComputerID) {
		return "", nil, tooloutcome.InvalidArguments("share applies to capture on a person's own computer (set computer_id).")
	}
	if s.desktopToolComputer("computer_desktop", raw) != microVMComputerID {
		return personalDesktopArgs(in)
	}
	return botDesktopArgs(in)
}

// botDesktopArgs is the Bot computer's vocabulary.
func botDesktopArgs(in desktopToolInput) (string, json.RawMessage, error) {
	if in.Action == "desktop.move" || in.Action == "desktop.drag" {
		return "", nil, tooloutcome.InvalidArguments(in.Action + " is available on a person's own computer only (set computer_id). On the Bot computer use click, scroll, type and key.")
	}
	if (in.Button != "" && in.Button != "left") || in.ToX != nil || in.ToY != nil || in.Display != nil {
		return "", nil, tooloutcome.InvalidArguments("button, to_x, to_y and display are for a person's own computer only; the Bot computer clicks with the left button.")
	}
	var args any
	clicks := 0
	switch in.Action {
	case "desktop.double_click":
		in.Action, clicks = "desktop.click", 2
	case "desktop.triple_click":
		in.Action, clicks = "desktop.click", 3
	}
	switch in.Action {
	case "desktop.start", "desktop.stop", "desktop.capture":
		args = struct{}{}
	case "desktop.trace":
		args = traceArgs{Last: in.Last, CaptureSeqs: in.CaptureSeqs, Columns: in.Columns}
	case "desktop.click":
		args = struct {
			X                int `json:"x,omitempty"`
			Y                int `json:"y,omitempty"`
			Clicks           int `json:"clicks,omitempty"`
			ScreenshotWidth  int `json:"screenshot_width,omitempty"`
			ScreenshotHeight int `json:"screenshot_height,omitempty"`
		}{X: ptrVal(in.X), Y: ptrVal(in.Y), Clicks: clicks, ScreenshotWidth: in.ScreenshotWidth, ScreenshotHeight: in.ScreenshotHeight}
	case "desktop.scroll":
		args = struct {
			Direction string `json:"direction"`
			Amount    int    `json:"amount,omitempty"`
		}{in.Direction, in.Amount}
	case "desktop.type":
		args = struct {
			Text string `json:"text"`
		}{Text: in.Text}
	case "desktop.key":
		args = struct {
			Key       string   `json:"key"`
			Modifiers []string `json:"modifiers,omitempty"`
		}{Key: in.Key, Modifiers: in.Modifiers}
	default:
		return "", nil, errors.New("computer_desktop requires a supported desktop action")
	}
	argsJSON, _ := json.Marshal(args)
	return in.Action, argsJSON, nil
}

// personalDesktopArgs is the vocabulary of a person's own computer.
func personalDesktopArgs(in desktopToolInput) (string, json.RawMessage, error) {
	type point struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	type shot struct {
		ScreenshotWidth  int    `json:"screenshot_width,omitempty"`
		ScreenshotHeight int    `json:"screenshot_height,omitempty"`
		Effect           string `json:"effect,omitempty"`
	}
	sh := shot{ScreenshotWidth: in.ScreenshotWidth, ScreenshotHeight: in.ScreenshotHeight, Effect: in.Effect}
	needPoint := func() error {
		if in.X == nil || in.Y == nil {
			return tooloutcome.InvalidArguments(in.Action + " requires x and y, in pixels of the latest capture")
		}
		return nil
	}
	var args any
	switch in.Action {
	case "desktop.start", "desktop.stop", "desktop.trace":
		return "", nil, tooloutcome.InvalidArguments(in.Action + " exists on the Bot computer only. A person's own computer is already running: use capture to see the screen.")
	case "desktop.capture":
		args = struct {
			Display *int `json:"display,omitempty"`
		}{in.Display}
	case "desktop.click", "desktop.double_click", "desktop.triple_click":
		if err := needPoint(); err != nil {
			return "", nil, err
		}
		args = struct {
			point
			Button string `json:"button,omitempty"`
			shot
		}{point{*in.X, *in.Y}, in.Button, sh}
	case "desktop.move":
		if err := needPoint(); err != nil {
			return "", nil, err
		}
		args = struct {
			point
			shot
		}{point{*in.X, *in.Y}, sh}
	case "desktop.drag":
		if err := needPoint(); err != nil {
			return "", nil, err
		}
		if in.ToX == nil || in.ToY == nil {
			return "", nil, tooloutcome.InvalidArguments("desktop.drag requires x and y (start) and to_x and to_y (end)")
		}
		args = struct {
			From   point  `json:"from"`
			To     point  `json:"to"`
			Button string `json:"button,omitempty"`
			shot
		}{point{*in.X, *in.Y}, point{*in.ToX, *in.ToY}, in.Button, sh}
	case "desktop.scroll":
		if (in.X == nil) != (in.Y == nil) {
			return "", nil, tooloutcome.InvalidArguments("desktop.scroll takes x and y together, or neither")
		}
		a := struct {
			Direction string `json:"direction"`
			Amount    int    `json:"amount,omitempty"`
			X         *int   `json:"x,omitempty"`
			Y         *int   `json:"y,omitempty"`
			shot
		}{Direction: in.Direction, Amount: in.Amount, X: in.X, Y: in.Y, shot: sh}
		args = a
	case "desktop.type":
		args = struct {
			Text   string `json:"text"`
			Effect string `json:"effect,omitempty"`
		}{in.Text, in.Effect}
	case "desktop.key":
		args = struct {
			Key       string   `json:"key"`
			Modifiers []string `json:"modifiers,omitempty"`
			Effect    string   `json:"effect,omitempty"`
		}{in.Key, macModifiers(in.Modifiers), in.Effect}
	default:
		return "", nil, errors.New("computer_desktop requires a supported desktop action")
	}
	argsJSON, _ := json.Marshal(args)
	return in.Action, argsJSON, nil
}

// macModifierNames maps the modifier names a model commonly writes to the four
// names the Mac accepts. The desktop app keeps the same table (desktop-keys.cjs).
var macModifierNames = map[string]string{
	"cmd": "cmd", "command": "cmd", "super": "cmd", "super_l": "cmd", "super_r": "cmd", "meta": "cmd", "win": "cmd",
	"ctrl": "ctrl", "control": "ctrl", "control_l": "ctrl", "control_r": "ctrl",
	"alt": "alt", "option": "alt", "opt": "alt", "alt_l": "alt", "alt_r": "alt",
	"shift": "shift", "shift_l": "shift", "shift_r": "shift",
}

// macModifiers returns the Mac names for known aliases. An unknown name stays
// as written, so the action check rejects it and names the accepted ones.
func macModifiers(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, m := range in {
		if name, ok := macModifierNames[strings.ToLower(strings.TrimSpace(m))]; ok {
			out[i] = name
		} else {
			out[i] = m
		}
	}
	return out
}

// macModifierOrder is the order the desktop app presses modifiers in.
var macModifierOrder = []string{"cmd", "ctrl", "alt", "shift"}

// keyChord reads a key as the Mac will press it: modifiers written into the key
// ("cmd+shift+t"; "ctrl++" is Ctrl with the plus key) merged with the modifiers
// list, in the Mac's names and order, and the key itself. A key whose prefix is
// not made of modifier names stays whole. Unknown modifier names in the list
// are kept last, for the action check to refuse.
func keyChord(key string, modifiers []string) ([]string, string) {
	base, prefix := key, ""
	if strings.HasSuffix(key, "++") {
		prefix, base = key[:len(key)-2], "+"
	} else if i := strings.LastIndex(key, "+"); i > 0 && i < len(key)-1 {
		prefix, base = key[:i], key[i+1:]
	}
	held := map[string]bool{}
	var unknown []string
	for _, m := range macModifiers(modifiers) {
		if _, ok := macModifierNames[m]; ok {
			held[m] = true
		} else {
			unknown = append(unknown, m)
		}
	}
	if prefix != "" {
		var inKey []string
		for _, part := range strings.Split(prefix, "+") {
			name, ok := macModifierNames[strings.ToLower(strings.TrimSpace(part))]
			if !ok {
				inKey, base = nil, key
				break
			}
			inKey = append(inKey, name)
		}
		for _, name := range inKey {
			held[name] = true
		}
	}
	var out []string
	for _, name := range macModifierOrder {
		if held[name] {
			out = append(out, name)
		}
	}
	return append(out, unknown...), base
}

// personalDesktop runs one translated computer_desktop call on a person's own
// computer. The approval gate, the computer's lock and the Mac's own refusals
// are in computerActionOn and the provider.
func (s *Server) personalDesktop(ctx context.Context, r Run, id, action string, args json.RawMessage, detail string, share bool) (string, error) {
	if id == desktopNeedsComputerID {
		msg := "Pass computer_id: there is no Bot computer"
		if macs := s.desktopMacs(); len(macs) > 0 {
			var names []string
			for _, m := range macs {
				names = append(names, m.Name+" (id="+m.ID+")")
			}
			msg += " and more than one Mac can be controlled: " + strings.Join(names, "; ") + "."
		} else {
			msg += " and no paired Mac with desktop control; list_computers shows what is paired."
		}
		return "", tooloutcome.InvalidArguments(msg)
	}
	row, err := s.store.computerRow(id)
	if err != nil || row.Kind != computer.RowKindPersonal || s.ensurePersonalSlot(id) == nil {
		return "", tooloutcome.InvalidArguments("computer_id must be the id of a person's own paired computer from list_computers; omit it for the Bot computer.")
	}
	if err := s.personalOfflineError(ctx, id); err != nil {
		return "", err
	}
	if share {
		if _, err := s.guardShare(ctx, r, row.Name); err != nil {
			return "", err
		}
	}
	out, err := s.computerActionOn(ctx, r, id, action, args)
	if err != nil {
		return "", err
	}
	// The picture of a person's own screen is not kept in the run's work trace.
	if action == "desktop.capture" {
		out = applyCaptureDetail(out, detail)
	}
	if share {
		return s.shareCapture(r, out)
	}
	return out, nil
}

// guardShare asks the Mac's owner (an approval card) before a picture of the
// screen goes to a conversation that has other people in it.
func (s *Server) guardShare(ctx context.Context, r Run, mac string) (func(error), error) {
	c, err := s.store.GetConversation(r.ConversationID)
	if err != nil {
		return nil, err
	}
	if !s.store.conversationHasOtherPeople(c) {
		return func(error) {}, nil
	}
	label := "Show a screenshot of " + oneLine(mac, 60) + " to everyone in this conversation"
	if id, ok := s.claimApproval(r, label); ok {
		return func(execErr error) { s.settleApprovalClaim(id, execErr) }, nil
	}
	return nil, tooloutcome.New(tooloutcome.NeedApproval, "user_confirmation_required", "not_executed",
		"This screenshot would be shown to other people in the conversation and needs the Mac owner's confirmation. Ask with request_approval and set target to exactly the label on the next line. Repeat the call only after they approve.\n"+approvalLabelText(label),
		"request_approval").Err()
}

// conversationHasOtherPeople reports whether a person besides the account owner
// takes part. A conversation holds the owner and Bots only (members lists Bots),
// so no conversation has another person today; the gate exists for the day one does.
func (st *Store) conversationHasOtherPeople(Conversation) bool { return false }

// shareCapture keeps the capture's picture as a published attachment of this
// run's reply and tells the model exactly what was attached.
func (s *Server) shareCapture(r Run, result string) (string, error) {
	fields, uri, ok := captureResultFields(result)
	if !ok {
		return "", errors.New("the capture returned no picture to share")
	}
	mime, encoded, _ := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("the capture picture could not be read")
	}
	c, err := s.store.GetConversation(r.ConversationID)
	if err != nil {
		return "", err
	}
	ext := ".png"
	if mime == "image/jpeg" {
		ext = ".jpg"
	}
	name := "screenshot-" + time.Now().UTC().Format("20060102-150405") + ext
	a, err := s.store.addAttachmentOpts(context.Background(), c.ID, name, mime, bytes.NewReader(pixels), attachmentOpts{Origin: attachmentOriginPublished, BotID: r.BotID})
	if err != nil {
		return "", err
	}
	created := a
	if a, err = s.store.stageRunAttachment(r, c, a, ""); err != nil {
		_ = s.store.deleteUnboundAttachment(created.ID)
		return "", err
	}
	shared, _ := json.Marshal(map[string]any{"attachment_id": a.ID, "name": a.Name, "url": s.attachmentLink(a.ID), "attached_to_reply": s.store.runAttachmentNames(r.ID)})
	fields["shared"] = shared
	out, err := json.Marshal(fields)
	return string(out), err
}

// desktopMacs lists the paired personal computers that declared a desktop
// capability, whether or not they are online now.
func (s *Server) desktopMacs() []ComputerRow {
	if s.store == nil {
		return nil
	}
	rows, err := s.store.listComputerRows()
	if err != nil {
		return nil
	}
	var out []ComputerRow
	for _, row := range rows {
		if row.Kind != computer.RowKindPersonal {
			continue
		}
		for _, c := range row.Capabilities {
			if c == "screen.capture" || strings.HasPrefix(c, "desktop.") {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// personalDesktopTools gives an account with no Bot computer (no VM) the
// desktop tool for its paired Macs. With a Bot computer the tool comes from microVMTools.
func (s *Server) personalDesktopTools(r Run) []Tool {
	if s.computer() != nil || len(s.desktopMacs()) == 0 {
		return nil
	}
	return []Tool{{
		Name:        "computer_desktop",
		Description: "Control the screen of a person's own paired Mac: capture (share attaches the picture to your reply), click, double_click, triple_click, move, drag, scroll, type, key. Coordinates are pixels of the latest capture. Each input declares its effect. Input with an effect other than none, or with none declared, is reviewed, and only a few need the person's confirmation. A system permission or privacy prompt on the Mac is the person's to answer: tell them what it asks and leave it. Pass computer_id when more than one Mac is paired. Files and commands: computer_action.",
		Parameters:  desktopToolSchema(),
		Timeout:     computerToolTimeout,
		Identity: func(raw json.RawMessage) tooloutcome.Identity {
			action, args, err := s.parseDesktopTool(raw)
			if err != nil {
				var in struct {
					Action string `json:"action"`
				}
				_ = json.Unmarshal(raw, &in)
				action, args = in.Action, raw
				if action == "" {
					action = "computer_desktop"
				}
			}
			return computerRecoveryIdentity(r.BotID, s.desktopToolComputer("computer_desktop", raw), action, args)
		},
		ResolveIdentity: func(ctx context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
			action, args, err := s.parseDesktopTool(raw)
			if err != nil {
				return tooloutcome.Identity{}, tooloutcome.InvalidArguments(err.Error())
			}
			return s.resolveComputerRecovery(ctx, r, s.desktopToolComputer("computer_desktop", raw), action, args)
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			action, args, err := s.parseDesktopTool(raw)
			if err != nil {
				return "", err
			}
			detail, err := captureDetailOf(raw)
			if err != nil {
				return "", err
			}
			var sh struct {
				Share bool `json:"share"`
			}
			_ = json.Unmarshal(raw, &sh)
			return s.personalDesktop(ctx, r, s.desktopToolComputer("computer_desktop", raw), action, args, detail, sh.Share)
		},
	}}
}

// desktopToolSchema is the argument schema of computer_desktop, the same for every kind of computer.
func desktopToolSchema() map[string]any {
	return objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"desktop.start", "desktop.stop", "desktop.capture", "desktop.snapshot", "desktop.trace", "desktop.click", "desktop.double_click", "desktop.triple_click", "desktop.type", "desktop.key", "desktop.scroll", "desktop.move", "desktop.drag", "start", "stop", "capture", "trace", "click", "double_click", "triple_click", "type", "key", "scroll", "move", "drag"}, "description": "desktop.start/stop/capture/click/double_click/triple_click/type/key/scroll (desktop.snapshot = capture). A double or triple click is one action: opening a folder, a word or a line selection is never two separate click calls."}, "x": map[string]any{"type": "integer", "minimum": 0, "description": "Screen pixel X from the latest capture"}, "y": map[string]any{"type": "integer", "minimum": 0, "description": "Screen pixel Y from the latest capture"}, "screenshot_width": map[string]any{"type": "integer", "minimum": 1, "maximum": 1280, "description": "Width of the screenshot used for x/y; provide together with screenshot_height"}, "screenshot_height": map[string]any{"type": "integer", "minimum": 1, "maximum": 800, "description": "Height of the screenshot used for x/y; provide together with screenshot_width"}, "direction": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}, "description": "Visible page scroll direction"}, "amount": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Mouse wheel steps, default 3"}, "text": map[string]any{"type": "string"}, "key": map[string]any{"type": "string", "description": "X11 key name, for example Return or Escape"}, "effect": effectParam, "computer_id": desktopComputerIDParam, "button": desktopButtonParam, "to_x": desktopToParam("X"), "to_y": desktopToParam("Y"), "display": desktopDisplayParam, "modifiers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "detail": captureDetailParam, "share": map[string]any{"type": "boolean", "description": "capture on a person's own computer: also attach this picture to your reply, where everyone in the conversation sees it. Without it only you see the picture. Use it only when they asked to receive it."}, "last": traceActionParams["last"], "capture_seqs": traceActionParams["capture_seqs"], "columns": traceActionParams["columns"]}, []string{"action"})
}
