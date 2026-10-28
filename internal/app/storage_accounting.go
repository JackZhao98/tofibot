package app

// Storage accounting (computer-decoupling S3, design section 11).
//
// What is counted, and how it stays correct and cheap:
//   - Attachment bytes are logical: one count per attachment row, grouped by the
//     row's origin (uploads, published, generated, outbox, mirror). SQLite
//     triggers on `attachments` keep `storage_counters` in step with every
//     insert, delete (including cascade deletes of a conversation) and update,
//     in the same transaction as the row. A read is five tiny rows, never a
//     rescan.
//   - A periodic reconcile recounts from the rows, repairs drift (for example
//     rows written by a build that had no triggers), measures what is not
//     incremental (message text, bytes on disk) and stores the per-Bot and
//     per-conversation breakdown.
//   - The database file (with its WAL) is read live from the file system.
//   - Billable server bytes = file bytes + database bytes. Message text lives in
//     the database, so the "conversations" class is shown but not added again.
//   - The computer disk is separate: it is measured by the host, never counted
//     against the server limit.
//
// Enforcement (storageAdmit) refuses a new file with storage_quota_exceeded when
// the account has a limit and is full, and with storage_host_full when the host
// volume is below the operator floor. A NULL limit means unlimited. Nothing is
// deleted automatically.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	storageClassConversations = "conversations"
	storageClassPhysical      = "physical"
	storageClassDatabase      = "db"

	// defaultHostMinFreeBytes is the free space below which new files are
	// refused on any mode (design 11.4).
	defaultHostMinFreeBytes int64 = 2 << 30
	storageReconcileEvery         = time.Hour
	storageBreakdownTop           = 50
	storageAPIVersion             = 1
)

// storageAttachmentClasses are the billable file classes, in display order.
var storageAttachmentClasses = []string{"uploads", "published", "generated", "outbox", "mirror"}

// storageClassSQL maps attachments.origin to a counter class. `upload` and any
// unknown origin count as uploads so no byte is ever left out.
func storageClassSQL(row string) string {
	return "CASE " + row + ".origin WHEN 'published' THEN 'published' WHEN 'generated' THEN 'generated' WHEN 'outbox' THEN 'outbox' WHEN 'mirror' THEN 'mirror' ELSE 'uploads' END"
}

// Error codes. They are part of the API; keep them stable.
const (
	StorageQuotaExceededCode = "storage_quota_exceeded"
	StorageHostFullCode      = "storage_host_full"
)

// storageError is returned by the file writers. Its text starts with the code
// so a model-side tool result carries it too.
type storageError struct {
	Code string
	Text string
}

func (e *storageError) Error() string { return e.Code + ": " + e.Text }

func storageErrorCode(err error) string {
	var se *storageError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

func newQuotaError() error {
	return &storageError{StorageQuotaExceededCode, "The storage limit for this account is reached. Delete files or ask the administrator."}
}

func newHostFullError() error {
	return &storageError{StorageHostFullCode, "The server disk is almost full, so no new files are accepted. Tell the operator of this server."}
}

// storageGate carries the policy a Store enforces. Zero value: no account
// limit, default host floor, real disk probe.
type storageGate struct {
	// limit returns the account's server byte limit; nil result = unlimited.
	limit func() *int64
	// minFree is the host free-space floor; 0 = default, negative = off.
	minFree int64
	// diskSpace probes the volume; nil = hostDiskSpace.
	diskSpace func(path string) (free, total uint64, err error)
}

func (g storageGate) floor() int64 {
	if g.minFree == 0 {
		return defaultHostMinFreeBytes
	}
	return g.minFree
}

func (g storageGate) probe(path string) (uint64, uint64, error) {
	if g.diskSpace != nil {
		return g.diskSpace(path)
	}
	return hostDiskSpace(path)
}

func migrateStorageCounters(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS storage_counters(
		 class TEXT PRIMARY KEY, bytes INTEGER NOT NULL DEFAULT 0, objects INTEGER NOT NULL DEFAULT 0, measured_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS storage_breakdown(
		 id INTEGER PRIMARY KEY CHECK(id=1), measured_at TEXT NOT NULL, detail TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS storage_attachments_ai AFTER INSERT ON attachments BEGIN
		 INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(` + storageClassSQL("NEW") + `,MAX(0,NEW.size),1,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		 ON CONFLICT(class) DO UPDATE SET bytes=bytes+excluded.bytes,objects=objects+1,measured_at=excluded.measured_at;
		END`,
		`CREATE TRIGGER IF NOT EXISTS storage_attachments_ad AFTER DELETE ON attachments BEGIN
		 INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(` + storageClassSQL("OLD") + `,0,0,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		 ON CONFLICT(class) DO UPDATE SET bytes=MAX(0,bytes-OLD.size),objects=MAX(0,objects-1),measured_at=excluded.measured_at;
		END`,
		`CREATE TRIGGER IF NOT EXISTS storage_attachments_au AFTER UPDATE OF size,origin ON attachments BEGIN
		 INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(` + storageClassSQL("OLD") + `,0,0,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		 ON CONFLICT(class) DO UPDATE SET bytes=MAX(0,bytes-OLD.size),objects=MAX(0,objects-1),measured_at=excluded.measured_at;
		 INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(` + storageClassSQL("NEW") + `,MAX(0,NEW.size),1,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		 ON CONFLICT(class) DO UPDATE SET bytes=bytes+excluded.bytes,objects=objects+1,measured_at=excluded.measured_at;
		END`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("storage counters: %w", err)
		}
	}
	// First start on a database that predates the counters: seed them once.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM storage_counters`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := reconcileAttachmentCounters(db); err != nil {
			return err
		}
	}
	return nil
}

// counterDrift is one class whose stored counter disagreed with the rows.
type counterDrift struct {
	Class                    string
	StoredBytes, ActualBytes int64
	StoredObjs, ActualObjs   int64
}

// reconcileAttachmentCounters recounts the five file classes from the rows in
// one transaction and returns the classes that differed.
func reconcileAttachmentCounters(db *sql.DB) ([]counterDrift, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	type pair struct{ bytes, objs int64 }
	stored, actual := map[string]pair{}, map[string]pair{}
	rows, err := tx.Query(`SELECT class,bytes,objects FROM storage_counters WHERE class IN ('uploads','published','generated','outbox','mirror')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var p pair
		if err = rows.Scan(&c, &p.bytes, &p.objs); err != nil {
			rows.Close()
			return nil, err
		}
		stored[c] = p
	}
	rows.Close()
	rows, err = tx.Query(`SELECT class,COALESCE(SUM(size),0),COUNT(*) FROM (SELECT ` + storageClassSQL("attachments") + ` AS class,size FROM attachments) GROUP BY class`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var p pair
		if err = rows.Scan(&c, &p.bytes, &p.objs); err != nil {
			rows.Close()
			return nil, err
		}
		actual[c] = p
	}
	rows.Close()
	var drift []counterDrift
	stamp := now()
	for _, c := range storageAttachmentClasses {
		s, a := stored[c], actual[c]
		if s != a {
			drift = append(drift, counterDrift{c, s.bytes, a.bytes, s.objs, a.objs})
		}
		if _, err = tx.Exec(`INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(?,?,?,?)
		 ON CONFLICT(class) DO UPDATE SET bytes=excluded.bytes,objects=excluded.objects,measured_at=excluded.measured_at`, c, a.bytes, a.objs, stamp); err != nil {
			return nil, err
		}
	}
	return drift, tx.Commit()
}

// --- reading -----------------------------------------------------------

// StorageClass is one row of the breakdown. Billable classes add up to the
// server bytes; others are shown for information.
type StorageClass struct {
	Class    string `json:"class"`
	Bytes    int64  `json:"bytes"`
	Objects  int64  `json:"objects"`
	Billable bool   `json:"billable"`
}

// StorageItem is one Bot or one conversation in the breakdown. For a Bot,
// an empty ID is the person's own files and messages.
type StorageItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Objects int64  `json:"objects"`
}

// StorageDetail is the part of the report that only a reconcile measures. It is
// stored as JSON in storage_breakdown and in account_storage.detail.
type StorageDetail struct {
	ByBot          []StorageItem `json:"by_bot"`
	ByConversation []StorageItem `json:"by_conversation"`
}

// StorageReport is the account's usage. The same value feeds GET /api/storage,
// the admin list and the snapshots.
type StorageReport struct {
	MeasuredAt          string         `json:"measured_at"`
	ServerBytes         int64          `json:"server_bytes"`
	FilesBytes          int64          `json:"files_bytes"`
	DatabaseBytes       int64          `json:"database_bytes"`
	PhysicalFilesBytes  int64          `json:"physical_files_bytes"`
	PendingCopyCount    int            `json:"pending_copy_count"`
	Classes             []StorageClass `json:"classes"`
	Detail              StorageDetail  `json:"-"`
	BreakdownMeasuredAt string         `json:"breakdown_measured_at,omitempty"`
}

func (s *Store) readCounters() (map[string]StorageClass, error) {
	rows, err := s.db.Query(`SELECT class,bytes,objects FROM storage_counters`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StorageClass{}
	for rows.Next() {
		var c StorageClass
		if err = rows.Scan(&c.Class, &c.Bytes, &c.Objects); err != nil {
			return nil, err
		}
		out[c.Class] = c
	}
	return out, rows.Err()
}

// databaseBytes is the SQLite file plus its WAL. It is two PRAGMAs and a stat.
func (s *Store) databaseBytes() int64 {
	var pageSize, pageCount int64
	if s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize) != nil || s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount) != nil {
		return 0
	}
	total := pageSize * pageCount
	var file string
	if s.db.QueryRow(`PRAGMA database_list`).Scan(new(any), new(any), &file) == nil && file != "" {
		if info, err := os.Stat(file + "-wal"); err == nil {
			total += info.Size()
		}
	}
	return total
}

// serverBytes is the billable usage: counted file bytes plus database bytes.
func (s *Store) serverBytes() (int64, error) {
	var files int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM storage_counters WHERE class IN ('uploads','published','generated','outbox','mirror')`).Scan(&files)
	if err != nil {
		return 0, err
	}
	return files + s.databaseBytes(), nil
}

// storageLive builds the report from the incremental counters and the live
// database size, plus the last measured values. It does no scan.
func (s *Store) storageLive() (StorageReport, error) {
	counters, err := s.readCounters()
	if err != nil {
		return StorageReport{}, err
	}
	rep := StorageReport{MeasuredAt: now()}
	for _, c := range storageAttachmentClasses {
		v := counters[c]
		v.Class, v.Billable = c, true
		rep.FilesBytes += v.Bytes
		rep.Classes = append(rep.Classes, v)
	}
	conv := counters[storageClassConversations]
	rep.Classes = append(rep.Classes, StorageClass{Class: storageClassConversations, Bytes: conv.Bytes, Objects: conv.Objects})
	rep.DatabaseBytes = s.databaseBytes()
	rep.Classes = append(rep.Classes, StorageClass{Class: storageClassDatabase, Bytes: rep.DatabaseBytes, Billable: true})
	rep.ServerBytes = rep.FilesBytes + rep.DatabaseBytes
	rep.PhysicalFilesBytes = counters[storageClassPhysical].Bytes
	if n, e := s.pendingGuestCopies(); e == nil {
		rep.PendingCopyCount = n
	}
	var at, detail string
	if s.db.QueryRow(`SELECT measured_at,detail FROM storage_breakdown WHERE id=1`).Scan(&at, &detail) == nil {
		rep.BreakdownMeasuredAt = at
		_ = json.Unmarshal([]byte(detail), &rep.Detail)
	}
	if rep.Detail.ByBot == nil {
		rep.Detail.ByBot = []StorageItem{}
	}
	if rep.Detail.ByConversation == nil {
		rep.Detail.ByConversation = []StorageItem{}
	}
	return rep, nil
}

// reconcileStorage is the periodic job body: repair the counters, measure what
// is not incremental, store the breakdown, and return the fresh report.
func (s *Store) reconcileStorage(ctx context.Context) (StorageReport, error) {
	drift, err := reconcileAttachmentCounters(s.db)
	if err != nil {
		return StorageReport{}, err
	}
	for _, d := range drift {
		log.Printf("[storage] counter %q was %d bytes / %d files, rows say %d / %d; repaired", d.Class, d.StoredBytes, d.StoredObjs, d.ActualBytes, d.ActualObjs)
	}
	var msgBytes, msgCount int64
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(CAST(content AS BLOB))),0),COUNT(*) FROM messages`).Scan(&msgBytes, &msgCount); err != nil {
		return StorageReport{}, err
	}
	physical := s.physicalAttachmentBytes()
	stamp := now()
	for _, c := range []struct {
		class        string
		bytes, count int64
	}{{storageClassConversations, msgBytes, msgCount}, {storageClassPhysical, physical, 0}} {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO storage_counters(class,bytes,objects,measured_at) VALUES(?,?,?,?)
		 ON CONFLICT(class) DO UPDATE SET bytes=excluded.bytes,objects=excluded.objects,measured_at=excluded.measured_at`, c.class, c.bytes, c.count, stamp); err != nil {
			return StorageReport{}, err
		}
	}
	detail, err := s.measureBreakdown(ctx)
	if err != nil {
		return StorageReport{}, err
	}
	raw, _ := json.Marshal(detail)
	if _, err = s.db.ExecContext(ctx, `INSERT INTO storage_breakdown(id,measured_at,detail) VALUES(1,?,?)
	 ON CONFLICT(id) DO UPDATE SET measured_at=excluded.measured_at,detail=excluded.detail`, stamp, string(raw)); err != nil {
		return StorageReport{}, err
	}
	return s.storageLive()
}

// physicalAttachmentBytes sums the files in the attachments directory (the
// bytes actually on disk; a future content-addressed store would make this
// smaller than the logical sum).
func (s *Store) physicalAttachmentBytes() int64 {
	root, err := s.attachmentRoot()
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

func (s *Store) measureBreakdown(ctx context.Context) (StorageDetail, error) {
	bots := map[string]*StorageItem{}
	convs := map[string]*StorageItem{}
	bump := func(m map[string]*StorageItem, id string, bytes, objs int64) {
		it := m[id]
		if it == nil {
			it = &StorageItem{ID: id}
			m[id] = it
		}
		it.Bytes += bytes
		it.Objects += objs
	}
	scan := func(q string, add func(a, b string, bytes, objs int64)) error {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var conv, bot sql.NullString
			var bytes, objs int64
			if err = rows.Scan(&conv, &bot, &bytes, &objs); err != nil {
				return err
			}
			add(conv.String, bot.String, bytes, objs)
		}
		return rows.Err()
	}
	add := func(conv, bot string, bytes, objs int64) {
		bump(bots, bot, bytes, objs)
		bump(convs, conv, bytes, objs)
	}
	if err := scan(`SELECT conversation_id,bot_id,COALESCE(SUM(size),0),COUNT(*) FROM attachments GROUP BY conversation_id,bot_id`, add); err != nil {
		return StorageDetail{}, err
	}
	// Message text is counted by author and by conversation, without the
	// object count (a message is not a file).
	if err := scan(`SELECT conversation_id,sender_bot_id,COALESCE(SUM(LENGTH(CAST(content AS BLOB))),0),0 FROM messages GROUP BY conversation_id,sender_bot_id`, add); err != nil {
		return StorageDetail{}, err
	}
	names := func(q string) map[string]string {
		out := map[string]string{}
		if rows, err := s.db.QueryContext(ctx, q); err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, name string
				if rows.Scan(&id, &name) == nil {
					out[id] = name
				}
			}
		}
		return out
	}
	botNames, convNames := names(`SELECT id,name FROM bots`), names(`SELECT id,name FROM conversations`)
	flatten := func(m map[string]*StorageItem, names map[string]string, limit int) []StorageItem {
		out := make([]StorageItem, 0, len(m))
		for id, it := range m {
			it.Name = names[id]
			out = append(out, *it)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Bytes != out[j].Bytes {
				return out[i].Bytes > out[j].Bytes
			}
			return out[i].ID < out[j].ID
		})
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	return StorageDetail{ByBot: flatten(bots, botNames, 0), ByConversation: flatten(convs, convNames, storageBreakdownTop)}, nil
}

// --- enforcement -------------------------------------------------------

// storageAdmit decides whether a new file may be written. It returns the number
// of bytes the file may take when a limit caps it (0 = no extra cap).
func (s *Store) storageAdmit(root string) (int64, error) {
	if free, _, err := s.storage.probe(root); err == nil {
		if floor := s.storage.floor(); floor > 0 && free < uint64(floor) {
			return 0, newHostFullError()
		}
	}
	if s.storage.limit == nil {
		return 0, nil
	}
	limit := s.storage.limit()
	if limit == nil {
		return 0, nil
	}
	used, err := s.serverBytes()
	if err != nil {
		return 0, err
	}
	if remaining := *limit - used; remaining > 0 {
		return remaining, nil
	}
	return 0, newQuotaError()
}

// --- background job ----------------------------------------------------

func (s *Server) startStorageReconciler() {
	if s.store == nil {
		return
	}
	rt := &s.storageJob
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.cancel != nil {
		return
	}
	interval := rt.interval
	if interval <= 0 {
		interval = storageReconcileEvery
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.cancel, rt.done = cancel, make(chan struct{})
	go func() {
		defer close(rt.done)
		s.reconcileStorageOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileStorageOnce(ctx)
			}
		}
	}()
}

func (s *Server) stopStorageReconciler() {
	rt := &s.storageJob
	rt.mu.Lock()
	cancel, done := rt.cancel, rt.done
	rt.cancel = nil
	rt.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// reconcileStorageOnce runs one pass and hands the result to the gateway, which
// records the snapshot.
func (s *Server) reconcileStorageOnce(ctx context.Context) {
	rep, err := s.store.reconcileStorage(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[storage] reconcile failed: %v", err)
		}
		return
	}
	if s.storageSnapshot != nil {
		s.storageSnapshot(rep)
	}
}

// --- HTTP: GET /api/storage -------------------------------------------

// ComputerUsage is the computer disk as measured by the host. It is separate
// from, and never added to, the server bytes.
type ComputerUsage struct {
	LogicalBytes   int64 `json:"logical_bytes"`
	AllocatedBytes int64 `json:"allocated_bytes"`
	QuotaBytes     int64 `json:"quota_bytes"`
}

// StorageView is the stable shape of GET /api/storage (version 1). New fields
// may be added; existing ones keep their meaning.
type StorageView struct {
	Version   int    `json:"version"`
	AccountID string `json:"account_id,omitempty"`
	StorageReport
	LimitBytes     *int64         `json:"limit_bytes"`
	RemainingBytes *int64         `json:"remaining_bytes"`
	OverLimit      bool           `json:"over_limit"`
	Computer       *ComputerUsage `json:"computer"`
	ByBot          []StorageItem  `json:"by_bot"`
	ByConversation []StorageItem  `json:"by_conversation"`
}

func newStorageView(accountID string, rep StorageReport, limit *int64, comp *ComputerUsage) StorageView {
	v := StorageView{Version: storageAPIVersion, AccountID: accountID, StorageReport: rep, LimitBytes: limit, Computer: comp, ByBot: rep.Detail.ByBot, ByConversation: rep.Detail.ByConversation}
	if limit != nil {
		rem := *limit - rep.ServerBytes
		v.OverLimit = rem <= 0
		if rem < 0 {
			rem = 0
		}
		v.RemainingBytes = &rem
	}
	return v
}

func (s *Server) storageLimit() *int64 {
	if s.store == nil || s.store.storage.limit == nil {
		return nil
	}
	return s.store.storage.limit()
}

func (s *Server) routeStorage(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "storage" {
		return false
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method_not_allowed", "use GET")
		return true
	}
	rep, err := s.store.storageLive()
	if err != nil {
		writeErr(w, 500, "storage", "cannot read storage usage")
		return true
	}
	var comp *ComputerUsage
	if s.computerUsage != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		comp = s.computerUsage(ctx)
		cancel()
	}
	writeJSON(w, 200, newStorageView(s.accountID, rep, s.storageLimit(), comp))
	return true
}
