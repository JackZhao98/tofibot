package app

// The outbox (computer-decoupling S1, owner answer Q1/Q14). A file the person
// should receive can be saved in /workspace/home/outbox instead of calling
// publish_file. At the end of a run that used the computer, the server copies
// every new or changed regular file there into its own file store as an
// attachment of origin `outbox`, bound to that run's reply.
//
// Safety rules, all enforced here or in the guest:
//   - the sync only reads; it never deletes or moves a file on the computer
//   - symbolic links are refused (the guest checks every path component), and
//     anything that is not a regular file is skipped
//   - 20 MiB per file, 200 MiB per sync, 8 files per reply; what does not fit
//     stays in the outbox and goes with a later reply
//   - a ledger (outbox_synced) keyed by path, size and mtime stops a file that
//     already has a live server copy from being copied again; the ledger entry
//     counts only while its attachment still exists
//   - a failure never fails the run: the files are still on the computer

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"path"
	"sort"
	"strings"
	"time"
)

// guestOutboxDir is a variable only so a test can point it at a test guest.
var guestOutboxDir = "/workspace/home/outbox"

const (
	outboxFileBytes   = maxAttachmentSize // 20 MiB
	outboxRunBytes    = 200 << 20
	outboxMaxEntries  = 200
	outboxMaxDepth    = 3
	outboxRunFileCap  = 8 // stageRunAttachment's per-reply cap
	outboxListTimeout = 60 * time.Second
)

type outboxEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MTimeNS   int64  `json:"mtime_ns"`
	Type      string `json:"type"`
	Directory bool   `json:"directory"`
}

// outboxResult says what one sync did. Counts only; no file names are logged.
type outboxResult struct {
	Staged   int
	Skipped  int // already synced, or not a regular file
	Refused  int // too large, a link, or over a cap: stays on the computer
	Deferred int // fits no cap now: stays for the next reply
}

// runUsedComputer reports whether the run made a computer tool call, so a run
// that never touched the computer does not wake it at its end.
func (s *Store) runUsedComputer(runID string) bool {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE run_id=? AND name LIKE 'computer\_%' ESCAPE '\'`, runID).Scan(&n)
	return err == nil && n > 0
}

// listOutbox walks the outbox breadth-first, bounded in depth and entries.
func (s *Server) listOutbox(ctx context.Context, r Run) ([]outboxEntry, error) {
	var files []outboxEntry
	level := []string{guestOutboxDir}
	seen := 0
	for depth := 0; depth <= outboxMaxDepth && len(level) > 0; depth++ {
		var next []string
		for _, dir := range level {
			args, _ := json.Marshal(map[string]any{"path": dir})
			out, err := s.microVMAction(ctx, r, "files.list", args)
			if err != nil {
				if depth == 0 && isNoSuchPath(err) {
					return nil, nil // no outbox yet: nothing to sync
				}
				return files, err
			}
			var entries []outboxEntry
			if json.Unmarshal([]byte(out), &entries) != nil {
				return files, errors.New("outbox listing is not readable")
			}
			for _, e := range entries {
				if seen++; seen > outboxMaxEntries {
					return files, nil
				}
				switch e.Type {
				case "file":
					files = append(files, e)
				case "dir":
					next = append(next, e.Path)
				}
				// symlink, other, or no type (an older guest): never followed.
			}
		}
		level = next
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func isNoSuchPath(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "no such file") || strings.Contains(m, "not exist") || strings.Contains(m, "file_not_found")
}

func (s *Store) outboxAlreadySynced(p string, size, mtime int64) bool {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM outbox_synced o JOIN attachments a ON a.id=o.attachment_id WHERE o.path=? AND o.size=? AND o.mtime_ns=?`, p, size, mtime).Scan(&n)
	return err == nil && n > 0
}

// outboxContentSynced: the same content from the same path already has a live
// server copy (the file was only touched). Returns that attachment id.
func (s *Store) outboxContentSynced(p, digest string) string {
	var id string
	_ = s.db.QueryRow(`SELECT o.attachment_id FROM outbox_synced o JOIN attachments a ON a.id=o.attachment_id WHERE o.path=? AND o.sha256=? LIMIT 1`, p, digest).Scan(&id)
	return id
}

func (s *Store) recordOutboxSynced(p string, size, mtime int64, digest, attachmentID string) {
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO outbox_synced(path,size,mtime_ns,sha256,attachment_id,synced_at) VALUES(?,?,?,?,?,?)`, p, size, mtime, digest, attachmentID, now()); err != nil {
		log.Printf("[outbox] ledger write failed: %v", err)
	}
}

func (s *Store) attachmentDigest(id string) string {
	var d *string
	_ = s.db.QueryRow(`SELECT sha256 FROM attachments WHERE id=?`, id).Scan(&d)
	if d == nil {
		return ""
	}
	return *d
}

// stageOutbox copies the run's new outbox files into the file store and stages
// them for the run's final reply. Best effort: it returns what it did and never
// an error the run must act on.
func (s *Server) stageOutbox(ctx context.Context, r Run, c Conversation) outboxResult {
	var res outboxResult
	if s.microVM == nil || s.store == nil || !s.store.runUsedComputer(r.ID) {
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listCtx, listCancel := context.WithTimeout(ctx, outboxListTimeout)
	files, err := s.listOutbox(listCtx, r)
	listCancel()
	if err != nil {
		log.Printf("[outbox] run %s: listing failed, files stay on the computer: %v", r.ID, err)
		return res
	}
	room := outboxRunFileCap
	var pending int
	if s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, r.ID).Scan(&pending) == nil {
		room -= pending
	}
	var total int64
	names := map[string]bool{}
	for _, f := range files {
		if s.store.outboxAlreadySynced(f.Path, f.Size, f.MTimeNS) {
			res.Skipped++
			continue
		}
		if f.Size > outboxFileBytes {
			res.Refused++
			continue
		}
		if room <= 0 || total+f.Size > outboxRunBytes {
			res.Deferred++
			continue
		}
		name := outboxAttachmentName(f.Path, names)
		a, _, err := s.importGuestFile(ctx, r, c, guestFileImport{
			Path: f.Path, Name: name, NoSymlinks: true,
			Opts: attachmentOpts{Origin: attachmentOriginOutbox, BotID: r.BotID, MaxBytes: outboxFileBytes},
		})
		if err != nil {
			if ctx.Err() != nil {
				res.Deferred++
				break
			}
			if code := storageErrorCode(err); code != "" {
				// Full account or full host: the rest stays in the outbox, and
				// one line says why instead of one per file.
				log.Printf("[outbox] run %s: %s, files stay on the computer", r.ID, code)
				res.Deferred++
				break
			}
			log.Printf("[outbox] run %s: a file was not copied and stays on the computer: %v", r.ID, err)
			res.Refused++
			continue
		}
		digest := s.store.attachmentDigest(a.ID)
		if existing := s.store.outboxContentSynced(f.Path, digest); existing != "" && existing != a.ID {
			// Only the timestamp changed: keep the earlier copy, drop this one.
			_ = s.store.deleteUnboundAttachment(a.ID)
			s.store.recordOutboxSynced(f.Path, f.Size, f.MTimeNS, digest, existing)
			res.Skipped++
			continue
		}
		staged, err := s.store.stageRunAttachment(r, c, a, "outbox:"+f.Path)
		if err != nil {
			_ = s.store.deleteUnboundAttachment(a.ID)
			log.Printf("[outbox] run %s: a copied file could not be attached to the reply and stays for the next one: %v", r.ID, err)
			res.Deferred++
			continue
		}
		s.store.recordOutboxSynced(f.Path, f.Size, f.MTimeNS, digest, staged.ID)
		total += f.Size
		room--
		res.Staged++
	}
	return res
}

// outboxAttachmentName is the file's own name; a second file with the same name
// in another folder gets its folder in front, so one reply never drops it as a
// "newer version" of the first.
func outboxAttachmentName(p string, used map[string]bool) string {
	name := path.Base(p)
	if used[strings.ToLower(name)] {
		dir := path.Base(path.Dir(p))
		name = dir + "-" + name
	}
	used[strings.ToLower(name)] = true
	return name
}
