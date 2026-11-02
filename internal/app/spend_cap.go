package app

// Monthly spend cap (item #9). The cap is one automation setting,
// monthly_spend_limit_usd (whole dollars, 0 = off, the default). Spend is the
// equivalent_usd the usage accounting already computes, summed over the
// calendar month in the account's time zone, for models that bill per call
// (API keys, and compatible endpoints that have a price). A ChatGPT/Codex
// sign-in has no per-call price: it is never counted and never refused. At 80%
// the owner gets one in-app notice. At 100% a new run that would call a billed
// model fails before the call with the typed code spend_cap_reached; a run in
// progress stops before its next billed call. The ledger keeps one row per billed call, because
// usage_calls keeps seven days only.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

const (
	autoMonthlySpendLimit = "monthly_spend_limit_usd"
	spendCapReachedCode   = "spend_cap_reached"
	spendAlertPercent     = 80
	spendLedgerKeepDays   = 70
	maxSpendLimitUSD      = 100000
)

func migrateSpendLedger(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS spend_ledger (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at TEXT NOT NULL, usd REAL NOT NULL);
	 CREATE INDEX IF NOT EXISTS spend_ledger_time ON spend_ledger(occurred_at);
	 CREATE TABLE IF NOT EXISTS spend_alerts (
	 month TEXT NOT NULL, limit_usd INTEGER NOT NULL, percent INTEGER NOT NULL, sent_at TEXT NOT NULL,
	 PRIMARY KEY(month, limit_usd, percent));`)
	return err
}

// modelBilledPerCall says whether a call to the model costs money per call.
// Codex models run on a plan (no per-call price). Claude and OpenAI API models
// bill. A compatible (local or hosted) endpoint bills only when its model has a
// price in the registry; an unpriced one counts as 0.
func modelBilledPerCall(model string) bool {
	switch provider.ProviderForModel(model) {
	case providerCodex:
		return false
	case providerOpenAI, providerAnthropic:
		return true
	}
	_, priced := usageEquivalent(model, provider.Usage{InputTokens: 1, OutputTokens: 1})
	return priced
}

// bookSpendTx adds one call's equivalent price to the ledger when the model bills per call.
func bookSpendTx(tx *sql.Tx, at time.Time, model string, usd float64) error {
	if usd <= 0 || !modelBilledPerCall(model) {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO spend_ledger(occurred_at,usd) VALUES(?,?)`, stampTime(at), usd)
	return err
}

func pruneSpendLedger(db *sql.DB, at time.Time) error {
	_, err := db.Exec(`DELETE FROM spend_ledger WHERE occurred_at < ?`, stampTime(at.Add(-spendLedgerKeepDays*24*time.Hour)))
	return err
}

// spendLocation is the account's time zone, or UTC when none is set.
func (s *Store) spendLocation() *time.Location {
	if zone, err := s.userTimezone(); err == nil && zone != "" {
		if loc, e := time.LoadLocation(zone); e == nil {
			return loc
		}
	}
	return time.UTC
}

// spendMonth is the calendar month (in loc) that holds at: its key and bounds.
func spendMonth(at time.Time, loc *time.Location) (key string, start, end time.Time) {
	local := at.In(loc)
	start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return start.Format("2006-01"), start, start.AddDate(0, 1, 0)
}

// SpendStatus is the month's spend against the limit.
type SpendStatus struct {
	Month      string  `json:"month"`
	MonthStart string  `json:"month_start"`
	MonthEnd   string  `json:"month_end"`
	Timezone   string  `json:"timezone"`
	SpendUSD   float64 `json:"spend_usd"`
	LimitUSD   int     `json:"limit_usd"`
	// State is off (no limit), ok, near (at or past 80%) or reached (at or past 100%).
	State string `json:"state"`
}

func (s *Store) spendStatus(at time.Time) (SpendStatus, error) {
	loc := s.spendLocation()
	key, start, end := spendMonth(at, loc)
	out := SpendStatus{Month: key, MonthStart: stampTime(start), MonthEnd: stampTime(end), Timezone: loc.String(), LimitUSD: autoIntTx(s.db, autoMonthlySpendLimit)}
	var sum sql.NullFloat64
	if err := s.db.QueryRow(`SELECT SUM(usd) FROM spend_ledger WHERE occurred_at>=? AND occurred_at<?`, stampTime(start), stampTime(end)).Scan(&sum); err != nil {
		return out, err
	}
	out.SpendUSD = sum.Float64
	out.State = spendState(out.SpendUSD, out.LimitUSD)
	return out, nil
}

// spendState is off (no limit), ok, near (at or past 80%) or reached (at or past 100%).
func spendState(spend float64, limit int) string {
	switch {
	case limit <= 0:
		return "off"
	case spend >= float64(limit):
		return "reached"
	case spend*100 >= float64(limit*spendAlertPercent):
		return "near"
	}
	return "ok"
}

// spendCapError refuses a run before its first model call. Its text is read by
// the model of a parent run and by the person: plain statements, no examples.
type spendCapError struct{ status SpendStatus }

func (e *spendCapError) Error() string {
	return fmt.Sprintf("%s: The monthly spend limit of $%d is reached. The spend this month is $%.2f. This run did not call the model. The owner can change the limit in Settings > Usage.", spendCapReachedCode, e.status.LimitUSD, e.status.SpendUSD)
}

// checkSpendCap returns a refusal when the month's spend is at the limit and
// the model bills per call. A model on a plan, an unpriced local model and a
// missing limit always pass.
func (s *Store) checkSpendCap(model string, at time.Time) error {
	if autoIntTx(s.db, autoMonthlySpendLimit) <= 0 || !modelBilledPerCall(model) {
		return nil
	}
	st, err := s.spendStatus(at)
	if err != nil {
		return nil // a read failure never stops work
	}
	if st.State == "reached" {
		return &spendCapError{status: st}
	}
	return nil
}

// spendPercent is the whole percent of the limit already spent (0 with no limit).
func spendPercent(st SpendStatus) int {
	if st.LimitUSD <= 0 {
		return 0
	}
	return int(st.SpendUSD * 100 / float64(st.LimitUSD))
}

// refuseBackground checks the cap for an unattended billed call (follow-up
// sweep, reviewer, grader, tool-search expansion). A refusal is logged once
// per month and limit, never posted to the chat.
func (s *Store) refuseBackground(model, what string) error {
	err := s.checkSpendCap(model, time.Now())
	var capErr *spendCapError
	if errors.As(err, &capErr) {
		if _, seen := s.spendRefusalLogged.LoadOrStore(fmt.Sprintf("%s/%d", capErr.status.Month, capErr.status.LimitUSD), true); !seen {
			log.Printf("[spend] limit reached; %s and other background model calls are refused until the month ends or the limit is raised", what)
		}
	}
	return err
}

// spendCapFailureMessage is the sentence a refused run shows (the error text without its code).
func spendCapFailureMessage(errorText string) string {
	_, rest, _ := strings.Cut(errorText, ": ")
	return rest
}

// noteSpend runs after a billed call is stored: at 80% of the limit it posts
// one notice per month and limit, in the Primary Assistant's chat.
func (s *Store) noteSpend(at time.Time) {
	s.publishSpend(at)
	if autoIntTx(s.db, autoMonthlySpendLimit) <= 0 {
		return
	}
	st, err := s.spendStatus(at)
	if err != nil || (st.State != "near" && st.State != "reached") {
		return
	}
	if err := s.postSpendAlert(st); err != nil {
		log.Printf("[spend] alert: %v", err)
	}
}

// publishSpend hands the month's status to the gateway, so the admin view
// reads a table and never opens a workspace.
func (s *Store) publishSpend(at time.Time) {
	if s.spendSnapshot == nil {
		return
	}
	if st, err := s.spendStatus(at); err == nil {
		s.spendSnapshot(st)
	}
}

func (s *Store) postSpendAlert(st SpendStatus) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO spend_alerts(month,limit_usd,percent,sent_at) VALUES(?,?,?,?)`, st.Month, st.LimitUSD, spendAlertPercent, now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	botID, err := primaryBotTx(tx)
	if err != nil || botID == "" {
		return err // no Bot, no chat: the Usage page still shows the state
	}
	var conv string
	if err = tx.QueryRow(`SELECT dm_conversation_id FROM bots WHERE id=?`, botID).Scan(&conv); err != nil {
		return err
	}
	t := now()
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return err
	}
	notice := &HandoffNotice{Type: "spend_alert", Reason: st.State, FromBotID: botID, ToBotID: botID, TargetConversationID: conv, OldValue: fmt.Sprintf("%d", st.LimitUSD), NewValue: fmt.Sprintf("%.2f", st.SpendUSD)}
	data, _ := json.Marshal(notice)
	m := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: "system", Content: fmt.Sprintf("This month's spend is $%.2f of the $%d limit (%d%%). Model calls that bill per use stop at the limit. Change it in Settings > Usage.", st.SpendUSD, st.LimitUSD, spendPercent(st)), Notice: notice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, nil, m.Content, string(data), t); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, conv, "message", m, t); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv); err != nil {
		return err
	}
	return tx.Commit()
}

// routeSpend serves GET /api/usage/spend: the month's spend and the limit.
func (s *Server) routeSpend(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "usage/spend" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	st, err := s.store.spendStatus(time.Now())
	if err != nil {
		writeErr(w, 500, "storage", "spend unavailable")
		return true
	}
	writeJSON(w, 200, st)
	return true
}
