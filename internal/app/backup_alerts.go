package app

// Administrator notification events for remote backups (backups design 12.4).
//
// The resource row and the Backups card already show the state; this file adds
// the event that makes an administrator look. One failure episode is one event:
// the code of the open episode is kept in backup_state (AlertCode), so a retry
// that fails the same way says nothing, a different code opens a new event, and
// the first success after an episode writes one backup_recovered event and
// closes it. A stale episode (no success within stale_after_hours) is one
// backup_stale event, closed by the next success. A failed drill is one event per
// code (verify_<code>) and closes quietly when a drill passes. The row holds only
// the stable code in detail; the client words it. Upstream text never gets here.

import (
	"log"
	"time"
)

const (
	backupAlertStale     = "backup_stale"
	backupAlertRecovered = "backup_recovered"
)

func (g *AccountGateway) alertBackupFailure(st *backupStatus, code string) {
	if st.AlertCode == code {
		return
	}
	st.AlertCode = code
	g.notifyAdmins(code)
}

func (g *AccountGateway) alertVerifyFailure(st *backupStatus, code string) {
	if st.VerifyAlertCode == code {
		return
	}
	st.VerifyAlertCode = code
	g.notifyAdmins("verify_" + code)
}

func (g *AccountGateway) alertBackupRecovered(st *backupStatus) {
	if st.AlertCode == "" && !st.StaleAlerted {
		return
	}
	st.AlertCode, st.StaleAlerted = "", false
	g.notifyAdmins(backupAlertRecovered)
}

// alertIfStale runs on the scheduler tick. A repository that never had a
// success is reported by its failures, not as stale.
//
// It rewrites the whole status row, so it holds the job slot (backupMu, without
// marking the slot busy for account create and delete) while it reads and
// saves: a job that holds the slot owns the row until it ends, and settles the
// episode itself. The tick then looks again next time.
func (g *AccountGateway) alertIfStale(cfg backupConfig, at time.Time) {
	if cfg.Remote != "on" || !g.backupMu.TryLock() {
		return
	}
	stale := g.markStale(cfg, at)
	g.backupMu.Unlock()
	if stale {
		g.notifyAdmins(backupAlertStale)
	}
}

// markStale records a new stale episode; the caller holds backupMu.
func (g *AccountGateway) markStale(cfg backupConfig, at time.Time) bool {
	st := g.loadBackupStatus()
	if st.StaleAlerted || st.LastSuccessAt == "" || st.Running {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, st.LastSuccessAt)
	if err != nil || at.Sub(t) <= time.Duration(cfg.StaleAfterHours)*time.Hour {
		return false
	}
	st.StaleAlerted = true
	g.saveBackupStatus(st)
	return true
}

// notifyAdmins writes one admin_alert row into every enabled administrator's
// workspace. A workspace that cannot open now is logged by account id only.
func (g *AccountGateway) notifyAdmins(detail string) {
	accounts, err := g.hostAccounts(g.backupJobContext())
	if err != nil {
		log.Printf("[backup] list administrators: %v", err)
		return
	}
	for _, a := range accounts {
		if a.Role != "admin" {
			continue
		}
		ws, err := g.workspace(a)
		if err != nil {
			log.Printf("[backup] notify administrator %s: %v", a.ID, err)
			continue
		}
		if err := ws.store.recordAdminAlert(detail); err != nil {
			log.Printf("[backup] notify administrator %s: %v", a.ID, err)
		}
	}
}

func (s *Store) recordAdminAlert(detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordAttentionTx(tx, attentionAdminAlert, "", "", nil, detail); err != nil {
		return err
	}
	return tx.Commit()
}
