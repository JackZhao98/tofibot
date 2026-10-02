package guest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	defaultDesktopHold = 90 * time.Second
	maxDesktopHold     = 2 * time.Minute
	desktopStopTimeout = (MaxTimeout + 15) * time.Second
)

func normalizedSource(source string) string {
	if source == "" {
		return ActionSourceHuman
	}
	return source
}

func desktopNeedsSession(action string) bool {
	switch action {
	case "desktop.capture", "desktop.click", "desktop.type", "desktop.key", "desktop.scroll",
		"browser.navigate", "browser.snapshot", "browser.action", "browser.type_private":
		return true
	default:
		return false
	}
}

func viewerDesktopAction(action string) bool {
	return action == "desktop.capture" || action == "browser.snapshot"
}

// action wraps the existing dispatch with lifecycle ownership. The wrapper is
// deliberately outside the action switch so browser-specific implementations
// do not need to know about idle cleanup or operation references.
func (s *Service) action(ctx context.Context, req ActionRequest) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, MaxTimeout*time.Second)
	defer cancel()
	req.Source = normalizedSource(req.Source)
	if _, err := s.ensureWorkspaceAlias(req.BotID, req.BotName); err != nil {
		return nil, fmt.Errorf("workspace alias: %w", err)
	}
	if needsSharedInputGate(req.Action) {
		gate := s.inputGate(req.BotID)
		if req.Action == "terminal.write" {
			if !gate.TryLock() {
				return nil, errors.New("computer_busy: desktop input is in use")
			}
		} else if err := lockInputGate(ctx, gate); err != nil {
			return nil, fmt.Errorf("computer_busy: waiting for shared desktop: %w", err)
		}
		defer gate.Unlock()
		if isHumanControlRequest(req) {
			return s.controlInput(ctx, req)
		}
		s.mu.Lock()
		controlled := s.activeInput != nil || s.inputs[req.BotID] != nil
		s.mu.Unlock()
		if controlled {
			return nil, errors.New("computer_busy: the user is controlling this desktop")
		}
	}
	switch req.Action {
	case "desktop.hold":
		return s.holdDesktop(req.BotID, req.RunID, req.Args)
	case "desktop.release":
		return s.releaseDesktop(req.BotID, req.RunID)
	}
	if req.Action == "shell.exec" {
		release, err := s.beginOptionalDesktopOperation(ctx, req.BotID)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if req.Action == "desktop.click" {
		if err := validateDesktopClickArgs(req.Args); err != nil {
			return nil, err
		}
	}

	if req.Action == "desktop.stop" || req.Action == "desktop.start" || !desktopNeedsSession(req.Action) {
		return s.dispatchAction(ctx, req)
	}
	if req.Source == ActionSourceViewer && viewerDesktopAction(req.Action) {
		// A polling viewer must never consume a slot by itself. If the model or
		// user has not explicitly started the Bot desktop, preserve the normal
		// not-running error for the UI to surface.
	} else if req.Source != ActionSourceViewer {
		if _, err := s.ensureDesktop(ctx, req.BotID); err != nil {
			return nil, err
		}
	}
	release, err := s.beginDesktopOperation(ctx, req.BotID, req.Source != ActionSourceViewer)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.dispatchAction(ctx, req)
}

func needsSharedInputGate(action string) bool {
	switch action {
	case "desktop.start", "desktop.stop", "desktop.hold", "desktop.release",
		"desktop.click", "desktop.type", "desktop.key", "desktop.scroll",
		"desktop.input", "desktop.clipboard.read", "desktop.clipboard.write",
		"browser.navigate", "browser.action", "browser.type_private":
		return true
	default:
		return false
	}
}

func lockInputGate(ctx context.Context, gate *sync.Mutex) error {
	if gate.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if gate.TryLock() {
				return nil
			}
		}
	}
}

func validateDesktopClickArgs(raw []byte) error {
	var args struct {
		X                int  `json:"x"`
		Y                int  `json:"y"`
		Button           int  `json:"button"`
		ScreenshotWidth  *int `json:"screenshot_width"`
		ScreenshotHeight *int `json:"screenshot_height"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return err
	}
	if (args.ScreenshotWidth == nil) != (args.ScreenshotHeight == nil) {
		return errors.New("screenshot_width and screenshot_height must be provided together")
	}
	if args.ScreenshotWidth != nil && (*args.ScreenshotWidth < 1 || *args.ScreenshotWidth > 1280 || *args.ScreenshotHeight < 1 || *args.ScreenshotHeight > 800) {
		return errors.New("screenshot dimensions must be within 1..1280 by 1..800")
	}
	return nil
}

func (s *Service) ensureDesktop(ctx context.Context, botID string) (map[string]any, error) {
	return s.startDesktop(ctx, botID)
}

func (s *Service) stopDesktop(ctx context.Context, botID string) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	d := s.desktopLocked(botID)
	var cancel context.CancelFunc
	if d != nil {
		d.stopping = true
		cancel = d.cancel
	}
	s.mu.Unlock()
	if d == nil {
		return map[string]any{"bot_id": botID, "already_stopped": true}, nil
	}
	if cancel != nil {
		cancel()
	}
	err := s.requestDesktopStop(ctx, d)
	return map[string]any{"bot_id": botID, "stopped": true}, err
}

func (s *Service) beginDesktopOperation(ctx context.Context, botID string, refresh bool) (func(), error) {
	if !botIDPattern.MatchString(botID) {
		return nil, errors.New("bot_id must be a canonical UUID")
	}
	s.mu.Lock()
	d := s.desktopLocked(botID)
	if d == nil {
		s.mu.Unlock()
		return nil, errors.New("desktop is not running; call desktop.start first")
	}
	if d.stopping {
		s.mu.Unlock()
		return nil, errors.New("desktop is stopping; retry shortly")
	}
	d.inFlight++
	if refresh {
		d.lastActivity = time.Now()
	}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if d.inFlight > 0 {
			d.inFlight--
		}
		if refresh {
			d.lastActivity = time.Now()
		}
		s.mu.Unlock()
	}, nil
}

func (s *Service) beginOptionalDesktopOperation(ctx context.Context, botID string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		d := s.desktopLocked(botID)
		if d == nil {
			s.mu.Unlock()
			// A normal shell action must remain usable when this Bot has no
			// desktop. It never starts one as a side effect.
			return func() {}, nil
		}
		if !d.stopping {
			d.inFlight++
			d.lastActivity = time.Now()
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				if d.inFlight > 0 {
					d.inFlight--
				}
				d.lastActivity = time.Now()
				s.mu.Unlock()
			}, nil
		}
		s.mu.Unlock()
		d.stopRequestMu.Lock()
		done := d.stopRequestDone
		d.stopRequestMu.Unlock()

		// Reaper marks stopping before its worker is necessarily scheduled. Start
		// or join that one worker, then retry acquisition against the post-stop
		// map. This closes the gap where Shell could otherwise run beside teardown.
		if done == nil {
			if err := s.requestDesktopStop(ctx, d); err != nil {
				return nil, err
			}
			continue
		}
		select {
		case <-done:
			d.stopRequestMu.Lock()
			err := d.stopRequestErr
			d.stopRequestMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("desktop teardown failed: %w", err)
			}
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *Service) holdDesktop(botID, runID string, raw []byte) (map[string]any, error) {
	if !botIDPattern.MatchString(botID) {
		return nil, errors.New("bot_id must be a canonical UUID")
	}
	if runID == "" {
		return nil, errors.New("run_id is required")
	}
	ttl := defaultDesktopHold
	if len(raw) != 0 && string(raw) != "null" {
		var args struct {
			TTLSeconds int `json:"ttl_sec"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.TTLSeconds > 0 {
			ttl = time.Duration(args.TTLSeconds) * time.Second
		}
	}
	if ttl > maxDesktopHold {
		ttl = maxDesktopHold
	}
	expires := time.Now().Add(ttl)
	s.mu.Lock()
	if s.holds[botID] == nil {
		s.holds[botID] = make(map[string]time.Time)
	}
	s.holds[botID][runID] = expires
	s.mu.Unlock()
	return map[string]any{"bot_id": botID, "run_id": runID, "expires_at": expires.UTC().Format(time.RFC3339Nano)}, nil
}

func (s *Service) releaseDesktop(botID, runID string) (map[string]any, error) {
	if !botIDPattern.MatchString(botID) {
		return nil, errors.New("bot_id must be a canonical UUID")
	}
	s.mu.Lock()
	if runs := s.holds[botID]; runs != nil {
		delete(runs, runID)
		if len(runs) == 0 {
			delete(s.holds, botID)
		}
	}
	if d := s.desktopLocked(botID); d != nil && !d.stopping {
		d.lastActivity = time.Now()
	}
	s.mu.Unlock()
	return map[string]any{"bot_id": botID, "run_id": runID, "released": true}, nil
}

func (s *Service) hasActiveHoldLocked(botID string, now time.Time) bool {
	runs := s.holds[botID]
	active := false
	for runID, expires := range runs {
		if !expires.After(now) {
			delete(runs, runID)
			continue
		}
		active = true
	}
	if len(runs) == 0 {
		delete(s.holds, botID)
	}
	return active
}

func (s *Service) hasAnyActiveHoldLocked(now time.Time) bool {
	active := false
	for botID := range s.holds {
		if s.hasActiveHoldLocked(botID, now) {
			active = true
		}
	}
	return active
}

func (s *Service) startDesktopReaper() {
	if s.idleTimeout <= 0 {
		close(s.reaperDone)
		return
	}
	interval := s.idleTimeout / 4
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	go func() {
		defer close(s.reaperDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.reapIdleDesktops()
			case <-s.reaperStop:
				return
			}
		}
	}()
}

func (s *Service) reapIdleDesktops() {
	now := time.Now()
	var candidates []*desktop
	s.mu.Lock()
	seen := make(map[*desktop]bool)
	for _, d := range s.desktops {
		if seen[d] {
			continue
		}
		seen[d] = true
		if d == nil || d.stopping || !d.readyClosed || d.inFlight != 0 || s.hasAnyActiveHoldLocked(now) {
			continue
		}
		if d.lastActivity.IsZero() || now.Sub(d.lastActivity) < s.idleTimeout {
			continue
		}
		d.stopping = true
		candidates = append(candidates, d)
	}
	s.mu.Unlock()
	for _, d := range candidates {
		go func(d *desktop) {
			_ = s.requestDesktopStop(context.Background(), d)
		}(d)
	}
}

func (s *Service) stopDesktopInstance(ctx context.Context, d *desktop) error {
	if d == nil {
		return nil
	}
	d.stopOnce.Do(func() {
		if d.stopDone == nil {
			d.stopDone = make(chan struct{})
		}
		s.releaseInputForDesktop(d)
		d.stopErr = stopDesktopProcesses(ctx, d)
		close(d.stopDone)
	})
	if d.stopDone != nil {
		select {
		case <-d.stopDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.removeDesktopAliasesLocked(d)
	err := d.stopErr
	s.mu.Unlock()
	return err
}

// requestDesktopStop gives each Bot one background teardown worker. A caller
// disconnect can return immediately while that worker waits for a bounded
// shell/browser action and then performs the single cmd.Wait cleanup.
func (s *Service) requestDesktopStop(ctx context.Context, d *desktop) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.stopRequestMu.Lock()
	if !d.stopRequestRunning {
		d.stopRequestRunning = true
		d.stopRequestDone = make(chan struct{})
		done := d.stopRequestDone
		go func() {
			cleanup, cancel := context.WithTimeout(context.Background(), desktopStopTimeout)
			defer cancel()
			err := waitDesktopReady(cleanup, d)
			if err == nil {
				// Action contexts are independently bounded by MaxTimeout. Do not
				// abandon this wait at the shorter process cleanup deadline or leave
				// a stopping desktop that can never be retried.
				err = s.waitForDesktopOperations(context.Background(), d)
			}
			if err == nil {
				err = s.stopDesktopInstance(cleanup, d)
			}
			d.stopRequestMu.Lock()
			d.stopRequestErr = err
			d.stopRequestRunning = false
			close(done)
			d.stopRequestMu.Unlock()
			if err != nil {
				s.mu.Lock()
				if s.sharedDesktop == d || s.desktops[d.botID] == d {
					d.stopping = false
					d.lastActivity = time.Now()
				}
				s.mu.Unlock()
			}
		}()
	}
	done := d.stopRequestDone
	d.stopRequestMu.Unlock()
	select {
	case <-done:
		d.stopRequestMu.Lock()
		err := d.stopRequestErr
		d.stopRequestMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitDesktopReady(ctx context.Context, d *desktop) error {
	if d == nil || d.ready == nil {
		return nil
	}
	select {
	case <-d.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) waitForDesktopOperations(ctx context.Context, d *desktop) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		count := d.inFlight
		s.mu.Unlock()
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
