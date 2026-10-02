//go:build linux

package guest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errSelectionTimeout = errors.New("clipboard operation timed out")

type inputClipboard struct {
	cmd    *exec.Cmd
	done   chan struct{}
	events chan string
}

func inputDependencies() error {
	for _, name := range []string{"xdotool", "xclip", "python3"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("desktop input dependency %s is unavailable", name)
		}
	}
	return nil
}

func executeInputEvents(ctx context.Context, session *inputSession, events []DesktopInputEvent) error {
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		var args []string
		if event.X != nil && (event.Type == "move" || event.Type == "down" || event.Type == "up" || event.Type == "wheel") {
			args = append(args, "mousemove", strconv.Itoa(*event.X), strconv.Itoa(*event.Y))
		}
		switch event.Type {
		case "move":
		case "down":
			session.buttons[event.Button] = true // record before sending, including uncertain partial failure
			args = append(args, "mousedown", strconv.Itoa(event.Button))
		case "up":
			args = append(args, "mouseup", strconv.Itoa(event.Button))
		case "keydown":
			if (strings.EqualFold(event.Key, "c") || strings.EqualFold(event.Key, "x")) && (session.keys["Control_L"] || session.keys["Control_R"]) {
				stopSelectionHelper(session.copyWatch)
				session.copyPending = true
				session.copyWatch, _ = startSelectionHelper(ctx, session, "watch", "")
			}
			session.keys[event.Key] = true
			args = append(args, "keydown", event.Key)
		case "keyup":
			args = append(args, "keyup", event.Key)
		case "wheel":
			for _, axis := range []struct{ delta, positive, negative int }{{event.DeltaY, 5, 4}, {event.DeltaX, 7, 6}} {
				amount, button := axis.delta, axis.positive
				if amount < 0 {
					amount, button = -amount, axis.negative
				}
				if amount > 0 {
					args = append(args, "click", "--repeat", strconv.Itoa(amount), "--delay", "0", strconv.Itoa(button))
				}
			}
		case "reset":
			if err := resetInputState(ctx, session); err != nil {
				return err
			}
		case "text":
			if err := resetInputState(ctx, session); err != nil {
				return err
			}
			if err := writeInputClipboard(ctx, session, event.Text); err != nil {
				return err
			}
			combo := "ctrl+v"
			focused := exec.CommandContext(ctx, "xdotool", "getwindowfocus")
			focused.Env = append(os.Environ(), "DISPLAY="+session.desktop.display)
			if window, err := focused.Output(); err == nil {
				property := exec.CommandContext(ctx, "xprop", "-id", strings.TrimSpace(string(window)), "WM_CLASS")
				property.Env = focused.Env
				if class, err := property.Output(); err == nil && strings.Contains(strings.ToLower(string(class)), "xterm") {
					combo = "ctrl+shift+v"
				}
			}
			session.keys["Control_L"] = true
			session.keys["v"] = true
			if combo == "ctrl+shift+v" {
				session.keys["Shift_L"] = true
			}
			args = []string{"key", "--clearmodifiers", combo}
		}
		if len(args) != 0 {
			if _, err := runXTool(ctx, session.desktop, args...); err != nil {
				return err
			}
		}
		if event.Type == "up" {
			delete(session.buttons, event.Button)
		}
		if event.Type == "keyup" {
			delete(session.keys, event.Key)
		}
		if event.Type == "text" {
			clear(session.keys)
			if err := waitSelectionEvent(ctx, session.clipboard, "delivered"); err != nil {
				if errors.Is(err, errSelectionTimeout) {
					return errInputTextNotAccepted
				}
				return err
			}
		}
	}
	return nil
}

func resetInputState(ctx context.Context, session *inputSession) error {
	keys := make([]string, 0, len(session.keys))
	for key := range session.keys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var args []string
	for _, key := range keys {
		args = append(args, "keyup", key)
	}
	for button := 1; button <= 3; button++ {
		if session.buttons[button] {
			args = append(args, "mouseup", strconv.Itoa(button))
		}
	}
	if len(args) != 0 {
		if _, err := runXTool(ctx, session.desktop, args...); err != nil {
			return err
		}
	}
	clear(session.keys)
	clear(session.buttons)
	return nil
}

func closeInputClipboard(session *inputSession) {
	stopSelectionHelper(session.clipboard)
	stopSelectionHelper(session.copyWatch)
	session.clipboard = nil
	session.copyWatch = nil
	session.copyPending = false
}

func stopSelectionHelper(helper *inputClipboard) {
	if helper == nil {
		return
	}
	_ = helper.cmd.Process.Kill()
	select {
	case <-helper.done:
	case <-time.After(time.Second):
	}
}

func startSelectionHelper(ctx context.Context, session *inputSession, mode, text string) (*inputClipboard, error) {
	cmd := exec.Command("python3", "-c", inputSelectionHelper, mode)
	cmd.Env = append(os.Environ(), "DISPLAY="+session.desktop.display)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stderr = &limitedBuffer{limit: 4096}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	helper := &inputClipboard{cmd: cmd, done: make(chan struct{}), events: make(chan string, 8)}
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			select {
			case helper.events <- scanner.Text():
			default:
			}
		}
		_ = cmd.Wait()
		close(helper.done)
	}()
	if err := waitSelectionEvent(ctx, helper, "ready"); err != nil {
		stopSelectionHelper(helper)
		return nil, err
	}
	return helper, nil
}

func waitSelectionEvent(ctx context.Context, helper *inputClipboard, want string) error {
	if helper == nil {
		return errors.New("clipboard helper unavailable")
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-helper.events:
			if event == want {
				return nil
			}
		case <-helper.done:
			// A one-shot watch may exit immediately after flushing its event.
			select {
			case event := <-helper.events:
				if event == want {
					return nil
				}
			default:
			}
			return errors.New("clipboard operation did not complete")
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errSelectionTimeout
		}
	}
}

func writeInputClipboard(ctx context.Context, session *inputSession, text string) error {
	closeInputClipboard(session)
	helper, err := startSelectionHelper(ctx, session, "own", text)
	session.clipboard = helper
	return err
}

func awaitInputCopy(ctx context.Context, session *inputSession) (bool, error) {
	if !session.copyPending {
		return true, nil
	}
	err := waitSelectionEvent(ctx, session.copyWatch, "changed")
	stopSelectionHelper(session.copyWatch)
	session.copyWatch = nil
	session.copyPending = false
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, nil
	}
	session.copyPending = false
	return true, nil
}

func readInputClipboard(ctx context.Context, session *inputSession) (string, error) {
	cmd := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-out", "-target", "UTF8_STRING")
	cmd.Env = append(os.Environ(), "DISPLAY="+session.desktop.display)
	out := &limitedBuffer{limit: MaxDesktopInput + 1}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", nil
		} // empty/non-text selection
		return "", err
	}
	text := out.String()
	if err := validateInputText(text, true); err != nil {
		return "", err
	}
	return text, nil
}
