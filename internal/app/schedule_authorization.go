package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	scheduleSourceUnknown                = "unknown"
	scheduleSourceChat                   = "native_chat"
	scheduleSourceForm                   = "native_schedule_form"
	maxScheduleAuthorizationRevisions    = 32
	maxScheduleAuthorizationBytes        = 64 << 10
	maxScheduleAuthorizationStorageBytes = 256 << 10
)

// These host-only records never appear on schedule cards or public input types.
// InitialAtUTC is the original timing anchor, not the advancing recurring cursor.
type scheduleExecutionSpec struct {
	AccountID       string `json:"account_id"`
	ConversationID  string `json:"conversation_id"`
	BotID           string `json:"bot_id"`
	Content         string `json:"content"`
	Kind            string `json:"kind"`
	Timezone        string `json:"timezone"`
	InitialAtUTC    string `json:"initial_at_utc"`
	IntervalSeconds int64  `json:"interval_seconds"`
	DailyTime       string `json:"daily_time"`
}

type scheduleAuthorizationRevision struct {
	ScheduleID          string                `json:"schedule_id"`
	Revision            int64                 `json:"revision"`
	PreviousRevision    int64                 `json:"previous_revision"`
	EventKind           string                `json:"event_kind"`
	AccountID           string                `json:"account_id"`
	ConversationID      string                `json:"conversation_id"`
	BotID               string                `json:"bot_id"`
	RequestID           string                `json:"request_id"`
	CreatedAt           string                `json:"created_at"`
	SourceKind          string                `json:"source_kind"`
	SourceRunID         string                `json:"source_run_id,omitempty"`
	SourceMessageID     string                `json:"source_message_id,omitempty"`
	SourceContext       json.RawMessage       `json:"source_context"`
	SourceDigest        string                `json:"source_digest"`
	ExecutionSpec       scheduleExecutionSpec `json:"execution_spec"`
	ExecutionSpecDigest string                `json:"execution_spec_digest"`
}

type scheduleAuthorizationSnapshot struct {
	ScheduleID          string                          `json:"schedule_id"`
	AccountID           string                          `json:"account_id"`
	RootRunID           string                          `json:"root_run_id"`
	TriggerMessageID    string                          `json:"trigger_message_id"`
	ScheduledForUTC     string                          `json:"scheduled_for_utc"`
	Revision            int64                           `json:"revision"`
	ExecutionSpec       scheduleExecutionSpec           `json:"execution_spec"`
	ExecutionSpecDigest string                          `json:"execution_spec_digest"`
	Revisions           []scheduleAuthorizationRevision `json:"revisions"`
}

type scheduleOccurrenceAuthorization struct {
	RootRunID        string
	ScheduleID       string
	AccountID        string
	ScheduledForUTC  string
	TriggerMessageID string
	CapturedAt       string
	Revision         int64
	Snapshot         scheduleAuthorizationSnapshot
	SnapshotDigest   string
}

type scheduleFormAuthorizationSource struct {
	Action          string                `json:"action"`
	SubmittedFields json.RawMessage       `json:"submitted_fields"`
	ExecutionSpec   scheduleExecutionSpec `json:"execution_spec"`
}

// Only host closures construct a source. Public Store callers and JSON fields
// cannot name the account, request, source run, or original intent.
type scheduleMutationSource struct {
	accountID, requestID, kind, action string
	fields                             json.RawMessage
	conversation                       Conversation
	run                                Run
}

func (s *Server) scheduleFormSource(action string, fields any) *scheduleMutationSource {
	x := &scheduleMutationSource{accountID: s.reviewAccountID(), requestID: uuid.NewString(), kind: scheduleSourceUnknown, action: action}
	b, err := json.Marshal(fields)
	if err != nil || len(b) > maxScheduleAuthorizationBytes || !utf8.Valid(b) {
		return x
	}
	x.kind, x.fields = scheduleSourceForm, b
	return x
}

func (s *Server) scheduleChatSource(c Conversation, r Run) *scheduleMutationSource {
	return &scheduleMutationSource{accountID: s.reviewAccountID(), requestID: uuid.NewString(), kind: scheduleSourceChat, conversation: c, run: r}
}

func migrateScheduleAuthorization(db *sql.DB) error {
	if err := ensureColumn(db, "schedules", "authorization_revision", `ALTER TABLE schedules ADD COLUMN authorization_revision INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schedule_authorization_revisions (
	 schedule_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0), previous_revision INTEGER NOT NULL,
	 event_kind TEXT NOT NULL CHECK(event_kind IN ('create','content_edit','pause','resume','delete','archive')),
	 account_id TEXT NOT NULL, conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL,
	 request_id TEXT NOT NULL, created_at TEXT NOT NULL,
	 source_kind TEXT NOT NULL CHECK(source_kind IN ('native_chat','native_schedule_form','unknown')),
	 source_run_id TEXT NOT NULL, source_message_id TEXT NOT NULL, source_context_json TEXT NOT NULL, source_digest TEXT NOT NULL,
	 execution_spec_json TEXT NOT NULL, execution_spec_digest TEXT NOT NULL,
	 PRIMARY KEY(schedule_id,revision), FOREIGN KEY(schedule_id) REFERENCES schedules(id) ON DELETE CASCADE);
	CREATE TABLE IF NOT EXISTS schedule_occurrence_authorizations (
	 root_run_id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, revision INTEGER NOT NULL, account_id TEXT NOT NULL,
	 scheduled_for_utc TEXT NOT NULL, trigger_message_id TEXT NOT NULL, snapshot_json TEXT NOT NULL,
	 snapshot_digest TEXT NOT NULL, captured_at TEXT NOT NULL,
	 FOREIGN KEY(root_run_id) REFERENCES runs(id) ON DELETE CASCADE,
	 FOREIGN KEY(schedule_id) REFERENCES schedules(id) ON DELETE CASCADE);
	CREATE TRIGGER IF NOT EXISTS schedule_authorization_revisions_immutable BEFORE UPDATE ON schedule_authorization_revisions
	 BEGIN SELECT RAISE(ABORT,'immutable schedule authorization revision'); END;
	CREATE TRIGGER IF NOT EXISTS schedule_occurrence_authorizations_immutable BEFORE UPDATE ON schedule_occurrence_authorizations
	 BEGIN SELECT RAISE(ABORT,'immutable schedule occurrence authorization'); END;`)
	return err
}

func scheduleExecutionScope(x Schedule, account, anchor string) scheduleExecutionSpec {
	return scheduleExecutionSpec{AccountID: account, ConversationID: x.ConversationID, BotID: x.BotID, Content: x.Content, Kind: x.Kind, Timezone: x.Timezone, InitialAtUTC: anchor, IntervalSeconds: x.IntervalSeconds, DailyTime: x.DailyTime}
}

func scheduleExecutionSpecDigest(spec scheduleExecutionSpec) string {
	b, _ := json.Marshal(spec)
	return digestBytes(b)
}

func scheduleImported(db reviewQuerier, kind, id string) (bool, error) {
	var imported bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM portability_provenance WHERE kind=? AND target_id=?)`, kind, id).Scan(&imported)
	return imported, err
}

func readScheduleAuthorizationRevision(db reviewQuerier, id string, revision int64) (scheduleAuthorizationRevision, error) {
	var x scheduleAuthorizationRevision
	var source, spec []byte
	err := db.QueryRow(`SELECT schedule_id,revision,previous_revision,event_kind,account_id,conversation_id,bot_id,request_id,created_at,source_kind,source_run_id,source_message_id,source_context_json,source_digest,execution_spec_json,execution_spec_digest FROM schedule_authorization_revisions WHERE schedule_id=? AND revision=?`, id, revision).Scan(&x.ScheduleID, &x.Revision, &x.PreviousRevision, &x.EventKind, &x.AccountID, &x.ConversationID, &x.BotID, &x.RequestID, &x.CreatedAt, &x.SourceKind, &x.SourceRunID, &x.SourceMessageID, &source, &x.SourceDigest, &spec, &x.ExecutionSpecDigest)
	if err != nil {
		return x, err
	}
	if len(source)+len(spec) > maxScheduleAuthorizationStorageBytes || !utf8.Valid(source) || !utf8.Valid(spec) || !json.Valid(source) || json.Unmarshal(spec, &x.ExecutionSpec) != nil || digestBytes(source) != x.SourceDigest || digestBytes(spec) != x.ExecutionSpecDigest || scheduleExecutionSpecDigest(x.ExecutionSpec) != x.ExecutionSpecDigest || x.ScheduleID != id || x.Revision != revision || x.ExecutionSpec.AccountID != x.AccountID || x.ExecutionSpec.ConversationID != x.ConversationID || x.ExecutionSpec.BotID != x.BotID {
		return x, errors.New("schedule authorization revision binding changed")
	}
	x.SourceContext = append(json.RawMessage(nil), source...)
	return x, nil
}

// Rebuild current stable scope using the journal anchor. Callers separately
// require active lifecycle, exact revision, full source chain and host proofs.
func readBoundScheduleAuthorization(db reviewQuerier, id, account string) (Schedule, int64, scheduleExecutionSpec, error) {
	x, err := scanSchedule(db.QueryRow(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE id=?`, id))
	var revision int64
	if err != nil {
		return x, 0, scheduleExecutionSpec{}, err
	}
	if err = db.QueryRow(`SELECT authorization_revision FROM schedules WHERE id=?`, id).Scan(&revision); err != nil {
		return x, 0, scheduleExecutionSpec{}, err
	}
	r, err := readScheduleAuthorizationRevision(db, id, revision)
	if err != nil {
		return x, revision, scheduleExecutionSpec{}, err
	}
	if r.AccountID != account {
		return x, revision, scheduleExecutionSpec{}, errors.New("schedule authorization account changed")
	}
	imported, err := scheduleImported(db, "schedule", id)
	if err != nil || imported {
		return x, revision, scheduleExecutionSpec{}, errors.New("schedule authorization import boundary")
	}
	spec := scheduleExecutionScope(x, account, r.ExecutionSpec.InitialAtUTC)
	if scheduleExecutionSpecDigest(spec) != r.ExecutionSpecDigest {
		return x, revision, spec, errors.New("schedule authorization scope changed")
	}
	return x, revision, spec, nil
}

func readScheduleOccurrenceAuthorization(db reviewQuerier, root string) (scheduleOccurrenceAuthorization, error) {
	var x scheduleOccurrenceAuthorization
	var raw []byte
	err := db.QueryRow(`SELECT root_run_id,schedule_id,revision,account_id,scheduled_for_utc,trigger_message_id,snapshot_json,snapshot_digest,captured_at FROM schedule_occurrence_authorizations WHERE root_run_id=?`, root).Scan(&x.RootRunID, &x.ScheduleID, &x.Revision, &x.AccountID, &x.ScheduledForUTC, &x.TriggerMessageID, &raw, &x.SnapshotDigest, &x.CapturedAt)
	if err != nil {
		return x, err
	}
	if len(raw) > maxScheduleAuthorizationBytes || !utf8.Valid(raw) || digestBytes(raw) != x.SnapshotDigest || json.Unmarshal(raw, &x.Snapshot) != nil || x.RootRunID != root || x.Snapshot.RootRunID != root || x.Snapshot.ScheduleID != x.ScheduleID || x.Snapshot.Revision != x.Revision || x.Snapshot.AccountID != x.AccountID || x.Snapshot.TriggerMessageID != x.TriggerMessageID || x.Snapshot.ScheduledForUTC != x.ScheduledForUTC || scheduleExecutionSpecDigest(x.Snapshot.ExecutionSpec) != x.Snapshot.ExecutionSpecDigest {
		return x, errors.New("schedule occurrence authorization binding changed")
	}
	return x, nil
}

func captureScheduleSource(db reviewQuerier, source *scheduleMutationSource, spec scheduleExecutionSpec, event string) (kind, run, message string, raw json.RawMessage) {
	kind, raw = scheduleSourceUnknown, json.RawMessage(`null`)
	if source == nil || source.accountID == "" || source.requestID == "" || source.accountID != spec.AccountID {
		return
	}
	var value any
	switch source.kind {
	case scheduleSourceForm:
		if source.action != event || !json.Valid(source.fields) {
			return
		}
		value = scheduleFormAuthorizationSource{Action: source.action, SubmittedFields: source.fields, ExecutionSpec: spec}
	case scheduleSourceChat:
		c, r := source.conversation, source.run
		if c.ID != spec.ConversationID || r.ConversationID != c.ID || r.BotID == "" || r.ParentRunID != "" || r.Kind != "" && r.Kind != "chat" && (r.Kind != runKindGroupChat || c.Kind != "group") {
			return
		}
		var active bool
		if db.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs r JOIN conversations c ON c.id=r.conversation_id JOIN bots b ON b.id=r.bot_id JOIN members m ON m.conversation_id=c.id AND m.bot_id=b.id WHERE r.id=? AND r.conversation_id=? AND r.bot_id=? AND r.status IN ('queued','running') AND c.archived=0 AND b.archived=0 AND c.user_visible=1 AND c.kind IN ('dm','group'))`, r.ID, c.ID, r.BotID).Scan(&active) != nil || !active {
			return
		}
		if imported, err := scheduleImported(db, "run", r.ID); err != nil || imported {
			return
		}
		x, _, err := readScheduleChatSourceContext(db, c, r)
		if err != nil {
			return
		}
		x.SourceRunBinding = mcpScheduleBinding(r)
		value, run, message = x, r.ID, r.TriggerMessageID
	default:
		return
	}
	b, err := json.Marshal(value)
	// Reserve room for the execution scope and revision metadata. Oversized
	// source evidence keeps ordinary scheduling available with unknown lineage.
	specBytes, _ := json.Marshal(spec)
	if err != nil || len(b)+len(specBytes)+2048 > maxScheduleAuthorizationBytes || !utf8.Valid(b) {
		run, message = "", ""
		return
	}
	return source.kind, run, message, b
}

// The journal insertion and token update belong to the caller's existing tx.
func appendScheduleAuthorizationTx(tx *sql.Tx, x Schedule, event, at string, source *scheduleMutationSource) error {
	var previous int64
	if err := tx.QueryRow(`SELECT authorization_revision FROM schedules WHERE id=?`, x.ID).Scan(&previous); err != nil {
		return err
	}
	account, anchor := "", x.NextAtUTC
	if previous > 0 {
		r, err := readScheduleAuthorizationRevision(tx, x.ID, previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			account, anchor = r.AccountID, r.ExecutionSpec.InitialAtUTC
		}
	}
	if account == "" && source != nil {
		account = source.accountID
	}
	if previous == int64(^uint64(0)>>1) {
		return errors.New("schedule authorization revision exhausted")
	}
	spec := scheduleExecutionScope(x, account, anchor)
	kind, run, message, raw := captureScheduleSource(tx, source, spec, event)
	request := uuid.NewString()
	if source != nil && source.requestID != "" {
		request = source.requestID
	}
	specRaw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schedule_authorization_revisions(schedule_id,revision,previous_revision,event_kind,account_id,conversation_id,bot_id,request_id,created_at,source_kind,source_run_id,source_message_id,source_context_json,source_digest,execution_spec_json,execution_spec_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.ID, previous+1, previous, event, account, x.ConversationID, x.BotID, request, at, kind, run, message, string(raw), digestBytes(raw), string(specRaw), digestBytes(specRaw)); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE schedules SET authorization_revision=? WHERE id=? AND authorization_revision=?`, previous+1, x.ID, previous)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return errors.New("schedule authorization revision changed")
	}
	return nil
}

func snapshotScheduleAuthorizationTx(tx *sql.Tx, x Schedule, r Run, scheduledFor, at string) error {
	var revision int64
	if err := tx.QueryRow(`SELECT authorization_revision FROM schedules WHERE id=?`, x.ID).Scan(&revision); err != nil {
		return err
	}
	var chain []scheduleAuthorizationRevision
	account, anchor := "", x.NextAtUTC
	// Even an overlimit or incomplete chain retains the stable anchor/account;
	// missing source history only removes automatic eligibility.
	if revision > 0 {
		latest, err := readScheduleAuthorizationRevision(tx, x.ID, revision)
		if err == nil {
			account, anchor = latest.AccountID, latest.ExecutionSpec.InitialAtUTC
		}
	}
	if revision > 0 && revision <= maxScheduleAuthorizationRevisions {
		for n := int64(1); n <= revision; n++ {
			entry, err := readScheduleAuthorizationRevision(tx, x.ID, n)
			if err != nil || entry.PreviousRevision != n-1 {
				chain = nil
				break
			}
			chain = append(chain, entry)
		}
	}
	spec := scheduleExecutionScope(x, account, anchor)
	snapshot := scheduleAuthorizationSnapshot{ScheduleID: x.ID, AccountID: account, RootRunID: r.ID, TriggerMessageID: r.TriggerMessageID, ScheduledForUTC: scheduledFor, Revision: revision, ExecutionSpec: spec, ExecutionSpecDigest: scheduleExecutionSpecDigest(spec), Revisions: chain}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(b) > maxScheduleAuthorizationBytes {
		// Do not truncate a source chain into apparent authority. Its absence is
		// explicitly ineligible; the original instruction/spec remains durable.
		snapshot.Revisions = nil
		b, err = json.Marshal(snapshot)
		if err != nil {
			return err
		}
	}
	if len(b) > maxScheduleAuthorizationStorageBytes {
		return errors.New("schedule occurrence authorization exceeds storage limit")
	}
	_, err = tx.Exec(`INSERT INTO schedule_occurrence_authorizations(root_run_id,schedule_id,revision,account_id,scheduled_for_utc,trigger_message_id,snapshot_json,snapshot_digest,captured_at) VALUES(?,?,?,?,?,?,?,?,?)`, r.ID, x.ID, revision, account, scheduledFor, r.TriggerMessageID, string(b), digestBytes(b), at)
	return err
}

func sameScheduleAuthorizationRevision(a, b scheduleAuthorizationRevision) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && bytes.Equal(x, y)
}

// Native schedule tools accept direct group roots as well as direct chats.
// This private reader keeps the ordinary AutoReview chat eligibility unchanged
// and captures the same bounded window: the request, text up to it, earlier
// attachment metadata and the source run's own records. Long conversations
// keep their recorded source instead of degrading to unknown lineage.
func readScheduleChatSourceContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, string, error) {
	var x mcpReviewContext
	var trigger, parent, kind sql.NullString
	if err := db.QueryRow(`SELECT trigger_message_id,parent_run_id,kind FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, r.ID, c.ID, r.BotID).Scan(&trigger, &parent, &kind); err != nil || trigger.String != r.TriggerMessageID || parent.String != r.ParentRunID || kind.String != r.Kind {
		return x, "", errors.New("run context binding changed")
	}
	if r.TriggerMessageID == "" || r.ParentRunID != "" || r.Kind != "" && r.Kind != "chat" && (r.Kind != runKindGroupChat || c.Kind != "group") {
		return x, "", errors.New("complete user context unavailable")
	}
	var conversationKind, origin string
	if err := db.QueryRow(`SELECT c.kind,COALESCE(r.origin_conversation_id,'') FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE r.id=?`, r.ID).Scan(&conversationKind, &origin); err != nil || conversationKind != c.Kind || origin != r.OriginConversationID {
		return x, "", errors.New("native schedule source binding changed")
	}
	var role, conv, intentSource string
	var intentSeq int64
	var intentKind, sender sql.NullString
	if err := db.QueryRow(`SELECT m.content,m.role,m.conversation_id,m.kind,m.sender_bot_id,m.seq,`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.id=?`, r.TriggerMessageID).Scan(&x.Intent, &role, &conv, &intentKind, &sender, &intentSeq, &intentSource); err != nil || role != "user" || conv != c.ID || sender.String != "" || intentKind.String != "" && intentKind.String != "user_message" || strings.TrimSpace(x.Intent) == "" {
		return x, "", errors.New("user intent unavailable")
	}
	if intentSource != mcpHostUserIngress {
		return x, "", errors.New("host-verified user intent provenance unavailable")
	}
	// encoding/json replaces invalid UTF-8 in strings. Validate original source
	// text first so that replacement bytes cannot become captured user intent.
	if !utf8.ValidString(x.Intent) {
		return x, "", errors.New("complete source text is not valid UTF-8")
	}
	x.IntentMessageID = r.TriggerMessageID
	if err := db.QueryRow(`SELECT instructions FROM bots WHERE id=?`, r.BotID).Scan(&x.Instructions); err != nil {
		return x, "", err
	}
	var err error
	// The request itself must be text; earlier uploads are an explicit manifest.
	if x.AttachmentBoundary, err = readMCPAttachmentBoundary(db, c.ID, r.TriggerMessageID, intentSeq); err != nil {
		return x, "", err
	}
	bounds := &mcpEvidenceBounds{}
	boundMCPIntent(&x, bounds)
	if x.Messages, x.MessageProvenance, err = readMCPMessageWindow(db, c.ID, intentSeq, false, bounds); err != nil {
		return x, "", err
	}
	// Persist the bounded text of the source run's prior calls and the current
	// pending schedule mutation. Tool output supplies context, never consent.
	if x.SourceToolResults, err = readMCPRunToolEvidence(db, r, false, bounds); err != nil {
		return x, "", err
	}
	x.Bounds = bounds.orNil()
	digest, err := fitMCPReviewContext(&x, mcpScheduleSourceBudget)
	if err != nil {
		return x, "", err
	}
	return x, digest, nil
}

// Leaves room for the execution scope and revision metadata in the 64 KiB
// per-revision authorization record.
const mcpScheduleSourceBudget = 40 << 10

// Preserve the exact submitted form JSON after the existing request boundary.
// A bounded tee does not change the historical one-megabyte decoding behavior.
func decodeScheduleForm(r *http.Request, value any) (json.RawMessage, error) {
	var captured bytes.Buffer
	err := json.NewDecoder(io.TeeReader(io.LimitReader(r.Body, 1<<20), &captured)).Decode(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(captured.Bytes()))
	var raw json.RawMessage
	if decoder.Decode(&raw) != nil {
		return nil, errors.New("invalid schedule form JSON")
	}
	return raw, nil
}
