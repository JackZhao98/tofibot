package app

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// GET /api/conversations/{id}/usage: what this conversation has used, how full
// the latest run's context is and what that context is made of. Every number
// is read from rows other features already keep (run_usage, usage_calls,
// run_context_snapshots, tool_activities); nothing is stored for it.

const (
	// usageSplitUnavailable is the stable code for a tool split that cannot be
	// derived: per-call rows live seven days (pruneUsage), so older runs, or runs
	// whose rows do not add up to the run totals, have no per-call detail.
	usageSplitCallDetail = "call_detail_incomplete"
	usageSplitNoUsage    = "no_usage"
	// usageContextNoSnapshot marks parts that could not be listed.
	usageContextNoSnapshot = contextSnapshotUnavailable
)

// Context groups shown by the Usage widget. Fixed order.
const (
	usageGroupInstructions = "instructions"
	usageGroupHistory      = "history"
	usageGroupToolDefs     = "tool_definitions"
	usageGroupToolResults  = "tool_results"
	usageGroupMemory       = "memory_notes"
	usageGroupAttachments  = "attachments"
	usageGroupOther        = "other"
)

var usageGroupOrder = []string{usageGroupInstructions, usageGroupHistory, usageGroupToolDefs, usageGroupToolResults, usageGroupMemory, usageGroupAttachments, usageGroupOther}

// usageGroupOf maps a snapshot part kind to its widget group. Unknown kinds
// land in "other" so a new kind never drops tokens from the sum.
func usageGroupOf(kind string) string {
	switch kind {
	case ctxKindSystemGuidance, ctxKindBotInstruction, ctxKindAgentReport:
		return usageGroupInstructions
	case ctxKindHistory, ctxKindSummary, ctxKindEarlierNote:
		return usageGroupHistory
	case ctxKindTools:
		return usageGroupToolDefs
	case ctxKindCapabilityRefs, "tool_carryover", "wait_result", "form_answers":
		return usageGroupToolResults
	case ctxKindMemory, ctxKindRunContext, ctxKindRequestNote, ctxKindScheduleStates:
		return usageGroupMemory
	}
	return usageGroupOther
}

type ConversationUsageTotals struct {
	Runs             int     `json:"runs"`
	RunsWithCalls    int     `json:"runs_with_call_detail"`
	Requests         int64   `json:"requests"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	EquivalentUSD    float64 `json:"equivalent_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
	// CacheReadTokens are input tokens served from the provider's prompt
	// cache. CacheHitRate is cache reads over the input of the calls whose
	// provider reported cache fields; null when none did.
	CacheReadTokens int64    `json:"cache_read_tokens"`
	CacheHitRate    *float64 `json:"cache_hit_rate"`
}

type ConversationUsageModel struct {
	Model            string  `json:"model"`
	Requests         int64   `json:"requests"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	EquivalentUSD    float64 `json:"equivalent_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
}

type ConversationContextPart struct {
	Kind      string `json:"kind"`
	Tokens    int    `json:"tokens"`
	Estimated bool   `json:"estimated"`
}

type ConversationLatestContext struct {
	RunID        string `json:"run_id"`
	Model        string `json:"model"`
	UsedTokens   int    `json:"used_tokens"`
	UsedMeasured bool   `json:"used_measured"`
	WindowTokens int    `json:"window_tokens"`
	WindowKnown  bool   `json:"window_known"`
	// CacheReadTokens and CacheHitRate describe the last model call: the part
	// of its input served from the prompt cache. The rate is null when the
	// provider reported no cache fields for that call.
	CacheReadTokens int64    `json:"cache_read_tokens"`
	CacheHitRate    *float64 `json:"cache_hit_rate"`
	// Parts describe the run's first model call (the stored snapshot); they
	// are estimates. PartsCode is set, and Parts empty, when no snapshot exists.
	Parts       []ConversationContextPart `json:"parts"`
	PartsTokens int                       `json:"parts_tokens"`
	PartsCode   string                    `json:"parts_code,omitempty"`
}

type ConversationToolSplit struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	// Tokens are input+output of provider-reported model calls. ToolTokens:
	// calls after which the run started at least one tool before its next
	// model call. ConversationTokens: all other chat calls. ReviewTokens:
	// safety-reviewer calls, counted in neither.
	ToolTokens         int64  `json:"tool_tokens"`
	ConversationTokens int64  `json:"conversation_tokens"`
	ReviewTokens       int64  `json:"review_tokens"`
	ToolModelCalls     int64  `json:"tool_model_calls"`
	ConversationCalls  int64  `json:"conversation_model_calls"`
	ToolCalls          int64  `json:"tool_calls"`
	Basis              string `json:"basis,omitempty"`
}

type ConversationUsage struct {
	ConversationID string                     `json:"conversation_id"`
	Totals         ConversationUsageTotals    `json:"totals"`
	PerModel       []ConversationUsageModel   `json:"per_model"`
	LatestContext  *ConversationLatestContext `json:"latest_context"`
	ToolSplit      ConversationToolSplit      `json:"tool_split"`
}

type convUsageRun struct {
	RunID               string
	Model               string
	TotalIn, TotalOut   int64
	WindowTokens        int
	WindowKnown         bool
	EstimatedIn, LastIn int64
	// Cache accounting: the last call's reads and whether it reported cache
	// fields, the run's total reads, and the input of reporting calls.
	LastCacheRead, TotalCacheRead, TotalCacheInput int64
	LastCacheReported                              bool
}

// cacheHitRate is reads over the input they were counted against, or nil
// when no input reported a cache (a rate of zero would claim a miss).
func cacheHitRate(read, input int64) *float64 {
	if input <= 0 {
		return nil
	}
	rate := float64(min(max(read, 0), input)) / float64(input)
	return &rate
}

type convUsageCall struct {
	RunID, Kind, Model string
	In, Out            int64
	USD                float64
	PriceKnown         bool
	At                 string
}

// computeConversationUsage is the pure aggregation. runs are in update order
// (the last is the latest); calls are ordered by run, time, id; toolStarts maps
// run id to the start stamps of its tool calls.
func computeConversationUsage(id string, runs []convUsageRun, calls []convUsageCall, toolStarts map[string][]string, snap *ContextSnapshot) ConversationUsage {
	out := ConversationUsage{ConversationID: id, PerModel: []ConversationUsageModel{}}
	byRun := map[string][]convUsageCall{}
	for _, c := range calls {
		byRun[c.RunID] = append(byRun[c.RunID], c)
	}
	models := map[string]*ConversationUsageModel{}
	split := ConversationToolSplit{Basis: "model_calls_followed_by_tool_start"}
	complete := len(runs) > 0
	var cacheInput int64
	for _, r := range runs {
		out.Totals.Runs++
		out.Totals.InputTokens += r.TotalIn
		out.Totals.OutputTokens += r.TotalOut
		out.Totals.CacheReadTokens += r.TotalCacheRead
		cacheInput += r.TotalCacheInput
		cs := byRun[r.RunID]
		var in, o int64
		for _, c := range cs {
			if c.Kind == usageKindChat || c.Kind == usageKindReview {
				in += c.In
				o += c.Out
			}
		}
		// Per-call rows survive seven days; a run is detailed only when they
		// add up to its cumulative totals (nothing expired or missing).
		if in != r.TotalIn || o != r.TotalOut || (r.TotalIn+r.TotalOut > 0 && len(cs) == 0) {
			complete = false
			continue
		}
		out.Totals.RunsWithCalls++
		starts := toolStarts[r.RunID]
		var chat []convUsageCall
		for _, c := range cs {
			switch c.Kind {
			case usageKindChat, usageKindReview:
				out.Totals.Requests++
				if !c.PriceKnown {
					out.Totals.UnpricedRequests++
				}
				m := models[c.Model]
				if m == nil {
					m = &ConversationUsageModel{Model: c.Model}
					models[c.Model] = m
				}
				m.Requests++
				m.InputTokens += c.In
				m.OutputTokens += c.Out
				m.EquivalentUSD += c.USD
				if !c.PriceKnown {
					m.UnpricedRequests++
				}
				if c.Kind == usageKindChat {
					chat = append(chat, c)
				} else {
					split.ReviewTokens += c.In + c.Out
				}
			case usageKindSearch:
				// A search fee is cost without tokens; it belongs to the model that ran it.
			}
			out.Totals.EquivalentUSD += c.USD
			if c.Kind == usageKindSearch {
				if m := models[c.Model]; m != nil {
					m.EquivalentUSD += c.USD
				} else {
					models[c.Model] = &ConversationUsageModel{Model: c.Model, EquivalentUSD: c.USD}
				}
			}
		}
		for i, c := range chat {
			end := ""
			if i+1 < len(chat) {
				end = chat[i+1].At
			}
			drove := false
			for _, st := range starts {
				if st >= c.At && (end == "" || st < end) {
					drove = true
					break
				}
			}
			if drove {
				split.ToolTokens += c.In + c.Out
				split.ToolModelCalls++
			} else {
				split.ConversationTokens += c.In + c.Out
				split.ConversationCalls++
			}
		}
	}
	for _, starts := range toolStarts {
		split.ToolCalls += int64(len(starts))
	}
	switch {
	case len(runs) == 0:
		out.ToolSplit = ConversationToolSplit{Code: usageSplitNoUsage}
	case !complete:
		out.ToolSplit = ConversationToolSplit{Code: usageSplitCallDetail, ToolCalls: split.ToolCalls}
	default:
		split.Available = true
		out.ToolSplit = split
	}
	out.Totals.CacheHitRate = cacheHitRate(out.Totals.CacheReadTokens, cacheInput)
	for _, m := range models {
		out.PerModel = append(out.PerModel, *m)
	}
	sort.Slice(out.PerModel, func(i, j int) bool {
		a, b := out.PerModel[i], out.PerModel[j]
		if a.InputTokens+a.OutputTokens != b.InputTokens+b.OutputTokens {
			return a.InputTokens+a.OutputTokens > b.InputTokens+b.OutputTokens
		}
		return a.Model < b.Model
	})
	if len(runs) > 0 {
		out.LatestContext = latestContext(runs[len(runs)-1], snap)
	}
	return out
}

func latestContext(r convUsageRun, snap *ContextSnapshot) *ConversationLatestContext {
	lc := &ConversationLatestContext{RunID: r.RunID, Model: r.Model, WindowTokens: r.WindowTokens, WindowKnown: r.WindowKnown, Parts: []ConversationContextPart{}}
	// A window stored from the heuristic is replaced once the model's real
	// window is known (the provider catalog can arrive after the run began).
	if lc.WindowTokens <= 0 || !lc.WindowKnown {
		lc.WindowTokens = provider.GetContextWindow(r.Model)
		lc.WindowKnown = provider.ContextWindowKnown(r.Model)
	}
	if r.LastCacheReported {
		lc.CacheReadTokens = r.LastCacheRead
		lc.CacheHitRate = cacheHitRate(r.LastCacheRead, r.LastIn)
	}
	// The provider-reported input of the last call is what the window held;
	// before any call returned, only the pre-call estimate exists.
	if r.LastIn > 0 {
		lc.UsedTokens, lc.UsedMeasured = int(r.LastIn), true
	} else {
		lc.UsedTokens = int(r.EstimatedIn)
	}
	if snap == nil {
		lc.PartsCode = usageContextNoSnapshot
		return lc
	}
	sums := map[string]int{}
	for _, p := range snap.Parts {
		if p.OnDemand {
			continue
		}
		t := p.Tokens
		if p.Message != nil && p.Message.Images > 0 {
			img := p.Message.Images * contextImageTokens
			if img > t {
				img = t
			}
			sums[usageGroupAttachments] += img
			t -= img
		}
		sums[usageGroupOf(p.Kind)] += t
	}
	for _, g := range usageGroupOrder {
		if sums[g] > 0 {
			lc.Parts = append(lc.Parts, ConversationContextPart{Kind: g, Tokens: sums[g], Estimated: true})
			lc.PartsTokens += sums[g]
		}
	}
	return lc
}

func (s *Store) conversationUsage(id string) (ConversationUsage, error) {
	var runs []convUsageRun
	rows, err := s.db.Query(`SELECT u.run_id,u.model,u.total_input,u.total_output,u.window_tokens,u.window_known,u.estimated_input,u.last_input,u.last_cache_read,u.last_cache_reported,u.total_cache_read,u.total_cache_input
	 FROM run_usage u JOIN runs r ON r.id=u.run_id WHERE r.conversation_id=? ORDER BY u.updated_at ASC,u.run_id ASC`, id)
	if err != nil {
		return ConversationUsage{}, err
	}
	for rows.Next() {
		var x convUsageRun
		var known, cacheReported int
		if err = rows.Scan(&x.RunID, &x.Model, &x.TotalIn, &x.TotalOut, &x.WindowTokens, &known, &x.EstimatedIn, &x.LastIn, &x.LastCacheRead, &cacheReported, &x.TotalCacheRead, &x.TotalCacheInput); err != nil {
			rows.Close()
			return ConversationUsage{}, err
		}
		x.WindowKnown = known != 0
		x.LastCacheReported = cacheReported != 0
		runs = append(runs, x)
	}
	if err = rows.Close(); err != nil {
		return ConversationUsage{}, err
	}
	var calls []convUsageCall
	rows, err = s.db.Query(`SELECT uc.run_id,uc.kind,uc.model,uc.input_tokens,uc.output_tokens,uc.equivalent_usd,uc.price_known,uc.occurred_at
	 FROM usage_calls uc JOIN runs r ON r.id=uc.run_id WHERE r.conversation_id=? ORDER BY uc.run_id,uc.occurred_at,uc.id`, id)
	if err != nil {
		return ConversationUsage{}, err
	}
	for rows.Next() {
		var x convUsageCall
		var known int
		if err = rows.Scan(&x.RunID, &x.Kind, &x.Model, &x.In, &x.Out, &x.USD, &known, &x.At); err != nil {
			rows.Close()
			return ConversationUsage{}, err
		}
		x.PriceKnown = known != 0
		calls = append(calls, x)
	}
	if err = rows.Close(); err != nil {
		return ConversationUsage{}, err
	}
	starts := map[string][]string{}
	rows, err = s.db.Query(`SELECT run_id,started_at FROM tool_activities WHERE conversation_id=? ORDER BY run_id,started_at`, id)
	if err != nil {
		return ConversationUsage{}, err
	}
	for rows.Next() {
		var run, at string
		if err = rows.Scan(&run, &at); err != nil {
			rows.Close()
			return ConversationUsage{}, err
		}
		starts[run] = append(starts[run], at)
	}
	if err = rows.Close(); err != nil {
		return ConversationUsage{}, err
	}
	var snap *ContextSnapshot
	if len(runs) > 0 {
		v, e := s.runContextSnapshot(runs[len(runs)-1].RunID)
		switch {
		case e == nil:
			snap = &v
		case errors.Is(e, sql.ErrNoRows):
		default:
			return ConversationUsage{}, e
		}
	}
	return computeConversationUsage(id, runs, calls, starts, snap), nil
}

// routeConversationUsage handles GET /api/conversations/{id}/usage behind the
// owner session gate, like /tools.
func (s *Server) routeConversationUsage(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 || parts[0] != "conversations" || parts[2] != "usage" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
		return true
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil || id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid conversation id")
		return true
	}
	if _, err = s.store.GetConversation(id); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return true
	}
	u, err := s.store.conversationUsage(id)
	if err != nil {
		log.Printf("[usage] conversation %s: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "storage", "could not read usage")
		return true
	}
	writeJSON(w, http.StatusOK, u)
	return true
}
