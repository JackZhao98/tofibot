package app

// `tofi restore-remote`: the bare-host half of `tofi restore --from-remote`
// (design 9.1). The host tool runs it once in the installed App image, passes
// one JSON request on stdin (so no key is on a command line or in a file) and
// reads one JSON answer on stdout. It never prints a key.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/backup"
	"github.com/JackZhao98/tofibot/internal/filestore"
	_ "modernc.org/sqlite"
)

type restoreRemoteRequest struct {
	Action      string `json:"action"` // instances | snapshots | fetch
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	PathStyle   bool   `json:"path_style"`
	AccessKeyID string `json:"access_key_id"`
	Secret      string `json:"secret"`
	InstanceID  string `json:"instance_id"`
	RecoveryKey string `json:"recovery_key"`
	Snapshot    string `json:"snapshot"`
	Mode        string `json:"mode"` // takeover | replica
	Out         string `json:"out"`
}

// RunRestoreRemote reads one request from in and writes one answer to out. The
// exit status is 0 when the answer has no "error".
func RunRestoreRemote(ctx context.Context, in io.Reader, out io.Writer) int {
	answer := func(v any, code int) int { _ = json.NewEncoder(out).Encode(v); return code }
	failWith := func(err error) int {
		code, account := errorCode(err)
		return answer(map[string]string{"error": code, "account": account}, 1)
	}
	var req restoreRemoteRequest
	if json.NewDecoder(io.LimitReader(in, 64<<10)).Decode(&req) != nil {
		return answer(map[string]string{"error": "invalid_request"}, 1)
	}
	prefix := strings.Trim(req.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	s3, err := filestore.NewS3(filestore.S3Config{Endpoint: strings.TrimRight(req.Endpoint, "/"), Region: orDefault(req.Region, "auto"), Bucket: req.Bucket, AccessKeyID: req.AccessKeyID, Secret: req.Secret, PathStyle: req.PathStyle})
	if err != nil {
		return failWith(&backup.Error{Code: "backup_store_misconfigured", Cause: err})
	}
	if req.Action == "instances" {
		seen := map[string]bool{}
		err = s3.List(ctx, prefix+"v1/", func(o filestore.ObjectInfo) error {
			rest := strings.TrimPrefix(o.Key, prefix+"v1/")
			if id, tail, ok := strings.Cut(rest, "/"); ok && tail == "server/backups/repo.json" {
				seen[id] = true
			}
			return nil
		})
		if err != nil {
			return failWith(&backup.Error{Code: storeCode(err), Cause: err})
		}
		ids := make([]string, 0, len(seen))
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return answer(map[string]any{"instances": ids}, 0)
	}
	repo, err := backup.Open(ctx, s3, prefix+"v1/"+req.InstanceID+"/server/backups/", req.RecoveryKey, backupWrapper)
	if err != nil {
		return failWith(err)
	}
	snaps, err := repo.List(ctx)
	if err != nil {
		return failWith(err)
	}
	if req.Action == "snapshots" {
		list := []map[string]any{}
		for i := len(snaps) - 1; i >= 0; i-- {
			if !snaps[i].Committed {
				continue
			}
			m, merr := repo.Manifest(ctx, snaps[i].Name)
			if merr != nil {
				return failWith(merr)
			}
			list = append(list, map[string]any{"name": m.Snapshot, "at": m.CreatedAt, "version": m.AppVersion, "reason": m.Reason, "schema": m.Schema, "accounts": len(m.Accounts) + 1, "schema_supported": m.Schema <= SchemaVersion})
		}
		return answer(map[string]any{"snapshots": list, "head_seq": 0}, 0)
	}
	if req.Action != "fetch" || req.Out == "" {
		return answer(map[string]string{"error": "invalid_request"}, 1)
	}
	dest := filepath.Join(req.Out, "data")
	rep, err := repo.RestoreAll(ctx, req.Snapshot, dest, backup.RestoreOpts{MaxSchema: SchemaVersion})
	if err != nil {
		return failWith(err)
	}
	res := map[string]any{"report": rep}
	if req.Mode == "replica" {
		// A second server: a new instance id (made on first start), no backup lineage, backups off.
		_ = os.Remove(filepath.Join(dest, "instance.json"))
		_ = os.RemoveAll(filepath.Join(dest, "backup"))
		if db, derr := sql.Open("sqlite", filepath.Join(dest, "tofi.db")); derr == nil {
			_, _ = db.Exec(`UPDATE org_resources SET config=json_set(config,'$.remote','off') WHERE id='backup_store'`)
			db.Close()
		}
	} else {
		seq, terr := repo.Takeover(ctx)
		if terr == nil {
			terr = backup.WriteState(dest, seq)
		}
		if terr != nil {
			_ = os.RemoveAll(dest)
			return failWith(terr)
		}
		res["writer_seq"] = seq
	}
	return answer(res, 0)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func storeCode(err error) string {
	if errors.Is(err, filestore.ErrDenied) {
		return "backup_store_misconfigured"
	}
	return "backup_store_unavailable"
}
