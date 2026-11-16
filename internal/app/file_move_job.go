package app

// The administrator's file move job (design section 6), the reconciliation report
// and the orphan sweep (section 7.1). One of the three runs at a time on a
// server. The move job's state lives in the root database (`file_move_state`,
// `file_move_items`), the rows themselves say what is left, so a restart picks a
// `running` job up where it stopped. Failures carry named codes; upstream text
// goes nowhere, and no log line names a user's file.
//
// Two locks keep it honest. `fileStepMu` is held for the whole of one file step
// and waited on by a backup capture (waitFileStep), so a snapshot never sees a row between two
// places. `fileJob.state` serialises every write of the job's state: the
// pause/resume/cancel handlers and the runner, so a cancel is never overwritten.

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

	"github.com/JackZhao98/tofibot/internal/filestore"
)

const fileMoveDDL = `CREATE TABLE IF NOT EXISTS file_move_state(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS file_move_items(
 account_id TEXT NOT NULL, attachment_id TEXT NOT NULL, code TEXT NOT NULL, size INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(account_id, attachment_id))`

// Move job states. The paused_* ones name why the job stopped itself; an administrator resumes them.
const (
	moveRunning       = "running"
	movePaused        = "paused"
	moveStoreDown     = "paused_store_unavailable"
	moveStoreRefused  = "paused_store_misconfigured"
	moveDiskFull      = "paused_disk_full"
	moveCancelled     = "cancelled"
	moveDone          = "done"
	moveDoneWithError = "done_with_errors"
)

// Per-account codes: an account the job could not walk is one failure line with attachment id "-".
const (
	codeAccountRestoring   = "account_restoring"
	codeAccountDeleting    = "account_deleting"
	codeAccountDisabled    = "account_disabled"
	codeAccountUnavailable = "account_unavailable"
	accountSkipItem        = "-"
)

var (
	errMovePause   = errors.New("move paused")
	errMoveCancel  = errors.New("move cancelled")
	errJobShutdown = errors.New("server stopping")
)

// fileStepPoll is how long a move waits between looks at a running backup; tests shorten it.
var fileStepPoll = 2 * time.Second

type fileMoveJob struct {
	Direction  string `json:"direction"`
	Status     string `json:"status"`
	Code       string `json:"code,omitempty"`
	FilesTotal int64  `json:"files_total"`
	FilesDone  int64  `json:"files_done"`
	BytesTotal int64  `json:"bytes_total"`
	BytesDone  int64  `json:"bytes_done"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func (j fileMoveJob) active() bool {
	return j.Status == moveRunning || strings.HasPrefix(j.Status, movePaused)
}

// fileReport is the last reconciliation or sweep.
type fileReport struct {
	Kind            string `json:"kind"` // reconcile | sweep
	Status          string `json:"status"`
	Code            string `json:"code,omitempty"`
	Deep            bool   `json:"deep,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	SkippedAccounts int64  `json:"skipped_accounts"`
	// ProbesDeleted are stale alias probes (TOFI's own objects) the sweep removed; `deleted` counts files only.
	ProbesDeleted int64 `json:"probes_deleted"`
	fileScan
}

// fileJobGate is the one slot for move, reconcile and sweep.
type fileJobGate struct {
	mu      sync.Mutex
	state   sync.Mutex // serialises the move job's state writes
	running string
	cancel  context.CancelCauseFunc
	wg      sync.WaitGroup
	closed  bool
}

func (g *AccountGateway) startFileJob(kind string) (context.Context, bool) {
	gate := &g.fileJob
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.closed || gate.running != "" {
		return nil, false
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	gate.running, gate.cancel = kind, cancel
	gate.wg.Add(1)
	return ctx, true
}

func (g *AccountGateway) endFileJob() {
	gate := &g.fileJob
	gate.mu.Lock()
	gate.running, gate.cancel = "", nil
	gate.mu.Unlock()
	gate.wg.Done()
}

func (g *AccountGateway) fileJobKind() string {
	g.fileJob.mu.Lock()
	defer g.fileJob.mu.Unlock()
	return g.fileJob.running
}

func (g *AccountGateway) stopFileJob(cause error) bool {
	g.fileJob.mu.Lock()
	defer g.fileJob.mu.Unlock()
	if g.fileJob.cancel == nil {
		return false
	}
	g.fileJob.cancel(cause)
	return true
}

// shutdownFileJobs stops a running job without changing its saved state, so the next start resumes it.
func (g *AccountGateway) shutdownFileJobs() {
	g.fileJob.mu.Lock()
	g.fileJob.closed = true
	if g.fileJob.cancel != nil {
		g.fileJob.cancel(errJobShutdown)
	}
	g.fileJob.mu.Unlock()
	g.fileJob.wg.Wait()
}

// withFileStep runs one file step with the step lock held, and never while a backup runs.
// beginBackupJob sets the busy flag and then waits for the lock, so either the step finishes
// before the backup captures anything or it does not start.
func (g *AccountGateway) withFileStep(ctx context.Context, fn func() error) error {
	for {
		g.fileStepMu.Lock()
		if !g.backupBusy.Load() {
			err := fn()
			g.fileStepMu.Unlock()
			return err
		}
		g.fileStepMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fileStepPoll):
		}
	}
}

// ---- state ----

func (g *AccountGateway) loadMoveState(key string, v any) bool {
	var raw string
	return g.root.store.db.QueryRow(`SELECT v FROM file_move_state WHERE k=?`, key).Scan(&raw) == nil && json.Unmarshal([]byte(raw), v) == nil
}

func (g *AccountGateway) saveMoveState(key string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	if _, err = g.root.store.db.Exec(`INSERT INTO file_move_state(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, string(raw)); err != nil {
		log.Printf("[files] job state not saved: %v", err)
	}
}

func (g *AccountGateway) loadMoveJob() (fileMoveJob, bool) {
	var j fileMoveJob
	ok := g.loadMoveState("job", &j)
	return j, ok
}

func (g *AccountGateway) saveMoveJob(j *fileMoveJob) {
	j.UpdatedAt = now()
	g.saveMoveState("job", j)
}

// saveRunnerJob is the runner's only write of the job. It carries the counters onto the stored
// job and applies `final`, but never overwrites a cancel an administrator already recorded.
func (g *AccountGateway) saveRunnerJob(job *fileMoveJob, final func(cur *fileMoveJob)) {
	g.fileJob.state.Lock()
	defer g.fileJob.state.Unlock()
	cur, ok := g.loadMoveJob()
	if !ok || cur.Status == moveCancelled {
		return
	}
	cur.FilesDone, cur.BytesDone = job.FilesDone, job.BytesDone
	if final != nil {
		final(&cur)
	}
	g.saveMoveJob(&cur)
}

// fileJobAccounts lists every account a job walks, disabled ones included.
func (g *AccountGateway) fileJobAccounts(ctx context.Context) ([]Account, error) {
	rows, err := g.root.store.db.QueryContext(ctx, `SELECT id,username,email,role,disabled,legacy,deleting FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Disabled, &a.Legacy, &a.Deleting); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// fileJobStore gives a job the account's running workspace store. It never opens a store of its own:
// a disabled account has no runtime (a restore or a deletion could race a private copy), so it, an
// account being deleted or restored, and one that cannot be reached come back with a code. The
// job records that code as one failure line and cannot end `done` while it stands.
func (g *AccountGateway) fileJobStore(a Account) (*Store, string) {
	switch {
	case a.Deleting:
		return nil, codeAccountDeleting
	case a.Disabled:
		return nil, codeAccountDisabled
	}
	ws, err := g.workspace(a)
	if err == nil {
		return ws.store, ""
	}
	if errors.Is(err, errAccountRestoring) {
		return nil, codeAccountRestoring
	}
	return nil, codeAccountUnavailable
}

// moveTotals sums what is left in one direction across the running workspaces; disabled
// accounts are not opened and are returned as a count instead.
func (g *AccountGateway) moveTotals(ctx context.Context, dir string) (files, bytes, disabled int64, err error) {
	accounts, err := g.fileJobAccounts(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, a := range accounts {
		st, code := g.fileJobStore(a)
		if code == codeAccountDisabled {
			disabled++
		}
		if code != "" {
			continue // the job reports this account when it walks it
		}
		f, b, cerr := st.moveCount(dir)
		if cerr != nil {
			return 0, 0, 0, cerr
		}
		files, bytes = files+f, bytes+b
	}
	return files, bytes, disabled, nil
}

// ---- the move runner ----

func (g *AccountGateway) runFileMove(ctx context.Context) {
	defer g.endFileJob()
	job, ok := g.loadMoveJob()
	if !ok {
		return
	}
	settle := func(err error) { g.settleMoveJob(ctx, &job, err) }
	accounts, err := g.fileJobAccounts(ctx)
	if err != nil {
		settle(err)
		return
	}
	db := g.root.store.db
	for _, a := range accounts {
		if ctx.Err() != nil {
			settle(ctx.Err())
			return
		}
		st, code := g.fileJobStore(a)
		if code != "" {
			// Not walked: one named failure line for the account, so the job cannot end `done`.
			_, _ = db.Exec(`INSERT INTO file_move_items(account_id,attachment_id,code,size,kind) VALUES(?,?,?,0,'')
			 ON CONFLICT(account_id,attachment_id) DO UPDATE SET code=excluded.code`, a.ID, accountSkipItem, code)
			continue
		}
		_, _ = db.Exec(`DELETE FROM file_move_items WHERE account_id=? AND attachment_id=?`, a.ID, accountSkipItem)
		after := ""
		for {
			var res moveResult
			merr := g.withFileStep(ctx, func() (e error) {
				res, e = st.moveNextFile(ctx, job.Direction, after)
				return e
			})
			if merr != nil {
				settle(merr)
				return
			}
			if !res.Found {
				break
			}
			after = res.ID
			switch {
			case res.Code != "":
				_, _ = db.Exec(`INSERT INTO file_move_items(account_id,attachment_id,code,size,kind) VALUES(?,?,?,?,?)
				 ON CONFLICT(account_id,attachment_id) DO UPDATE SET code=excluded.code,size=excluded.size,kind=excluded.kind`, a.ID, res.ID, res.Code, res.Size, res.Kind)
			case !res.Skipped:
				_, _ = db.Exec(`DELETE FROM file_move_items WHERE account_id=? AND attachment_id=?`, a.ID, res.ID)
				job.FilesDone++
				job.BytesDone += res.Size
			}
			g.saveRunnerJob(&job, nil)
		}
	}
	var failed int
	_ = db.QueryRow(`SELECT COUNT(*) FROM file_move_items`).Scan(&failed)
	g.saveRunnerJob(&job, func(cur *fileMoveJob) {
		cur.Status, cur.Code, cur.FinishedAt = moveDone, "", now()
		if failed > 0 {
			cur.Status, cur.Code = moveDoneWithError, "file_move_failed"
		}
	})
	log.Printf("[files] move %s finished: %d moved, %d failed", job.Direction, job.FilesDone, failed)
}

// settleMoveJob records why the runner stopped: a pause, a cancel, or a named stop.
func (g *AccountGateway) settleMoveJob(ctx context.Context, job *fileMoveJob, err error) {
	var status, code, finished string
	if ctx.Err() != nil {
		switch context.Cause(ctx) {
		case errMovePause:
			status = movePaused
		case errMoveCancel:
			status, finished = moveCancelled, now()
		default:
			return // the server is stopping: the job stays `running` and resumes at start
		}
	} else {
		var fe *fileStoreError
		switch {
		case errors.Is(err, errMoveDiskFull):
			status, code = moveDiskFull, "disk_full"
		case errors.As(err, &fe) && fe.code == codeFileStoreMisconfigured:
			status, code = moveStoreRefused, fe.code
		case errors.As(err, &fe):
			status, code = moveStoreDown, fe.code
		default:
			status, code = movePaused, "file_move_internal"
		}
	}
	g.saveRunnerJob(job, func(cur *fileMoveJob) { cur.Status, cur.Code, cur.FinishedAt = status, code, finished })
}

// startFileMove resumes a job the last process left running, and settles a report it left half done.
func (g *AccountGateway) startFileMove() {
	var rep fileReport
	if g.loadMoveState("report", &rep) && rep.Status == "running" {
		rep.Status, rep.Code, rep.FinishedAt = "failed", "interrupted", now()
		g.saveMoveState("report", &rep)
	}
	if job, ok := g.loadMoveJob(); ok && job.Status == moveRunning {
		if ctx, started := g.startFileJob("move"); started {
			go g.runFileMove(ctx)
		}
	}
}

// ---- reconciliation and sweep ----

func (g *AccountGateway) runFileReport(ctx context.Context, kind string, deep, sweep, dry bool) {
	defer g.endFileJob()
	rep := fileReport{Kind: kind, Status: "running", Deep: deep, DryRun: dry, StartedAt: now()}
	g.saveMoveState("report", &rep)
	fail := func(code string) {
		rep.Status, rep.Code, rep.FinishedAt = "failed", code, now()
		g.saveMoveState("report", &rep)
	}
	accounts, err := g.fileJobAccounts(ctx)
	if err != nil {
		fail("file_scan_failed")
		return
	}
	var total fileScan
	for _, a := range accounts {
		if ctx.Err() != nil {
			fail("interrupted")
			return
		}
		st, code := g.fileJobStore(a)
		if code != "" {
			rep.SkippedAccounts++
			continue
		}
		scan, serr := st.scanFiles(ctx, scanOpts{Deep: deep, Sweep: sweep, DryRun: dry})
		total.add(scan, a.Username)
		if serr != nil {
			var fe *fileStoreError
			switch {
			case ctx.Err() != nil:
				fail("interrupted")
			case errors.As(serr, &fe):
				fail(fe.code)
			default:
				fail("file_scan_failed")
			}
			return
		}
	}
	if sweep && !dry {
		rep.ProbesDeleted = g.sweepProbes(ctx, time.Now())
	}
	rep.fileScan = total
	rep.Status, rep.FinishedAt = "done", now()
	g.saveMoveState("report", &rep)
}

// ---- HTTP ----

type filePlace struct {
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
}

type fileStoreRemoval struct {
	Code     string `json:"code"`
	ClearsAt string `json:"clears_at,omitempty"`
}

type fileMoveView struct {
	// RemoveBlock is why the file store cannot be removed now (nil when it can), so the card can say so before a press.
	RemoveBlock *fileStoreRemoval `json:"remove_block"`
	Configured  bool              `json:"configured"`
	Kind        string            `json:"kind"` // where new files go: s3 | disk
	Disk        filePlace         `json:"disk"`
	Bucket      filePlace         `json:"bucket"`
	// BucketName is the current bucket; Previous are the earlier ones, with what is left in each.
	BucketName string           `json:"bucket_name"`
	ToCurrent  filePlace        `json:"to_current"`
	Previous   []previousBucket `json:"previous"`
	// DisabledAccounts are not opened by a job and are left out of the counts above.
	DisabledAccounts int64        `json:"disabled_accounts"`
	Job              *fileMoveJob `json:"job"`
	Failures         struct {
		Count  int64            `json:"count"`
		ByCode map[string]int64 `json:"by_code"`
		Items  []reportItem     `json:"items"`
	} `json:"failures"`
	Report *fileReport `json:"report"`
	Busy   string      `json:"busy"`
}

func (g *AccountGateway) fileMoveView(ctx context.Context) (fileMoveView, error) {
	var v fileMoveView
	if row, found, err := g.getOrgResource(ctx, orgFileStoreID); err != nil {
		return v, err
	} else if found {
		var cfg fileStoreConfig
		_ = json.Unmarshal([]byte(row.Config), &cfg)
		v.Configured, v.Kind, v.BucketName = true, cfg.Kind, cfg.Bucket
		if v.Kind != fileStoreKindDisk {
			v.Kind = fileStoreKindS3
		}
		v.Previous = []previousBucket{}
		usage, uerr := g.fileStoreUsage(ctx, false)
		if uerr != nil {
			return v, uerr
		}
		if code, _, clearsAt := fileStoreRemoveBlock(usage); code != "" {
			v.RemoveBlock = &fileStoreRemoval{Code: code, ClearsAt: clearsAt}
		}
		if len(cfg.Prev) > 0 {
			for _, p := range cfg.Prev {
				u := usage[p.Gen]
				v.Previous = append(v.Previous, previousBucket{Gen: p.Gen, Bucket: p.Bucket, ReplacedAt: p.ReplacedAt, Files: u.Files, Bytes: u.Bytes, Pending: u.Pending + u.Staging})
			}
		}
	}
	if v.Previous == nil {
		v.Previous = []previousBucket{}
	}
	var err error
	if v.Disk.Files, v.Disk.Bytes, v.DisabledAccounts, err = g.moveTotals(ctx, moveToBucket); err != nil {
		return v, err
	}
	if v.Bucket.Files, v.Bucket.Bytes, _, err = g.moveTotals(ctx, moveToDisk); err != nil {
		return v, err
	}
	if len(v.Previous) > 0 {
		if v.ToCurrent.Files, v.ToCurrent.Bytes, _, err = g.moveTotals(ctx, moveToGeneration); err != nil {
			return v, err
		}
	}
	if job, ok := g.loadMoveJob(); ok {
		v.Job = &job
	}
	var rep fileReport
	if g.loadMoveState("report", &rep) {
		v.Report = &rep
	}
	v.Busy = g.fileJobKind()
	v.Failures.ByCode, v.Failures.Items = map[string]int64{}, []reportItem{}
	db := g.root.store.db
	rows, err := db.QueryContext(ctx, `SELECT code,COUNT(*) FROM file_move_items GROUP BY code`)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var code string
		var n int64
		if err = rows.Scan(&code, &n); err != nil {
			rows.Close()
			return v, err
		}
		v.Failures.ByCode[code] = n
		v.Failures.Count += n
	}
	rows.Close()
	items, err := db.QueryContext(ctx, `SELECT COALESCE(a.username,''),i.code,i.kind,i.size FROM file_move_items i LEFT JOIN accounts a ON a.id=i.account_id ORDER BY i.account_id,i.attachment_id LIMIT ?`, reportItemCap)
	if err != nil {
		return v, err
	}
	defer items.Close()
	for items.Next() {
		var it reportItem
		if err = items.Scan(&it.Account, &it.Issue, &it.Kind, &it.Size); err != nil {
			return v, err
		}
		v.Failures.Items = append(v.Failures.Items, it)
	}
	return v, items.Err()
}

// previousBucket is an earlier bucket generation as the card shows it. `Gen` is only an
// API handle for retire; the card shows the bucket name and the date.
type previousBucket struct {
	Gen        string `json:"gen"`
	Bucket     string `json:"bucket"`
	ReplacedAt string `json:"replaced_at"`
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	Pending    int64  `json:"pending"`
}

// moveDestinationProblem is what start and resume both check: the resource exists and the
// destination switch points where the files are going.
func (g *AccountGateway) moveDestinationProblem(ctx context.Context, dir string) (code, msg string, err error) {
	row, found, err := g.getOrgResource(ctx, orgFileStoreID)
	if err != nil {
		return "", "", err
	}
	var cfg fileStoreConfig
	if found {
		_ = json.Unmarshal([]byte(row.Config), &cfg)
	}
	switch {
	case !found:
		return "file_store_not_configured", "Set up File storage first.", nil
	case dir == moveToBucket && cfg.Kind == fileStoreKindDisk:
		return "destination_is_disk", "New files are set to go to this server's disk. Set the destination to the bucket first.", nil
	case dir == moveToDisk && cfg.Kind != fileStoreKindDisk:
		return "destination_is_bucket", "New files are set to go to the bucket. Set the destination to this server's disk first.", nil
	case dir == moveToGeneration && cfg.Kind == fileStoreKindDisk:
		return "destination_is_disk", "New files are set to go to this server's disk. Set the destination to the bucket first, or move the files to disk.", nil
	case dir == moveToGeneration && len(cfg.Prev) == 0:
		return "no_previous_bucket", "There is no earlier bucket to move files from.", nil
	}
	if dir == moveToGeneration {
		return g.generationsDistinct(ctx, cfg)
	}
	return "", "", nil
}

// generationsDistinct is the one check that the earlier buckets are not the current bucket under another
// name, made when a to_generation job starts or resumes: a unique probe is put into the current bucket and
// looked for through every earlier generation. Visible means one physical bucket, and the job must not run
// (it would journal the only copy of each file for deletion). The probe is removed either way.
func (g *AccountGateway) generationsDistinct(ctx context.Context, cfg fileStoreConfig) (code, msg string, err error) {
	b, err := g.fileBackendFor(ctx, "-")
	if err != nil {
		return "", "", err
	}
	if b.Store == nil {
		return "file_store_not_configured", "Set up File storage first.", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	key := cfg.Prefix + fileStoreLayoutV1 + "/" + g.root.instance.ID + "/_probe/" + newID()
	payload := []byte("tofi-alias-probe")
	if perr := b.Store.Put(ctx, key, bytes.NewReader(payload), filestore.ObjectMeta{Size: int64(len(payload)), SHA256: "probe", SafeMIME: "application/octet-stream"}); perr != nil {
		return "file_store_unavailable", fileStoreMessage(codeFileStoreUnavailable), nil
	}
	defer deleteProbe(b.Store, key)
	for _, p := range cfg.Prev {
		old, oerr := b.Old(p.Gen)
		if oerr != nil || old == nil {
			return "file_store_misconfigured", fileStoreMessage(codeFileStoreMisconfigured), nil
		}
		switch g.probeAnswer(ctx, old, p.Gen, key) {
		case probeVisible:
			return codeSamePhysicalBucket, "The current bucket is the same bucket as an earlier one. Nothing was moved.", nil
		case probeAbsent:
		case probeRefused:
			return codePreviousBucketRefused, "An earlier bucket refused its key, so it cannot be checked that it is a different bucket from the current one. Nothing was moved.", nil
		default:
			return "file_store_unavailable", fileStoreMessage(codeFileStoreUnavailable), nil
		}
	}
	return "", "", nil
}

// probeResult is what one generation's store says about a probe just written into another bucket.
type probeResult int

const (
	probeUnanswered probeResult = iota // outage or timeout: no answer either way
	probeAbsent                        // not there: another bucket
	probeVisible                       // there: the same bucket under another name
	probeRefused                       // the key is refused, so the store cannot tell
)

// probeAnswer asks st (generation gen) for the probe key. A missing bucket is another bucket: MinIO
// names it in x-minio-error-code on a HEAD (NoSuchBucket), S3 answers 404. S3 answers 403, not 404, for
// a missing key when the key may not list the bucket (no s3:ListBucket); such a key still reads, so
// when it reads an object this generation is known to hold, the 403 means the probe is not there.
func (g *AccountGateway) probeAnswer(ctx context.Context, st filestore.Store, gen, key string) probeResult {
	_, err := st.Stat(ctx, key)
	switch {
	case err == nil:
		return probeVisible
	case errors.Is(err, filestore.ErrNotFound), noSuchBucket(err):
		return probeAbsent
	case !errors.Is(err, filestore.ErrDenied):
		return probeUnanswered
	}
	known, ok := g.knownObjectKey(ctx, gen)
	if !ok {
		return probeRefused
	}
	if _, kerr := st.Stat(ctx, known); kerr != nil {
		return probeRefused
	}
	return probeAbsent
}

// knownObjectKey is the key of one object a row of generation gen names, from the first running
// workspace that has one (disabled accounts are not opened, as for a job).
func (g *AccountGateway) knownObjectKey(ctx context.Context, gen string) (string, bool) {
	accounts, err := g.fileJobAccounts(ctx)
	if err != nil {
		return "", false
	}
	for _, a := range accounts {
		st, code := g.fileJobStore(a)
		if code != "" {
			continue
		}
		var disk string
		if st.db.QueryRowContext(ctx, `SELECT disk_name FROM attachments WHERE disk_name>='obj:' AND disk_name<'obj;' AND `+genOfDisk+`=? LIMIT 1`, gen).Scan(&disk) == nil {
			_, k := objectRef(disk)
			return k, true
		}
	}
	return "", false
}

// noSuchBucket: the bucket itself is gone, so it cannot be the same one as another.
func noSuchBucket(err error) bool {
	return errors.Is(err, filestore.ErrDenied) && filestore.ErrorCode(err) == "NoSuchBucket"
}

// deleteProbe removes a probe object, once more if the first delete fails.
func deleteProbe(st filestore.Store, key string) {
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := st.Delete(ctx, key)
		cancel()
		if err == nil {
			return
		}
	}
}

// sweepProbes removes probe objects older than a day under the current and every earlier bucket
// (a probe left behind by a failed delete). Best effort: it returns how many it removed.
func (g *AccountGateway) sweepProbes(ctx context.Context, now time.Time) int64 {
	row, found, err := g.getOrgResource(ctx, orgFileStoreID)
	if err != nil || !found {
		return 0
	}
	var cfg fileStoreConfig
	if json.Unmarshal([]byte(row.Config), &cfg) != nil {
		return 0
	}
	b, err := g.fileBackendFor(ctx, "-")
	if err != nil || b.Store == nil {
		return 0
	}
	type target struct {
		st     filestore.Store
		prefix string
	}
	targets := []target{{b.Store, cfg.Prefix + fileStoreLayoutV1 + "/" + g.root.instance.ID + "/_probe/"}}
	for _, p := range cfg.Prev {
		if old, oerr := b.Old(p.Gen); oerr == nil && old != nil {
			targets = append(targets, target{old, p.Prefix + fileStoreLayoutV1 + "/" + g.root.instance.ID + "/_probe/"})
		}
	}
	var removed int64
	for _, t := range targets {
		var keys []string
		_ = t.st.List(ctx, t.prefix, func(o filestore.ObjectInfo) error {
			if !o.Modified.IsZero() && o.Modified.Before(now.Add(-24*time.Hour)) {
				keys = append(keys, o.Key)
			}
			return nil
		})
		for _, k := range keys {
			if t.st.Delete(ctx, k) == nil {
				removed++
			}
		}
	}
	return removed
}

func (g *AccountGateway) adminFileMove(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/file-move"
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	ctx := r.Context()
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		v, err := g.fileMoveView(ctx)
		if err != nil {
			storageFailure(w, "read file move", err)
			return true
		}
		writeJSON(w, 200, v)
	case rest == "" && r.Method == http.MethodPost:
		g.startMove(w, r)
	case rest == "pause" && r.Method == http.MethodPost:
		g.fileJob.state.Lock()
		defer g.fileJob.state.Unlock()
		if job, ok := g.loadMoveJob(); !ok || job.Status != moveRunning || g.fileJobKind() != "move" || !g.stopFileJob(errMovePause) {
			writeErr(w, 409, "no_running_move", "No file move is running.")
			return true
		}
		writeJSON(w, 202, map[string]any{"ok": true})
	case rest == "resume" && r.Method == http.MethodPost:
		g.fileJob.state.Lock()
		defer g.fileJob.state.Unlock()
		job, ok := g.loadMoveJob()
		if !ok || !strings.HasPrefix(job.Status, movePaused) {
			writeErr(w, 409, "no_paused_move", "No paused file move to resume.")
			return true
		}
		code, msg, err := g.moveDestinationProblem(ctx, job.Direction)
		if err != nil {
			storageFailure(w, "read resource", err)
			return true
		}
		if code != "" {
			writeErr(w, 409, code, msg)
			return true
		}
		jobCtx, started := g.startFileJob("move")
		if !started {
			writeErr(w, 409, "file_job_running", "Another file job is running.")
			return true
		}
		job.Status, job.Code = moveRunning, ""
		g.saveMoveJob(&job)
		go g.runFileMove(jobCtx)
		writeJSON(w, 202, map[string]any{"ok": true})
	case rest == "cancel" && r.Method == http.MethodPost:
		g.fileJob.state.Lock()
		defer g.fileJob.state.Unlock()
		job, ok := g.loadMoveJob()
		if !ok || !job.active() {
			writeErr(w, 409, "no_active_move", "No file move to cancel.")
			return true
		}
		// Recorded first, under the lock: the runner never overwrites it, whatever it was doing.
		job.Status, job.Code, job.FinishedAt = moveCancelled, "", now()
		g.saveMoveJob(&job)
		if g.fileJobKind() == "move" {
			g.stopFileJob(errMoveCancel)
		}
		writeJSON(w, 202, map[string]any{"ok": true})
	case rest == "retire" && r.Method == http.MethodPost:
		var in struct {
			Gen string `json:"gen"`
		}
		if decodeStrict(r, 1<<10, &in) != nil || !genNamePattern.MatchString(in.Gen) {
			writeErr(w, 400, "invalid_request", "name the earlier bucket")
			return true
		}
		g.fileJob.state.Lock()
		defer g.fileJob.state.Unlock()
		if job, ok := g.loadMoveJob(); ok && job.active() {
			writeErr(w, 409, "file_move_active", "A file move is running or paused. Finish or cancel it first.")
			return true
		}
		code, msg, status, err := g.retireFileStoreGen(ctx, in.Gen)
		switch {
		case err != nil:
			storageFailure(w, "retire earlier bucket", err)
		case code != "":
			writeErr(w, status, code, msg)
		default:
			writeJSON(w, 200, map[string]any{"retired": true})
		}
	case (rest == "reconcile" || rest == "sweep") && r.Method == http.MethodPost:
		var in struct {
			Deep   bool `json:"deep"`
			DryRun bool `json:"dry_run"`
		}
		if r.ContentLength != 0 && decodeStrict(r, 1<<10, &in) != nil {
			writeErr(w, 400, "invalid_request", "invalid request")
			return true
		}
		if job, ok := g.loadMoveJob(); ok && job.Status == moveRunning {
			writeErr(w, 409, "file_job_running", "A file move is running.")
			return true
		}
		jobCtx, started := g.startFileJob(rest)
		if !started {
			writeErr(w, 409, "file_job_running", "Another file job is running.")
			return true
		}
		go g.runFileReport(jobCtx, rest, in.Deep, rest == "sweep", in.DryRun)
		writeJSON(w, 202, map[string]any{"started": true})
	default:
		writeErr(w, 404, "not_found", "not found")
	}
	return true
}

func (g *AccountGateway) startMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Direction string `json:"direction"`
	}
	if decodeStrict(r, 1<<10, &in) != nil || (in.Direction != moveToBucket && in.Direction != moveToDisk && in.Direction != moveToGeneration) {
		writeErr(w, 400, "invalid_request", "direction is to_bucket, to_disk or to_generation")
		return
	}
	ctx := r.Context()
	g.fileJob.state.Lock()
	defer g.fileJob.state.Unlock()
	code, msg, err := g.moveDestinationProblem(ctx, in.Direction)
	if err != nil {
		storageFailure(w, "read resource", err)
		return
	}
	if code != "" {
		writeErr(w, 409, code, msg)
		return
	}
	if job, ok := g.loadMoveJob(); ok && job.active() {
		writeErr(w, 409, "file_move_active", "A file move is already running or paused. Resume or cancel it first.")
		return
	}
	files, bytes, _, err := g.moveTotals(ctx, in.Direction)
	if err != nil {
		storageFailure(w, "count files", err)
		return
	}
	if files == 0 {
		writeErr(w, 409, "nothing_to_move", "There are no files to move in that direction.")
		return
	}
	if in.Direction == moveToDisk {
		// The bytes stay on this server's disk: refuse by name when they do not fit under the host floor.
		if free, _, perr := g.root.store.storage.probe(g.config.DataDir); perr != nil || free < uint64(max(g.root.store.storage.floor(), 0))+uint64(bytes) {
			writeErr(w, 409, "disk_space_low", "This server's disk does not have room for these files. Free some space first.")
			return
		}
	}
	jobCtx, started := g.startFileJob("move")
	if !started {
		writeErr(w, 409, "file_job_running", "Another file job is running.")
		return
	}
	job := fileMoveJob{Direction: in.Direction, Status: moveRunning, FilesTotal: files, BytesTotal: bytes, StartedAt: now()}
	if _, err = g.root.store.db.Exec(`DELETE FROM file_move_items`); err != nil {
		g.endFileJob()
		storageFailure(w, "reset file move", err)
		return
	}
	g.saveMoveJob(&job)
	go g.runFileMove(jobCtx)
	writeJSON(w, 202, map[string]any{"started": true})
}

// ---- physical counting (design 7.4) ----

// bucketPhysicalCache keeps the bucket byte sum of one account for a day: it costs one listing.
type bucketPhysicalCache struct {
	mu    sync.Mutex
	bytes int64
	at    time.Time
}

// physicalBucketBytes sums the objects under this account's prefix. It lists at most once a
// day and only for an account that has rows in the bucket; a failure keeps the last sum.
func (s *Store) physicalBucketBytes(ctx context.Context) int64 {
	c := &s.bucketPhysical
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < 24*time.Hour {
		return c.bytes
	}
	if n, err := s.objectRows(); err != nil || n == 0 {
		return 0
	}
	b, err := s.fileBackend(ctx)
	if err != nil || b.Store == nil {
		return c.bytes
	}
	var total int64
	if err = b.Store.List(ctx, b.Prefix, func(o filestore.ObjectInfo) error { total += o.Size; return nil }); err != nil {
		return c.bytes
	}
	c.bytes, c.at = total, time.Now()
	return total
}
