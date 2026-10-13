package app

// Storage for proactive follow-ups (docs/agent-plan/proactive-followups.md
// section 1.2). Loops are owned by a Bot (bot_id) and come from a source
// (chat today, email from P1). P0 keeps everything in shadow: rows describe
// what would be said, and nothing here ever writes to a conversation.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	followupModeOff    = "off"
	followupModeShadow = "shadow"
	followupModeOn     = "on"

	followupSourceChat  = "chat"
	followupSourceEmail = "email"

	usageKindFollowup = "followup"
)

func migrateFollowups(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS followup_account(
  id INTEGER PRIMARY KEY CHECK(id=1),
  max_nudges_per_day INTEGER NOT NULL DEFAULT 3,
  quiet_start TEXT NOT NULL DEFAULT '22:00', quiet_end TEXT NOT NULL DEFAULT '08:00',
  weekend_min_importance INTEGER NOT NULL DEFAULT 80,
  digest_weekday TEXT NOT NULL DEFAULT 'mon', digest_time TEXT NOT NULL DEFAULT '09:00',
  daily_budget_usd REAL NOT NULL DEFAULT 0.5,
  updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS followup_settings(
  bot_id TEXT PRIMARY KEY REFERENCES bots(id) ON DELETE CASCADE,
  mode TEXT NOT NULL DEFAULT 'off' CHECK(mode IN ('off','shadow','on')),
  source_email INTEGER NOT NULL DEFAULT 1, source_chat INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS followup_cursors(
  bot_id TEXT NOT NULL, source TEXT NOT NULL CHECK(source IN ('email','chat')),
  cursor TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(bot_id,source));
CREATE TABLE IF NOT EXISTS open_loops(
  id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  source TEXT NOT NULL DEFAULT 'chat' CHECK(source IN ('email','chat')),
  kind TEXT NOT NULL CHECK(kind IN ('awaiting_other','owner_promise','owner_reminder')),
  counterparty_name TEXT NOT NULL DEFAULT '', counterparty_address TEXT NOT NULL DEFAULT '',
  topic TEXT NOT NULL, topic_digest TEXT NOT NULL,
  category TEXT NOT NULL, importance INTEGER NOT NULL,
  band TEXT NOT NULL DEFAULT 'digest' CHECK(band IN ('nudge','digest')),
  delivery TEXT NOT NULL DEFAULT 'shadow' CHECK(delivery IN ('shadow','delivered')),
  automated_sender INTEGER NOT NULL DEFAULT 0,
  expected_by_utc TEXT NOT NULL, resolution_json TEXT NOT NULL,
  suggested_next TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL CHECK(state IN ('open','resolved_silent','handled_by_owner','nudged','snoozed','dismissed','digest_only','expired')),
  nudge_count INTEGER NOT NULL DEFAULT 0, last_nudged_at TEXT NOT NULL DEFAULT '',
  snoozed_until_utc TEXT NOT NULL DEFAULT '', nudge_question_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT '', close_reason TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS open_loops_dedupe ON open_loops(bot_id,topic_digest) WHERE state IN ('open','nudged','snoozed','digest_only');
CREATE INDEX IF NOT EXISTS open_loops_due ON open_loops(bot_id,state,expected_by_utc);
CREATE TABLE IF NOT EXISTS open_loop_evidence(
  loop_id TEXT NOT NULL REFERENCES open_loops(id) ON DELETE CASCADE,
  pointer TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('origin','resolution','owner_action')),
  quote TEXT NOT NULL DEFAULT '',
  observed_at TEXT NOT NULL, PRIMARY KEY(loop_id,pointer,role));
CREATE TABLE IF NOT EXISTS followup_feedback(
  id TEXT PRIMARY KEY, bot_id TEXT NOT NULL, loop_id TEXT NOT NULL,
  verdict TEXT NOT NULL CHECK(verdict IN ('useful','dismissed','ignored','annoying','handled')),
  sender_domain TEXT NOT NULL DEFAULT '', category TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS followup_feedback_loop ON followup_feedback(loop_id,created_at);
CREATE TABLE IF NOT EXISTS followup_suppressions(
  bot_id TEXT NOT NULL, scope TEXT NOT NULL CHECK(scope IN ('sender','domain','category')),
  key TEXT NOT NULL, effect TEXT NOT NULL CHECK(effect IN ('mute','digest_only')),
  reason TEXT NOT NULL, until_utc TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
  PRIMARY KEY(bot_id,scope,key));
CREATE TABLE IF NOT EXISTS followup_sweeps(
  id TEXT PRIMARY KEY, bot_id TEXT NOT NULL, source TEXT NOT NULL, started_at TEXT NOT NULL,
  items_seen INTEGER NOT NULL, loops_created INTEGER NOT NULL, model TEXT NOT NULL,
  input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, equivalent_usd REAL NOT NULL, error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS followup_sweeps_time ON followup_sweeps(bot_id,started_at);`)
	if err != nil {
		return err
	}
	// Gate counters for the shadow review page ("items rejected by each gate").
	for _, col := range []string{"rejected_invalid", "rejected_band", "merged"} {
		if err = ensureColumn(db, "followup_sweeps", col, `ALTER TABLE followup_sweeps ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Follow-up model spend is rolled up beside, never inside, chat and review.
	for _, col := range []string{"followup_input_tokens", "followup_output_tokens", "followup_requests", "followup_unpriced_requests"} {
		if err = ensureColumn(db, "usage_hourly", col, `ALTER TABLE usage_hourly ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return ensureColumn(db, "usage_hourly", "followup_equivalent_usd", `ALTER TABLE usage_hourly ADD COLUMN followup_equivalent_usd REAL NOT NULL DEFAULT 0`)
}

// deleteFollowupsForBotTx removes the tables that carry no foreign key to bots.
func deleteFollowupsForBotTx(tx *sql.Tx, botID string) error {
	for _, q := range []string{
		`DELETE FROM followup_cursors WHERE bot_id=?`,
		`DELETE FROM followup_feedback WHERE bot_id=?`,
		`DELETE FROM followup_suppressions WHERE bot_id=?`,
		`DELETE FROM followup_sweeps WHERE bot_id=?`,
		`DELETE FROM open_loops WHERE bot_id=?`, // also cascades evidence; explicit for stores with foreign keys off
	} {
		if _, err := tx.Exec(q, botID); err != nil {
			return err
		}
	}
	return nil
}

// ---- settings ----

func (s *Store) followupAccount() (followupAccountSettings, error) {
	cfg := defaultFollowupAccountSettings()
	err := s.db.QueryRow(`SELECT max_nudges_per_day,quiet_start,quiet_end,weekend_min_importance,digest_weekday,digest_time,daily_budget_usd FROM followup_account WHERE id=1`).
		Scan(&cfg.MaxNudgesPerDay, &cfg.QuietStart, &cfg.QuietEnd, &cfg.WeekendMinImportance, &cfg.DigestWeekday, &cfg.DigestTime, &cfg.DailyBudgetUSD)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	return cfg, err
}

var followupWeekdays = map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}

func validateFollowupAccount(c followupAccountSettings) error {
	if c.MaxNudgesPerDay < 0 || c.MaxNudgesPerDay > 10 {
		return errors.New("max_nudges_per_day must be 0-10")
	}
	for _, v := range []string{c.QuietStart, c.QuietEnd, c.DigestTime} {
		if _, ok := parseHHMM(v); !ok {
			return errors.New("times must be HH:MM")
		}
	}
	if c.WeekendMinImportance < 0 || c.WeekendMinImportance > 100 {
		return errors.New("weekend_min_importance must be 0-100")
	}
	if !followupWeekdays[c.DigestWeekday] {
		return errors.New("digest_weekday must be mon-sun")
	}
	if c.DailyBudgetUSD < 0.01 || c.DailyBudgetUSD > 50 {
		return errors.New("daily_budget_usd must be between 0.01 and 50")
	}
	return nil
}

func (s *Store) putFollowupAccount(c followupAccountSettings) error {
	if err := validateFollowupAccount(c); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO followup_account(id,max_nudges_per_day,quiet_start,quiet_end,weekend_min_importance,digest_weekday,digest_time,daily_budget_usd,updated_at) VALUES(1,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET max_nudges_per_day=excluded.max_nudges_per_day,quiet_start=excluded.quiet_start,quiet_end=excluded.quiet_end,weekend_min_importance=excluded.weekend_min_importance,digest_weekday=excluded.digest_weekday,digest_time=excluded.digest_time,daily_budget_usd=excluded.daily_budget_usd,updated_at=excluded.updated_at`,
		c.MaxNudgesPerDay, c.QuietStart, c.QuietEnd, c.WeekendMinImportance, c.DigestWeekday, c.DigestTime, c.DailyBudgetUSD, now())
	return err
}

type followupBotSettings struct {
	BotID       string `json:"bot_id"`
	Mode        string `json:"mode"`
	SourceChat  bool   `json:"source_chat"`
	SourceEmail bool   `json:"source_email"`
}

func (s *Store) followupBotSettings(botID string) (followupBotSettings, error) {
	out := followupBotSettings{BotID: botID, Mode: followupModeOff, SourceChat: true, SourceEmail: true}
	var chat, email int
	err := s.db.QueryRow(`SELECT mode,source_chat,source_email FROM followup_settings WHERE bot_id=?`, botID).Scan(&out.Mode, &chat, &email)
	if err == sql.ErrNoRows {
		return out, nil
	}
	out.SourceChat, out.SourceEmail = chat != 0, email != 0
	return out, err
}

func (s *Store) putFollowupBotSettings(x followupBotSettings) error {
	switch x.Mode {
	case followupModeOff, followupModeShadow:
	case followupModeOn:
		return errFollowupDeliveryUnavailable
	default:
		return errors.New("mode must be off or shadow")
	}
	if _, err := s.GetBot(x.BotID); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO followup_settings(bot_id,mode,source_email,source_chat,updated_at) VALUES(?,?,?,?,?)
ON CONFLICT(bot_id) DO UPDATE SET mode=excluded.mode,source_email=excluded.source_email,source_chat=excluded.source_chat,updated_at=excluded.updated_at`,
		x.BotID, x.Mode, boolInt(x.SourceEmail), boolInt(x.SourceChat), now())
	return err
}

// errFollowupDeliveryUnavailable: mode "on" posts into chats, which ships with
// P3. Until then the API accepts only off and shadow.
var errFollowupDeliveryUnavailable = errors.New("delivering follow-ups is not available yet; use shadow mode")

// ---- cursors ----

// chatCursor returns the last scanned message seq for a Bot's DM.
func (s *Store) followupCursor(botID, source string) (int64, string, error) {
	var cursor, updated string
	err := s.db.QueryRow(`SELECT cursor,updated_at FROM followup_cursors WHERE bot_id=? AND source=?`, botID, source).Scan(&cursor, &updated)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	var n int64
	for _, r := range cursor {
		if r < '0' || r > '9' {
			return 0, "", errors.New("corrupt follow-up cursor")
		}
		n = n*10 + int64(r-'0')
	}
	return n, updated, nil
}

// ---- chat input ----

type chatSweepItem struct {
	MessageID string `json:"-"`
	Seq       int64  `json:"-"`
	Index     int    `json:"item"`
	Previous  string `json:"previous_assistant_message,omitempty"`
	Text      string `json:"user_message"`
	WrittenAt string `json:"written_at"`
}

const (
	chatSweepBatch      = 80
	chatSweepItemRunes  = 1200
	chatSweepPrevRunes  = 300
	followupQuoteRunes  = 300
	followupTopicRunes  = 120
	followupNextRunes   = 160
	followupNameRunes   = 80
	followupPhraseRunes = 60
)

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}

// chatSweepBatch returns the owner-authored messages after `after` in a Bot's
// DM, plus the highest seq looked at (the next cursor). Only role=user rows of
// kind "" that the host recorded as user ingress count: assistant text,
// scheduled_task triggers, notices, cards and imported transcripts can never
// create a loop.
func (s *Store) chatSweepBatch(botID string, after int64) (items []chatSweepItem, maxSeq int64, err error) {
	var dm string
	if err = s.db.QueryRow(`SELECT dm_conversation_id FROM bots WHERE id=?`, botID).Scan(&dm); err != nil {
		return nil, after, err
	}
	rows, err := s.db.Query(`SELECT m.id,m.seq,m.content,m.created_at,(m.role='user' AND m.kind='' AND i.message_id IS NOT NULL)
FROM messages m LEFT JOIN user_message_ingress i ON i.message_id=m.id
WHERE m.conversation_id=? AND m.seq>? ORDER BY m.seq LIMIT ?`, dm, after, chatSweepBatch)
	if err != nil {
		return nil, after, err
	}
	type scanned struct {
		id, content, at string
		seq             int64
		owner           bool
	}
	var batch []scanned
	for rows.Next() {
		var x scanned
		var owner int
		if err = rows.Scan(&x.id, &x.seq, &x.content, &x.at, &owner); err != nil {
			rows.Close()
			return nil, after, err
		}
		x.owner = owner != 0
		batch = append(batch, x)
	}
	if err = rows.Close(); err != nil {
		return nil, after, err
	}
	maxSeq = after
	for _, x := range batch {
		if x.seq > maxSeq {
			maxSeq = x.seq
		}
		if !x.owner || strings.TrimSpace(x.content) == "" {
			continue
		}
		item := chatSweepItem{MessageID: x.id, Seq: x.seq, Index: len(items), Text: truncateRunes(x.content, chatSweepItemRunes), WrittenAt: x.at}
		var prev string
		e := s.db.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND seq<? AND role='assistant' AND kind='' ORDER BY seq DESC LIMIT 1`, dm, x.seq).Scan(&prev)
		if e != nil && e != sql.ErrNoRows {
			return nil, after, e
		}
		item.Previous = truncateRunes(prev, chatSweepPrevRunes)
		items = append(items, item)
	}
	return items, maxSeq, nil
}

// ---- loops ----

type storedLoop struct {
	BotID            string
	Source           string
	Kind             string
	CounterpartyName string
	Topic            string
	Category         string
	Importance       int
	Band             string
	ExpectedBy       time.Time
	ResolutionPhrase []string
	SuggestedNext    string
	EvidencePointer  string
	EvidenceQuote    string
}

func topicDigest(counterparty, topic string) string {
	sum := sha256.Sum256([]byte(normalizeTopicStem(counterparty) + "|" + normalizeTopicStem(topic)))
	return hex.EncodeToString(sum[:])
}

// insertLoopTx stores a loop, or merges it into the live loop with the same
// digest: evidence is added and importance may rise, never fall. It reports
// whether a new row was created.
func insertLoopTx(tx *sql.Tx, l storedLoop, at time.Time) (id string, created bool, err error) {
	stamp := at.UTC().Format(time.RFC3339Nano)
	digest := topicDigest(l.CounterpartyName, l.Topic)
	state := "open"
	if l.Band == bandDigest {
		state = "digest_only"
	}
	var existing string
	var oldImportance int
	e := tx.QueryRow(`SELECT id,importance FROM open_loops WHERE bot_id=? AND topic_digest=? AND state IN ('open','nudged','snoozed','digest_only')`, l.BotID, digest).Scan(&existing, &oldImportance)
	switch {
	case e == nil:
		if l.Importance > oldImportance {
			band := importanceBand(l.Importance)
			if _, err = tx.Exec(`UPDATE open_loops SET importance=?,band=?,state=CASE WHEN state='digest_only' AND ?='nudge' THEN 'open' ELSE state END,updated_at=? WHERE id=?`, l.Importance, band, band, stamp, existing); err != nil {
				return "", false, err
			}
		}
		if err = addEvidenceTx(tx, existing, l.EvidencePointer, "origin", l.EvidenceQuote, stamp); err != nil {
			return "", false, err
		}
		return existing, false, nil
	case e != sql.ErrNoRows:
		return "", false, e
	}
	resolution, err := json.Marshal(map[string]any{"chat_phrases": nonNilStrings(l.ResolutionPhrase)})
	if err != nil {
		return "", false, err
	}
	id = uuid.NewString()
	if _, err = tx.Exec(`INSERT INTO open_loops(id,bot_id,source,kind,counterparty_name,topic,topic_digest,category,importance,band,delivery,expected_by_utc,resolution_json,suggested_next,state,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,'shadow',?,?,?,?,?,?)`,
		id, l.BotID, l.Source, l.Kind, l.CounterpartyName, l.Topic, digest, l.Category, l.Importance, l.Band, l.ExpectedBy.UTC().Format(time.RFC3339Nano), string(resolution), l.SuggestedNext, state, stamp, stamp); err != nil {
		return "", false, err
	}
	if err = addEvidenceTx(tx, id, l.EvidencePointer, "origin", l.EvidenceQuote, stamp); err != nil {
		return "", false, err
	}
	return id, true, nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func addEvidenceTx(tx *sql.Tx, loopID, pointer, role, quote, at string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO open_loop_evidence(loop_id,pointer,role,quote,observed_at) VALUES(?,?,?,?,?)`, loopID, pointer, role, truncateRunes(quote, followupQuoteRunes), at)
	return err
}

type sweepRecord struct {
	BotID           string
	Source          string
	StartedAt       time.Time
	ItemsSeen       int
	LoopsCreated    int
	RejectedInvalid int
	RejectedBand    int
	Merged          int
	Model           string
	InputTokens     int64
	OutputTokens    int64
	USD             float64
	PriceKnown      bool
	Err             string
}

// recordSweepTx writes the audit row and the follow-up usage rollup.
func recordSweepTx(tx *sql.Tx, r sweepRecord) error {
	if _, err := tx.Exec(`INSERT INTO followup_sweeps(id,bot_id,source,started_at,items_seen,loops_created,model,input_tokens,output_tokens,equivalent_usd,error,rejected_invalid,rejected_band,merged) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), r.BotID, r.Source, r.StartedAt.UTC().Format(time.RFC3339Nano), r.ItemsSeen, r.LoopsCreated, r.Model, r.InputTokens, r.OutputTokens, r.USD, r.Err, r.RejectedInvalid, r.RejectedBand, r.Merged); err != nil {
		return err
	}
	if r.Model == "" && r.InputTokens == 0 && r.OutputTokens == 0 {
		return nil
	}
	unknown := 0
	if !r.PriceKnown {
		unknown = 1
	}
	_, err := tx.Exec(`INSERT INTO usage_hourly(hour,bot_id,input_tokens,output_tokens,requests,equivalent_usd,unpriced_requests,followup_input_tokens,followup_output_tokens,followup_requests,followup_equivalent_usd,followup_unpriced_requests) VALUES(?,?,0,0,0,0,0,?,?,1,?,?)
ON CONFLICT(hour,bot_id) DO UPDATE SET followup_input_tokens=followup_input_tokens+excluded.followup_input_tokens,followup_output_tokens=followup_output_tokens+excluded.followup_output_tokens,followup_requests=followup_requests+1,followup_equivalent_usd=followup_equivalent_usd+excluded.followup_equivalent_usd,followup_unpriced_requests=followup_unpriced_requests+excluded.followup_unpriced_requests`,
		r.StartedAt.UTC().Format("2006-01-02T15"), r.BotID, r.InputTokens, r.OutputTokens, r.USD, unknown)
	return err
}

func setCursorTx(tx *sql.Tx, botID, source string, seq int64) error {
	_, err := tx.Exec(`INSERT INTO followup_cursors(bot_id,source,cursor,updated_at) VALUES(?,?,?,?)
ON CONFLICT(bot_id,source) DO UPDATE SET cursor=excluded.cursor,updated_at=excluded.updated_at`, botID, source, strconv.FormatInt(seq, 10), now())
	return err
}

// followupSpendSince sums follow-up model spend (all Bots) at or after `since`.
func (s *Store) followupSpendSince(since time.Time) (float64, error) {
	var v sql.NullFloat64
	err := s.db.QueryRow(`SELECT SUM(equivalent_usd) FROM followup_sweeps WHERE started_at>=?`, since.UTC().Format(time.RFC3339Nano)).Scan(&v)
	return v.Float64, err
}

// recentFailedSweeps counts error sweeps since the cursor last moved.
func (s *Store) recentFailedSweeps(botID, source string) (int, error) {
	_, updated, err := s.followupCursor(botID, source)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM followup_sweeps WHERE bot_id=? AND source=? AND error<>'' AND started_at>=?`, botID, source, updated).Scan(&n)
	return n, err
}

// ---- preview ----

type followupLoopView struct {
	ID               string   `json:"id"`
	BotID            string   `json:"bot_id"`
	BotName          string   `json:"bot_name"`
	Source           string   `json:"source"`
	Kind             string   `json:"kind"`
	Topic            string   `json:"topic"`
	CounterpartyName string   `json:"counterparty_name,omitempty"`
	Category         string   `json:"category"`
	Importance       int      `json:"importance"`
	Band             string   `json:"band"`
	ExpectedBy       string   `json:"expected_by"`
	SuggestedNext    string   `json:"suggested_next,omitempty"`
	Quote            string   `json:"quote,omitempty"`
	Phrases          []string `json:"resolution_phrases"`
	State            string   `json:"state"`
	Verdict          string   `json:"verdict,omitempty"`
	CreatedAt        string   `json:"created_at"`
}

func (s *Store) followupLoopViews(limit int) ([]followupLoopView, error) {
	rows, err := s.db.Query(`SELECT l.id,l.bot_id,COALESCE(b.name,''),l.source,l.kind,l.topic,l.counterparty_name,l.category,l.importance,l.band,l.expected_by_utc,l.suggested_next,
COALESCE((SELECT quote FROM open_loop_evidence e WHERE e.loop_id=l.id AND e.role='origin' ORDER BY e.observed_at LIMIT 1),''),
l.resolution_json,l.state,COALESCE((SELECT verdict FROM followup_feedback f WHERE f.loop_id=l.id ORDER BY f.created_at DESC,f.rowid DESC LIMIT 1),''),l.created_at
FROM open_loops l LEFT JOIN bots b ON b.id=l.bot_id
ORDER BY CASE WHEN l.state IN ('open','digest_only','nudged','snoozed') THEN 0 ELSE 1 END, l.expected_by_utc, l.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []followupLoopView{}
	for rows.Next() {
		var v followupLoopView
		var resolution string
		if err = rows.Scan(&v.ID, &v.BotID, &v.BotName, &v.Source, &v.Kind, &v.Topic, &v.CounterpartyName, &v.Category, &v.Importance, &v.Band, &v.ExpectedBy, &v.SuggestedNext, &v.Quote, &resolution, &v.State, &v.Verdict, &v.CreatedAt); err != nil {
			return nil, err
		}
		var parsed struct {
			Phrases []string `json:"chat_phrases"`
		}
		_ = json.Unmarshal([]byte(resolution), &parsed)
		v.Phrases = nonNilStrings(parsed.Phrases)
		out = append(out, v)
	}
	return out, rows.Err()
}

var followupVerdicts = map[string]bool{"useful": true, "annoying": true, "handled": true}

var errLoopNotFound = errors.New("follow-up not found")

// addFollowupFeedback records the owner's review of a shadow item. "handled"
// closes the loop silently; "annoying" dismisses it. Both stop it being
// counted as a would-be nudge. Learning rules (suppressions) ship with P3.
func (s *Store) addFollowupFeedback(loopID, verdict string) error {
	if !followupVerdicts[verdict] {
		return errors.New("verdict must be useful, annoying or handled")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var botID, category, state string
	if err = tx.QueryRow(`SELECT bot_id,category,state FROM open_loops WHERE id=?`, loopID).Scan(&botID, &category, &state); err != nil {
		if err == sql.ErrNoRows {
			return errLoopNotFound
		}
		return err
	}
	t := now()
	if _, err = tx.Exec(`INSERT INTO followup_feedback(id,bot_id,loop_id,verdict,category,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), botID, loopID, verdict, category, t); err != nil {
		return err
	}
	switch verdict {
	case "handled":
		_, err = tx.Exec(`UPDATE open_loops SET state='handled_by_owner',closed_at=?,close_reason='owner_feedback',updated_at=? WHERE id=? AND state IN ('open','nudged','snoozed','digest_only')`, t, t, loopID)
	case "annoying":
		_, err = tx.Exec(`UPDATE open_loops SET state='dismissed',closed_at=?,close_reason='owner_feedback',updated_at=? WHERE id=? AND state IN ('open','nudged','snoozed','digest_only')`, t, t, loopID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

type followupDayStats struct {
	Sweeps          int     `json:"sweeps"`
	LoopsCreated    int     `json:"loops_created"`
	RejectedInvalid int     `json:"rejected_invalid"`
	RejectedBand    int     `json:"rejected_band"`
	Merged          int     `json:"merged"`
	SpentUSD        float64 `json:"spent_usd"`
	WouldNudge      int     `json:"would_nudge"`
	WouldDigest     int     `json:"would_digest"`
	Useful          int     `json:"useful"`
	Annoying        int     `json:"annoying"`
	Handled         int     `json:"handled"`
}

// followupStats summarizes the owner-local day starting at dayStart.
func (s *Store) followupStats(dayStart time.Time) (followupDayStats, error) {
	var st followupDayStats
	since := dayStart.UTC().Format(time.RFC3339Nano)
	var usd, ri, rb, mg, lc sql.NullFloat64
	if err := s.db.QueryRow(`SELECT COUNT(*),SUM(loops_created),SUM(rejected_invalid),SUM(rejected_band),SUM(merged),SUM(equivalent_usd) FROM followup_sweeps WHERE started_at>=? AND error=''`, since).Scan(&st.Sweeps, &lc, &ri, &rb, &mg, &usd); err != nil {
		return st, err
	}
	st.LoopsCreated, st.RejectedInvalid, st.RejectedBand, st.Merged, st.SpentUSD = int(lc.Float64), int(ri.Float64), int(rb.Float64), int(mg.Float64), usd.Float64
	if spend, err := s.followupSpendSince(dayStart); err == nil {
		st.SpentUSD = spend // includes failed calls, which also cost
	}
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(band='nudge'),0),COALESCE(SUM(band='digest'),0) FROM open_loops WHERE state IN ('open','digest_only','nudged','snoozed')`).Scan(&st.WouldNudge, &st.WouldDigest); err != nil {
		return st, err
	}
	err := s.db.QueryRow(`SELECT COALESCE(SUM(verdict='useful'),0),COALESCE(SUM(verdict='annoying'),0),COALESCE(SUM(verdict='handled'),0) FROM followup_feedback`).Scan(&st.Useful, &st.Annoying, &st.Handled)
	return st, err
}
