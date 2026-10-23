package app

// "View context" (D53): a per-run record of what the first model call of the
// run was given, part by part, so the owner can see what the context holds.
//
// The record is taken where the run is already assembled, never by assembling
// again: buildContextPartsWith reports the history window and the note pieces
// to r.ctxCapture, assembleRunSystemSegments yields the system prompt as
// segments that concatenate to the exact bytes sent, and the run site adds the
// tool list. Nothing here changes a prompt byte. Part kinds are open strings:
// a new block in buildContextPartsWith needs one row in contextMarkerKinds
// (otherwise it shows as "other" with its first line as the label).

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

const (
	contextSnapshotVersion = 1
	// maxContextSnapshotBytes bounds one run's stored parts (uncompressed JSON).
	// Over it, the largest texts are dropped and only their sizes stay.
	maxContextSnapshotBytes = 256 << 10
	// contextSnapshotKeep is how many runs per conversation keep a snapshot.
	contextSnapshotKeep = 50
	// contextSnapshotUnavailable is the stable code for a run with no snapshot:
	// it ran before capture existed, was pruned, or has not built a context.
	contextSnapshotUnavailable = "context_snapshot_unavailable"
)

// Part kinds. The set is open; the UI shows an unknown kind by its raw name.
const (
	ctxKindSystemGuidance = "system_guidance"
	ctxKindBotInstruction = "bot_instructions"
	ctxKindAgentReport    = "agent_report_prompt"
	ctxKindTools          = "tools"
	ctxKindToolsOnDemand  = "tools_on_demand"
	ctxKindMemory         = "memory"
	ctxKindSummary        = "summary"
	ctxKindHistory        = "history_message"
	ctxKindEarlierNote    = "earlier_turns_note"
	ctxKindCapabilityRefs = "capability_refs"
	ctxKindScheduleStates = "schedule_states"
	ctxKindRunContext     = "run_context"
	ctxKindRequestNote    = "request_note"
	ctxKindOther          = "other"
)

// contextMarkerKinds maps the opening marker of a context message to its kind.
// First match wins. Add one row for every new block buildContextPartsWith
// appends.
var contextMarkerKinds = []struct{ prefix, kind string }{
	{"[derived history data]", ctxKindSummary},
	{"[memory data]", ctxKindMemory},
	{"[recent capability references]", ctxKindCapabilityRefs},
	{"[earlier conversation]", ctxKindEarlierNote},
	{"[original scheduled task]", "scheduled_task"},
	{"[earlier scheduled runs]", "scheduled_order"},
	{"[untrusted open work reference]", "open_work"},
	{"[waiting on]", "waiting_on"},
	{"[task record]", "task_record"},
	{"[task updates]", "task_updates"},
	{"[wait result]", "wait_result"},
	{"[untrusted completed tool results from interrupted work]", "tool_carryover"},
	{"[untrusted prior form answers]", "form_answers"},
	{"[untrusted group profile metadata]", "group_profiles"},
	{"[co-addressed]", "co_addressed"},
	{"[coordination]", "coordination"},
	{"[colleague messages addressed to you]", "mentions_since_trigger"},
	{"[current handoff task]", "handoff_task"},
	{"[completed colleague task]", "colleague_result"},
	{"[failed colleague task]", "colleague_failure"},
	{"[group mention]", "group_mention"},
	{"[run context]", ctxKindRunContext},
}

// ContextPart is one piece of what the model call received.
type ContextPart struct {
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	Role   string `json:"role,omitempty"`
	Chars  int    `json:"chars"`
	Tokens int    `json:"tokens"`
	// Text is the exact text, with secret-shaped values replaced (Redactions
	// counts them). Empty with TextDropped set when the snapshot bound removed
	// it; the sizes stay.
	Text        string `json:"text,omitempty"`
	TextDropped bool   `json:"text_dropped,omitempty"`
	Redactions  int    `json:"redactions,omitempty"`
	// OnDemand parts are available to the model but were not in this call.
	OnDemand bool `json:"on_demand,omitempty"`
	// Message describes a history message.
	Message *ContextMessage `json:"message,omitempty"`
	// Items break a tools part into its tools.
	Items []ContextItem `json:"items,omitempty"`
}

type ContextMessage struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Sender    string `json:"sender,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// Gisted: an older reply of the Bot's own, or a closed exchange, kept as a
	// short gist instead of verbatim.
	Gisted bool `json:"gisted,omitempty"`
	// Trimmed: cut to fit the history window.
	Trimmed bool `json:"trimmed,omitempty"`
	Images  int  `json:"images,omitempty"`
	// Request marks the message this run answers.
	Request bool `json:"request,omitempty"`
}

type ContextItem struct {
	Name   string `json:"name"`
	Chars  int    `json:"chars"`
	Tokens int    `json:"tokens"`
}

// ContextHistory says what the history window left out.
type ContextHistory struct {
	Considered int `json:"considered"`
	Included   int `json:"included"`
	Skipped    int `json:"skipped"`
	// SkippedBySummary are skipped messages the summary already covers.
	SkippedBySummary int `json:"skipped_by_summary"`
	// EarlierUnfetched: older pages of the conversation were not read at all.
	EarlierUnfetched bool `json:"earlier_unfetched,omitempty"`
}

// ContextSnapshot is the stored document.
type ContextSnapshot struct {
	Version        int            `json:"version"`
	RunID          string         `json:"run_id"`
	ConversationID string         `json:"conversation_id"`
	BotID          string         `json:"bot_id"`
	Model          string         `json:"model,omitempty"`
	CapturedAt     string         `json:"captured_at"`
	Parts          []ContextPart  `json:"parts"`
	History        ContextHistory `json:"history"`
}

type ContextKindTotal struct {
	Kind   string `json:"kind"`
	Parts  int    `json:"parts"`
	Chars  int    `json:"chars"`
	Tokens int    `json:"tokens"`
}

type ContextTotals struct {
	// Chars and Tokens count what the call was given; on-demand parts are
	// counted apart. Tokens are estimates (agent.EstimateTextTokens), not the
	// provider's count.
	Chars          int                `json:"chars"`
	Tokens         int                `json:"tokens"`
	OnDemandChars  int                `json:"on_demand_chars"`
	OnDemandTokens int                `json:"on_demand_tokens"`
	DroppedTexts   int                `json:"dropped_texts"`
	ByKind         []ContextKindTotal `json:"by_kind"`
}

// ContextSnapshotView is the API body.
type ContextSnapshotView struct {
	ContextSnapshot
	Totals ContextTotals `json:"totals"`
}

func (snap ContextSnapshot) View() ContextSnapshotView {
	t := ContextTotals{ByKind: []ContextKindTotal{}}
	index := map[string]int{}
	for _, p := range snap.Parts {
		if p.TextDropped {
			t.DroppedTexts++
		}
		if p.OnDemand {
			t.OnDemandChars += p.Chars
			t.OnDemandTokens += p.Tokens
			continue
		}
		t.Chars += p.Chars
		t.Tokens += p.Tokens
		i, ok := index[p.Kind]
		if !ok {
			i = len(t.ByKind)
			index[p.Kind] = i
			t.ByKind = append(t.ByKind, ContextKindTotal{Kind: p.Kind})
		}
		t.ByKind[i].Parts++
		t.ByKind[i].Chars += p.Chars
		t.ByKind[i].Tokens += p.Tokens
	}
	return ContextSnapshotView{ContextSnapshot: snap, Totals: t}
}

// --- capture -----------------------------------------------------------

type ctxHistoryEntry struct {
	msg     ContextMessage
	role    string
	content string
	images  int
}

// contextCapture is filled by buildContextPartsWith and the run site. Every
// method is a no-op on a nil receiver, so callers other than a real run
// (debug preview, approval expiry, tests of the builder) pay nothing.
type contextCapture struct {
	history     []ctxHistoryEntry // newest first, as the window walks them
	skippedSum  int
	chosenStart int
	noteAt      int
	noteBase    string
	noteStates  string
	messages    []ContextPart
	stats       ContextHistory
	system      []systemSegment
}

func newContextCapture() *contextCapture { return &contextCapture{} }

func (c *contextCapture) addHistory(m Message, role, content string, gisted, trimmed bool, images int, request bool, sender string) {
	if c == nil {
		return
	}
	c.history = append(c.history, ctxHistoryEntry{
		msg:  ContextMessage{ID: m.ID, Kind: m.Kind, Sender: sender, CreatedAt: m.CreatedAt, Gisted: gisted, Trimmed: trimmed, Images: images, Request: request},
		role: role, content: content, images: images,
	})
}

func (c *contextCapture) skipBySummary() {
	if c != nil {
		c.skippedSum++
	}
}

func (c *contextCapture) markHistoryStart(pmLen int) {
	if c != nil {
		c.chosenStart = pmLen
	}
}

// markNote records where the request note will be inserted (before the last
// message) and the pieces it is built from.
func (c *contextCapture) markNote(pmLen int, base, states string) {
	if c == nil {
		return
	}
	c.noteAt = pmLen - 1
	if c.noteAt < 0 {
		c.noteAt = 0
	}
	c.noteBase, c.noteStates = base, states
}

func contextKindFor(content string) string {
	for _, m := range contextMarkerKinds {
		if strings.HasPrefix(content, m.prefix) {
			return m.kind
		}
	}
	return ctxKindOther
}

func partFor(kind, role, text string, images int) ContextPart {
	return ContextPart{Kind: kind, Role: role, Chars: len([]rune(text)), Tokens: agent.EstimateTextTokens(text) + images*1500, Text: text}
}

// finishMessages turns the final message list into parts. pm is the list
// buildContextPartsWith returns (note already inserted); the history window
// occupies pm[chosenStart:] in its pre-note positions.
func (c *contextCapture) finishMessages(pm []runtime.Message, considered int, moreBefore bool) {
	if c == nil {
		return
	}
	chronological := make([]ctxHistoryEntry, len(c.history))
	for i, h := range c.history {
		chronological[len(c.history)-1-i] = h
	}
	hist := map[int]ctxHistoryEntry{}
	for k, h := range chronological {
		at := c.chosenStart + k
		if at >= c.noteAt {
			at++ // the note sits before the last message
		}
		hist[at] = h
	}
	c.messages = c.messages[:0]
	for i, m := range pm {
		if h, ok := hist[i]; ok && i != c.noteAt {
			p := partFor(ctxKindHistory, h.role, h.content, h.images)
			msg := h.msg
			p.Message = &msg
			c.messages = append(c.messages, p)
			continue
		}
		if i == c.noteAt && m.Content != "" {
			c.messages = append(c.messages, c.noteParts(m)...)
			continue
		}
		kind := contextKindFor(m.Content)
		p := partFor(kind, m.Role, m.Content, len(m.ImageURLs))
		if kind == ctxKindOther {
			p.Label = firstLine(m.Content, 80)
		}
		c.messages = append(c.messages, p)
	}
	skipped := considered - len(c.history)
	if skipped < 0 {
		skipped = 0
	}
	c.stats = ContextHistory{Considered: considered, Included: len(c.history), Skipped: skipped, SkippedBySummary: c.skippedSum, EarlierUnfetched: moreBefore}
}

// noteParts splits the request note into the run context, the schedule states
// and the request lines. The three texts concatenate to the note.
func (c *contextCapture) noteParts(m runtime.Message) []ContextPart {
	text := m.Content
	var out []ContextPart
	take := func(kind, piece string) {
		if piece == "" {
			return
		}
		out = append(out, partFor(kind, m.Role, piece, 0))
		text = strings.TrimPrefix(text, piece)
	}
	if !strings.HasPrefix(text, c.noteBase) {
		return []ContextPart{partFor(ctxKindRequestNote, m.Role, text, 0)}
	}
	take(ctxKindRunContext, c.noteBase)
	if c.noteStates != "" && strings.HasPrefix(text, "\n"+c.noteStates) {
		take(ctxKindScheduleStates, "\n"+c.noteStates)
	}
	take(ctxKindRequestNote, text)
	return out
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// snapshot assembles the document for the run's first model call.
func (c *contextCapture) snapshot(r Run, conversationID, model, system string, tools []Tool, hasAgentSuffix bool) ContextSnapshot {
	snap := ContextSnapshot{Version: contextSnapshotVersion, RunID: r.ID, ConversationID: conversationID, BotID: r.BotID, Model: model, CapturedAt: now()}
	segs := c.system
	if len(segs) == 0 && system != "" {
		segs = []systemSegment{{ctxKindSystemGuidance, system}}
	}
	for _, seg := range segs {
		snap.Parts = append(snap.Parts, partFor(seg.Kind, "system", seg.Text, 0))
	}
	if hasAgentSuffix {
		if suffix := runtime.SystemPromptSuffix(r.structured, len(tools) > 0); suffix != "" {
			snap.Parts = append(snap.Parts, partFor(ctxKindAgentReport, "system", suffix, 0))
		}
	}
	snap.Parts = append(snap.Parts, toolParts(tools)...)
	snap.Parts = append(snap.Parts, c.messages...)
	snap.History = c.stats
	return snap
}

// toolParts renders the tool list as the model receives it: one JSON array
// of definitions for the tools sent, and one for the on-demand ones whose
// schemas are held back until a tool activates them.
func toolParts(tools []Tool) []ContextPart {
	var sent, held []Tool
	for _, t := range tools {
		if t.Deferred {
			held = append(held, t)
		} else {
			sent = append(sent, t)
		}
	}
	build := func(kind string, list []Tool, onDemand bool) []ContextPart {
		if len(list) == 0 {
			return nil
		}
		type def struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters,omitempty"`
		}
		var text bytes.Buffer
		text.WriteString("[")
		items := make([]ContextItem, 0, len(list))
		tokens := 0
		for i, t := range list {
			encoded, err := marshalPlain(def{t.Name, t.Description, t.Parameters})
			if err != nil {
				encoded = []byte(`{"name":` + strconvQuote(t.Name) + `}`)
			}
			if i > 0 {
				text.WriteString(",\n")
			}
			text.Write(encoded)
			n := agent.EstimateToolDefinitionTokens(provider.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
			tokens += n
			items = append(items, ContextItem{Name: t.Name, Chars: len([]rune(string(encoded))), Tokens: n})
		}
		text.WriteString("]")
		sort.SliceStable(items, func(i, j int) bool { return items[i].Chars > items[j].Chars })
		p := ContextPart{Kind: kind, Role: "system", Chars: len([]rune(text.String())), Tokens: tokens, Text: text.String(), OnDemand: onDemand, Items: items}
		return []ContextPart{p}
	}
	return append(build(ctxKindTools, sent, false), build(ctxKindToolsOnDemand, held, true)...)
}

func strconvQuote(s string) string {
	b, _ := marshalPlain(s)
	return string(b)
}

// marshalPlain is json.Marshal without HTML escaping, so the shown schema
// matches the bytes the provider adapter sends.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// --- redaction ----------------------------------------------------------

// The vault never reaches a prompt: private form fields and request_secret_input
// values are sealed (secrets.go) and a model only sees a secret_ref handle, which
// is not a secret. What can reach a prompt is text people or tools carried in
// themselves: chat messages, memories, tool results. Those pass through this
// list before they are stored. It catches common credential shapes, not every
// possible secret.
var contextSecretPatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), "private key"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), "token"},
	{regexp.MustCompile(`\b(?:sk-ant-|sk-|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xox[abprs]-|AIza|hf_)[A-Za-z0-9_\-]{16,}`), "key"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "key"},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`), "bearer token"},
}

var (
	contextURLUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	contextSecretField = regexp.MustCompile(`(?i)\b((?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)["']?\s*[:=]\s*["']?)([^\s"',;]{6,})`)
)

// redactContextText replaces credential-shaped values and returns how many it
// replaced.
func redactContextText(s string) (string, int) {
	n := 0
	mark := func(label string) string { n++; return "[redacted " + label + "]" }
	for _, p := range contextSecretPatterns {
		label := p.label
		s = p.re.ReplaceAllStringFunc(s, func(string) string { return mark(label) })
	}
	s = contextURLUserinfo.ReplaceAllStringFunc(s, func(m string) string {
		n++
		return contextURLUserinfo.FindStringSubmatch(m)[1] + "[redacted credentials]@"
	})
	s = contextSecretField.ReplaceAllStringFunc(s, func(m string) string {
		sub := contextSecretField.FindStringSubmatch(m)
		if strings.HasPrefix(sub[2], "[redacted") || strings.HasPrefix(sub[2], "secret_ref") {
			return m
		}
		n++
		return sub[1] + "[redacted value]"
	})
	return s, n
}

// boundContextSnapshot redacts every text, then drops the largest texts until
// the document fits maxBytes. Sizes (Chars, Tokens, Items) never change.
func boundContextSnapshot(snap *ContextSnapshot, maxBytes int) {
	for i := range snap.Parts {
		p := &snap.Parts[i]
		if p.Text == "" {
			continue
		}
		p.Text, p.Redactions = redactContextText(p.Text)
	}
	size := func() int { b, _ := json.Marshal(snap); return len(b) }
	for size() > maxBytes {
		largest, at := 0, -1
		for i, p := range snap.Parts {
			if len(p.Text) > largest {
				largest, at = len(p.Text), i
			}
		}
		if at < 0 {
			return // nothing left to drop; the structure alone is over the bound
		}
		snap.Parts[at].Text = ""
		snap.Parts[at].TextDropped = true
	}
}

// --- storage ------------------------------------------------------------

func migrateRunContext(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_context_snapshots (
	 run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
	 conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
	 version INTEGER NOT NULL, captured_at TEXT NOT NULL,
	 total_chars INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0,
	 encoding TEXT NOT NULL DEFAULT 'gzip+json', body BLOB NOT NULL
	 ); CREATE INDEX IF NOT EXISTS run_context_snapshots_conv ON run_context_snapshots(conversation_id, captured_at DESC)`); err != nil {
		return err
	}
	return ensureColumn(db, "run_context_snapshots", "texts_dropped", `ALTER TABLE run_context_snapshots ADD COLUMN texts_dropped INTEGER NOT NULL DEFAULT 0`)
}

// saveRunContextSnapshot stores the first snapshot of a run (a resumed or
// retried run keeps the one it already has) and prunes the conversation to its
// newest contextSnapshotKeep.
func (s *Store) saveRunContextSnapshot(snap ContextSnapshot) error {
	boundContextSnapshot(&snap, maxContextSnapshotBytes)
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	if _, err = zw.Write(raw); err != nil {
		return err
	}
	if err = zw.Close(); err != nil {
		return err
	}
	view := snap.View()
	dropped := view.Totals.DroppedTexts
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO run_context_snapshots(run_id,conversation_id,bot_id,model,version,captured_at,total_chars,total_tokens,texts_dropped,body)
	 VALUES(?,?,?,?,?,?,?,?,?,?)`, snap.RunID, snap.ConversationID, snap.BotID, snap.Model, snap.Version, snap.CapturedAt, view.Totals.Chars, view.Totals.Tokens, dropped, zipped.Bytes()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM run_context_snapshots WHERE conversation_id=? AND run_id NOT IN
	 (SELECT run_id FROM run_context_snapshots WHERE conversation_id=? ORDER BY captured_at DESC, rowid DESC LIMIT ?)`, snap.ConversationID, snap.ConversationID, contextSnapshotKeep); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) runContextSnapshot(runID string) (ContextSnapshot, error) {
	var body []byte
	var encoding string
	if err := s.db.QueryRow(`SELECT encoding, body FROM run_context_snapshots WHERE run_id=?`, runID).Scan(&encoding, &body); err != nil {
		return ContextSnapshot{}, err
	}
	var raw []byte
	if encoding == "gzip+json" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return ContextSnapshot{}, err
		}
		defer zr.Close()
		if raw, err = io.ReadAll(io.LimitReader(zr, 4*maxContextSnapshotBytes)); err != nil {
			return ContextSnapshot{}, err
		}
	} else {
		return ContextSnapshot{}, errors.New("unknown context snapshot encoding")
	}
	var snap ContextSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return ContextSnapshot{}, err
	}
	return snap, nil
}

// recordRunContext is the run site's one call: it never fails the run.
func (s *Server) recordRunContext(r Run, c Conversation, model, system string, tools []Tool) {
	if r.ctxCapture == nil {
		return
	}
	started := time.Now()
	snap := r.ctxCapture.snapshot(r, c.ID, model, system, tools, r.Kind != runKindTriage)
	if err := s.store.saveRunContextSnapshot(snap); err != nil {
		log.Printf("[context] snapshot %s: %v", r.ID, err)
		return
	}
	if d := time.Since(started); d > 100*time.Millisecond {
		log.Printf("[context] snapshot %s took %s", r.ID, d.Round(time.Millisecond))
	}
}

// --- API ----------------------------------------------------------------

// routeRunContext handles GET /api/runs/{id}/context. Like the tool activity
// details it sits behind the owner session gate in handle(); there is no other
// reader, and a snapshot is never part of an export or a Bot's own tools.
func (s *Server) routeRunContext(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 || parts[0] != "runs" || parts[2] != "context" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
		return true
	}
	runID, err := url.PathUnescape(parts[1])
	if err != nil || runID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid run id")
		return true
	}
	if _, err = s.store.GetRun(runID); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "run not found")
		return true
	}
	snap, err := s.store.runContextSnapshot(runID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, contextSnapshotUnavailable, "no context was recorded for this run")
		return true
	}
	if err != nil {
		log.Printf("[context] read %s: %v", runID, err)
		writeErr(w, http.StatusInternalServerError, "storage", "could not read the context")
		return true
	}
	writeJSON(w, http.StatusOK, snap.View())
	return true
}
