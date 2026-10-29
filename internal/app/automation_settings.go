package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Automation settings: every behaviour the system performs on the owner's
// behalf is one item here, with a default, a current value and a way to change
// it. The registry below is the inventory; the Settings page renders it as is.
//
// Resolution order for a stored item: environment override (wins and locks the
// control), then the stored value, then the default. A few items live in older
// tables (the model default, structured replies, MCP auto-review mode); they
// are linked, so the page and the older surfaces always agree.
//
// Reads are on hot paths (every run, every history build), so stored values are
// cached in memory and every write updates the cache: a change applies to the
// next run without a restart.

const (
	autoConversationModel     = "conversation_model"
	autoBackgroundModel       = "background_model"
	autoStructuredOutput      = "structured_output"
	autoForceSayHaikuDM       = "force_say_haiku_dm"
	autoWebSearch             = "web_search"
	autoGroupExchangeGists    = "group_exchange_gists"
	autoOwnRepliesVerbatim    = "own_replies_verbatim"
	autoCompletionReview      = "completion_review"
	autoAnswerGate            = "answer_gate"
	autoMCPReview             = "mcp_auto_review"
	autoRememberedApprovalHrs = "remembered_approval_hours"
	autoProviderRetries       = "provider_retries"
	autoScheduleReceiptReview = "schedule_receipt_review"
	autoDesktopIdle           = "desktop_idle_seconds"
	autoGroupTriage           = "group_triage"
	autoGroupTriageModel      = "group_triage_model"
	autoGroupFollowUp         = "group_followup_to_working"
	// The web workspace reads the two widget switches (docs/design/widgets-and-sidebar.md); the server stores them and nothing else.
	autoWidgetArtifacts = "widget_artifacts_auto"
	autoWidgetComputer  = "widget_computer_auto"

	// defaultConversationModel is what conversations, scheduled runs and tool
	// use run on until the owner picks another model.
	defaultConversationModel = "claude-sonnet-5-5"
	// anthropicBackgroundModel is the default for background work when the
	// conversation default is an Anthropic model.
	anthropicBackgroundModel = "claude-haiku-5-5"
	// codexDefaultModel is the small Codex model for background work beside a
	// Codex conversation default (as Haiku is beside a Claude one).
	codexDefaultModel = "codex-gpt-5.6-luna"
	// codexConversationFallback is the conversation default for a Codex-only
	// workspace while the live catalog is not loaded; with a catalog, the newest
	// Sol in it wins (codexConversationDefault).
	codexConversationFallback = "codex-gpt-6-sol"

	defaultProviderRetries = 8
)

type automationKind string

const (
	autoKindBool     automationKind = "bool"
	autoKindInt      automationKind = "int"
	autoKindEnum     automationKind = "enum"
	autoKindModel    automationKind = "model"
	autoKindReadOnly automationKind = "readonly"
)

type automationItem struct {
	Key, Group string
	Kind       automationKind
	Default    string // canonical: "true"/"false", decimal, enum value or model ID
	Env        string // environment variable that overrides the stored value
	Min, Max   int
	Unit       string
	Options    []string
}

// automationItems is the inventory, in display order.
var automationItems = []automationItem{
	{Key: autoConversationModel, Group: "models", Kind: autoKindModel, Default: defaultConversationModel},
	{Key: autoBackgroundModel, Group: "models", Kind: autoKindModel, Env: "TOFI_BACKGROUND_MODEL"},
	{Key: autoStructuredOutput, Group: "replies", Kind: autoKindBool, Default: "true"},
	{Key: autoForceSayHaikuDM, Group: "replies", Kind: autoKindBool, Default: "true", Env: "TOFI_FORCE_SAY"},
	{Key: autoWebSearch, Group: "replies", Kind: autoKindBool, Default: "true", Env: "TOFI_WEB_SEARCH"},
	{Key: autoGroupExchangeGists, Group: "history", Kind: autoKindBool, Default: "true"},
	{Key: autoOwnRepliesVerbatim, Group: "history", Kind: autoKindInt, Default: strconv.Itoa(ownVerbatimReplies), Min: 1, Max: 20},
	{Key: autoTaskRetentionDays, Group: "history", Kind: autoKindInt, Default: taskRetentionDefault(), Min: 0, Max: 3650, Unit: "days"},
	{Key: autoCompletionReview, Group: "checks", Kind: autoKindBool, Default: "true"},
	{Key: autoAnswerGate, Group: "checks", Kind: autoKindInt, Default: "2", Min: 0, Max: 5},
	{Key: autoMCPReview, Group: "checks", Kind: autoKindEnum, Default: "off", Options: []string{"off", "shadow", "auto"}},
	{Key: autoRememberedApprovalHrs, Group: "checks", Kind: autoKindInt, Default: strconv.Itoa(int(rememberedApprovalValidity / time.Hour)), Min: 1, Max: 720, Unit: "hours"},
	{Key: autoProviderRetries, Group: "reliability", Kind: autoKindInt, Default: strconv.Itoa(defaultProviderRetries), Min: 0, Max: 10, Env: "TOFI_PROVIDER_RETRIES"},
	{Key: autoScheduleReceiptReview, Group: "schedules", Kind: autoKindBool, Default: "true"},
	{Key: autoDesktopIdle, Group: "computer", Kind: autoKindReadOnly, Unit: "seconds"},
	{Key: autoGroupTriage, Group: "replies", Kind: autoKindBool, Default: "true"},
	{Key: autoGroupTriageModel, Group: "models", Kind: autoKindModel},
	{Key: autoGroupFollowUp, Group: "replies", Kind: autoKindBool, Default: "true"},
	{Key: autoCommandCheckMinutes, Group: "computer", Kind: autoKindInt, Default: commandCheckMinutesDefault(), Min: 5, Max: 1440, Unit: "minutes"},
	{Key: autoTaskStallRounds, Group: "checks", Kind: autoKindInt, Default: strconv.Itoa(defaultTaskStallRounds), Min: 1, Max: 20},
	{Key: autoTaskTreeIdleLimit, Group: "reliability", Kind: autoKindInt, Default: strconv.Itoa(defaultTaskTreeIdleLimit), Min: 10, Max: 500},
	{Key: autoWidgetArtifacts, Group: "widgets", Kind: autoKindBool, Default: "true"},
	{Key: autoWidgetComputer, Group: "widgets", Kind: autoKindBool, Default: "true"},
}

func automationItemFor(key string) (automationItem, bool) {
	for _, item := range automationItems {
		if item.Key == key {
			return item, true
		}
	}
	return automationItem{}, false
}

// automationCache holds the stored values; the zero value is ready and loads
// from the store on first use.
type automationCache struct {
	mu     sync.RWMutex
	loaded bool
	values map[string]string
}

func migrateAutomationSettings(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS automation_settings(
key TEXT PRIMARY KEY,
value TEXT NOT NULL,
updated_at TEXT NOT NULL)`)
	if err != nil {
		return err
	}
	// task_tree_message_limit became task_tree_idle_message_limit (decision 0064,
	// amended 2026-10-24): a number the person set stays; otherwise the new default.
	if _, err = db.Exec(`INSERT OR IGNORE INTO automation_settings(key,value,updated_at) SELECT ?,value,updated_at FROM automation_settings WHERE key=?`, autoTaskTreeIdleLimit, autoTaskTreeOldLimit); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM automation_settings WHERE key=?`, autoTaskTreeOldLimit)
	return err
}

func (s *Store) automationValues() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key,value FROM automation_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// putAutomationValue stores a value, or clears it (back to the default) when
// value is nil.
func (s *Store) putAutomationValue(key string, value *string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if value == nil {
		_, err = tx.Exec(`DELETE FROM automation_settings WHERE key=?`, key)
	} else {
		_, err = tx.Exec(`INSERT INTO automation_settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, *value, now())
	}
	if err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// autoStored returns the stored value of a plain item.
func (s *Server) autoStored(key string) (string, bool) {
	c := &s.automation
	c.mu.RLock()
	if c.loaded {
		v, ok := c.values[key]
		c.mu.RUnlock()
		return v, ok
	}
	c.mu.RUnlock()
	if s.store == nil {
		return "", false
	}
	values, err := s.store.automationValues()
	if err != nil {
		return "", false // an unreadable store means the defaults; try again next read
	}
	c.mu.Lock()
	if !c.loaded {
		c.values, c.loaded = values, true
	}
	v, ok := c.values[key]
	c.mu.Unlock()
	return v, ok
}

func (s *Server) saveAutomationValue(key string, value *string) error {
	if err := s.store.putAutomationValue(key, value); err != nil {
		return err
	}
	c := &s.automation
	c.mu.Lock()
	if c.loaded {
		if value == nil {
			delete(c.values, key)
		} else {
			c.values[key] = *value
		}
	}
	c.mu.Unlock()
	return nil
}

// autoEnv reads an item's environment override as a canonical value; ok is
// false when the variable is unset or does not hold a valid value.
func autoEnv(item automationItem) (string, bool) {
	if item.Env == "" {
		return "", false
	}
	raw := strings.TrimSpace(os.Getenv(item.Env))
	if raw == "" {
		return "", false
	}
	switch item.Kind {
	case autoKindBool:
		switch strings.ToLower(raw) {
		case "on", "true", "1", "yes":
			return "true", true
		case "off", "false", "0", "no":
			return "false", true
		}
	case autoKindInt:
		if n, err := strconv.Atoi(raw); err == nil && n >= item.Min && n <= item.Max {
			return strconv.Itoa(n), true
		}
	case autoKindModel:
		return raw, true
	}
	return "", false
}

// autoValue is the canonical value in force for a plain (stored) item:
// environment, then stored, then default.
func (s *Server) autoValue(key string) string {
	item, ok := automationItemFor(key)
	if !ok {
		return ""
	}
	if v, ok := autoEnv(item); ok {
		return v
	}
	if v, ok := s.autoStored(key); ok {
		return v
	}
	return item.Default
}

func (s *Server) autoBool(key string) bool { return s.autoValue(key) == "true" }

func (s *Server) autoInt(key string) int {
	n, err := strconv.Atoi(s.autoValue(key))
	if err != nil {
		item, _ := automationItemFor(key)
		n, _ = strconv.Atoi(item.Default)
	}
	return n
}

// The accessors below are the read points the rest of the server uses. Each
// is cheap enough for a hot path.

// forceSayHaikuDMEnabled says whether a Haiku run in a direct message must
// call a tool on every model turn (tool_choice required), so its reply goes
// through say. Read at the ForceToolUse call site of a run.
func (s *Server) forceSayHaikuDMEnabled() bool { return s.autoBool(autoForceSayHaikuDM) }

// webSearchEnabled says whether Anthropic runs may use server-side web search
// and the server-side page reader that goes with it.
// TOFI_WEB_SEARCH=off still disables it inside webSearchMaxUses; this adds the
// stored switch.
func (s *Server) webSearchEnabled() bool { return s.autoBool(autoWebSearch) }

// searchUses is webSearchMaxUses behind the stored switch.
func (s *Server) searchUses(model string, tools []Tool) int {
	if !s.webSearchEnabled() {
		return 0
	}
	return webSearchMaxUses(model, tools)
}

// fetchUses is webFetchMaxUses behind the same stored switch as search.
func (s *Server) fetchUses(model string, tools []Tool) int {
	if !s.webSearchEnabled() {
		return 0
	}
	return webFetchMaxUses(model, tools)
}

// automaticModelItem names the model items that may be left on "automatic"
// (an empty stored value) instead of holding a concrete model.
func automaticModelItem(key string) bool {
	return key == autoBackgroundModel || key == autoGroupTriageModel
}

// groupTriageEnabled says whether a group message that names no one is routed
// by a background model to the best-suited member. Off restores the older rule
// (the Primary Assistant, else a random member).
func (s *Server) groupTriageEnabled() bool { return s.autoBool(autoGroupTriage) }

// groupFollowUpEnabled says whether an unaddressed group message sent while a
// member still works on the person's previous message goes to that member as
// queued steering instead of a new triage. Off triages every such message.
func (s *Server) groupFollowUpEnabled() bool { return s.autoBool(autoGroupFollowUp) }

// groupTriageModel is the model that routes unaddressed group messages: the
// one chosen for this item, else the background model.
func (s *Server) groupTriageModel() string {
	if stored, ok := s.autoStored(autoGroupTriageModel); ok && stored != "" {
		return stored
	}
	return s.backgroundModel(backgroundTriage)
}

func (s *Server) groupExchangeGistsEnabled() bool { return s.autoBool(autoGroupExchangeGists) }
func (s *Server) ownRepliesVerbatim() int         { return s.autoInt(autoOwnRepliesVerbatim) }
func (s *Server) completionReviewEnabled() bool   { return s.autoBool(autoCompletionReview) }

// answerGateRounds is how many times a draft reply is graded and sent back
// before the latest draft ships; 0 turns the gate off.
func (s *Server) answerGateRounds() int { return s.autoInt(autoAnswerGate) }
func (s *Server) scheduleReceiptReviewEnabled() bool {
	return s.autoBool(autoScheduleReceiptReview)
}
func (s *Server) providerRetries() int { return s.autoInt(autoProviderRetries) }
func (s *Server) rememberedApprovalWindow() time.Duration {
	return time.Duration(s.autoInt(autoRememberedApprovalHrs)) * time.Hour
}

// backgroundDefaultModel is what background work runs on when no background
// model is chosen: Haiku beside an Anthropic conversation default, Luna beside a
// Codex one (the reviewer for reviews), otherwise the conversation default itself.
func (s *Server) backgroundDefaultModel(purpose string) string {
	conversation, _ := s.modelDefaults()
	switch p := runtime.ModelProvider(conversation); {
	case p == providerAnthropic:
		return anthropicBackgroundModel
	case p == providerCodex && purpose == backgroundReview && s.codexConnected():
		return codexReviewModel // Codex ships a purpose-built reviewer
	case p == providerCodex && s.codexConnected():
		return codexDefaultModel // the smallest Codex model
	}
	return conversation
}

// applyConversationModel validates and stores the global model, keeping the
// reasoning effort the model supports. It is the one write path for both
// /api/model-settings and the automation page.
func (s *Server) applyConversationModel(ctx context.Context, x modelSettings) (modelSettings, int, error) {
	x.Model = strings.TrimSpace(x.Model)
	x.ReasoningEffort = strings.TrimSpace(x.ReasoningEffort)
	if isFollowGlobalModel(x.Model) {
		// The global setting is what "default" resolves to; it must name a concrete model.
		return x, http.StatusBadRequest, errors.New("model is required")
	}
	if x.ReasoningEffort == followGlobalModel {
		x.ReasoningEffort = ""
	}
	if x.ReasoningEffort == "" {
		_, x.ReasoningEffort = s.modelDefaults()
		if models, _, _ := s.loadModels(ctx); len(models) > 0 {
			if option, ok := s.matchModel(models, x.Model); ok && option.DefaultReasoning != "" {
				x.ReasoningEffort = option.DefaultReasoning
			}
		}
	}
	if err := s.validateModelChoice(ctx, x.Model, x.ReasoningEffort); err != nil {
		return x, http.StatusBadRequest, err
	}
	if err := s.saveModelDefaults(x); err != nil {
		return x, http.StatusInternalServerError, err
	}
	return x, http.StatusOK, nil
}

type automationView struct {
	Key       string   `json:"key"`
	Group     string   `json:"group"`
	Kind      string   `json:"kind"`
	Default   any      `json:"default"`
	Value     any      `json:"value"`
	Effective any      `json:"effective,omitempty"`
	Source    string   `json:"source"` // default, stored or env
	Env       string   `json:"env,omitempty"`
	Locked    bool     `json:"locked,omitempty"`
	ReadOnly  bool     `json:"read_only,omitempty"`
	Options   []string `json:"options,omitempty"`
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`
	Unit      string   `json:"unit,omitempty"`
}

func typedAutomationValue(kind automationKind, v string) any {
	switch kind {
	case autoKindBool:
		return v == "true"
	case autoKindInt:
		n, _ := strconv.Atoi(v)
		return n
	}
	return v
}

func (s *Server) automationView(ctx context.Context, item automationItem) automationView {
	v := automationView{Key: item.Key, Group: item.Group, Kind: string(item.Kind), Options: item.Options, Unit: item.Unit}
	if item.Kind == autoKindInt {
		lo, hi := item.Min, item.Max
		v.Min, v.Max = &lo, &hi
	}
	def := item.Default
	switch item.Key {
	case autoBackgroundModel:
		def = s.backgroundDefaultModel(backgroundTriage)
	case autoGroupTriageModel:
		def = s.backgroundModel(backgroundTriage)
	}
	v.Default = typedAutomationValue(item.Kind, def)
	env, envSet := autoEnv(item)
	if envSet {
		v.Env, v.Locked, v.Source = item.Env, true, "env"
	}
	var cur string
	switch item.Key {
	case autoConversationModel:
		cur, _ = s.modelDefaults()
		v.Effective = cur
	case autoStructuredOutput:
		cur = strconv.FormatBool(s.store.structuredOutputOn())
	case autoMCPReview:
		x, err := s.store.getAutoReviewSettings()
		cur = "off"
		if err == nil && x.Mode != "" {
			cur = x.Mode
		}
	case autoBackgroundModel:
		stored, _ := s.autoStored(item.Key)
		cur = stored // "" = automatic
		if envSet {
			cur = env
		}
		v.Effective = s.backgroundModel(backgroundTriage)
	case autoGroupTriageModel:
		cur, _ = s.autoStored(item.Key) // "" = the background model
		v.Effective = s.groupTriageModel()
	case autoDesktopIdle:
		v.ReadOnly = true
		v.Value, v.Default = nil, nil
		if s.computer() != nil {
			if info, err, _ := s.cachedMicroVMInfo(ctx); err == nil && info.DesktopIdleSeconds != nil {
				v.Value = *info.DesktopIdleSeconds
			}
		}
		if v.Source == "" {
			v.Source = "default"
		}
		return v
	default:
		cur = s.autoValue(item.Key)
	}
	if envSet && !automaticModelItem(item.Key) {
		cur = env
	}
	if automaticModelItem(item.Key) {
		v.Value = cur
	} else {
		v.Value = typedAutomationValue(item.Kind, cur)
	}
	if v.Source == "" {
		v.Source = "default"
		if automaticModelItem(item.Key) {
			if cur != "" {
				v.Source = "stored"
			}
		} else if cur != def {
			v.Source = "stored"
		}
	}
	return v
}

func (s *Server) automationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeAutomationSettings(w, r)
	case http.MethodPut:
		var body struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if decode(r, &body) != nil || len(body.Values) == 0 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "expected values")
			return
		}
		type change struct {
			item  automationItem
			value *string // nil = back to the default
		}
		var changes []change
		for k, raw := range body.Values {
			item, ok := automationItemFor(k)
			if !ok {
				writeErr(w, http.StatusBadRequest, "unknown_setting", "unknown setting "+k)
				return
			}
			if item.Kind == autoKindReadOnly {
				writeErr(w, http.StatusBadRequest, "read_only", k+" is set outside this workspace")
				return
			}
			if _, locked := autoEnv(item); locked {
				writeErr(w, http.StatusConflict, "env_override", k+" is set by the server environment")
				return
			}
			value, err := s.parseAutomationValue(r.Context(), item, raw)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_setting", k+": "+err.Error())
				return
			}
			changes = append(changes, change{item, value})
		}
		for _, c := range changes {
			if err := s.applyAutomationValue(r.Context(), c.item, c.value); err != nil {
				var ae *automationApplyError
				if errors.As(err, &ae) {
					writeErr(w, ae.status, "invalid_setting", c.item.Key+": "+ae.Error())
				} else {
					writeErr(w, http.StatusInternalServerError, "storage", "Could not save "+c.item.Key)
				}
				return
			}
		}
		s.writeAutomationSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT")
	}
}

func (s *Server) writeAutomationSettings(w http.ResponseWriter, r *http.Request) {
	items := make([]automationView, 0, len(automationItems))
	for _, item := range automationItems {
		items = append(items, s.automationView(r.Context(), item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type automationApplyError struct {
	status int
	err    error
}

func (e *automationApplyError) Error() string { return e.err.Error() }

// parseAutomationValue checks a submitted value against the item's type and
// returns its canonical form; null resets to the default (nil).
func (s *Server) parseAutomationValue(ctx context.Context, item automationItem, raw json.RawMessage) (*string, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var out string
	switch item.Kind {
	case autoKindBool:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return nil, errors.New("expected true or false")
		}
		out = strconv.FormatBool(b)
	case autoKindInt:
		var f float64
		if json.Unmarshal(raw, &f) != nil || f != float64(int(f)) {
			return nil, errors.New("expected a whole number")
		}
		n := int(f)
		if n < item.Min || n > item.Max {
			return nil, fmt.Errorf("must be between %d and %d", item.Min, item.Max)
		}
		out = strconv.Itoa(n)
	case autoKindEnum:
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, errors.New("expected text")
		}
		for _, o := range item.Options {
			if o == v {
				return &v, nil
			}
		}
		return nil, fmt.Errorf("must be one of %s", strings.Join(item.Options, ", "))
	case autoKindModel:
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, errors.New("expected a model ID")
		}
		v = strings.TrimSpace(v)
		if automaticModelItem(item.Key) {
			switch strings.ToLower(v) {
			case "", "auto", followGlobalModel:
				return nil, nil // automatic
			}
		} else if v == "" {
			return nil, errors.New("a model is required")
		}
		if err := s.validateModelID(ctx, v); err != nil {
			return nil, err
		}
		out = v
	default:
		return nil, errors.New("not settable")
	}
	return &out, nil
}

func (s *Server) applyAutomationValue(ctx context.Context, item automationItem, value *string) error {
	switch item.Key {
	case autoConversationModel:
		model := defaultConversationModel
		if value != nil {
			model = *value
		}
		_, status, err := s.applyConversationModel(ctx, modelSettings{Model: model})
		if err != nil {
			return &automationApplyError{status, err}
		}
		return nil
	case autoStructuredOutput:
		on := item.Default == "true"
		if value != nil {
			on = *value == "true"
		}
		return s.store.putUserStructuredOutput(on)
	case autoMCPReview:
		mode := item.Default
		if value != nil {
			mode = *value
		}
		return s.store.putAutoReviewMode(mode)
	}
	return s.saveAutomationValue(item.Key, value)
}
