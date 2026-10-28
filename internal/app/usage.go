package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// ContextUsage describes one measured run. Token totals are cumulative model
// traffic, not context occupancy; estimated_input is the last pre-call view.
type ContextUsage struct {
	RunID            string `json:"run_id"`
	ConversationID   string `json:"conversation_id"`
	ConversationName string `json:"conversation_name"`
	BotID            string `json:"bot_id"`
	BotName          string `json:"bot_name"`
	RunStatus        string `json:"run_status"`
	Model            string `json:"model"`
	WindowTokens     int    `json:"window_tokens"`
	WindowKnown      bool   `json:"window_known"`
	CompactAt        int    `json:"compact_at"`
	EstimatedInput   int    `json:"estimated_input"`
	LastInput        int64  `json:"last_input"`
	TotalInput       int64  `json:"total_input"`
	TotalOutput      int64  `json:"total_output"`
	CompactCount     int    `json:"compact_count"`
	UpdatedAt        string `json:"updated_at"`
}

type AgentUsageTotal struct {
	BotID        string `json:"bot_id"`
	BotName      string `json:"bot_name"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Runs         int    `json:"runs"`
	Compactions  int    `json:"compactions"`
}

type UsageCall struct {
	ID               int64   `json:"id"`
	BotID            string  `json:"bot_id"`
	BotName          string  `json:"bot_name"`
	ConversationName string  `json:"conversation_name"`
	Model            string  `json:"model"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	EquivalentUSD    float64 `json:"equivalent_usd"`
	PriceKnown       bool    `json:"price_known"`
	TriggerContent   string  `json:"trigger_content"`
	OccurredAt       string  `json:"occurred_at"`
	// Kind is "chat" (the Bot's own model) or "review" (MCP safety reviewer).
	Kind string `json:"kind"`
}

const (
	usageKindChat   = "chat"
	usageKindReview = "review"
)

type UsagePeriod struct {
	BotID            string  `json:"bot_id"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	Requests         int64   `json:"requests"`
	EquivalentUSD    float64 `json:"equivalent_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
}

// API-equivalent prices are a dated reference, not Codex billing. Cached input
// is priced at 0.1x of the input rate (the cached-input price OpenAI lists for
// the GPT-5 and later families).
func usageReferencePrice(model string) (input, output float64, ok bool) {
	switch strings.TrimPrefix(model, "codex-") {
	case "gpt-6-astra":
		return 10, 50, true
	case "gpt-6-sol":
		return 2, 10, true
	case "gpt-6-luna":
		return .1, .5, true
	case "gpt-5.6", "gpt-5.6-sol":
		return 4, 20, true
	case "gpt-5.6-terra":
		return 2, 12, true
	case "gpt-5.6-luna":
		return .2, 1.2, true
	case "gpt-5.5":
		return 5, 30, true
	case "gpt-5.4":
		return 2.5, 15, true
	default:
		return 0, 0, false
	}
}

// usageEquivalent is the API-list-price equivalent of one model call and
// whether the model has a known price. Codex models use the reference table
// above; every other model uses the provider registry (dated and aliased IDs
// match by prefix), with cache reads and writes at their own rates.
func usageEquivalent(model string, u provider.Usage) (float64, bool) {
	if i, o, ok := usageReferencePrice(model); ok {
		if u.InputTokens > 272_000 {
			i *= 2
			o *= 1.5
		}
		read := min(max(u.CacheReadTokens, 0), u.InputTokens)
		return (float64(u.InputTokens-read)*i + float64(read)*i*0.1 + float64(u.OutputTokens)*o) / 1_000_000, true
	}
	info, ok := provider.GetModelInfo(model)
	if !ok || info.InputCostPer1M <= 0 {
		return 0, false
	}
	return provider.CalculateCost(model, u), true
}

func migrateUsage(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_usage (
	 run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
	 model TEXT NOT NULL, window_tokens INTEGER NOT NULL, window_known INTEGER NOT NULL,
	 estimated_input INTEGER NOT NULL DEFAULT 0, last_input INTEGER NOT NULL DEFAULT 0,
	 total_input INTEGER NOT NULL DEFAULT 0, total_output INTEGER NOT NULL DEFAULT 0,
	 compact_count INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL
	 ); CREATE INDEX IF NOT EXISTS run_usage_updated ON run_usage(updated_at DESC);
	 CREATE TABLE IF NOT EXISTS usage_calls (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	 bot_id TEXT NOT NULL, model TEXT NOT NULL, input_tokens INTEGER NOT NULL,
	 output_tokens INTEGER NOT NULL, equivalent_usd REAL NOT NULL, price_known INTEGER NOT NULL,
	 trigger_content TEXT NOT NULL, occurred_at TEXT NOT NULL);
	 CREATE INDEX IF NOT EXISTS usage_calls_time ON usage_calls(occurred_at DESC);
	 CREATE TABLE IF NOT EXISTS usage_hourly (
	 hour TEXT NOT NULL, bot_id TEXT NOT NULL, input_tokens INTEGER NOT NULL,
	 output_tokens INTEGER NOT NULL, requests INTEGER NOT NULL, equivalent_usd REAL NOT NULL,
	 unpriced_requests INTEGER NOT NULL, PRIMARY KEY(hour,bot_id));
	 CREATE INDEX IF NOT EXISTS usage_hourly_time ON usage_hourly(hour);`)
	if err != nil {
		return err
	}
	// Rows written before this column existed stay "chat": reviewer calls from
	// that era cannot be told apart and remain inside the chat totals.
	if err := ensureColumn(db, "usage_calls", "kind", `ALTER TABLE usage_calls ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat'`); err != nil {
		return err
	}
	// Reviewer spend is rolled up beside, never inside, the chat columns.
	for _, col := range []string{"review_input_tokens", "review_output_tokens", "review_requests", "review_unpriced_requests"} {
		if err := ensureColumn(db, "usage_hourly", col, `ALTER TABLE usage_hourly ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if err := ensureColumn(db, "usage_hourly", "review_equivalent_usd", `ALTER TABLE usage_hourly ADD COLUMN review_equivalent_usd REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// Prompt-cache accounting. cache_reported marks calls whose provider sent
	// cache fields at all; total_cache_input is the input of those calls only,
	// so a hit rate never divides by traffic that could not report a cache.
	for _, col := range []string{"last_cache_read", "last_cache_reported", "total_cache_read", "total_cache_input"} {
		if err := ensureColumn(db, "run_usage", col, `ALTER TABLE run_usage ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	for _, col := range []string{"cache_read_tokens", "cache_write_tokens", "cache_reported"} {
		if err := ensureColumn(db, "usage_calls", col, `ALTER TABLE usage_calls ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	for _, col := range []string{"estimated_system", "estimated_messages", "estimated_reasoning"} {
		if err := ensureColumn(db, "run_usage", col, `ALTER TABLE run_usage ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return pruneUsage(db, time.Now().UTC())
}

func pruneUsage(db *sql.DB, at time.Time) error {
	if _, err := db.Exec(`DELETE FROM usage_calls WHERE occurred_at < ?`, stampTime(at.Add(-7*24*time.Hour))); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM usage_hourly WHERE hour < ?`, at.Add(-31*24*time.Hour).UTC().Format("2006-01-02T15"))
	return err
}

func (s *Store) recordContextEstimate(runID, model string, estimated int) error {
	known := provider.ContextWindowKnown(model)
	window := provider.GetContextWindow(model)
	_, err := s.db.Exec(`INSERT INTO run_usage(run_id,model,window_tokens,window_known,estimated_input,updated_at)
	 VALUES(?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET estimated_input=excluded.estimated_input,updated_at=excluded.updated_at`,
		runID, model, window, known, estimated, now())
	return err
}

// recordContextBreakdown stores the parts of the latest estimate. The row is
// created by recordContextEstimate, which runs first at every report.
func (s *Store) recordContextBreakdown(runID string, b runtime.ContextBreakdown) error {
	_, err := s.db.Exec(`UPDATE run_usage SET estimated_system=?,estimated_messages=?,estimated_reasoning=? WHERE run_id=?`, b.System, b.Messages, b.Reasoning, runID)
	return err
}

func (s *Store) recordModelUsage(runID string, u provider.Usage) error {
	input, output := u.InputTokens, u.OutputTokens
	t := time.Now().UTC()
	stamp := stampTime(t)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var botID, model, prompt string
	err = tx.QueryRow(`SELECT r.bot_id,u.model,COALESCE(m.content,'') FROM runs r JOIN run_usage u ON u.run_id=r.id LEFT JOIN messages m ON m.id=r.trigger_message_id WHERE r.id=?`, runID).Scan(&botID, &model, &prompt)
	if err != nil {
		return err
	}
	price, known := usageEquivalent(model, u)
	var cacheInput int64
	if u.CacheReported {
		cacheInput = input
	}
	if _, err = tx.Exec(`UPDATE run_usage SET last_input=?,total_input=total_input+?,total_output=total_output+?,last_cache_read=?,last_cache_reported=?,total_cache_read=total_cache_read+?,total_cache_input=total_cache_input+?,updated_at=? WHERE run_id=?`, input, input, output, u.CacheReadTokens, u.CacheReported, u.CacheReadTokens, cacheInput, stamp, runID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO usage_calls(run_id,bot_id,model,input_tokens,output_tokens,equivalent_usd,price_known,trigger_content,occurred_at,cache_read_tokens,cache_write_tokens,cache_reported) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, runID, botID, model, input, output, price, known, prompt, stamp, u.CacheReadTokens, u.CacheWriteTokens, u.CacheReported); err != nil {
		return err
	}
	unknown := 0
	if !known {
		unknown = 1
	}
	if _, err = tx.Exec(`INSERT INTO usage_hourly(hour,bot_id,input_tokens,output_tokens,requests,equivalent_usd,unpriced_requests) VALUES(?,?,?,?,?,?,?) ON CONFLICT(hour,bot_id) DO UPDATE SET input_tokens=input_tokens+excluded.input_tokens,output_tokens=output_tokens+excluded.output_tokens,requests=requests+1,equivalent_usd=equivalent_usd+excluded.equivalent_usd,unpriced_requests=unpriced_requests+excluded.unpriced_requests`, t.Format("2006-01-02T15"), botID, input, output, 1, price, unknown); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return pruneUsage(s.db, t)
}

// recordAuxiliaryModelUsage attributes a background model call made on a
// run's behalf (the MCP reviewer) to that run: its own usage_calls row under
// the model actually called, the run's cumulative totals and the hourly
// rollup. last_input stays the main model's context view.
func (s *Store) recordAuxiliaryModelUsage(runID, model string, input, output int64) error {
	t := time.Now().UTC()
	stamp := stampTime(t)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var botID, prompt string
	if err = tx.QueryRow(`SELECT r.bot_id,COALESCE(m.content,'') FROM runs r LEFT JOIN messages m ON m.id=r.trigger_message_id WHERE r.id=?`, runID).Scan(&botID, &prompt); err != nil {
		return err
	}
	price, known := usageEquivalent(model, provider.Usage{InputTokens: input, OutputTokens: output})
	if _, err = tx.Exec(`UPDATE run_usage SET total_input=total_input+?,total_output=total_output+?,updated_at=? WHERE run_id=?`, input, output, stamp, runID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO usage_calls(run_id,bot_id,model,input_tokens,output_tokens,equivalent_usd,price_known,trigger_content,occurred_at,kind) VALUES(?,?,?,?,?,?,?,?,?,?)`, runID, botID, model, input, output, price, known, prompt, stamp, usageKindReview); err != nil {
		return err
	}
	unknown := 0
	if !known {
		unknown = 1
	}
	if _, err = tx.Exec(`INSERT INTO usage_hourly(hour,bot_id,input_tokens,output_tokens,requests,equivalent_usd,unpriced_requests,review_input_tokens,review_output_tokens,review_requests,review_equivalent_usd,review_unpriced_requests) VALUES(?,?,0,0,0,0,0,?,?,1,?,?) ON CONFLICT(hour,bot_id) DO UPDATE SET review_input_tokens=review_input_tokens+excluded.review_input_tokens,review_output_tokens=review_output_tokens+excluded.review_output_tokens,review_requests=review_requests+1,review_equivalent_usd=review_equivalent_usd+excluded.review_equivalent_usd,review_unpriced_requests=review_unpriced_requests+excluded.review_unpriced_requests`, t.Format("2006-01-02T15"), botID, input, output, price, unknown); err != nil {
		return err
	}
	return tx.Commit()
}

// usagePeriod sums one window for either chat or review calls. The two never
// overlap: reviewer calls have kind "review" and their own hourly columns.
func (s *Store) usagePeriod(d time.Duration, kind string) ([]UsagePeriod, error) {
	cutoff := time.Now().UTC().Add(-d)
	// The thirty-day rollup has hour precision; the shorter windows use exact calls.
	// Search fees belong to the chat spend that caused them; they add cost and
	// are not model requests.
	query := `SELECT bot_id,SUM(input_tokens),SUM(output_tokens),SUM(CASE WHEN kind=? THEN 0 ELSE 1 END),SUM(equivalent_usd),SUM(1-price_known) FROM usage_calls WHERE occurred_at>=? AND kind IN (?,?) GROUP BY bot_id`
	searchKind := usageKindSearch
	if kind != usageKindChat {
		searchKind = kind
	}
	args := []any{usageKindSearch, stampTime(cutoff), kind, searchKind}
	if d > 7*24*time.Hour {
		cols := `input_tokens,output_tokens,requests,equivalent_usd,unpriced_requests`
		if kind == usageKindReview {
			cols = `review_input_tokens,review_output_tokens,review_requests,review_equivalent_usd,review_unpriced_requests`
		}
		parts := strings.Split(cols, ",")
		query = `SELECT bot_id,SUM(` + parts[0] + `),SUM(` + parts[1] + `),SUM(` + parts[2] + `),SUM(` + parts[3] + `),SUM(` + parts[4] + `) FROM usage_hourly WHERE hour>=? GROUP BY bot_id HAVING SUM(` + parts[2] + `)>0`
		args = []any{cutoff.Format("2006-01-02T15")}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UsagePeriod, 0)
	for rows.Next() {
		var p UsagePeriod
		if err = rows.Scan(&p.BotID, &p.InputTokens, &p.OutputTokens, &p.Requests, &p.EquivalentUSD, &p.UnpricedRequests); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) listUsageCalls() ([]UsageCall, error) {
	if err := pruneUsage(s.db, time.Now().UTC()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT uc.id,uc.bot_id,COALESCE(b.name,'Deleted Bot'),COALESCE(c.name,'Deleted conversation'),uc.model,uc.input_tokens,uc.output_tokens,uc.equivalent_usd,uc.price_known,uc.trigger_content,uc.occurred_at,uc.kind FROM usage_calls uc JOIN runs r ON r.id=uc.run_id LEFT JOIN bots b ON b.id=uc.bot_id LEFT JOIN conversations c ON c.id=r.conversation_id WHERE uc.kind<>'web_fetch' ORDER BY uc.occurred_at DESC,uc.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UsageCall, 0)
	for rows.Next() {
		var x UsageCall
		var known int
		if err = rows.Scan(&x.ID, &x.BotID, &x.BotName, &x.ConversationName, &x.Model, &x.InputTokens, &x.OutputTokens, &x.EquivalentUSD, &known, &x.TriggerContent, &x.OccurredAt, &x.Kind); err != nil {
			return nil, err
		}
		x.PriceKnown = known != 0
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) recordContextCompact(runID string, compacted int) error {
	_, err := s.db.Exec(`UPDATE run_usage SET estimated_input=?,compact_count=compact_count+1,updated_at=? WHERE run_id=?`, compacted, now(), runID)
	return err
}

const usageSelect = `SELECT u.run_id,r.conversation_id,c.name,r.bot_id,b.name,r.status,u.model,u.window_tokens,u.window_known,
u.estimated_input,u.last_input,u.total_input,u.total_output,u.compact_count,u.updated_at
FROM run_usage u JOIN runs r ON r.id=u.run_id JOIN conversations c ON c.id=r.conversation_id JOIN bots b ON b.id=r.bot_id`

func scanContextUsage(row interface{ Scan(...any) error }) (ContextUsage, error) {
	var u ContextUsage
	var known int
	err := row.Scan(&u.RunID, &u.ConversationID, &u.ConversationName, &u.BotID, &u.BotName, &u.RunStatus, &u.Model, &u.WindowTokens, &known, &u.EstimatedInput, &u.LastInput, &u.TotalInput, &u.TotalOutput, &u.CompactCount, &u.UpdatedAt)
	u.WindowKnown = known != 0
	u.CompactAt = u.WindowTokens * 80 / 100
	return u, err
}

func (s *Store) runContextUsage(runID string) (ContextUsage, error) {
	return scanContextUsage(s.db.QueryRow(usageSelect+` WHERE u.run_id=?`, runID))
}

func (s *Store) listContextUsage() ([]ContextUsage, error) {
	rows, err := s.db.Query(usageSelect + ` WHERE c.user_visible=1 AND u.run_id=(
	 SELECT u2.run_id FROM run_usage u2 JOIN runs r2 ON r2.id=u2.run_id
	 WHERE r2.conversation_id=r.conversation_id AND r2.bot_id=r.bot_id
	 ORDER BY u2.updated_at DESC,u2.run_id DESC LIMIT 1)
	 ORDER BY u.updated_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ContextUsage, 0)
	for rows.Next() {
		u, e := scanContextUsage(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, u)
	}
	return result, rows.Err()
}

func (s *Store) listAgentUsageTotals() ([]AgentUsageTotal, error) {
	rows, err := s.db.Query(`SELECT b.id,b.name,COALESCE(SUM(u.total_input),0),COALESCE(SUM(u.total_output),0),COUNT(u.run_id),COALESCE(SUM(u.compact_count),0)
	 FROM bots b LEFT JOIN runs r ON r.bot_id=b.id LEFT JOIN run_usage u ON u.run_id=r.id
	 WHERE b.archived=0 GROUP BY b.id,b.name ORDER BY b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AgentUsageTotal, 0)
	for rows.Next() {
		var x AgentUsageTotal
		if err = rows.Scan(&x.BotID, &x.BotName, &x.InputTokens, &x.OutputTokens, &x.Runs, &x.Compactions); err != nil {
			return nil, err
		}
		result = append(result, x)
	}
	return result, rows.Err()
}

func (s *Server) routeUsage(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "usage" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	rows, err := s.store.listContextUsage()
	if err != nil {
		writeErr(w, 500, "storage", err.Error())
		return true
	}
	agents, err := s.store.listAgentUsageTotals()
	if err != nil {
		writeErr(w, 500, "storage", err.Error())
		return true
	}
	periods := make(map[string][]UsagePeriod)
	reviewPeriods := make(map[string][]UsagePeriod)
	for key, duration := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour} {
		if periods[key], err = s.store.usagePeriod(duration, usageKindChat); err == nil {
			reviewPeriods[key], err = s.store.usagePeriod(duration, usageKindReview)
		}
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
	}
	calls, err := s.store.listUsageCalls()
	if err != nil {
		writeErr(w, 500, "storage", err.Error())
		return true
	}
	writeJSON(w, 200, map[string]any{"contexts": rows, "agents": agents, "periods": periods, "review_periods": reviewPeriods, "calls": calls, "price_source": "https://developers.openai.com/api/docs/pricing", "price_as_of": "2026-09-25", "note": "API-equivalent estimate only, not Codex billing or savings. Input is priced uncached because cached tokens are not reported. Safety-review calls are reported separately in review_periods. Compaction summary calls are included in chat usage; tool fees are excluded. Request detail retained seven days; thirty-day totals are hour-bucketed."})
	return true
}

func (s *Server) contextUsageTool(runID string) Tool {
	return Tool{Name: "get_context_usage", Description: "Read this run's last measured context input and remaining room before automatic compaction. Use when asked about context or compression; token totals are cumulative traffic, not current occupancy. Values may lag tool results until the next model call.", Parameters: objectSchema(map[string]any{}, nil), Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		u, err := s.store.runContextUsage(runID)
		if err != nil {
			return "", err
		}
		remaining := u.CompactAt - u.EstimatedInput
		if remaining < 0 {
			remaining = 0
		}
		b, err := json.Marshal(map[string]any{"model": u.Model, "window_tokens_local_config": u.WindowTokens, "window_registered_locally": u.WindowKnown, "estimated_input_at_last_model_call": u.EstimatedInput, "actual_input_at_last_model_call": u.LastInput, "tokens_until_compaction_estimate": remaining, "compaction_threshold_fraction": 0.8, "compaction_count_this_run": u.CompactCount, "note": "Window size comes from local model metadata and may differ from the provider. The estimate was captured before the last model call; subsequent tool results can increase context. Cumulative tokens are not context occupancy."})
		return string(b), err
	}}
}

// usageKindSearch marks the fee for provider-side web searches a run's model
// ran. It has no tokens: the searched pages' text is billed as ordinary input
// on the model request that read them.
const usageKindSearch = "web_search"

// webSearchUSD is the provider's list price of one server-side search
// ($10 per 1,000 searches; Anthropic's pricing page, "Web search tool").
// Web fetch has no per-request fee there, so only its page text is billed, as input.
const webSearchUSD = 0.01

// usageKindFetch marks one page a run's model read through the provider's
// server-side fetch tool. The provider charges nothing for the fetch itself
// (the page text is billed as input on the request that read it), so a row
// carries no money and no tokens: it makes the reads countable beside the
// search fees, and it is never a model request.
const usageKindFetch = "web_fetch"

// recordWebFetchUsage books n server-side page reads as n usage_calls rows of
// kind web_fetch priced at zero. The hourly rollup is untouched: no money was
// spent and no request was added.
func (s *Store) recordWebFetchUsage(runID string, n int64) error {
	if n <= 0 {
		return nil
	}
	var botID, model, prompt string
	if err := s.db.QueryRow(`SELECT r.bot_id,u.model,COALESCE(m.content,'') FROM runs r JOIN run_usage u ON u.run_id=r.id LEFT JOIN messages m ON m.id=r.trigger_message_id WHERE r.id=?`, runID).Scan(&botID, &model, &prompt); err != nil {
		return err
	}
	stamp := stampTime(time.Now().UTC())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := int64(0); i < n; i++ {
		if _, err = tx.Exec(`INSERT INTO usage_calls(run_id,bot_id,model,input_tokens,output_tokens,equivalent_usd,price_known,trigger_content,occurred_at,kind) VALUES(?,?,?,0,0,0,1,?,?,?)`, runID, botID, model, prompt, stamp, usageKindFetch); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recordWebSearchUsage books the fee for n server-side searches a run's model
// request ran: its own usage_calls row, and the cost in the bot's hourly
// rollup (no request is added).
func (s *Store) recordWebSearchUsage(runID string, n int64) error {
	if n <= 0 {
		return nil
	}
	t := time.Now().UTC()
	stamp := stampTime(t)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var botID, model, prompt string
	if err = tx.QueryRow(`SELECT r.bot_id,u.model,COALESCE(m.content,'') FROM runs r JOIN run_usage u ON u.run_id=r.id LEFT JOIN messages m ON m.id=r.trigger_message_id WHERE r.id=?`, runID).Scan(&botID, &model, &prompt); err != nil {
		return err
	}
	fee := float64(n) * webSearchUSD
	if _, err = tx.Exec(`INSERT INTO usage_calls(run_id,bot_id,model,input_tokens,output_tokens,equivalent_usd,price_known,trigger_content,occurred_at,kind) VALUES(?,?,?,0,0,?,1,?,?,?)`, runID, botID, model, fee, prompt, stamp, usageKindSearch); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO usage_hourly(hour,bot_id,input_tokens,output_tokens,requests,equivalent_usd,unpriced_requests) VALUES(?,?,0,0,0,?,0) ON CONFLICT(hour,bot_id) DO UPDATE SET equivalent_usd=equivalent_usd+excluded.equivalent_usd`, t.Format("2006-01-02T15"), botID, fee); err != nil {
		return err
	}
	return tx.Commit()
}
