package app

// Work trace (design divide-and-trace, line B). Every screenshot that passes
// through the server on its way to a Bot is kept as a small picture of the run,
// so the person can see what the Bot did on its computer and so the Bot can
// check the sequence of states (desktop.trace). The capture point is
// finishCapture; this file holds the table, the privacy decision that runs
// BEFORE a picture is stored, and the HTTP routes.
//
// Privacy rules, decided in this order for every capture:
//  1. The person controls the screen (a takeover card of the run is open): the
//     capture is refused; nothing is stored and the Bot does not receive it.
//  2. The run holds a private input (secret window): from the moment a form
//     password or private input exists until the next successful navigation, a
//     capture is recorded as redacted and no picture is stored.
//  3. The page is a private origin: an origin a vault secret is bound to, or an
//     entry of the trace_private_origins setting. Recorded as redacted.
//  4. work_trace is off: nothing is stored and no row is written.
// Text in a label is a host or a short action word, never a typed value. There
// is no pixel redaction: a secret that appears on screen outside these rules
// cannot be recognised, and the manual page says so.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	autoWorkTrace           = "work_trace"
	autoTraceRetentionDays  = "trace_retention_days"
	autoTracePrivateOrigins = "trace_private_origins"

	defaultTraceRetentionDays = 7

	attachmentOriginTrace = "trace"

	// The stored picture of one capture: long side, JPEG quality and byte cap.
	traceTileLongSide      = 640
	traceTileQualityFirst  = 70
	traceTileBytesMax      = 120 << 10
	traceCapturesPerRunMax = 60

	tracePrivateOriginsMax = 100
	tracePrivateOriginLen  = 200
)

// Why a capture has no stored picture.
const (
	traceReasonSecretInput   = "secret_input"
	traceReasonPrivateOrigin = "private_origin"
	traceReasonLimit         = "limit"
	traceReasonStorage       = "storage"
)

func traceRetentionDefault() string { return fmt.Sprint(defaultTraceRetentionDays) }

func migrateRunCaptures(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_captures(
	 run_id TEXT NOT NULL, seq INTEGER NOT NULL, call_id TEXT NOT NULL DEFAULT '',
	 attachment_id TEXT, taken_at TEXT NOT NULL, tool TEXT NOT NULL DEFAULT '',
	 action TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT 'full',
	 redacted INTEGER NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '',
	 width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0,
	 PRIMARY KEY(run_id,seq),
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
	 FOREIGN KEY(attachment_id) REFERENCES attachments(id) ON DELETE SET NULL);
	CREATE INDEX IF NOT EXISTS run_captures_attachment ON run_captures(attachment_id);
	CREATE INDEX IF NOT EXISTS run_captures_taken ON run_captures(taken_at);
	CREATE TABLE IF NOT EXISTS run_secret_windows(
	 run_id TEXT PRIMARY KEY, opened_at TEXT NOT NULL,
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE);`)
	return err
}

// RunCapture is one row of the trace of a run.
type RunCapture struct {
	RunID        string `json:"run_id,omitempty"`
	Seq          int    `json:"seq"`
	CallID       string `json:"call_id,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	TakenAt      string `json:"taken_at"`
	Tool         string `json:"tool,omitempty"`
	Action       string `json:"action,omitempty"`
	Label        string `json:"label"`
	Detail       string `json:"detail,omitempty"`
	Redacted     bool   `json:"redacted"`
	Reason       string `json:"reason,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

func (s *Store) runCaptures(runID string) ([]RunCapture, error) {
	rows, err := s.db.Query(`SELECT seq,call_id,COALESCE(attachment_id,''),taken_at,tool,action,label,detail,redacted,reason,width,height FROM run_captures WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunCapture{}
	for rows.Next() {
		c := RunCapture{RunID: runID}
		var redacted int
		if err = rows.Scan(&c.Seq, &c.CallID, &c.AttachmentID, &c.TakenAt, &c.Tool, &c.Action, &c.Label, &c.Detail, &redacted, &c.Reason, &c.Width, &c.Height); err != nil {
			return nil, err
		}
		c.Redacted = redacted != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// addRunCapture appends a row with the next number of the run and returns it.
func (s *Store) addRunCapture(c RunCapture) (int, error) {
	var seq int
	redacted := 0
	if c.Redacted {
		redacted = 1
	}
	err := s.db.QueryRow(`INSERT INTO run_captures(run_id,seq,call_id,attachment_id,taken_at,tool,action,label,detail,redacted,reason,width,height)
	 VALUES(?,(SELECT COALESCE(MAX(seq),0)+1 FROM run_captures WHERE run_id=?),?,?,?,?,?,?,?,?,?,?,?) RETURNING seq`,
		c.RunID, c.RunID, c.CallID, nullString(c.AttachmentID), c.TakenAt, c.Tool, c.Action, c.Label, c.Detail, redacted, c.Reason, c.Width, c.Height).Scan(&seq)
	return seq, err
}

func (s *Store) runCaptureCount(runID string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_captures WHERE run_id=?`, runID).Scan(&n)
	return n
}

// Secret window: the run holds a private input and the screen may show it.
func (s *Store) openSecretWindow(runID string) {
	if runID == "" {
		return
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO run_secret_windows(run_id,opened_at) VALUES(?,?)`, runID, now()); err != nil {
		log.Printf("[trace] could not open the secret window of run %s: %v", runID, err)
	}
}

func (s *Store) closeSecretWindow(runID string) {
	_, _ = s.db.Exec(`DELETE FROM run_secret_windows WHERE run_id=?`, runID)
}

func (s *Store) secretWindowOpen(runID string) bool {
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_secret_windows WHERE run_id=?)`, runID).Scan(&found)
	return found
}

// runTraceState is what the server learns about a run's screen between two
// captures: the page it last saw and the last action it took.
type runTraceState struct {
	mu         sync.Mutex
	pageURL    string
	lastAction string
}

func (s *Server) traceState(runID string) *runTraceState {
	if v, ok := s.traceRuns.Load(runID); ok {
		return v.(*runTraceState)
	}
	v, _ := s.traceRuns.LoadOrStore(runID, &runTraceState{})
	return v.(*runTraceState)
}

func (s *Server) noteTracePage(runID, pageURL string) {
	pageURL = strings.TrimSpace(pageURL)
	if runID == "" || pageURL == "" {
		return
	}
	st := s.traceState(runID)
	st.mu.Lock()
	st.pageURL = pageURL
	st.mu.Unlock()
}

func (s *Server) noteTraceAction(runID, label string) {
	if runID == "" || label == "" {
		return
	}
	st := s.traceState(runID)
	st.mu.Lock()
	st.lastAction = label
	st.mu.Unlock()
}

// forgetTraceState drops the in-memory notes of a finished run.
func (s *Server) forgetTraceState(runID string) { s.traceRuns.Delete(runID) }

// pageURLFromResult reads the address a browser result names.
func pageURLFromResult(result string) string {
	var res struct {
		URL     string `json:"url"`
		Current *struct {
			URL string `json:"url"`
		} `json:"current"`
	}
	if json.Unmarshal([]byte(result), &res) != nil {
		return ""
	}
	if res.Current != nil && res.Current.URL != "" {
		return res.Current.URL
	}
	return res.URL
}

// navigationSucceeded says whether a computer_browser action opened a page.
func navigationAction(action string, raw json.RawMessage) bool {
	switch action {
	case "browser.navigate":
		return true
	case "browser.action":
		var in struct {
			TargetAction string `json:"target_action"`
		}
		return json.Unmarshal(raw, &in) == nil && in.TargetAction == "navigate"
	}
	return false
}

// traceLabelText cuts a label to a short single line free of credential shapes.
func traceLabelText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	text, _ = redactContextText(text)
	if utf8.RuneCountInString(text) > limit {
		r := []rune(text)
		text = string(r[:limit-1]) + "…"
	}
	return text
}

func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// traceActionLabel is the short account of what an action did, for the strip
// under a picture. It carries no typed text, no path and no query string.
func traceActionLabel(action string, raw json.RawMessage) string {
	var in struct {
		X, Y         *int
		Clicks       int
		Direction    string
		Text         string
		Key          string
		URL          string
		Click        string
		TargetAction string `json:"target_action"`
		Action       string `json:"action"`
	}
	_ = json.Unmarshal(raw, &in)
	switch action {
	case "desktop.click":
		verb := "click"
		switch in.Clicks {
		case 2:
			verb = "double click"
		case 3:
			verb = "triple click"
		}
		if in.X != nil && in.Y != nil {
			return fmt.Sprintf("%s %d,%d", verb, *in.X, *in.Y)
		}
		return verb
	case "desktop.type":
		return fmt.Sprintf("type %d characters", utf8.RuneCountInString(in.Text))
	case "desktop.key":
		if utf8.RuneCountInString(in.Key) <= 1 {
			return "key"
		}
		return traceLabelText("key "+in.Key, 40)
	case "desktop.scroll":
		return traceLabelText("scroll "+in.Direction, 40)
	case "browser.navigate":
		if host := urlHost(in.URL); host != "" {
			return traceLabelText("navigate "+host, 40)
		}
		return "navigate"
	case "browser.click":
		if in.Click != "" {
			return traceLabelText("click "+in.Click, 40)
		}
		return "click"
	case "browser.upload":
		return "upload"
	case "browser.action":
		if in.TargetAction == "" {
			in.TargetAction = in.Action
		}
		return traceLabelText("tab "+in.TargetAction, 40)
	}
	return traceLabelText(strings.TrimPrefix(strings.TrimPrefix(action, "desktop."), "browser."), 40)
}

// normalizeTraceOrigins validates the trace_private_origins value: one entry
// per line (a comma also separates). An entry with a scheme names one origin;
// an entry without a scheme names a host and its subdomains.
func normalizeTraceOrigins(text string) ([]string, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ',' || r == '\r' })
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if utf8.RuneCountInString(f) > tracePrivateOriginLen {
			return nil, errors.New("an entry is too long")
		}
		entry, err := normalizeTraceOrigin(f)
		if err != nil {
			return nil, err
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	if len(out) > tracePrivateOriginsMax {
		return nil, fmt.Errorf("at most %d entries", tracePrivateOriginsMax)
	}
	return out, nil
}

func normalizeTraceOrigin(entry string) (string, error) {
	if strings.Contains(entry, "://") {
		u, err := url.Parse(entry)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return "", fmt.Errorf("%q is not a web origin", entry)
		}
		return formOrigin(u), nil
	}
	host := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(entry, "*."), "."))
	if host == "" || strings.ContainsAny(host, " /?#@") {
		return "", fmt.Errorf("%q is not a host name", entry)
	}
	return host, nil
}

// traceOriginMatches says whether a page address falls under one entry.
func traceOriginMatches(entry, pageURL string) bool {
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	if strings.Contains(entry, "://") {
		return formOrigin(u) == entry
	}
	host := strings.ToLower(u.Hostname())
	return host == entry || strings.HasSuffix(host, "."+entry)
}

func (s *Server) tracePrivateOrigins() []string {
	list, _ := normalizeTraceOrigins(s.autoValue(autoTracePrivateOrigins))
	return list
}

// privateTraceOrigin says whether the page is one whose picture is never kept:
// an origin a vault secret is bound to, or one the person listed.
func (s *Server) privateTraceOrigin(pageURL string) bool {
	if strings.TrimSpace(pageURL) == "" {
		return false
	}
	for _, entry := range s.tracePrivateOrigins() {
		if traceOriginMatches(entry, pageURL) {
			return true
		}
	}
	if s.secretVault == nil {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	origin := formOrigin(u)
	s.secretVault.mu.Lock()
	defer s.secretVault.mu.Unlock()
	for _, rec := range s.secretVault.records {
		if rec.Target != "" && strings.Contains(rec.Target, "://") && rec.Target == origin {
			return true
		}
	}
	return false
}

// traceRedaction names the privacy rule that applies to the next capture of the
// run, or "" when the picture may be kept.
func (s *Server) traceRedaction(runID, pageURL string) string {
	if s.store.secretWindowOpen(runID) {
		return traceReasonSecretInput
	}
	if s.privateTraceOrigin(pageURL) {
		return traceReasonPrivateOrigin
	}
	return ""
}

// traceTileJPEG shrinks a capture to the stored picture: long side 640 and at
// most 120 KB.
func traceTileJPEG(img image.Image) ([]byte, int, int, bool) {
	small := resizeToLongSide(img, traceTileLongSide)
	b := small.Bounds()
	flat := flattenOnWhite(small)
	for _, quality := range []int{traceTileQualityFirst, 55, 40, 30} {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, flat, &jpeg.Options{Quality: quality}) == nil && buf.Len() <= traceTileBytesMax {
			return buf.Bytes(), b.Dx(), b.Dy(), true
		}
	}
	return nil, 0, 0, false
}

// finishCapture is the one point every screenshot passes on its way to the
// model. It decides what the person's trace may keep, stores the picture, then
// scales the result for the requested detail and numbers it.
func (s *Server) finishCapture(ctx context.Context, r Run, tool, action string, args json.RawMessage, result, detail string) (string, error) {
	pageURL := pageURLFromResult(result)
	if pageURL != "" {
		s.noteTracePage(r.ID, pageURL)
	}
	if s.store == nil {
		return applyCaptureDetail(result, detail), nil
	}
	if s.store.holdsPendingTakeover(r.ID) {
		return "", tooloutcome.New(tooloutcome.Denied, "takeover_active", "not_executed", "The person controls the screen now. Wait for the handover to end, then capture again.", "explain_blocker").Err()
	}
	out := applyCaptureDetail(result, detail)
	if r.ID == "" || !s.autoBool(autoWorkTrace) {
		return out, nil
	}
	st := s.traceState(r.ID)
	st.mu.Lock()
	if pageURL == "" {
		pageURL = st.pageURL
	}
	label := st.lastAction
	st.lastAction = ""
	st.mu.Unlock()
	if label == "" {
		label = strings.TrimPrefix(strings.TrimPrefix(action, "desktop."), "browser.")
	}
	row := RunCapture{RunID: r.ID, TakenAt: now(), Tool: tool, Action: action, Label: label, Detail: detail}
	reason := s.traceRedaction(r.ID, pageURL)
	if reason == "" && s.store.runCaptureCount(r.ID) >= traceCapturesPerRunMax {
		reason = traceReasonLimit
	}
	switch reason {
	case traceReasonSecretInput, traceReasonPrivateOrigin:
		row.Redacted, row.Reason = true, reason
	case "":
		_, uri, ok := captureResultFields(result)
		if ok {
			img, decoded := decodeCaptureImage(uri)
			if decoded {
				if data, w, h, fit := traceTileJPEG(img); fit {
					att, err := s.store.addAttachmentOpts(ctx, r.ConversationID, fmt.Sprintf("capture-%s-%d.jpg", traceShortID(r.ID), s.store.runCaptureCount(r.ID)+1), "image/jpeg", bytes.NewReader(data), attachmentOpts{Origin: attachmentOriginTrace, BotID: r.BotID, MaxBytes: traceTileBytesMax})
					if err == nil {
						row.AttachmentID, row.Width, row.Height = att.ID, w, h
					} else {
						row.Reason = traceReasonStorage
					}
				}
			}
		}
	default:
		row.Reason = reason
	}
	seq, err := s.store.addRunCapture(row)
	if err != nil {
		log.Printf("[trace] could not record a capture of run %s: %v", r.ID, err)
		return out, nil
	}
	return withCaptureSeq(out, seq), nil
}

func traceShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// deleteRunTrace removes the pictures and rows of one run.
func (s *Store) deleteRunTrace(runID string) (int, error) {
	rows, err := s.db.Query(`SELECT attachment_id FROM run_captures WHERE run_id=? AND attachment_id IS NOT NULL`, runID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.deleteUnboundAttachment(id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if _, err := s.db.Exec(`DELETE FROM run_captures WHERE run_id=?`, runID); err != nil {
		return 0, err
	}
	s.deleteRunTraceStrip(runID)
	return len(ids), nil
}

func (s *Server) routeRunCaptures(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 || parts[0] != "runs" || parts[2] != "captures" {
		return false
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
	switch r.Method {
	case http.MethodGet:
		captures, err := s.store.runCaptures(runID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "Could not read the trace")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"captures": captures, "trace": s.store.runTraceStrip(runID)})
	case http.MethodDelete:
		n, err := s.store.deleteRunTrace(runID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "Could not delete the trace")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
	}
	return true
}

// noteEngineTrace records what a browser_ tool call leaves for the work trace:
// the page it reached, a short label, and the end of the secret window when it
// opened a page.
func (s *Server) noteEngineTrace(runID, tool string, args map[string]any, page *pwPage) {
	if pageURL, _ := page.get(); pageURL != "" {
		s.noteTracePage(runID, pageURL)
	}
	label := strings.TrimPrefix(tool, "browser_")
	switch tool {
	case "browser_snapshot", "browser_take_screenshot":
		return
	case "browser_navigate":
		pageURL, _ := args["url"].(string)
		if host := urlHost(pageURL); host != "" {
			label = "navigate " + host
		}
	case "browser_type", "browser_fill_form":
		label = "type"
	}
	s.noteTraceAction(runID, traceLabelText(label, 40))
	if tool == "browser_navigate" && s.store != nil {
		s.store.closeSecretWindow(runID)
	}
}
