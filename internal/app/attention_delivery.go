package app

// Notification delivery view (cloud-services design 10, decision D78, phase 2).
//
// Phase 1 wrote pointer rows into attention. Phase 2 serves them to a client
// that shows them (the web notifier, the Mac app's main process) through
// GET /api/notifications?view=delivery. The rules of that view live in one pure
// function, deliverable: per-kind switches, rows the person already read,
// needs_you cards that were answered elsewhere, one item per conversation and
// kind with a count, the quiet mark for a burst, and the title and preview.
// The HTTP layer only fetches the facts the function needs. A client keeps the
// three things only the device knows: whether the person is looking at the
// conversation, showing the item, and where a click goes.

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	// attentionMaxWait is the longest long poll (the same cap as a device job poll).
	attentionMaxWait = 25 * time.Second
	// attentionMaxWaiters bounds the open long polls of one workspace.
	attentionMaxWaiters = 4
	// attentionQuietWindow: an item that follows a deliverable row of its
	// conversation within this window is marked quiet (a client replaces the
	// banner without sound).
	attentionQuietWindow = 30 * time.Second
	// attentionPreviewRunes is the preview cap (the web client's previewText).
	attentionPreviewRunes = 160
	// attentionDeliveryVersion is server-info notifications.delivery.
	attentionDeliveryVersion = 1
)

// Timing of the long poll; tests shorten them.
var (
	attentionSettle    = 2 * time.Second
	attentionPollEvery = time.Second
)

// notificationsServerInfo is the server-info field a client reads before it
// sends a single delivery request: an older server has no such field.
func notificationsServerInfo() map[string]any {
	return map[string]any{"delivery": attentionDeliveryVersion, "max_wait": int(attentionMaxWait / time.Second)}
}

// deliveryFacts is everything deliverable needs besides the rows. It is read by
// the HTTP layer; the function itself touches nothing.
type deliveryFacts struct {
	Prefs   userPreferences
	Content bool // the request asks for message content (preview)
	// Known conversations: absent means deleted, and its rows are dropped.
	Titles  map[string]string
	Reads   map[string]int64 // conversation_reads.read_seq
	Pending map[string]bool  // the conversation has a pending card on a waiting run
	Bots    map[string]string
	// Previews keyed by deliveryPreviewKey(conversation, seq); already trimmed.
	Previews map[string]string
}

func deliveryPreviewKey(conversationID string, seq int64) string {
	return conversationID + "#" + strconv.FormatInt(seq, 10)
}

// DeliveryItem is one thing to show. id is the last row merged into it, so a
// client that remembers the highest id it showed never shows it twice.
type DeliveryItem struct {
	ID             int64  `json:"id"`
	Kind           string `json:"kind"`
	Detail         string `json:"detail"`
	ConversationID string `json:"conversation_id,omitempty"`
	BotID          string `json:"bot_id,omitempty"`
	MessageSeq     *int64 `json:"message_seq"`
	Count          int    `json:"count"`
	Quiet          bool   `json:"quiet"`
	Title          string `json:"title"`
	BotName        string `json:"bot_name"`
	Preview        string `json:"preview"`
	CreatedAt      string `json:"created_at"`
}

// kindEnabled applies the per-kind account switch.
func kindEnabled(p userPreferences, kind string) bool {
	switch kind {
	case attentionReply:
		return p.NotifyChats
	case attentionTaskResult:
		return p.NotifyTasks
	case attentionNeedsYou:
		return p.NotifyNeedsYou
	case attentionSpendAlert:
		return p.NotifySpendAlerts
	}
	return false
}

// deliverableRow is the per-row filter: the switch, the read cursor, and a card
// that is no longer waiting for the person.
func deliverableRow(row AttentionRow, f deliveryFacts) bool {
	if !kindEnabled(f.Prefs, row.Kind) {
		return false
	}
	if row.ConversationID != "" {
		if _, known := f.Titles[row.ConversationID]; !known {
			return false
		}
		if row.MessageSeq != nil && *row.MessageSeq <= f.Reads[row.ConversationID] {
			return false
		}
	}
	if row.Kind == attentionNeedsYou && !f.Pending[row.ConversationID] {
		return false
	}
	return true
}

// deliverable is the only rule point of phase 2. rows are the unseen rows in id
// order; before are rows with ids at or below the cursor that fall in the quiet
// window ahead of the first unseen row (used for the quiet mark only). The
// result holds one item per (kind, detail, conversation), in the order of each
// item's last row. Filtered rows produce nothing; the caller still advances its
// cursor past them.
func deliverable(rows, before []AttentionRow, f deliveryFacts) []DeliveryItem {
	type group struct {
		item    DeliveryItem
		firstID int64
		first   time.Time
	}
	groups := map[string]*group{}
	var order []string
	for _, row := range rows {
		if !deliverableRow(row, f) {
			continue
		}
		key := row.Kind + "\x00" + row.Detail + "\x00" + row.ConversationID
		g := groups[key]
		if g == nil {
			g = &group{firstID: row.ID, first: parseAttentionTime(row.CreatedAt)}
			groups[key] = g
			order = append(order, key)
		}
		g.item.Count++
		g.item.ID, g.item.Kind, g.item.Detail = row.ID, row.Kind, row.Detail
		g.item.ConversationID, g.item.BotID, g.item.MessageSeq, g.item.CreatedAt = row.ConversationID, row.BotID, row.MessageSeq, row.CreatedAt
	}
	// Deliverable rows of the same conversation, from this and earlier responses.
	delivered := map[string][]AttentionRow{}
	for _, list := range [][]AttentionRow{before, rows} {
		for _, row := range list {
			if row.ConversationID != "" && deliverableRow(row, f) {
				delivered[row.ConversationID] = append(delivered[row.ConversationID], row)
			}
		}
	}
	items := make([]DeliveryItem, 0, len(order))
	for _, key := range order {
		g := groups[key]
		it := g.item
		it.Title = f.Titles[it.ConversationID]
		it.BotName = f.Bots[it.BotID]
		if f.Content && (it.Kind == attentionReply || it.Kind == attentionTaskResult) && it.MessageSeq != nil {
			it.Preview = f.Previews[deliveryPreviewKey(it.ConversationID, *it.MessageSeq)]
		}
		// Quiet: a deliverable row of the conversation, older than this item's
		// first row, inside the window. Stateless: it reads created_at only.
		// needs_you and spend_alert are more urgent than a reply: never quiet.
		burstKind := it.Kind == attentionReply || it.Kind == attentionTaskResult
		for _, prior := range delivered[it.ConversationID] {
			if !burstKind {
				break
			}
			age := g.first.Sub(parseAttentionTime(prior.CreatedAt))
			if prior.ID < g.firstID && age >= 0 && age <= attentionQuietWindow {
				it.Quiet = true
				break
			}
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func parseAttentionTime(stamp string) time.Time {
	t, err := time.Parse(storedTimeLayout, stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

var (
	previewMarkdown = regexp.MustCompile("\\[(.*?)]\\([^)]*\\)?|[*_`#>]")
	previewSpace    = regexp.MustCompile(`\s+`)
	replyMarker     = regexp.MustCompile(`(?i)^\s*\[message_id=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\]\s*`)
	replyPartial    = regexp.MustCompile(`(?i)^\s*\[message_id(?:=[0-9a-f-]*)?$`)
)

// attentionPreview is the web client's previewText: reply markers and light
// markdown removed, white space collapsed, 160 characters with an ellipsis.
func attentionPreview(content string) string {
	body, hadMarker := content, false
	for m := replyMarker.FindString(body); m != ""; m = replyMarker.FindString(body) {
		body, hadMarker = body[len(m):], true
	}
	if replyPartial.MatchString(body) && (hadMarker || replyPartial.MatchString(content)) {
		return ""
	}
	text := previewMarkdown.ReplaceAllString(body, "$1")
	text = strings.TrimSpace(previewSpace.ReplaceAllString(text, " "))
	if utf8.RuneCountInString(text) > attentionPreviewRunes {
		return string([]rune(text)[:attentionPreviewRunes-1]) + "…"
	}
	return text
}

// attentionContext returns rows at or below the cursor that sit in the quiet
// window ahead of the oldest unseen row.
func (s *Store) attentionContext(after int64, firstCreated string) ([]AttentionRow, error) {
	first := parseAttentionTime(firstCreated)
	if first.IsZero() || after <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id,kind,COALESCE(detail,''),COALESCE(conversation_id,''),COALESCE(bot_id,''),message_seq,created_at FROM attention WHERE id<=? AND created_at>=? ORDER BY id LIMIT ?`,
		after, stampTime(first.Add(-attentionQuietWindow)), attentionMaxLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttentionRow
	for rows.Next() {
		var row AttentionRow
		var seq sql.NullInt64
		if err := rows.Scan(&row.ID, &row.Kind, &row.Detail, &row.ConversationID, &row.BotID, &seq, &row.CreatedAt); err != nil {
			return nil, err
		}
		if seq.Valid {
			v := seq.Int64
			row.MessageSeq = &v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// attentionTail is the highest id ever issued, so a cursor taken now never
// points below a row written later even when retention emptied the table.
func (s *Store) attentionTail() (int64, error) {
	var tail sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(
		COALESCE((SELECT seq FROM sqlite_sequence WHERE name='attention'),0),
		COALESCE((SELECT MAX(id) FROM attention),0))`).Scan(&tail); err != nil {
		return 0, err
	}
	return tail.Int64, nil
}

// attentionPending reports whether any row exists after the cursor (a primary key probe).
func (s *Store) attentionPending(after int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM attention WHERE id>? LIMIT 1`, after).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// deliveryFactsFor reads what deliverable needs for the given rows.
func (s *Store) deliveryFactsFor(lists ...[]AttentionRow) (deliveryFacts, error) {
	prefs, err := s.userPreferences()
	if err != nil {
		return deliveryFacts{}, err
	}
	f := deliveryFacts{Prefs: prefs, Titles: map[string]string{}, Reads: map[string]int64{}, Pending: map[string]bool{}, Bots: map[string]string{}, Previews: map[string]string{}}
	for _, list := range lists {
		for _, row := range list {
			if row.BotID != "" {
				if _, ok := f.Bots[row.BotID]; !ok {
					var name string
					if err := s.db.QueryRow(`SELECT name FROM bots WHERE id=?`, row.BotID).Scan(&name); err != nil && err != sql.ErrNoRows {
						return f, err
					}
					f.Bots[row.BotID] = name
				}
			}
			conv := row.ConversationID
			if conv == "" {
				continue
			}
			if _, done := f.Titles[conv]; !done {
				var name string
				var read int64
				var pending int
				err := s.db.QueryRow(`SELECT c.name,COALESCE((SELECT read_seq FROM conversation_reads WHERE conversation_id=c.id),0),
					EXISTS(SELECT 1 FROM runs r JOIN run_input_waits w ON w.run_id=r.id AND w.state='waiting'
						JOIN questions q ON q.id=w.question_id AND q.status=? WHERE r.conversation_id=c.id)
					FROM conversations c WHERE c.id=?`, questionPending, conv).Scan(&name, &read, &pending)
				if err == sql.ErrNoRows {
					continue
				}
				if err != nil {
					return f, err
				}
				f.Titles[conv], f.Reads[conv], f.Pending[conv] = name, read, pending != 0
			}
		}
	}
	return f, nil
}

// fillPreviews reads the text of the messages the items point at. Only rows of
// the unseen list can reach an item, so only those are read.
func (s *Store) fillPreviews(f *deliveryFacts, rows []AttentionRow) error {
	for _, row := range rows {
		if row.MessageSeq == nil || row.ConversationID == "" || (row.Kind != attentionReply && row.Kind != attentionTaskResult) {
			continue
		}
		key := deliveryPreviewKey(row.ConversationID, *row.MessageSeq)
		if _, ok := f.Previews[key]; ok {
			continue
		}
		var content string
		err := s.db.QueryRow(`SELECT substr(content,1,2000) FROM messages WHERE conversation_id=? AND seq=?`, row.ConversationID, *row.MessageSeq).Scan(&content)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		f.Previews[key] = attentionPreview(content)
	}
	return nil
}

// deliveryView builds the delivery response body for rows after the cursor.
func (s *Store) deliveryView(rows []AttentionRow, after int64, content bool) (items []DeliveryItem, language string, err error) {
	var before []AttentionRow
	if len(rows) > 0 {
		if before, err = s.attentionContext(after, rows[0].CreatedAt); err != nil {
			return nil, "", err
		}
	}
	f, err := s.deliveryFactsFor(rows, before)
	if err != nil {
		return nil, "", err
	}
	f.Content = content
	if content {
		if err = s.fillPreviews(&f, rows); err != nil {
			return nil, "", err
		}
	}
	return deliverable(rows, before, f), f.Prefs.Language, nil
}

// waitForAttention long-polls like waitForJob: it returns once a row exists
// after the cursor and the settle time has passed (the rest of a burst lands in
// the same response and other devices get time to move the read cursor), or
// empty when wait runs out or the client goes away. It reports whether the
// request was cancelled.
func (s *Server) waitForAttention(ctx context.Context, after int64, wait time.Duration) (cancelled bool, err error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		found, err := s.store.attentionPending(after)
		if err != nil {
			return false, err
		}
		if found {
			select {
			case <-ctx.Done():
				return true, nil
			case <-time.After(attentionSettle):
				return false, nil
			}
		}
		select {
		case <-ctx.Done():
			return true, nil
		case <-deadline.C:
			return false, nil
		case <-time.After(attentionPollEvery):
		}
	}
}

// attentionWaiters counts the open long polls of a workspace.
type attentionWaiters struct{ n atomic.Int32 }

func (w *attentionWaiters) acquire() bool {
	for {
		n := w.n.Load()
		if n >= attentionMaxWaiters {
			return false
		}
		if w.n.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (w *attentionWaiters) release() { w.n.Add(-1) }

// parseAttentionWait reads ?wait=<seconds>; more than the cap is the cap.
func parseAttentionWait(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return min(time.Duration(n)*time.Second, attentionMaxWait), true
}

func (s *Server) serveNotifications(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	delivery := false
	switch view := strings.TrimSpace(query.Get("view")); view {
	case "":
	case "delivery":
		delivery = true
	default:
		writeErr(w, http.StatusBadRequest, "invalid_view", "view must be delivery")
		return
	}
	var after int64
	if v := strings.TrimSpace(query.Get("after")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid_cursor", "after must be a non-negative integer")
			return
		}
		after = n
	}
	limit := attentionDefaultLimit
	if v := strings.TrimSpace(query.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		limit = min(n, attentionMaxLimit)
	}
	wait, ok := parseAttentionWait(query.Get("wait"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_wait", "wait must be a non-negative number of seconds")
		return
	}
	content := true
	switch strings.TrimSpace(query.Get("content")) {
	case "", "1", "true":
	case "0", "false":
		content = false
	default:
		writeErr(w, http.StatusBadRequest, "invalid_content", "content must be 0 or 1")
		return
	}
	tail := false
	switch strings.TrimSpace(query.Get("tail")) {
	case "":
	case "1":
		tail = true
	default:
		writeErr(w, http.StatusBadRequest, "invalid_tail", "tail must be 1")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if tail {
		n, err := s.store.attentionTail()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "notifications unavailable")
			return
		}
		if delivery {
			writeJSON(w, http.StatusOK, deliveryBody{Items: []DeliveryItem{}, NextAfter: n})
		} else {
			writeJSON(w, http.StatusOK, rawBody{Notifications: []AttentionRow{}, NextAfter: n})
		}
		return
	}
	if wait > 0 {
		if !s.attentionWaiters.acquire() {
			writeErr(w, http.StatusTooManyRequests, "too_many_waiters", "too many open notification waits")
			return
		}
		cancelled, err := s.waitForAttention(r.Context(), after, wait)
		s.attentionWaiters.release()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "notifications unavailable")
			return
		}
		if cancelled {
			return
		}
	}
	rows, err := s.store.attentionAfter(after, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "notifications unavailable")
		return
	}
	next := after
	if len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	if !delivery {
		writeJSON(w, http.StatusOK, rawBody{Notifications: rows, NextAfter: next})
		return
	}
	items, language, err := s.store.deliveryView(rows, after, content)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "notifications unavailable")
		return
	}
	writeJSON(w, http.StatusOK, deliveryBody{Items: items, NextAfter: next, Language: language})
}

// rawBody is the phase 1 response; its bytes must not change.
type rawBody struct {
	Notifications []AttentionRow `json:"notifications"`
	NextAfter     int64          `json:"next_after"`
}

type deliveryBody struct {
	Items     []DeliveryItem `json:"items"`
	NextAfter int64          `json:"next_after"`
	Language  string         `json:"language"`
}
