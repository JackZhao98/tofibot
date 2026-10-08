package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestComputerWatchdogNeverRestartsAHibernatedComputer(t *testing.T) {
	clock := &fakeWatchdogClock{at: time.Now()}
	guest := "unresponsive"
	recovers := 0
	w := &computerWatchdog{now: clock.now, state: computerHealthUnknown, wake: make(chan struct{}, 1),
		probe: func(context.Context) (computer.GuestHealth, error) {
			return computer.GuestHealth{State: "hibernated", Guest: guest}, nil
		},
		recover: func(context.Context) error { recovers++; return nil }}
	// One failed probe, then the computer hibernates: the count resets.
	w.check(context.Background())
	guest = "hibernated"
	for i := 0; i < 3*watchdogFailureThreshold; i++ {
		w.check(context.Background())
	}
	if recovers != 0 || w.view().State != computerHealthy || w.view().Failures != 0 {
		t.Fatalf("hibernated computer treated as unresponsive: %d %+v", recovers, w.view())
	}
	if err := w.admission(); err != nil {
		t.Fatalf("hibernated computer must accept calls (restored on use): %v", err)
	}
}

func TestHibernatedComputerIsAvailableToTheModel(t *testing.T) {
	for state, want := range map[string]bool{"ready": true, "hibernated": true, "hibernating": true, "resuming": true, "starting": false, "stopped": false, "error": false} {
		if computer.Available(state) != want {
			t.Fatalf("Available(%q) = %v", state, !want)
		}
	}
	s := &Server{microVM: &computer.Client{}}
	s.microVMInfoCache.info = computer.Info{State: "hibernated", Phase: "hibernated", Hibernation: true}
	s.microVMInfoCache.at = time.Now()
	status := s.microVMStatus(context.Background())
	if !strings.HasPrefix(status, "VM status hibernated/hibernated (") || !strings.Contains(status, "resumes automatically") {
		t.Fatalf("status = %q", status)
	}
}

func TestCapacityParsesSnapshotPromises(t *testing.T) {
	base := `{"total_bytes":107374182400,"available_bytes":53687091200,"allocated_bytes":0,"promised_bytes":0,"unallocated_promises_bytes":0,"warning":false,"accounts":[]`
	good := base + `,"snapshot_reserved_bytes":1140850688,"snapshot_allocated_bytes":40,"snapshot_unallocated_reserved_bytes":1140850648,"admission_remaining_bytes":1}`
	out, err := parseAccountCapacity([]byte(good))
	if err != nil || out.SnapshotReserved != 1140850688 {
		t.Fatalf("parse = %+v %v", out, err)
	}
	for _, bad := range []string{
		base + `,"snapshot_reserved_bytes":10,"snapshot_allocated_bytes":11,"snapshot_unallocated_reserved_bytes":-1,"admission_remaining_bytes":1}`,
		base + `,"snapshot_reserved_bytes":10,"snapshot_allocated_bytes":0,"snapshot_unallocated_reserved_bytes":0,"admission_remaining_bytes":1}`,
		// Admission may not exceed space left after the snapshot promise.
		base + `,"snapshot_reserved_bytes":53687091200,"snapshot_allocated_bytes":0,"snapshot_unallocated_reserved_bytes":53687091200,"admission_remaining_bytes":1}`,
	} {
		if _, err := parseAccountCapacity([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	// Legacy brokers omit the fields entirely.
	if _, err := parseAccountCapacity([]byte(base + `,"admission_remaining_bytes":1}`)); err != nil {
		t.Fatalf("legacy capacity rejected: %v", err)
	}
}
