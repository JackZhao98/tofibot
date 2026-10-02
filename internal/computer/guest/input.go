package guest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const inputLease = 15 * time.Second

var errInputTextNotAccepted = errors.New("text_not_accepted")

// Input events use native 1280x800 desktop pixels, never scaled poster pixels.
type DesktopInputEvent struct {
	Type   string `json:"type"`
	X      *int   `json:"x,omitempty"`
	Y      *int   `json:"y,omitempty"`
	Button int    `json:"button,omitempty"`
	DeltaX int    `json:"delta_x,omitempty"`
	DeltaY int    `json:"delta_y,omitempty"`
	Key    string `json:"key,omitempty"`
	Text   string `json:"text,omitempty"`
}

type inputSession struct {
	id          string
	ownerBot    string
	desktop     *desktop
	expires     time.Time
	timer       *time.Timer
	seq         uint64
	keys        map[string]bool
	buttons     map[int]bool
	clipboard   *inputClipboard
	copyWatch   *inputClipboard
	copyPending bool
}

func (s *Service) inputGate(botID string) *sync.Mutex {
	// X11 input targets the one shared workspace display. Keep this gate
	// global even when requests carry different logical BotIDs.
	return &s.sharedInputMu
}

func (s *Service) currentInputLocked(botID string) *inputSession {
	if s.activeInput != nil {
		return s.activeInput
	}
	return s.inputs[botID]
}

func isInputObserver(action string) bool {
	return action == "terminal.list" || action == "terminal.read" || action == "desktop.capture" || action == "browser.snapshot" || action == "files.list" || action == "files.read" || action == "files.export_chunk"
}

func isHumanControlRequest(req ActionRequest) bool {
	return req.Source == ActionSourceHuman && strings.HasPrefix(req.RunID, "human-control:") &&
		(req.Action == "desktop.hold" || req.Action == "desktop.release" || req.Action == "desktop.input" || req.Action == "desktop.clipboard.read" || req.Action == "desktop.clipboard.write")
}

// Called under the per-Bot action gate. The guest lease is the final authority
// if the browser, app or manager disappears; it never starts a desktop.
func (s *Service) controlInput(ctx context.Context, req ActionRequest) (any, error) {
	s.mu.Lock()
	current := s.currentInputLocked(req.BotID)
	s.mu.Unlock()
	if current != nil && !current.expires.After(time.Now()) {
		s.finishInput(current)
		current = nil
	}
	if req.Action == "desktop.release" {
		if current != nil && current.id == req.RunID {
			s.finishInput(current)
		}
		return map[string]any{"released": true}, nil
	}
	if req.Action == "desktop.hold" {
		var args struct {
			TTL     int  `json:"ttl_sec"`
			Acquire bool `json:"acquire"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if current != nil && current.id != req.RunID {
			return nil, errors.New("computer_busy: another control session owns this desktop")
		}
		if current == nil {
			if !args.Acquire {
				return nil, errors.New("control_expired: take control again")
			}
			if err := inputDependencies(); err != nil {
				return nil, err
			}
			s.mu.Lock()
			d := s.desktopLocked(req.BotID)
			if s.closed || d == nil || d.stopping || !d.readyClosed || d.startErr != nil {
				s.mu.Unlock()
				return nil, errors.New("desktop_stopped: control requires a running desktop")
			}
			if d.inFlight != 0 || s.hasAnyActiveHoldLocked(time.Now()) {
				s.mu.Unlock()
				return nil, errors.New("computer_busy: desktop is in use")
			}
			current = &inputSession{id: req.RunID, ownerBot: req.BotID, desktop: d, keys: map[string]bool{}, buttons: map[int]bool{}}
			if s.inputs == nil {
				s.inputs = make(map[string]*inputSession)
			}
			s.inputs[req.BotID] = current
			s.activeInput = current
			s.mu.Unlock()
		}
		current.expires = time.Now().Add(inputLease)
		if current.timer != nil {
			current.timer.Stop()
		}
		current.timer = time.AfterFunc(inputLease, func() { s.expireInput(current) })
		s.mu.Lock()
		if s.holds[req.BotID] == nil {
			s.holds[req.BotID] = map[string]time.Time{}
		}
		s.holds[req.BotID][req.RunID] = current.expires
		s.mu.Unlock()
		return map[string]any{"lease_ms": inputLease.Milliseconds()}, nil
	}
	if current == nil || current.id != req.RunID {
		return nil, errors.New("control_expired: take control again")
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result any
	var err error
	switch req.Action {
	case "desktop.input":
		var args struct {
			Seq    uint64              `json:"seq"`
			Events []DesktopInputEvent `json:"events"`
		}
		if err = decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if args.Seq == 0 {
			return nil, errors.New("input_sequence: seq must start at 1")
		}
		if args.Seq <= current.seq {
			return map[string]any{"seq": current.seq}, nil
		}
		if args.Seq != current.seq+1 {
			return nil, errors.New("input_sequence: input batch is out of order")
		}
		if err = validateInputEvents(args.Events); err != nil {
			return nil, err
		}
		err = executeInputEvents(callCtx, current, args.Events)
		warning := ""
		if errors.Is(err, errInputTextNotAccepted) {
			// No text receiver is a focus issue, not a broken control session.
			// The batch is acknowledged once and its remaining events are dropped.
			warning, err = "text_not_accepted", nil
		}
		if err == nil {
			current.seq = args.Seq
		}
		result = map[string]any{"seq": current.seq, "warning": warning}
	case "desktop.clipboard.read":
		var changed bool
		changed, err = awaitInputCopy(callCtx, current)
		if err != nil {
			break
		}
		if !changed {
			return map[string]any{"text": "", "changed": false}, nil
		}
		var text string
		text, err = readInputClipboard(callCtx, current)
		result = map[string]any{"text": text, "changed": true}
	case "desktop.clipboard.write":
		var args struct {
			Text string `json:"text"`
		}
		if err = decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if err = validateInputText(args.Text, true); err != nil {
			return nil, err
		}
		err = writeInputClipboard(callCtx, current, args.Text)
		result = map[string]any{"written": err == nil}
	default:
		return nil, errors.New("invalid control action")
	}
	if err != nil {
		// An uncertain partial input is never replayed. Release all tracked
		// downs before admitting another owner; caller must reacquire.
		s.finishInput(current)
		return nil, fmt.Errorf("control_expired: input failed: %w", err)
	}
	s.mu.Lock()
	current.desktop.lastActivity = time.Now()
	s.mu.Unlock()
	return result, nil
}

func (s *Service) expireInput(session *inputSession) {
	gate := s.inputGate(session.desktop.botID)
	gate.Lock()
	defer gate.Unlock()
	s.mu.Lock()
	current := s.activeInput
	if current == nil {
		current = s.inputs[session.desktop.botID]
	}
	s.mu.Unlock()
	if current == session && !session.expires.After(time.Now()) {
		s.finishInput(session)
	}
}

func (s *Service) finishInput(session *inputSession) {
	if session.timer != nil {
		session.timer.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resetErr := resetInputState(ctx, session)
	closeInputClipboard(session)
	s.mu.Lock()
	for botID, candidate := range s.inputs {
		if candidate == session {
			delete(s.inputs, botID)
		}
	}
	if s.activeInput == session {
		s.activeInput = nil
	}
	ownerBot := session.ownerBot
	if ownerBot == "" {
		ownerBot = session.desktop.botID
	}
	if holds := s.holds[ownerBot]; holds != nil {
		delete(holds, session.id)
	}
	// An explicit control session is user activity, unlike passive viewing.
	session.desktop.lastActivity = time.Now()
	if resetErr != nil && (len(session.keys) > 0 || len(session.buttons) > 0) {
		// If X11 cannot confirm key/button release, retire this display rather
		// than hand a possibly stuck modifier or drag to the next owner.
		session.desktop.stopping = true
		go func() { _ = s.requestDesktopStop(context.Background(), session.desktop) }()
	}
	s.mu.Unlock()
}

func (s *Service) releaseInputForDesktop(d *desktop) {
	s.mu.Lock()
	current := s.activeInput
	if current == nil {
		current = s.inputs[d.botID]
	}
	s.mu.Unlock()
	if current == nil || current.desktop != d {
		return
	}
	gate := s.inputGate(d.botID)
	gate.Lock()
	defer gate.Unlock()
	s.mu.Lock()
	current = s.activeInput
	if current == nil {
		current = s.inputs[d.botID]
	}
	s.mu.Unlock()
	if current != nil && current.desktop == d {
		s.finishInput(current)
	}
}

var inputKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

func validateInputText(text string, emptyOK bool) error {
	if (!emptyOK && text == "") || len(text) > MaxDesktopInput || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("invalid input text: UTF-8 text must be at most 64 KiB without NUL")
	}
	return nil
}

func validateInputEvents(events []DesktopInputEvent) error {
	if len(events) == 0 || len(events) > 64 {
		return errors.New("invalid input batch: requires 1..64 events")
	}
	for _, event := range events {
		if (event.X == nil) != (event.Y == nil) {
			return errors.New("invalid input coordinates")
		}
		if event.X != nil && (*event.X < 0 || *event.X >= 1280 || *event.Y < 0 || *event.Y >= 800) {
			return errors.New("invalid input coordinates")
		}
		switch event.Type {
		case "move":
			if event.X == nil {
				return errors.New("input move coordinates are required")
			}
		case "down", "up":
			if event.Button < 1 || event.Button > 3 {
				return errors.New("invalid input button")
			}
		case "wheel":
			if event.DeltaX < -20 || event.DeltaX > 20 || event.DeltaY < -20 || event.DeltaY > 20 {
				return errors.New("invalid input wheel amount")
			}
		case "keydown", "keyup":
			if !inputKeyPattern.MatchString(event.Key) {
				return errors.New("invalid input key name")
			}
		case "text":
			if err := validateInputText(event.Text, false); err != nil {
				return err
			}
		case "reset":
		default:
			return errors.New("invalid input event type")
		}
	}
	return nil
}
