package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/computer"
)

const computerControlLease = 15 * time.Second
const humanDesktopIdle = 15 * time.Second

type computerControl struct {
	mu           sync.Mutex
	id           string
	botID        string
	expires      time.Time
	timer        *time.Timer
	terminal     bool
	seq          uint64
	released     bool
	lastActivity time.Time
	keys         map[string]bool
	buttons      map[int]bool
}

type computerControlArgs struct {
	ControlID     string          `json:"control_id"`
	Seq           uint64          `json:"seq"`
	Events        json.RawMessage `json:"events"`
	Text          string          `json:"text"`
	ExpectedOwner string          `json:"expected_owner,omitempty"`
	Interacting   bool            `json:"interacting,omitempty"`
}

func (s *Server) controlGuest(ctx context.Context, c *computerControl, action string, args any) (json.RawMessage, error) {
	s.prepareGuestTimezoneForAction(ctx, action)
	callCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	result, err := s.microVM.Action(callCtx, computer.Action{BotID: c.botID, RunID: "human-control:" + c.id, Source: "human", Name: action, Args: raw})
	return result.Result, err
}

func (s *Server) handleComputerControl(w http.ResponseWriter, r *http.Request, botID, action string, raw json.RawMessage) {
	// Existing API middleware checks Origin; also reject explicit cross-site
	// fetch metadata, including requests lacking an Origin header.
	if !s.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeErr(w, 403, "csrf", "origin rejected")
		return
	}
	var args computerControlArgs
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		writeErr(w, 400, "invalid_request", "invalid control arguments")
		return
	}
	switch action {
	case "desktop.control.acquire", "desktop.control.renew", "desktop.control.release", "desktop.control.input", "desktop.control.clipboard.read", "desktop.control.clipboard.write":
	default:
		writeErr(w, 400, "invalid_request", "unsupported control action")
		return
	}
	if action == "desktop.control.acquire" {
		if args.ControlID != "" {
			writeErr(w, 400, "invalid_request", "acquire does not accept control_id")
			return
		}
		lease := s.computerLease(botID)
		locked := lease.TryLock()
		if !locked && args.ExpectedOwner != "" {
			waitCtx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			locked = lockComputerLease(waitCtx, lease) == nil
			cancel()
		}
		if !locked {
			s.computerOwnerMu.Lock()
			oauth := strings.HasPrefix(s.computerOwners["oauth:"+botID], oauthOwnerPrefix)
			s.computerOwnerMu.Unlock()
			if oauth {
				waitCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				locked = lockComputerLease(waitCtx, lease) == nil
				cancel()
			}
		}
		if !locked {
			log.Printf("[desktop-control] acquire denied bot=%s reason=computer-lease-busy", botID)
			writeErr(w, 409, "computer_busy", "电脑正在执行其他操作")
			return
		}
		defer lease.Unlock()
		c := &computerControl{id: uuid.NewString(), botID: botID, lastActivity: time.Now(), keys: map[string]bool{}, buttons: map[int]bool{}}
		if args.ExpectedOwner != "" {
			if err := s.transferDesktopToHuman(r.Context(), c, args.ExpectedOwner); err != nil {
				writeErr(w, 409, "computer_busy", err.Error())
				return
			}
		}
		if !s.claimComputerOwner(botID, "human-control:"+c.id) {
			log.Printf("[desktop-control] acquire denied bot=%s reason=owner-conflict", botID)
			writeErr(w, 409, "computer_busy", "电脑已被占用")
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := s.oauthInputHold(r.Context(), botID, true); err != nil {
			log.Printf("[desktop-control] acquire failed bot=%s reason=input-hold error=%v", botID, err)
			_ = s.oauthInputHold(context.Background(), botID, false)
			s.releaseComputerOwner(botID, "human-control:"+c.id)
			writeControlError(w, err)
			return
		}
		_, err := s.controlGuest(r.Context(), c, "desktop.hold", map[string]any{"ttl_sec": 15, "acquire": true})
		if err != nil {
			log.Printf("[desktop-control] acquire failed bot=%s reason=guest-hold error=%v", botID, err)
			// The guest may have accepted a request whose response was lost.
			_, _ = s.controlGuest(context.Background(), c, "desktop.release", struct{}{})
			_ = s.oauthInputHold(context.Background(), botID, false)
			s.releaseComputerOwner(botID, "human-control:"+c.id)
			writeControlError(w, err)
			return
		}
		s.computerOwnerMu.Lock()
		if s.computerControls == nil {
			s.computerControls = map[string]*computerControl{}
		}
		s.computerControls[botID] = c
		s.computerOwnerMu.Unlock()
		s.renewControlTimer(c)
		log.Printf("[desktop-control] acquired bot=%s control=%s", botID, c.id)
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"control_id": c.id, "lease_ms": 15000, "renew_after_ms": 5000}})
		return
	}
	if _, err := uuid.Parse(args.ControlID); err != nil {
		writeErr(w, 400, "invalid_request", "control_id is required")
		return
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls[botID]
	s.computerOwnerMu.Unlock()
	if c == nil || c.id != args.ControlID || c.terminal {
		if action == "desktop.control.release" {
			writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]bool{"released": true}})
		} else {
			writeErr(w, 409, "control_expired", "控制已释放，请重新接管")
		}
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if action == "desktop.control.release" {
		s.releaseControl(c)
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]bool{"released": true}})
		return
	}
	if c.released || !c.expires.After(time.Now()) {
		s.releaseControl(c)
		writeErr(w, 409, "control_expired", "控制已过期，请重新接管")
		return
	}
	lease := s.computerLease(botID)
	if err := lockComputerLease(r.Context(), lease); err != nil {
		writeControlError(w, err)
		return
	}
	defer lease.Unlock()
	var result json.RawMessage
	var err error
	switch action {
	case "desktop.control.renew":
		// Only the authenticated human renewal may yield this lease. A Bot
		// request never revokes it, and this mutex serializes pending input.
		s.computerOwnerMu.Lock()
		waiting := len(s.desktopWaiters) > 0
		s.computerOwnerMu.Unlock()
		if waiting && !args.Interacting && len(c.keys) == 0 && len(c.buttons) == 0 && time.Since(c.lastActivity) >= humanDesktopIdle {
			s.releaseControl(c)
			writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"released": true, "reason": "bot_waiting"}})
			return
		}
		result, err = s.controlGuest(r.Context(), c, "desktop.hold", map[string]any{"ttl_sec": 15})
		if err == nil {
			s.renewControlTimer(c)
		}
	case "desktop.control.input":
		result, err = s.controlGuest(r.Context(), c, "desktop.input", map[string]any{"seq": args.Seq, "events": args.Events})
		if err == nil && args.Seq > c.seq {
			s.recordHumanDesktopInput(c, args)
			c.seq = args.Seq
		}
	case "desktop.control.clipboard.read":
		result, err = s.controlGuest(r.Context(), c, "desktop.clipboard.read", struct{}{})
	case "desktop.control.clipboard.write":
		result, err = s.controlGuest(r.Context(), c, "desktop.clipboard.write", map[string]any{"text": args.Text})
	}
	if err != nil {
		s.releaseControl(c)
		log.Printf("[desktop-control] action failed bot=%s control=%s action=%s error=%v", botID, c.id, action, err)
		writeControlError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) renewControlTimer(c *computerControl) {
	c.expires = time.Now().Add(computerControlLease)
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(computerControlLease, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.expires.After(time.Now()) {
			s.releaseControl(c)
		}
	})
}

// Caller holds c.mu. Keep the existing owner claimed until guest key/button
// cleanup has finished or the bounded transport timeout has elapsed.
func (s *Server) releaseControl(c *computerControl) {
	if c.released {
		return
	}
	c.released = true
	if c.timer != nil {
		c.timer.Stop()
	}
	if !c.terminal {
		_, _ = s.controlGuest(context.Background(), c, "desktop.release", struct{}{})
		_ = s.oauthInputHold(context.Background(), c.botID, false)
	}
	s.computerOwnerMu.Lock()
	key := c.botID
	if c.terminal {
		key = "terminal:" + c.botID
	}
	if s.computerControls[key] == c {
		delete(s.computerControls, key)
	}
	if c.terminal {
		if s.terminalOwners[c.botID] == "human-control:"+c.id {
			delete(s.terminalOwners, c.botID)
		}
	} else if s.computerOwners[c.botID] == "human-control:"+c.id {
		delete(s.computerOwners, c.botID)
		s.desktopChangedLocked()
	}
	s.computerOwnerMu.Unlock()
	log.Printf("[desktop-control] released bot=%s control=%s", c.botID, c.id)
}

func (s *Server) closeComputerControls() {
	s.computerOwnerMu.Lock()
	all := make([]*computerControl, 0, len(s.computerControls))
	for _, c := range s.computerControls {
		all = append(all, c)
	}
	s.computerOwnerMu.Unlock()
	for _, c := range all {
		c.mu.Lock()
		s.releaseControl(c)
		c.mu.Unlock()
	}
}

func writeControlError(w http.ResponseWriter, err error) {
	message := err.Error()
	for _, code := range []string{"computer_busy", "control_expired", "input_sequence", "desktop_stopped"} {
		if strings.Contains(message, code) {
			writeErr(w, 409, code, message)
			return
		}
	}
	if errors.Is(err, context.Canceled) {
		writeErr(w, 409, "control_expired", "控制请求已取消")
		return
	}
	writeErr(w, 502, "control_unavailable", "桌面控制暂不可用，请重新接管")
}

// Caller owns the shared action mutex: finish the previous tool before freeing
// its guest hold, then swap ownership atomically. The durable run is untouched.
func (s *Server) transferDesktopToHuman(ctx context.Context, c *computerControl, expected string) error {
	s.computerOwnerMu.Lock()
	var bot string
	for id, owner := range s.computerOwners {
		if owner == expected && !strings.HasPrefix(owner, "human") && !strings.HasPrefix(id, "oauth:") {
			bot = id
		}
	}
	s.computerOwnerMu.Unlock()
	if bot == "" {
		return errors.New("控制者已变化，请稍后重试")
	}
	if _, err := s.microVM.Action(ctx, computer.Action{BotID: bot, RunID: expected, Source: "model", Name: "desktop.release", Args: json.RawMessage(`{}`)}); err != nil {
		return err
	}
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	if s.computerOwners[bot] != expected {
		return errors.New("控制者已变化，请稍后重试")
	}
	delete(s.computerOwners, bot)
	delete(s.desktopObserved, expected)
	s.desktopPointer = nil
	s.computerOwners[c.botID] = "human-control:" + c.id
	s.desktopChangedLocked()
	return nil
}

func (s *Server) recordHumanDesktopInput(c *computerControl, args computerControlArgs) {
	var events []struct {
		Type   string `json:"type"`
		Key    string `json:"key"`
		Button int    `json:"button"`
	}
	if json.Unmarshal(args.Events, &events) != nil {
		return
	}
	if c.keys == nil {
		c.keys = map[string]bool{}
	}
	if c.buttons == nil {
		c.buttons = map[int]bool{}
	}
	for _, e := range events {
		active := true
		switch e.Type {
		case "keydown":
			c.keys[e.Key] = true
		case "keyup":
			delete(c.keys, e.Key)
		case "down":
			c.buttons[e.Button] = true
		case "up":
			delete(c.buttons, e.Button)
		case "text", "reset":
			c.keys = map[string]bool{}
			c.buttons = map[int]bool{}
		case "move":
			active = len(c.buttons) > 0
		case "wheel":
		default:
			active = false
		}
		if active {
			c.lastActivity = time.Now()
		}
	}
}
