package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// fakeSleeper is a computer with a power button, the way the E2B computer has
// one: it reports a state and counts pauses.
type fakeSleeper struct {
	*fakeComputer
	lock   sync.Mutex
	state  string
	pauses int
}

func newFakeSleeper() *fakeSleeper {
	f := &fakeSleeper{fakeComputer: newFakeComputer(computer.DefaultID), state: "ready"}
	f.kind = envKindE2B
	return f
}

func (f *fakeSleeper) Info(context.Context) (computer.Info, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	return computer.Info{Kind: envKindE2B, State: f.state, Hibernation: true}, nil
}

func (f *fakeSleeper) Pause(context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.pauses++
	f.state = computer.StateHibernated
	return nil
}

func (f *fakeSleeper) pauseCount() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.pauses
}

func computerInfoJSON(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/computers/firecracker/info", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("info: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sleepCode(t *testing.T, s *Server) (int, string) {
	t.Helper()
	rec := providerRequest(t, s, http.MethodPost, "/api/computers/firecracker/sleep", "")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "" {
		return rec.Code, body.Error.Code
	}
	return rec.Code, body.Code
}

// The power button turns an idle cloud computer off (E2B's pause) and says so;
// a second press finds it off and pauses nothing.
func TestComputerSleepTurnsAnIdleComputerOff(t *testing.T) {
	s := newRegistryServer(t)
	f := newFakeSleeper()
	s.microVM = f
	if info := computerInfoJSON(t, s); info["can_sleep"] != true {
		t.Fatalf("info does not offer the power button: %v", info)
	}
	if code, errCode := sleepCode(t, s); code != http.StatusAccepted {
		t.Fatalf("sleep: %d %s", code, errCode)
	}
	if f.pauseCount() != 1 {
		t.Fatalf("pauses = %d, want 1", f.pauseCount())
	}
	if info := computerInfoJSON(t, s); info["state"] != computer.StateHibernated {
		t.Fatalf("after sleep the computer reports %v", info["state"])
	}
	if code, _ := sleepCode(t, s); code != http.StatusOK || f.pauseCount() != 1 {
		t.Fatalf("second press: %d, pauses = %d", code, f.pauseCount())
	}
}

// A call in flight, a run holding the desktop, or the person controlling it:
// the computer stays on and the answer names why.
func TestComputerSleepRefusesWhileInUse(t *testing.T) {
	s := newRegistryServer(t)
	f := newFakeSleeper()
	s.microVM = f

	slot := s.slotFor(f.ID())
	slot.mu.Lock()
	slot.active++
	slot.mu.Unlock()
	if code, errCode := sleepCode(t, s); code != http.StatusConflict || errCode != "computer_busy" {
		t.Fatalf("call in flight: %d %s", code, errCode)
	}
	slot.mu.Lock()
	slot.active--
	slot.mu.Unlock()

	for _, owner := range []string{"run_1", "human-control:c1"} {
		if !s.claimComputerOwner("bot_1", owner) {
			t.Fatalf("claim %s", owner)
		}
		if code, errCode := sleepCode(t, s); code != http.StatusConflict || errCode != "computer_busy" {
			t.Fatalf("desktop held by %s: %d %s", owner, code, errCode)
		}
		s.clearDesktopOwners()
	}
	if f.pauseCount() != 0 {
		t.Fatalf("a busy computer was paused %d times", f.pauseCount())
	}
	if code, _ := sleepCode(t, s); code != http.StatusAccepted || f.pauseCount() != 1 {
		t.Fatalf("idle again: %d, pauses = %d", code, f.pauseCount())
	}
}

// A computer that is still starting, or one without a power button (the VM),
// is not turned off from here.
func TestComputerSleepNeedsAReadyComputerWithAPowerButton(t *testing.T) {
	s := newRegistryServer(t)
	f := newFakeSleeper()
	f.state = "starting"
	s.microVM = f
	if code, errCode := sleepCode(t, s); code != http.StatusConflict || errCode != "computer_not_ready" {
		t.Fatalf("starting computer: %d %s", code, errCode)
	}

	vm := newRegistryServer(t)
	vm.microVM = newFakeComputer(computer.DefaultID)
	if info := computerInfoJSON(t, vm); info["can_sleep"] != nil {
		t.Fatalf("a VM offers the power button: %v", info)
	}
	if code, errCode := sleepCode(t, vm); code != http.StatusConflict || errCode != "computer_sleep_unsupported" {
		t.Fatalf("VM: %d %s", code, errCode)
	}
}

// The idle auto-off is a setting: 15 minutes by default, 5 to 60.
func TestComputerIdleOffSetting(t *testing.T) {
	s := newRegistryServer(t)
	if got := s.e2bIdleTimeout(); got != 15*time.Minute {
		t.Fatalf("default = %v", got)
	}
	if code := autoPutCode(t, s, `{"computer_idle_off_minutes":30}`); code != http.StatusOK {
		t.Fatalf("set 30: %d", code)
	}
	if got := s.e2bIdleTimeout(); got != 30*time.Minute {
		t.Fatalf("after set = %v", got)
	}
	for _, bad := range []string{`4`, `61`} {
		if code := autoPutCode(t, s, `{"computer_idle_off_minutes":`+bad+`}`); code != http.StatusBadRequest {
			t.Fatalf("value %s: %d, want 400", bad, code)
		}
	}
}
