package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
)

const (
	scheduleOnce        = "once"
	scheduleInterval    = "interval"
	scheduleDaily       = "daily"
	scheduleActive      = "active"
	schedulePaused      = "paused"
	scheduleComplete    = "completed"
	scheduleDeleted     = "deleted"
	minScheduleInterval = time.Minute
	maxScheduleInterval = 365 * 24 * time.Hour
	maxScheduleContent  = 32 * 1024
	scheduleTimeLayout  = "2006-01-02T15:04:05.000000000Z07:00"
	maxOccurrenceRoots  = 50
	maxOccurrenceQuery  = 8 * 1024
	maxOccurrenceError  = 2400 // Unicode code points, including a truncation marker.
)

// ScheduleSpec is the input shared by the HTTP and builtin-tool surfaces.
// All persisted instants are UTC; DailyTime is interpreted in Timezone.
type ScheduleSpec struct {
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	CreatedBy       string `json:"-"`
	Content         string `json:"content"`
	Kind            string `json:"kind"`
	RunAt           string `json:"run_at,omitempty"`
	IntervalSeconds int64  `json:"interval_seconds,omitempty"`
	DailyTime       string `json:"daily_time,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
}

type Schedule struct {
	ID                   string `json:"id"`
	Title                string `json:"title,omitempty"`
	Description          string `json:"description,omitempty"`
	CreatedBy            string `json:"created_by,omitempty"`
	ConversationID       string `json:"conversation_id"`
	BotID                string `json:"bot_id"`
	ConversationName     string `json:"conversation_name,omitempty"`
	ConversationKind     string `json:"conversation_kind,omitempty"`
	ConversationArchived bool   `json:"conversation_archived,omitempty"`
	BotName              string `json:"bot_name,omitempty"`
	BotArchived          bool   `json:"bot_archived,omitempty"`
	Content              string `json:"content"`
	Kind                 string `json:"kind"`
	Timezone             string `json:"timezone"`
	NextAtUTC            string `json:"next_at_utc"`
	IntervalSeconds      int64  `json:"interval_seconds,omitempty"`
	DailyTime            string `json:"daily_time,omitempty"`
	Status               string `json:"status"`
	// Status is the schedule lifecycle; ExecutionStatus aggregates the latest occurrence.
	LastRunID       string `json:"last_run_id,omitempty"`
	ExecutionStatus string `json:"execution_status,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// ScheduleOccurrence reports one exact occurrence, including delegated runs
// in other conversations. Only the selected failure's bounded public run error
// is included; task content, transcripts and other attempts remain excluded.
type ScheduleOccurrence struct {
	RootRunID            string `json:"root_run_id"`
	ScheduleID           string `json:"schedule_id"`
	ScheduledForUTC      string `json:"scheduled_for_utc"`
	Title                string `json:"title,omitempty"`
	Description          string `json:"description,omitempty"`
	CreatedBy            string `json:"created_by,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	Timezone             string `json:"timezone,omitempty"`
	IntervalSeconds      int64  `json:"interval_seconds,omitempty"`
	DailyTime            string `json:"daily_time,omitempty"`
	OccurrenceNumber     int64  `json:"occurrence_number,omitempty"`
	ExecutionStatus      string `json:"execution_status"`
	StatusRunID          string `json:"status_run_id"`
	StatusError          string `json:"status_error,omitempty"`
	ResultInConversation bool   `json:"result_in_conversation"`
}

type scheduleFamilyRun struct {
	id, parent, bot, kind, trigger, created, status string
	statusError                                     string
	resultInConversation                            bool
}

// migrateSchedules is called by Store.migrate during app startup. Keeping the
// DDL here makes the feature removable without changing the run table shape.
func migrateSchedules(db *sql.DB) error {
	if db == nil {
		return errors.New("schedule database is required")
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schedules (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL,
	bot_id TEXT NOT NULL,
	content TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL CHECK(kind IN ('once','interval','daily')),
	timezone TEXT NOT NULL,
	next_at_utc TEXT NOT NULL,
	interval_seconds INTEGER NOT NULL DEFAULT 0,
	daily_time TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused','completed','deleted')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
	FOREIGN KEY(bot_id) REFERENCES bots(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS schedules_due ON schedules(status,next_at_utc);
CREATE INDEX IF NOT EXISTS schedules_conversation ON schedules(conversation_id,status);
CREATE TABLE IF NOT EXISTS schedule_occurrences (
	schedule_id TEXT NOT NULL,
	scheduled_for_utc TEXT NOT NULL,
	run_id TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT '',
	timezone TEXT NOT NULL DEFAULT '',
	interval_seconds INTEGER NOT NULL DEFAULT 0,
	daily_time TEXT NOT NULL DEFAULT '',
	occurrence_number INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	PRIMARY KEY(schedule_id,scheduled_for_utc),
	FOREIGN KEY(schedule_id) REFERENCES schedules(id) ON DELETE CASCADE,
	FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS schedule_occurrences_run ON schedule_occurrences(run_id);
`)
	if err != nil {
		return err
	}
	for _, column := range []struct{ table, name, ddl string }{
		{"schedules", "description", `ALTER TABLE schedules ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "description", `ALTER TABLE schedule_occurrences ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
		{"schedules", "title", `ALTER TABLE schedules ADD COLUMN title TEXT NOT NULL DEFAULT ''`},
		{"schedules", "created_by", `ALTER TABLE schedules ADD COLUMN created_by TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "title", `ALTER TABLE schedule_occurrences ADD COLUMN title TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "created_by", `ALTER TABLE schedule_occurrences ADD COLUMN created_by TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "kind", `ALTER TABLE schedule_occurrences ADD COLUMN kind TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "timezone", `ALTER TABLE schedule_occurrences ADD COLUMN timezone TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "interval_seconds", `ALTER TABLE schedule_occurrences ADD COLUMN interval_seconds INTEGER NOT NULL DEFAULT 0`},
		{"schedule_occurrences", "daily_time", `ALTER TABLE schedule_occurrences ADD COLUMN daily_time TEXT NOT NULL DEFAULT ''`},
		{"schedule_occurrences", "occurrence_number", `ALTER TABLE schedule_occurrences ADD COLUMN occurrence_number INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureSchedules() error {
	s.scheduleSchemaOnce.Do(func() { s.scheduleSchemaErr = migrateSchedules(s.db) })
	return s.scheduleSchemaErr
}

func scanSchedule(r interface{ Scan(...any) error }) (Schedule, error) {
	var x Schedule
	err := r.Scan(&x.ID, &x.ConversationID, &x.BotID, &x.Content, &x.Title, &x.Description, &x.CreatedBy, &x.Kind,
		&x.Timezone, &x.NextAtUTC, &x.IntervalSeconds, &x.DailyTime,
		&x.Status, &x.CreatedAt, &x.UpdatedAt)
	return x, err
}

func (s *Store) validateScheduleTarget(conversationID, botID string) (Conversation, string, error) {
	c, err := s.GetConversation(conversationID)
	if err != nil {
		return Conversation{}, "", fmt.Errorf("conversation not found: %w", err)
	}
	if c.Archived {
		return Conversation{}, "", ErrArchiveBlocked
	}
	if botID == "" && c.Kind == "dm" {
		botID = c.BotID
	}
	if botID == "" {
		return Conversation{}, "", errors.New("bot_id is required")
	}
	if c.Kind == "dm" && botID != c.BotID {
		return Conversation{}, "", errors.New("bot is not the DM recipient")
	}
	ok, err := s.IsMember(c.ID, botID)
	if err != nil || !ok {
		return Conversation{}, "", errors.New("bot is not a conversation member")
	}
	b, err := s.GetBot(botID)
	if err != nil {
		return Conversation{}, "", errors.New("scheduled bot not found")
	}
	if b.Archived {
		return Conversation{}, "", ErrArchiveBlocked
	}
	return c, botID, nil
}

func parseClock(raw string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(raw))
	if err != nil {
		return 0, 0, fmt.Errorf("daily_time must be HH:MM")
	}
	return t.Hour(), t.Minute(), nil
}

func parseRunAt(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("run_at is required")
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			// Go normalizes a nonexistent wall clock through a DST gap. Do not
			// silently move a one-time request; daily schedules have an explicit
			// skip policy in dailyCandidates instead.
			if t.Format(layout) != raw {
				return time.Time{}, errors.New("run_at local time does not exist in timezone")
			}
			// ParseInLocation does not specify which side of a DST fold it
			// selects. Match the daily policy explicitly: the earlier instant.
			candidates := dailyCandidates(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), loc)
			if len(candidates) == 0 {
				return time.Time{}, errors.New("run_at local time does not exist in timezone")
			}
			return candidates[0].Add(time.Duration(t.Second()) * time.Second), nil
		}
	}
	return time.Time{}, errors.New("run_at must be RFC3339 or YYYY-MM-DD HH:MM")
}

func validateScheduleSpec(spec ScheduleSpec, now time.Time) (ScheduleSpec, time.Time, error) {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Content = strings.TrimSpace(spec.Content)
	spec.RunAt = strings.TrimSpace(spec.RunAt)
	spec.DailyTime = strings.TrimSpace(spec.DailyTime)
	if spec.Content == "" || len([]rune(spec.Content)) > maxScheduleContent {
		return spec, time.Time{}, fmt.Errorf("content is required and must be at most %d runes", maxScheduleContent)
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return spec, time.Time{}, fmt.Errorf("unknown timezone %q", spec.Timezone)
	}
	now = now.UTC()
	switch spec.Kind {
	case scheduleOnce, scheduleInterval:
		if spec.DailyTime != "" {
			return spec, time.Time{}, errors.New("daily_time is only valid for daily schedules")
		}
		at, e := parseRunAt(spec.RunAt, loc)
		if e != nil {
			return spec, time.Time{}, e
		}
		if !at.After(now) {
			return spec, time.Time{}, errors.New("run_at must be in the future")
		}
		if spec.Kind == scheduleInterval {
			if spec.IntervalSeconds < int64(minScheduleInterval/time.Second) {
				return spec, time.Time{}, fmt.Errorf("interval_seconds must be at least %d", int64(minScheduleInterval/time.Second))
			}
			if spec.IntervalSeconds > int64(maxScheduleInterval/time.Second) {
				return spec, time.Time{}, fmt.Errorf("interval_seconds must be at most %d", int64(maxScheduleInterval/time.Second))
			}
		} else if spec.IntervalSeconds != 0 {
			return spec, time.Time{}, errors.New("interval_seconds is only valid for interval schedules")
		}
		return spec, at.UTC(), nil
	case scheduleDaily:
		if spec.RunAt != "" || spec.IntervalSeconds != 0 {
			return spec, time.Time{}, errors.New("run_at and interval_seconds are not valid for daily schedules")
		}
		if _, _, e := parseClock(spec.DailyTime); e != nil {
			return spec, time.Time{}, e
		}
		at, e := nextDaily(now, loc, spec.DailyTime)
		if e != nil {
			return spec, time.Time{}, e
		}
		return spec, at.UTC(), nil
	default:
		return spec, time.Time{}, errors.New("kind must be once, interval, or daily")
	}
}

// nextDaily interprets daily_time as a wall clock. A nonexistent DST clock
// time is skipped. Ambiguous wall times are resolved by choosing the earliest
// matching instant; advancing from an occurrence skips the repeated minute.
func nextDaily(after time.Time, loc *time.Location, clock string) (time.Time, error) {
	hour, minute, err := parseClock(clock)
	if err != nil {
		return time.Time{}, err
	}
	local := after.In(loc)
	for day := 0; day < 370; day++ {
		date := time.Date(local.Year(), local.Month(), local.Day()+day, 0, 0, 0, 0, loc)
		candidates := dailyCandidates(date.Year(), date.Month(), date.Day(), hour, minute, loc)
		// The earliest instant owns this date even when it has already passed.
		// Selecting the later side of a fold would violate the once-per-day policy.
		if len(candidates) > 0 && candidates[0].After(after) {
			return candidates[0], nil
		}
	}
	return time.Time{}, errors.New("could not find next daily occurrence")
}

func dailyCandidates(year int, month time.Month, day, hour, minute int, loc *time.Location) []time.Time {
	nominal := time.Date(year, month, day, hour, minute, 0, 0, loc)
	local := nominal.In(loc)
	if local.Year() != year || local.Month() != month || local.Day() != day || local.Hour() != hour || local.Minute() != minute {
		return nil
	}
	seen := map[int64]bool{}
	var out []time.Time
	for delta := -3 * time.Hour; delta <= 3*time.Hour; delta += time.Minute {
		candidate := nominal.Add(delta)
		inLoc := candidate.In(loc)
		if inLoc.Year() == year && inLoc.Month() == month && inLoc.Day() == day && inLoc.Hour() == hour && inLoc.Minute() == minute && !seen[candidate.UnixNano()] {
			seen[candidate.UnixNano()] = true
			out = append(out, candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func nextDailyAfterOccurrence(occurrence time.Time, loc *time.Location, clock string) (time.Time, error) {
	local := occurrence.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	return nextDaily(start.Add(-time.Nanosecond), loc, clock)
}

func scheduleTime(t time.Time) string { return t.UTC().Format(scheduleTimeLayout) }

func (s *Store) CreateSchedule(conversationID, botID string, spec ScheduleSpec) (Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return Schedule{}, err
	}
	if strings.TrimSpace(spec.Timezone) == "" {
		zone, err := s.userTimezone()
		if err != nil {
			return Schedule{}, err
		}
		if zone == "" {
			return Schedule{}, errors.New("timezone is required; configure an IANA timezone in preferences")
		}
		spec.Timezone = zone
	}
	c, botID, err := s.validateScheduleTarget(conversationID, botID)
	if err != nil {
		return Schedule{}, err
	}
	spec, next, err := validateScheduleSpec(spec, time.Now().UTC())
	if err != nil {
		return Schedule{}, err
	}
	spec.Title, spec.Description, err = normalizeDisplayMetadata(spec.Title, spec.Description, false)
	if err != nil {
		return Schedule{}, err
	}
	if spec.CreatedBy != "" && spec.CreatedBy != "user" && spec.CreatedBy != "bot" {
		return Schedule{}, errors.New("invalid schedule creator")
	}
	t := now()
	x := Schedule{ID: uuid.NewString(), ConversationID: c.ID, BotID: botID, Content: spec.Content, Title: spec.Title, Description: spec.Description, CreatedBy: spec.CreatedBy, Kind: spec.Kind, Timezone: spec.Timezone, NextAtUTC: scheduleTime(next), IntervalSeconds: spec.IntervalSeconds, DailyTime: spec.DailyTime, Status: scheduleActive, CreatedAt: t, UpdatedAt: t}
	tx, err := s.db.Begin()
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback()
	if err = requireConversationActiveTx(tx, c.ID); err != nil {
		return Schedule{}, err
	}
	if err = requireActiveMemberTx(tx, c.ID, botID); err != nil {
		return Schedule{}, err
	}
	if _, err = tx.Exec(`INSERT INTO schedules(id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.ConversationID, x.BotID, x.Content, x.Title, x.Description, x.CreatedBy, x.Kind, x.Timezone, x.NextAtUTC, x.IntervalSeconds, x.DailyTime, x.Status, x.CreatedAt, x.UpdatedAt); err != nil {
		return Schedule{}, err
	}
	if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, t); err != nil {
		return Schedule{}, err
	}
	if err = tx.Commit(); err != nil {
		return Schedule{}, err
	}
	return x, nil
}

func insertScheduleEvent(tx *sql.Tx, conv, typ string, value any, created string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conv, typ, string(b), created); err != nil {
		return err
	}
	return insertWorkspaceEventTx(tx, workspaceScopeGroups, created)
}

func (s *Store) GetSchedule(id string) (Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return Schedule{}, err
	}
	x, err := scanSchedule(s.db.QueryRow(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE id=?`, id))
	if err != nil {
		return Schedule{}, err
	}
	return s.decorateSchedule(x)
}

func (s *Store) ListSchedules(conversationID string) ([]Schedule, error) {
	return s.listSchedules(`conversation_id=?`, []any{conversationID}, nil)
}

// ListScheduleAgenda returns either upcoming or historical schedules for a
// conversation and bot. A false history value selects the active agenda.
func (s *Store) ListScheduleAgenda(conversationID, botID string, history bool) ([]Schedule, error) {
	where := `conversation_id=?`
	args := []any{conversationID}
	if botID != "" {
		where += ` AND bot_id=?`
		args = append(args, botID)
	}
	return s.listSchedules(where, args, &history)
}

// ListBotSchedules returns schedules across all conversations owned by a bot.
func (s *Store) ListBotSchedules(botID string, history bool) ([]Schedule, error) {
	return s.listSchedules(`bot_id=?`, []any{botID}, &history)
}

func (s *Store) listSchedules(where string, args []any, history *bool) ([]Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE `+where+` AND status<>? ORDER BY next_at_utc,id`, append(args, scheduleDeleted)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Schedule, 0)
	for rows.Next() {
		x, e := scanSchedule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	filtered := make([]Schedule, 0, len(out))
	for i := range out {
		out[i], err = s.decorateSchedule(out[i])
		if err != nil {
			return nil, err
		}
		if history == nil || *history == scheduleHistory(out[i]) {
			filtered = append(filtered, out[i])
		}
	}
	return filtered, nil
}

func (s *Store) decorateSchedule(x Schedule) (Schedule, error) {
	var conversationArchived, botArchived int
	if err := s.db.QueryRow(`SELECT c.name,c.kind,c.archived,b.name,b.archived FROM conversations c JOIN bots b ON b.id=? WHERE c.id=?`, x.BotID, x.ConversationID).Scan(&x.ConversationName, &x.ConversationKind, &conversationArchived, &x.BotName, &botArchived); err != nil {
		return Schedule{}, err
	}
	x.ConversationArchived, x.BotArchived = conversationArchived != 0, botArchived != 0
	return s.withScheduleExecution(x)
}

// Resolve the immutable occurrence trigger, not the mutable schedule content.
// UNION bounds corrupt ancestry cycles and also follows cross-conversation
// delegations, returns and retries without changing their execution kind.
func (s *Store) scheduleTaskForRun(r Run) (*Message, error) {
	if r.ParentRunID == "" || r.Kind == runKindSchedule {
		return nil, nil
	}
	var trigger string
	err := s.db.QueryRow(`WITH RECURSIVE ancestors(id,parent_run_id) AS (
		SELECT id,parent_run_id FROM runs WHERE id=?
		UNION
		SELECT parent.id,parent.parent_run_id FROM runs parent JOIN ancestors child ON parent.id=child.parent_run_id
	)
	SELECT root.trigger_message_id FROM ancestors a
	JOIN schedule_occurrences o ON o.run_id=a.id
	JOIN runs root ON root.id=o.run_id AND root.kind=?`, r.ID, runKindSchedule).Scan(&trigger)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := s.GetMessage(trigger)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func scheduleHistory(x Schedule) bool {
	if x.Kind != scheduleOnce {
		return false
	}
	return x.ExecutionStatus == "done" || x.ExecutionStatus == "cancelled"
}

func (s *Store) withScheduleExecution(x Schedule) (Schedule, error) {
	rows, err := s.db.Query(`WITH RECURSIVE latest AS (
 SELECT run_id FROM schedule_occurrences WHERE schedule_id=? ORDER BY scheduled_for_utc DESC LIMIT 1
), family(id,parent_run_id,bot_id,kind,trigger_message_id,created_at,status) AS (
 SELECT r.id,r.parent_run_id,r.bot_id,r.kind,r.trigger_message_id,r.created_at,r.status FROM runs r JOIN latest l ON l.run_id=r.id
 UNION
 SELECT child.id,child.parent_run_id,child.bot_id,child.kind,child.trigger_message_id,child.created_at,child.status FROM runs child JOIN family parent ON child.parent_run_id=parent.id
)
SELECT id,parent_run_id,bot_id,kind,trigger_message_id,created_at,status FROM family ORDER BY created_at,id`, x.ID)
	if err != nil {
		return Schedule{}, err
	}
	defer rows.Close()
	family := make([]scheduleFamilyRun, 0)
	for rows.Next() {
		var r scheduleFamilyRun
		var parent, kind, trigger sql.NullString
		if err = rows.Scan(&r.id, &parent, &r.bot, &kind, &trigger, &r.created, &r.status); err != nil {
			return Schedule{}, err
		}
		r.parent, r.kind, r.trigger = parent.String, kind.String, trigger.String
		family = append(family, r)
	}
	if err = rows.Err(); err != nil {
		return Schedule{}, err
	}
	x.LastRunID, x.ExecutionStatus = scheduleFamilyOutcome(family)
	return x, nil
}

// Both latest-schedule and exact-occurrence reads use this aggregate. Input is
// ordered by created_at,id so peers with equal status have a stable selection.
func scheduleFamilyOutcome(family []scheduleFamilyRun) (runID, status string) {
	byID := make(map[string]scheduleFamilyRun, len(family))
	children := make(map[string][]string)
	for _, r := range family {
		byID[r.id] = r
		if r.parent != "" {
			children[r.parent] = append(children[r.parent], r.id)
		}
	}
	// RetryRun keeps the failed run in the causal family. Treat that failed
	// ancestor as superseded only when a matching descendant exists.
	superseded := make(map[string]bool)
	for _, r := range family {
		if r.status != "failed" && r.status != "interrupted" || r.trigger == "" {
			continue
		}
		queue := append([]string(nil), children[r.id]...)
		seen := map[string]bool{}
		hasRetry, cyclic := false, false
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if id == r.id {
				cyclic = true
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			d := byID[id]
			if d.bot == r.bot && d.kind == r.kind && d.trigger == r.trigger {
				hasRetry = true
			}
			queue = append(queue, children[id]...)
		}
		// Corrupt ancestry cannot make a failure its own successful retry.
		superseded[r.id] = hasRetry && !cyclic
	}
	// Active work dominates terminal siblings; failures dominate cancellation
	// and completion. Iteration is chronological, so the last matching run is
	// deterministic when multiple peers share a status.
	for _, wanted := range []string{"queued", "running", "failed", "interrupted", "cancelled", runWaiting, "done"} {
		for i := len(family) - 1; i >= 0; i-- {
			r := family[i]
			if r.status == wanted && !superseded[r.id] {
				return r.id, r.status
			}
		}
	}
	return "", ""
}

func validateOccurrenceRootIDs(ids []string) error {
	if len(ids) == 0 || len(ids) > maxOccurrenceRoots {
		return errors.New("root_run_ids must contain between 1 and 50 UUIDs")
	}
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil || len(id) != 36 || parsed.String() != id {
			return errors.New("root_run_ids must contain canonical lowercase UUIDs")
		}
	}
	return nil
}

// ScheduleOccurrences reads only actual occurrence roots in this conversation.
// One statement keeps root binding and family outcomes in the same snapshot;
// soft deletion affects future claims, not access to a past occurrence.
func (s *Store) ScheduleOccurrences(ctx context.Context, conversationID string, rootIDs []string) ([]ScheduleOccurrence, error) {
	if err := validateOccurrenceRootIDs(rootIDs); err != nil {
		return nil, err
	}
	args := []any{conversationID, conversationID, runKindSchedule}
	for _, id := range rootIDs {
		args = append(args, id)
	}
	args = append(args, maxOccurrenceError+1)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(rootIDs)), ",")
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE roots(root_run_id,schedule_id,scheduled_for_utc,title,description,created_by,kind,timezone,interval_seconds,daily_time,occurrence_number) AS (
		SELECT o.run_id,o.schedule_id,o.scheduled_for_utc,o.title,o.description,o.created_by,o.kind,o.timezone,o.interval_seconds,o.daily_time,o.occurrence_number FROM schedule_occurrences o
		JOIN schedules s ON s.id=o.schedule_id JOIN runs r ON r.id=o.run_id
		WHERE s.conversation_id=? AND r.conversation_id=? AND r.kind=? AND o.run_id IN (`+placeholders+`)
	), family(root_run_id,id,parent_run_id,bot_id,kind,trigger_message_id,created_at,status) AS (
		SELECT roots.root_run_id,r.id,r.parent_run_id,r.bot_id,r.kind,r.trigger_message_id,r.created_at,r.status
		FROM roots JOIN runs r ON r.id=roots.root_run_id
		UNION
		SELECT parent.root_run_id,child.id,child.parent_run_id,child.bot_id,child.kind,child.trigger_message_id,child.created_at,child.status
		FROM runs child JOIN family parent ON child.parent_run_id=parent.id
	)
		SELECT roots.root_run_id,roots.schedule_id,roots.scheduled_for_utc,roots.title,roots.description,roots.created_by,roots.kind,roots.timezone,roots.interval_seconds,roots.daily_time,roots.occurrence_number,f.id,f.parent_run_id,f.bot_id,f.kind,f.trigger_message_id,f.created_at,f.status,
		CASE WHEN f.status IN ('failed','interrupted') THEN substr(COALESCE(diagnostic.error,''),1,?) ELSE '' END,
		CASE WHEN f.status='done' AND EXISTS (
			SELECT 1 FROM messages m WHERE m.run_id=f.id AND m.conversation_id=? AND m.role='assistant' AND m.kind=''
			-- Historical final messages may contain only ASCII whitespace or the
			-- internal sender marker. Neither is a delivered user-facing answer.
			AND trim(m.content, char(9)||char(10)||char(11)||char(12)||char(13)||' ')<>''
			AND trim(m.content, char(9)||char(10)||char(11)||char(12)||char(13)||' ') NOT LIKE ('[sender % id=' || f.bot_id || ']')
			AND NOT EXISTS (SELECT 1 FROM stream_assistant_turns turn WHERE turn.message_id=m.id)
			AND EXISTS (SELECT 1 FROM tool_activities tool WHERE tool.run_id=f.id AND tool.name='complete_scheduled_task' AND tool.status='completed')
		) THEN 1 ELSE 0 END
	FROM roots JOIN family f ON f.root_run_id=roots.root_run_id JOIN runs diagnostic ON diagnostic.id=f.id
	ORDER BY roots.root_run_id,f.created_at,f.id`, append(args, conversationID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	occurrences := make(map[string]ScheduleOccurrence)
	families := make(map[string][]scheduleFamilyRun)
	for rows.Next() {
		var occurrence ScheduleOccurrence
		var r scheduleFamilyRun
		var parent, kind, trigger sql.NullString
		var resultInConversation int
		if err := rows.Scan(&occurrence.RootRunID, &occurrence.ScheduleID, &occurrence.ScheduledForUTC, &occurrence.Title, &occurrence.Description, &occurrence.CreatedBy, &occurrence.Kind, &occurrence.Timezone, &occurrence.IntervalSeconds, &occurrence.DailyTime, &occurrence.OccurrenceNumber, &r.id, &parent, &r.bot, &kind, &trigger, &r.created, &r.status, &r.statusError, &resultInConversation); err != nil {
			return nil, err
		}
		r.parent, r.kind, r.trigger = parent.String, kind.String, trigger.String
		r.resultInConversation = resultInConversation != 0
		occurrences[occurrence.RootRunID] = occurrence
		families[occurrence.RootRunID] = append(families[occurrence.RootRunID], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ScheduleOccurrence, 0, len(occurrences))
	for _, id := range rootIDs {
		if occurrence, ok := occurrences[id]; ok {
			occurrence.StatusRunID, occurrence.ExecutionStatus = scheduleFamilyOutcome(families[id])
			for _, r := range families[id] {
				if occurrence.ExecutionStatus == "done" && r.resultInConversation {
					occurrence.ResultInConversation = true
				}
				if r.id == occurrence.StatusRunID {
					occurrence.StatusError = boundedOccurrenceError(r.statusError)
				}
			}
			out = append(out, occurrence)
			delete(occurrences, id) // Return duplicate requested IDs only once.
		}
	}
	return out, nil
}

func boundedOccurrenceError(value string) string {
	runes := []rune(value)
	if len(runes) <= maxOccurrenceError {
		return value
	}
	const marker = "\n[… truncated]"
	return string(runes[:maxOccurrenceError-len([]rune(marker))]) + marker
}

func (s *Store) setScheduleStatus(id, status string) (Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return Schedule{}, err
	}
	if status != scheduleActive && status != schedulePaused {
		return Schedule{}, fmt.Errorf("invalid schedule status %q", status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback()
	var conversationID, botID string
	if err = tx.QueryRow(`SELECT conversation_id,bot_id FROM schedules WHERE id=?`, id).Scan(&conversationID, &botID); err != nil {
		return Schedule{}, err
	}
	if err = requireConversationActiveTx(tx, conversationID); err != nil {
		return Schedule{}, err
	}
	if err = requireActiveBotTx(tx, botID); err != nil {
		return Schedule{}, err
	}
	if status == scheduleActive {
		if err = requireActiveMemberTx(tx, conversationID, botID); err != nil {
			return Schedule{}, err
		}
	}
	updated := now()
	res, err := tx.Exec(`UPDATE schedules SET status=?,updated_at=? WHERE id=? AND status IN (?,?)`, status, updated, id, scheduleActive, schedulePaused)
	if err != nil {
		return Schedule{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Schedule{}, errors.New("schedule not found or already terminal")
	}
	x, err := scanSchedule(tx.QueryRow(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE id=?`, id))
	if err != nil {
		return Schedule{}, err
	}
	if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, updated); err != nil {
		return Schedule{}, err
	}
	if err = tx.Commit(); err != nil {
		return Schedule{}, err
	}
	return x, nil
}

func (s *Store) PauseSchedule(id string) (Schedule, error) {
	return s.setScheduleStatus(id, schedulePaused)
}
func (s *Store) ResumeSchedule(id string) (Schedule, error) {
	return s.setScheduleStatus(id, scheduleActive)
}
func (s *Store) DeleteSchedule(id string) error {
	if err := s.ensureSchedules(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated := now()
	res, err := tx.Exec(`UPDATE schedules SET status=?,updated_at=? WHERE id=? AND status<>?`, scheduleDeleted, updated, id, scheduleDeleted)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("schedule already deleted")
	}
	var conversationID string
	if err = tx.QueryRow(`SELECT conversation_id FROM schedules WHERE id=?`, id).Scan(&conversationID); err != nil {
		return err
	}
	if err = insertScheduleEvent(tx, conversationID, "schedule", map[string]any{"id": id, "status": scheduleDeleted}, updated); err != nil {
		return err
	}
	return tx.Commit()
}

func parseStoredTime(raw string) (time.Time, error) { return time.Parse(time.RFC3339Nano, raw) }

func nextForSchedule(x Schedule, now time.Time) (time.Time, error) {
	at, err := parseStoredTime(x.NextAtUTC)
	if err != nil {
		return time.Time{}, err
	}
	switch x.Kind {
	case scheduleInterval:
		step := time.Duration(x.IntervalSeconds) * time.Second
		for !at.After(now) {
			at = at.Add(step)
		}
		return at, nil
	case scheduleDaily:
		loc, err := time.LoadLocation(x.Timezone)
		if err != nil {
			return time.Time{}, err
		}
		next, err := nextDailyAfterOccurrence(at, loc, x.DailyTime)
		for err == nil && !next.After(now) {
			next, err = nextDailyAfterOccurrence(next, loc, x.DailyTime)
		}
		return next, err
	default:
		return time.Time{}, nil
	}
}

// ClaimDueSchedules atomically advances each due schedule and creates its
// queued run, user message, occurrence record, and events. A recurring schedule
// coalesces missed ticks to one fire and advances to the first future tick.
func (s *Store) ClaimDueSchedules(at time.Time) ([]Run, error) {
	if err := s.ensureSchedules(); err != nil {
		return nil, err
	}
	at = at.UTC()
	claimed := make([]Run, 0)
	for len(claimed) < 32 {
		tx, err := s.db.Begin()
		if err != nil {
			return claimed, err
		}
		var x Schedule
		x, err = scanSchedule(tx.QueryRow(`SELECT schedules.id,schedules.conversation_id,schedules.bot_id,schedules.content,schedules.title,schedules.description,schedules.created_by,schedules.kind,schedules.timezone,schedules.next_at_utc,schedules.interval_seconds,schedules.daily_time,schedules.status,schedules.created_at,schedules.updated_at
			FROM schedules JOIN conversations ON conversations.id=schedules.conversation_id JOIN bots ON bots.id=schedules.bot_id WHERE schedules.status=? AND schedules.next_at_utc<=? AND conversations.archived=0 AND bots.archived=0
			AND EXISTS (SELECT 1 FROM members WHERE members.conversation_id=schedules.conversation_id AND members.bot_id=schedules.bot_id)
			AND (schedules.kind<>? OR NOT EXISTS (SELECT 1 FROM schedule_occurrences once_occurrence WHERE once_occurrence.schedule_id=schedules.id))
			AND NOT EXISTS (WITH RECURSIVE family(id) AS (
				SELECT run_id FROM schedule_occurrences WHERE schedule_id=schedules.id
				UNION
				SELECT child.id FROM runs child JOIN family parent ON child.parent_run_id=parent.id
			) SELECT 1 FROM runs WHERE id IN family AND status IN ('queued','running'))
			ORDER BY schedules.next_at_utc,schedules.id LIMIT 1`, scheduleActive, scheduleTime(at), scheduleOnce))
		if err == sql.ErrNoRows {
			tx.Rollback()
			break
		}
		if err != nil {
			tx.Rollback()
			return claimed, err
		}
		occurrence, err := parseStoredTime(x.NextAtUTC)
		if err != nil {
			tx.Rollback()
			return claimed, fmt.Errorf("schedule %s has invalid next_at_utc: %w", x.ID, err)
		}
		next := occurrence
		status := x.Status
		if x.Kind == scheduleOnce {
			// A one-time schedule is only completed after its run publishes a
			// result. Claiming merely moves it into the queued state; marking it
			// completed here made the UI report success before any work ran.
		} else {
			next, err = nextForSchedule(x, at)
			if err != nil {
				tx.Rollback()
				return claimed, err
			}
		}
		updated := now()
		res, err := tx.Exec(`UPDATE schedules SET status=?,next_at_utc=?,updated_at=? WHERE id=? AND status=? AND next_at_utc=?`, status, scheduleTime(next), updated, x.ID, scheduleActive, x.NextAtUTC)
		if err != nil {
			tx.Rollback()
			return claimed, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			tx.Rollback()
			continue
		}
		run := Run{ID: uuid.NewString(), ConversationID: x.ConversationID, BotID: x.BotID, Status: "queued", Kind: runKindSchedule, OriginConversationID: x.ConversationID, CreatedAt: updated, UpdatedAt: updated}
		if err = tx.QueryRow(`SELECT model FROM bots WHERE id=?`, x.BotID).Scan(&run.Model); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, x.ConversationID).Scan(&run.QueueSeq); err != nil {
			tx.Rollback()
			return claimed, err
		}
		var seq int64
		if err = tx.QueryRow(nextMessageSeqSQL, x.ConversationID, x.ConversationID, streamDraftActive).Scan(&seq); err != nil {
			tx.Rollback()
			return claimed, err
		}
		// Keep the task instruction durable for context construction, but mark it
		// as a background event. The UI must not present it as a user message.
		m := Message{ID: uuid.NewString(), ConversationID: x.ConversationID, Seq: seq, Role: "user", Kind: "scheduled_task", RunID: run.ID, Content: x.Content, CreatedAt: updated}
		run.TriggerMessageID = m.ID
		if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.RunID, m.Content, m.CreatedAt); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.ConversationID, run.BotID, run.Status, run.Model, run.Kind, run.OriginConversationID, run.TriggerMessageID, run.QueueSeq, run.CreatedAt, run.UpdatedAt); err != nil {
			tx.Rollback()
			return claimed, err
		}
		var occurrenceNumber int64
		if err = tx.QueryRow(`SELECT COUNT(*)+1 FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&occurrenceNumber); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if _, err = tx.Exec(`INSERT INTO schedule_occurrences(schedule_id,scheduled_for_utc,run_id,created_at,title,description,created_by,kind,timezone,interval_seconds,daily_time,occurrence_number) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, x.ID, scheduleTime(occurrence), run.ID, updated, x.Title, x.Description, x.CreatedBy, x.Kind, x.Timezone, x.IntervalSeconds, x.DailyTime, occurrenceNumber); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if err = insertScheduleEvent(tx, x.ConversationID, "message", m, updated); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if err = insertScheduleEvent(tx, x.ConversationID, "run", run, updated); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, updated, x.ConversationID); err != nil {
			tx.Rollback()
			return claimed, err
		}
		x.Status = status
		x.NextAtUTC = scheduleTime(next)
		x.UpdatedAt = updated
		if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, updated); err != nil {
			tx.Rollback()
			return claimed, err
		}
		if err = tx.Commit(); err != nil {
			return claimed, err
		}
		claimed = append(claimed, run)
	}
	return claimed, nil
}

type ScheduleWorker struct {
	server   *Server
	cancel   context.CancelFunc
	done     chan struct{}
	errors   chan error
	stopOnce sync.Once
}

// StartScheduleWorker starts one local scheduler. The caller owns the worker
// and must call Stop before closing the Store. Errors are logged and exposed by
// Errors, which is closed when the worker exits.
func StartScheduleWorker(ctx context.Context, server *Server) (*ScheduleWorker, error) {
	if ctx == nil || server == nil || server.store == nil {
		return nil, errors.New("schedule worker requires context and server")
	}
	if err := server.store.ensureSchedules(); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	w := &ScheduleWorker{server: server, cancel: cancel, done: make(chan struct{}), errors: make(chan error, 16)}
	go w.loop(child)
	return w, nil
}

func (w *ScheduleWorker) Errors() <-chan error { return w.errors }
func (w *ScheduleWorker) Stop() {
	w.stopOnce.Do(func() { w.cancel() })
	<-w.done
}
func (w *ScheduleWorker) report(err error) {
	if err == nil {
		return
	}
	log.Printf("[schedule] %v", err)
	select {
	case w.errors <- err:
	default:
	}
}
func (w *ScheduleWorker) loop(ctx context.Context) {
	defer close(w.errors)
	defer close(w.done)
	if ctx.Err() != nil {
		return
	}
	w.tick()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			w.tick()
		}
	}
}
func (w *ScheduleWorker) tick() {
	if !w.server.modelConfigured() {
		return // leave due schedules and queued work durable until a model exists
	}
	runs, err := w.server.store.ClaimDueSchedules(time.Now().UTC())
	for _, r := range runs {
		w.dispatch(r)
	}
	w.report(err)
}
func (w *ScheduleWorker) dispatch(r Run) {
	c, err := w.server.store.GetConversation(r.ConversationID)
	if err != nil {
		w.report(fmt.Errorf("load scheduled run %s conversation: %w", r.ID, err))
		return
	}
	w.server.enqueue(c, r)
}

type scheduleRequest struct {
	BotID           string `json:"bot_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Content         string `json:"content"`
	Kind            string `json:"kind"`
	RunAt           string `json:"run_at"`
	IntervalSeconds int64  `json:"interval_seconds"`
	DailyTime       string `json:"daily_time"`
	Timezone        string `json:"timezone"`
}

func (x scheduleRequest) spec() ScheduleSpec {
	return ScheduleSpec{Title: x.Title, Description: x.Description, Content: x.Content, Kind: x.Kind, RunAt: x.RunAt, IntervalSeconds: x.IntervalSeconds, DailyTime: x.DailyTime, Timezone: x.Timezone}
}

// routeSchedules handles complete paths after /api/ and returns true when the
// path belongs to scheduling. app.go can call it before its default switch.
func (s *Server) routeSchedules(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 3 && parts[0] == "conversations" && parts[2] == "schedule-occurrences" {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return true
		}
		if len(r.URL.RawQuery) > maxOccurrenceQuery {
			writeErr(w, http.StatusBadRequest, "invalid_request", "schedule occurrence query is too large")
			return true
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		values := query["root_run_ids"]
		if err != nil || len(query) != 1 || len(values) != 1 || len(values[0]) > maxOccurrenceRoots*37-1 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "provide one root_run_ids parameter containing 1 to 50 UUIDs")
			return true
		}
		ids := strings.Split(values[0], ",")
		if err := validateOccurrenceRootIDs(ids); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		conversationID, err := url.PathUnescape(parts[1])
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid conversation ID")
			return true
		}
		// Follow the same owner-authenticated read rules as messages/runs:
		// archived conversations and hidden Bot traces remain readable by ID.
		if _, err := s.store.GetConversation(conversationID); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
			return true
		}
		occurrences, err := s.store.ScheduleOccurrences(r.Context(), conversationID, ids)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "could not read schedule occurrences")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"occurrences": occurrences})
		return true
	}
	if len(parts) == 3 && parts[0] == "bots" && parts[2] == "schedules" && r.Method == http.MethodGet {
		botID, _ := url.PathUnescape(parts[1])
		rawView := r.URL.Query().Get("view")
		if rawView == "" && r.URL.Query().Get("history") == "true" {
			rawView = "history"
		}
		history, ok := scheduleView(rawView)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_view", "view must be upcoming or history")
			return true
		}
		v, err := s.store.ListBotSchedules(botID, history)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"schedules": v})
		return true
	}
	if len(parts) == 3 && parts[0] == "conversations" && parts[2] == "schedules" {
		conversationID, _ := url.PathUnescape(parts[1])
		c, err := s.store.GetConversation(conversationID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
			return true
		}
		if r.Method == http.MethodGet {
			view := r.URL.Query().Get("view")
			var v []Schedule
			var err error
			switch view {
			case "":
				v, err = s.store.ListSchedules(c.ID)
			case "upcoming", "history":
				v, err = s.store.ListScheduleAgenda(c.ID, "", view == "history")
			default:
				writeErr(w, http.StatusBadRequest, "invalid_view", "view must be upcoming or history")
				return true
			}
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "storage", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"schedules": v})
			return true
		}
		if r.Method == http.MethodPost {
			var req scheduleRequest
			if decode(r, &req) != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", "invalid schedule request")
				return true
			}
			spec := req.spec()
			spec.CreatedBy = "user"
			x, err := s.store.CreateSchedule(c.ID, req.BotID, spec)
			if err != nil {
				if errors.Is(err, ErrArchiveBlocked) {
					writeErr(w, http.StatusConflict, "archive_blocked", err.Error())
					return true
				}
				writeErr(w, http.StatusBadRequest, "invalid_schedule", err.Error())
				return true
			}
			writeJSON(w, http.StatusCreated, x)
			return true
		}
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
		return true
	}
	if len(parts) >= 2 && parts[0] == "schedules" {
		id, _ := url.PathUnescape(parts[1])
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			x, err := s.store.GetSchedule(id)
			if err != nil {
				writeErr(w, http.StatusNotFound, "not_found", "schedule not found")
				return true
			}
			writeJSON(w, http.StatusOK, x)
			return true
		case r.Method == http.MethodPatch && len(parts) == 2:
			var patch SchedulePatch
			if decode(r, &patch) != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", "invalid schedule edit")
				return true
			}
			x, err := s.store.PatchSchedule(id, patch)
			if err != nil {
				if errors.Is(err, ErrEditConflict) {
					writeErr(w, http.StatusConflict, "edit_conflict", err.Error())
					return true
				}
				if errors.Is(err, ErrArchiveBlocked) {
					writeErr(w, http.StatusConflict, "archive_blocked", err.Error())
					return true
				}
				writeErr(w, http.StatusBadRequest, "invalid_schedule", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, x)
			return true
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "pause":
			x, err := s.store.PauseSchedule(id)
			if err != nil {
				if errors.Is(err, ErrArchiveBlocked) {
					writeErr(w, http.StatusConflict, "archive_blocked", err.Error())
					return true
				}
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, x)
			return true
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "resume":
			x, err := s.store.ResumeSchedule(id)
			if err != nil {
				if errors.Is(err, ErrArchiveBlocked) {
					writeErr(w, http.StatusConflict, "archive_blocked", err.Error())
					return true
				}
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, x)
			return true
		case r.Method == http.MethodDelete && len(parts) == 2:
			if err := s.store.DeleteSchedule(id); err != nil {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return true
			}
			w.WriteHeader(http.StatusNoContent)
			return true
		}
	}
	return false
}

func scheduleView(raw string) (bool, bool) {
	switch raw {
	case "upcoming", "":
		return false, true
	case "history":
		return true, true
	default:
		return false, false
	}
}

func (s *Server) scheduleTools(c Conversation, r Run) []Tool {
	// A scheduled run is already executing an existing schedule. Exposing
	// schedule-management tools here makes the model inspect or recreate the
	// schedule instead of performing its content.
	if r.Kind == runKindSchedule {
		return nil
	}
	check := func(id string) (Schedule, error) {
		x, err := s.store.GetSchedule(id)
		if err != nil {
			return Schedule{}, err
		}
		if x.ConversationID != c.ID {
			return Schedule{}, errors.New("schedule is outside the current conversation")
		}
		return x, nil
	}
	return []Tool{
		{Name: "create_schedule", Description: "Schedule future work by a member of this conversation. Use kind once for a single run, interval for repeating elapsed intervals, or daily for a local wall-clock time. Timezone defaults to the configured user timezone; if none is configured, provide the IANA timezone supported by the request or ask the user before guessing.", Parameters: objectSchema(map[string]any{"bot_id": map[string]any{"type": "string", "description": "Active member to execute the work; defaults to you"}, "title": displayTitleSchema(), "description": displayDescriptionSchema(), "content": map[string]any{"type": "string", "description": "Complete execution instructions written in English. Preserve exact names, quoted user data and localized output requirements; keep title and description separate."}, "kind": map[string]any{"type": "string", "enum": []string{"once", "interval", "daily"}}, "run_at": map[string]any{"type": "string", "description": "Required for once: RFC3339 timestamp with explicit UTC offset"}, "interval_seconds": map[string]any{"type": "integer", "description": "Required for interval: elapsed seconds between runs"}, "daily_time": map[string]any{"type": "string", "description": "Required for daily: HH:MM in the selected timezone"}, "timezone": map[string]any{"type": "string", "description": "IANA timezone; omit only when user timezone is configured"}}, []string{"title", "description", "content", "kind"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var req scheduleRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			if _, _, err := normalizeDisplayMetadata(req.Title, req.Description, true); err != nil {
				return "", err
			}
			if req.BotID == "" {
				req.BotID = r.BotID
			}
			spec := req.spec()
			spec.CreatedBy = "bot"
			x, err := s.store.CreateSchedule(c.ID, req.BotID, spec)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(x)
			return string(b), nil
		}},
		{Name: "update_schedule", Description: "Edit this conversation's schedule title, description and optionally its complete execution instruction, only when the user requests an edit. Write authored execution instructions in English while preserving quoted data and localized output requirements. Timing, recurrence and past executions are preserved.", Parameters: objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string"}, "title": displayTitleSchema(), "description": displayDescriptionSchema(), "content": map[string]any{"type": "string", "description": "Optional complete replacement execution instruction in English; omit to preserve it"}}, []string{"schedule_id", "title", "description"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var input struct {
				ScheduleID string `json:"schedule_id"`
				SchedulePatch
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return "", err
			}
			if _, err := check(input.ScheduleID); err != nil {
				return "", err
			}
			if input.Title == nil || input.Description == nil {
				return "", errors.New("title and description required")
			}
			if _, _, err := normalizeDisplayMetadata(*input.Title, *input.Description, true); err != nil {
				return "", err
			}
			x, err := s.store.PatchSchedule(input.ScheduleID, input.SchedulePatch)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(x)
			return string(b), nil
		}},
		{Name: "list_schedules", Description: "List scheduled work for this conversation or this Bot across conversations. Use view upcoming, history, or all.", Parameters: objectSchema(map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"conversation", "self"}}, "view": map[string]any{"type": "string", "enum": []string{"upcoming", "history", "all"}}}, nil), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				Scope string `json:"scope"`
				View  string `json:"view"`
			}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", err
				}
			}
			if in.Scope == "" {
				in.Scope = "conversation"
			}
			if in.View == "" {
				in.View = "all"
			}
			var v []Schedule
			var err error
			switch {
			case in.Scope == "conversation" && in.View == "all":
				v, err = s.store.ListSchedules(c.ID)
			case in.Scope == "conversation" && (in.View == "upcoming" || in.View == "history"):
				v, err = s.store.ListScheduleAgenda(c.ID, "", in.View == "history")
			case in.Scope == "self" && (in.View == "upcoming" || in.View == "history"):
				v, err = s.store.ListBotSchedules(r.BotID, in.View == "history")
			case in.Scope == "self" && in.View == "all":
				v, err = s.store.listSchedules(`bot_id=?`, []any{r.BotID}, nil)
			default:
				return "", errors.New("scope must be conversation or self and view must be upcoming, history, or all")
			}
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(v)
			return string(b), nil
		}},
		{Name: "pause_schedule", Description: "pause a schedule in this conversation", Parameters: objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string"}}, []string{"schedule_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				ScheduleID string `json:"schedule_id"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			if _, err := check(in.ScheduleID); err != nil {
				return "", err
			}
			x, err := s.store.PauseSchedule(in.ScheduleID)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(x)
			return string(b), nil
		}},
		{Name: "resume_schedule", Description: "resume a schedule in this conversation", Parameters: objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string"}}, []string{"schedule_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				ScheduleID string `json:"schedule_id"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			if _, err := check(in.ScheduleID); err != nil {
				return "", err
			}
			x, err := s.store.ResumeSchedule(in.ScheduleID)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(x)
			return string(b), nil
		}},
		{Name: "delete_schedule", Description: "delete a schedule in this conversation", Parameters: objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string"}}, []string{"schedule_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				ScheduleID string `json:"schedule_id"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			if _, err := check(in.ScheduleID); err != nil {
				return "", err
			}
			return "deleted", s.store.DeleteSchedule(in.ScheduleID)
		}},
	}
}
