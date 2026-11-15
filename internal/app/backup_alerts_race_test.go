package app

import (
	"testing"
	"time"
)

// alertIfStale reads and rewrites the whole status row. While a backup job holds the slot (between
// claiming it and its first save it waits for a file step, so the row still says not running), a
// write from the scheduler tick would put back the row it read before the job's save: the card shows
// the wrong running state, and the job's final save drops stale_alerted again. The tick therefore
// leaves the row to the job and looks again on the next tick.
func TestStaleCheckLeavesTheStatusToARunningJob(t *testing.T) {
	b := newBackupFixture(t)
	b.run(t)
	cfg, _, _, err := b.g.backupConfigRow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg = cfg.withDefaults()
	late := time.Now().Add(40 * time.Hour)
	if !b.g.beginBackupJob() {
		t.Fatal("slot busy")
	}
	b.g.alertIfStale(cfg, late)
	st := b.g.loadBackupStatus()
	b.g.endBackupJob()
	if st.StaleAlerted || len(adminAlerts(t, b.g, b.admin)) != 0 {
		t.Fatalf("the tick wrote while a job held the slot: %+v %v", st, adminAlerts(t, b.g, b.admin))
	}
	b.g.alertIfStale(cfg, late)
	if got := adminAlerts(t, b.g, b.admin); len(got) != 1 || got[0] != backupAlertStale || !b.g.loadBackupStatus().StaleAlerted {
		t.Fatalf("after the job: %v", got)
	}
	if b.g.backupBusy.Load() {
		t.Fatal("the stale check left the slot marked busy")
	}
}
