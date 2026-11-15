package app

// Host backup channel (backups P2): /api/host/backup/{state|run|snapshots},
// the same trust model as /api/host/computer/ (host_control.go). `tofi update`
// asks for a `pre-update` snapshot before it stops anything, and `tofi backups`
// lists the remote snapshots through it. The host also leaves its /etc/tofi
// archive in <data>/host-etc/ (root-owned directory, one file readable by the
// App's uid); the engine encrypts and uploads it with the snapshot. Its bytes
// are never logged, listed or returned.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/JackZhao98/tofibot/internal/backup"
)

const (
	hostBackupRoute = "/api/host/backup/"
	hostEtcDir      = "host-etc"
	hostEtcFile     = "etc.tar.gz"
	// hostRunTimeout bounds one snapshot started by the host.
	hostRunTimeout = 2 * time.Hour
)

// diskSpace is replaced by tests.
var diskSpace = hostDiskSpace

// entryBytes is what restoring one account writes (older snapshots: the database).
func entryBytes(e backup.Entry) int64 {
	if e.Bytes > 0 {
		return e.Bytes
	}
	return e.DBSize
}

// checkRestoreSpace refuses a restore that would not fit under dir: the bytes
// plus a tenth (at least 64 MiB) for staging and growth. The refusal is named
// (restore_no_disk_space) and nothing has been touched yet.
func checkRestoreSpace(dir string, need int64) error {
	probe := dir
	for {
		if _, err := os.Stat(probe); err == nil || filepath.Dir(probe) == probe {
			break
		}
		probe = filepath.Dir(probe)
	}
	free, _, err := diskSpace(probe)
	if err != nil {
		return nil // cannot measure: the restore proceeds and fails by itself if the disk fills
	}
	margin := max(need/10, 64<<20)
	if uint64(need+margin) > free {
		return &restoreError{507, "restore_no_disk_space"}
	}
	return nil
}

func (g *AccountGateway) serveHostBackup(w http.ResponseWriter, r *http.Request, op string) {
	ctx := r.Context()
	switch op {
	case "state":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "use GET")
			return
		}
		cfg, _, found, err := g.backupConfigRow(ctx)
		if err != nil {
			writeErr(w, 500, "internal", "backup configuration unavailable")
			return
		}
		st := g.loadBackupStatus()
		writeJSON(w, 200, map[string]any{
			"configured": found && cfg.Bucket != "", "remote": cfg.Remote, "before_update": cfg.BeforeUpdate,
			"last_status": st.LastStatus, "last_code": st.LastCode, "last_success_at": st.LastSuccessAt,
			"last_snapshot": st.LastSnapshot, "running": g.backupBusy.Load(),
		})
	case "run":
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if decodeStrict(r, 1024, &in) != nil || in.Reason != "pre-update" {
			writeErr(w, 400, "invalid_request", "reason must be pre-update")
			return
		}
		cfg, _, _, err := g.backupConfigRow(ctx)
		if err != nil {
			writeErr(w, 500, "internal", "backup configuration unavailable")
			return
		}
		if cfg.Remote != "on" {
			writeErr(w, 409, "backup_remote_off", "remote backup is not turned on")
			return
		}
		if !g.beginBackupJob() {
			writeErr(w, 409, "backup_in_progress", "a backup job is already running")
			return
		}
		defer g.endBackupJob()
		// The snapshot outlives a dropped host connection: it is bounded by its own clock.
		jobCtx, cancel := context.WithTimeout(g.backupJobContext(), hostRunTimeout)
		defer cancel()
		if err := g.runBackupLocked(jobCtx, in.Reason); err != nil {
			code, account := errorCode(err)
			body := map[string]string{"code": code, "message": "the remote snapshot failed"}
			if account != "" {
				body["account"] = account
			}
			writeJSON(w, backupStatusFor(code), body)
			return
		}
		st := g.loadBackupStatus()
		writeJSON(w, 200, map[string]any{"snapshot_id": st.LastSnapshot, "at": st.LastSuccessAt})
	case "snapshots":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "use GET")
			return
		}
		repo, _, err := g.backupRepo(ctx)
		if err != nil {
			code, _ := errorCode(err)
			writeJSON(w, backupStatusFor(code), map[string]string{"code": code, "message": "backup storage is not ready"})
			return
		}
		snaps, err := repo.List(ctx)
		out := []map[string]any{}
		for i := len(snaps) - 1; err == nil && i >= 0; i-- {
			if !snaps[i].Committed {
				continue
			}
			m, merr := repo.Manifest(ctx, snaps[i].Name)
			if merr != nil {
				err = merr
				break
			}
			out = append(out, map[string]any{"name": m.Snapshot, "at": m.CreatedAt, "version": m.AppVersion, "reason": m.Reason, "size_bytes": m.Size(), "has_etc": m.Etc != nil})
		}
		if err != nil {
			code, _ := errorCode(err)
			writeJSON(w, backupStatusFor(code), map[string]string{"code": code, "message": "backup storage did not answer"})
			return
		}
		writeJSON(w, 200, map[string]any{"snapshots": out})
	default:
		writeErr(w, 404, "not_found", "unknown backup operation")
	}
}
