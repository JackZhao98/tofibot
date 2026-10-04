package app

// Portable bundles are data, never executable snapshots. Only allowlisted rows
// enter the current authenticated workspace; source IDs are provenance.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const portableMaxBytes = 16 << 20
const portableMaxRecords = 20000

var portableCategories = []string{"bot_config", "chats", "memories", "schedules", "attachments", "settings"}
var portableExcluded = []string{"credentials", "private_ssh_keys", "attachments", "guest_files_and_disk", "extensions_and_skills", "work_items", "mail_drafts", "run_and_tool_history", "reactions", "summaries", "browser_preferences", "authentication_and_approvals"}

type portableOrigin struct {
	InstanceID     string          `json:"instance_id"`
	RecordID       string          `json:"record_id"`
	RunID          string          `json:"run_id,omitempty"`
	SenderBotID    string          `json:"sender_bot_id,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	BotID          string          `json:"bot_id,omitempty"`
	Status         string          `json:"status,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Notice         json.RawMessage `json:"notice,omitempty"`
}
type portableBot struct {
	Bot
	Avatar json.RawMessage `json:"avatar,omitempty"`
	Origin portableOrigin  `json:"origin"`
}
type portableConversation struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	BotID       string   `json:"bot_id,omitempty"`
	BotIDs      []string `json:"bot_ids"`
	UpdatedAt   string   `json:"updated_at"`
	Archived    bool     `json:"archived"`
	UserVisible bool     `json:"user_visible"`
}
type portableMessage struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Seq            int64          `json:"seq"`
	Role           string         `json:"role"`
	Kind           string         `json:"kind,omitempty"`
	SenderBotID    string         `json:"sender_bot_id,omitempty"`
	Content        string         `json:"content"`
	CreatedAt      string         `json:"created_at"`
	Origin         portableOrigin `json:"origin"`
}
type portableMemory struct {
	Memory
	Origin portableOrigin `json:"origin"`
}
type portableSchedule struct {
	ID              string         `json:"id"`
	ConversationID  string         `json:"conversation_id"`
	BotID           string         `json:"bot_id"`
	Content         string         `json:"content"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	CreatedBy       string         `json:"created_by"`
	Kind            string         `json:"kind"`
	Timezone        string         `json:"timezone"`
	NextAtUTC       string         `json:"next_at_utc"`
	IntervalSeconds int64          `json:"interval_seconds"`
	DailyTime       string         `json:"daily_time"`
	Status          string         `json:"status"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	Origin          portableOrigin `json:"origin"`
}
type portableSettings struct {
	Timezone        string `json:"timezone"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	DictationModel  string `json:"dictation_model"`
}
type portableBundle struct {
	Format             string                      `json:"format"`
	Version            int                         `json:"version"`
	Kind               string                      `json:"kind"`
	SourceInstance     string                      `json:"source_instance"`
	CreatedAt          string                      `json:"created_at"`
	Included           []string                    `json:"included"`
	Counts             map[string]int              `json:"counts"`
	Excluded           []string                    `json:"excluded"`
	AttachmentCount    int                         `json:"excluded_attachment_count"`
	SkippedGroups      int                         `json:"skipped_group_count"`
	Bots               []portableBot               `json:"bots"`
	Conversations      []portableConversation      `json:"conversations"`
	Messages           []portableMessage           `json:"messages"`
	Memories           []portableMemory            `json:"memories"`
	Schedules          []portableSchedule          `json:"schedules"`
	Settings           *portableSettings           `json:"settings,omitempty"`
	Attachments        []portableAttachment        `json:"attachments,omitempty"`
	AttachmentBindings []portableAttachmentBinding `json:"attachment_bindings,omitempty"`
	MissingAttachments []portableMissingAttachment `json:"missing_attachments,omitempty"`
}
type portableSelection struct {
	Categories []string `json:"categories"`
	BotIDs     []string `json:"bot_ids"`
}
type portableImportRequest struct {
	Bundle    json.RawMessage   `json:"bundle"`
	Selection portableSelection `json:"selection"`
	PreviewID string            `json:"preview_id,omitempty"`
}
type portablePreview struct {
	SourceFormat    string         `json:"source_format"`
	SourceVersion   int            `json:"source_version"`
	EstimatedBytes  int            `json:"estimated_bytes"`
	Dependencies    []string       `json:"dependencies"`
	AttachmentBytes int64          `json:"attachment_bytes"`
	CanApply        bool           `json:"can_apply"`
	ID              string         `json:"preview_id"`
	Counts          map[string]int `json:"counts"`
	Bots            []portableBot  `json:"bots"`
	Conflicts       []string       `json:"conflicts"`
	Warnings        []string       `json:"warnings"`
	Excluded        []string       `json:"excluded"`
	ExpiresAt       string         `json:"expires_at"`
}
type portableResult struct {
	ImportID string            `json:"import_id"`
	Counts   map[string]int    `json:"counts"`
	IDMap    map[string]string `json:"id_map"`
}

func migratePortability(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS portability_imports(
id TEXT PRIMARY KEY,digest TEXT NOT NULL,destination TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('preview','applied')),
expires_at INTEGER NOT NULL,created_at TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS portability_provenance(kind TEXT NOT NULL,target_id TEXT NOT NULL,source_json TEXT NOT NULL,PRIMARY KEY(kind,target_id));
PRAGMA synchronous=FULL;
CREATE TABLE IF NOT EXISTS portability_asset_staging(import_id TEXT NOT NULL,target_id TEXT NOT NULL UNIQUE,sha256 TEXT NOT NULL,size INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(import_id,target_id));
CREATE TABLE IF NOT EXISTS portability_missing_assets(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,metadata_json TEXT NOT NULL,FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE);`)
	return err
}

// Detect duplicate fields, excessive nesting and trailing input before typed
// decoding. No archive extraction, URL fetching, directory walking or scripts.
func portableJSON(data []byte, out any) error {
	if len(data) == 0 || len(data) > portableMaxBytes {
		return errors.New("bundle exceeds the 16 MiB limit or is empty")
	}
	if !utf8.Valid(data) {
		return errors.New("bundle must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("JSON nesting limit exceeded")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || keys[key] {
						return errors.New("duplicate JSON field")
					}
					keys[key] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if err = walk(depth + 1); err != nil {
						return err
					}
				}
			default:
				return errors.New("invalid JSON")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return errors.New("invalid, duplicate-field or excessively nested JSON bundle")
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	if err := portableCanonicalFields(data, reflect.TypeOf(out)); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("bundle contains invalid or unsupported fields")
	}
	return nil
}
func portableID(id string) bool  { u, err := uuid.Parse(id); return err == nil && u.String() == id }
func portableTime(t string) bool { _, err := time.Parse(time.RFC3339Nano, t); return err == nil }
func portableSet(values []string, allowed []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, v := range values {
		found := false
		for _, a := range allowed {
			found = found || v == a
		}
		if !found || set[v] {
			return nil, errors.New("unknown or duplicate selection")
		}
		set[v] = true
	}
	return set, nil
}
func (b portableBundle) counts() map[string]int {
	n := 0
	if b.Settings != nil {
		n = 1
	}
	counts := map[string]int{"bot_config": len(b.Bots), "conversations": len(b.Conversations), "chats": len(b.Messages), "memories": len(b.Memories), "schedules": len(b.Schedules), "settings": n}
	if b.Version == 2 {
		counts["attachments"] = len(b.Attachments)
		counts["attachment_bindings"] = len(b.AttachmentBindings)
		counts["missing_attachments"] = b.AttachmentCount
	}
	return counts
}
func parsePortableBundle(data []byte) (portableBundle, error) {
	var header struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(data, &header) != nil {
		return portableBundle{}, errors.New("only uncompressed TOFI JSON bundles are supported")
	}
	if header.Format == "tofi.bot" {
		var old struct {
			Format   string   `json:"format"`
			Version  int      `json:"version"`
			Included []string `json:"included"`
			Bot      struct {
				Name         string          `json:"name"`
				Instructions string          `json:"instructions"`
				Model        string          `json:"model"`
				Reasoning    string          `json:"reasoning_effort,omitempty"`
				Avatar       json.RawMessage `json:"avatar,omitempty"`
			} `json:"bot"`
		}
		if err := portableJSON(data, &old); err != nil {
			return portableBundle{}, err
		}
		if old.Version != 1 || !reflect.DeepEqual(old.Included, []string{"bot_config"}) {
			return portableBundle{}, errors.New("unsupported Bot package version")
		}
		// Deterministic source identities keep preview and apply bound to the same legacy file.
		id := uuid.NewSHA1(uuid.NameSpaceOID, data).String()
		dm := uuid.NewSHA1(uuid.NameSpaceOID, []byte(id+":dm")).String()
		t := "1970-01-01T00:00:00Z"
		b := portableBundle{Format: "tofi.bundle", Version: 1, Kind: "bot", SourceInstance: "legacy-bot-package", CreatedAt: t, Included: old.Included, Excluded: portableExcluded}
		b.Bots = []portableBot{{Bot: Bot{ID: id, Name: old.Bot.Name, Instructions: old.Bot.Instructions, Model: old.Bot.Model, ReasoningEffort: old.Bot.Reasoning, DMConversationID: dm, CreatedAt: t}, Avatar: old.Bot.Avatar, Origin: portableOrigin{InstanceID: b.SourceInstance, RecordID: id}}}
		b.Conversations = []portableConversation{{ID: dm, Kind: "dm", Name: old.Bot.Name, BotID: id, BotIDs: []string{id}, UpdatedAt: t, UserVisible: true}}
		b.Counts = b.counts()
		return b, b.validate()
	}
	var b portableBundle
	if err := portableJSON(data, &b); err != nil {
		return b, err
	}
	return b, b.validate()
}
func (b portableBundle) validate() error {
	bad := func() error { return errors.New("bundle has invalid counts, records or references") }
	if b.Format != "tofi.bundle" || (b.Version != 1 && b.Version != 2) || (b.Kind != "account" && b.Kind != "bot") {
		return errors.New("unsupported bundle format or version")
	}
	if len(b.SourceInstance) > 200 || b.SourceInstance == "" || !portableTime(b.CreatedAt) || b.AttachmentCount < 0 || b.SkippedGroups < 0 || !reflect.DeepEqual(b.Counts, b.counts()) {
		return bad()
	}
	categories, err := portableSet(b.Included, portableCategories)
	if err != nil || !categories["bot_config"] {
		return bad()
	}
	if (!categories["chats"] && len(b.Messages) > 0) || (!categories["memories"] && len(b.Memories) > 0) || (!categories["schedules"] && len(b.Schedules) > 0) || (!categories["settings"] && b.Settings != nil) || (b.Kind == "bot" && (len(b.Bots) != 1 || b.Settings != nil)) {
		return bad()
	}
	missingRefs := 0
	for _, x := range b.MissingAttachments {
		missingRefs += len(x.MessageIDs)
	}
	if len(b.Bots) > 1000 || len(b.Bots)+len(b.Conversations)+len(b.Messages)+len(b.Memories)+len(b.Schedules)+len(b.Attachments)+len(b.AttachmentBindings)+len(b.MissingAttachments)+missingRefs > portableMaxRecords {
		return errors.New("bundle record limit exceeded")
	}
	ids := map[string]bool{}
	bots := map[string]portableBot{}
	convs := map[string]portableConversation{}
	unique := func(id string) bool {
		if !portableID(id) || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	originOK := func(o portableOrigin) bool {
		return len(o.InstanceID) <= 200 && len(o.RecordID) <= 200 && len(o.RunID) <= 200 && len(o.SenderBotID) <= 200 && len(o.BotID) <= 200 && len(o.Kind) <= 100 && len(o.Status) <= 100 && len(o.ConversationID) <= 200 && len(o.Notice) <= 256<<10
	}
	for _, x := range b.Bots {
		if !unique(x.ID) || !portableID(x.DMConversationID) || strings.TrimSpace(x.Name) == "" || utf8.RuneCountInString(x.Name) > 200 || utf8.RuneCountInString(x.Instructions) > 200000 || utf8.RuneCountInString(x.Model) > 200 || len(x.ReasoningEffort) > 100 || !portableTime(x.CreatedAt) || len(x.Avatar) > 4096 || !originOK(x.Origin) {
			return bad()
		}
		bots[x.ID] = x
	}
	for _, x := range b.Conversations {
		if !unique(x.ID) || utf8.RuneCountInString(x.Name) > 200 || !portableTime(x.UpdatedAt) || (x.Kind != "dm" && x.Kind != "group") {
			return bad()
		}
		members := map[string]bool{}
		for _, id := range x.BotIDs {
			if _, ok := bots[id]; !ok || members[id] {
				return bad()
			}
			members[id] = true
		}
		if b.Kind == "bot" && x.Kind != "dm" {
			return bad()
		}
		if x.Kind == "dm" {
			bot, ok := bots[x.BotID]
			if !ok || bot.DMConversationID != x.ID || len(x.BotIDs) != 1 || !members[x.BotID] {
				return bad()
			}
		} else if x.BotID != "" {
			return bad()
		}
		convs[x.ID] = x
	}
	for _, x := range b.Bots {
		if _, ok := convs[x.DMConversationID]; !ok {
			return bad()
		}
	}
	reference := func(conv, bot string) bool {
		_, ok := convs[conv]
		if !ok {
			return false
		}
		if bot == "" {
			return true
		}
		_, ok = bots[bot]
		return ok
	}
	sequences := map[string]map[int64]bool{}
	for _, x := range b.Messages {
		if !unique(x.ID) || !reference(x.ConversationID, x.SenderBotID) || x.Seq < 1 || (x.Role != "user" && x.Role != "assistant" && x.Role != "system") || len(x.Kind) > 100 || len(x.Content) > 2<<20 || !portableTime(x.CreatedAt) || !originOK(x.Origin) {
			return bad()
		}
		if sequences[x.ConversationID] == nil {
			sequences[x.ConversationID] = map[int64]bool{}
		}
		if sequences[x.ConversationID][x.Seq] {
			return bad()
		}
		sequences[x.ConversationID][x.Seq] = true
	}
	for _, x := range b.Memories {
		if !unique(x.ID) || !reference(x.ConversationID, x.BotID) || len(x.Content) > 2<<20 || len(x.Title) > 1000 || len(x.Description) > 200000 || x.Revision < 1 || !portableTime(x.CreatedAt) || !portableTime(x.UpdatedAt) || !originOK(x.Origin) {
			return bad()
		}
	}
	for _, x := range b.Schedules {
		if !unique(x.ID) || x.BotID == "" || !reference(x.ConversationID, x.BotID) || len(x.Content) > 200000 || strings.TrimSpace(x.Content) == "" || len(x.Title) > 1000 || len(x.Description) > 200000 || len(x.CreatedBy) > 200 || !portableTime(x.CreatedAt) || !portableTime(x.UpdatedAt) || !portableTime(x.NextAtUTC) || !originOK(x.Origin) {
			return bad()
		}
		if _, err := time.LoadLocation(x.Timezone); err != nil {
			return bad()
		}
		if x.Kind != "once" && x.Kind != "interval" && x.Kind != "daily" {
			return bad()
		}
		if x.Kind == "interval" && x.IntervalSeconds < 1 {
			return bad()
		}
		if x.Kind == "daily" {
			if _, err := time.Parse("15:04", x.DailyTime); err != nil {
				return bad()
			}
		}
		if x.Status != "active" && x.Status != "paused" && x.Status != "completed" && x.Status != "deleted" {
			return bad()
		}
	}
	if err := b.validatePortableAttachments(categories, ids, convs, originOK); err != nil {
		return err
	}
	if b.Settings != nil {
		x := b.Settings
		if len(x.Model) > 200 || len(x.ReasoningEffort) > 100 || (x.DictationModel != "" && !validDictationModel(x.DictationModel)) {
			return bad()
		}
		if x.Timezone != "" {
			if _, err := time.LoadLocation(x.Timezone); err != nil {
				return bad()
			}
		}
	}
	return nil
}

func selectPortable(b portableBundle, sel portableSelection) (portableBundle, error) {
	if len(sel.Categories) == 0 {
		for _, category := range b.Included {
			if category != "settings" {
				sel.Categories = append(sel.Categories, category)
			}
		}
	}
	cat, err := portableSet(sel.Categories, b.Included)
	if err != nil || !cat["bot_config"] {
		return b, errors.New("Bot configuration is required for reference closure")
	}
	selected := map[string]bool{}
	for _, id := range sel.BotIDs {
		if selected[id] {
			return b, errors.New("duplicate Bot selection")
		}
		selected[id] = true
	}
	all := len(sel.BotIDs) == 0
	out := b
	out.Included = append([]string(nil), sel.Categories...)
	sort.Strings(out.Included)
	out.Bots = nil
	for _, x := range b.Bots {
		if all || selected[x.ID] {
			out.Bots = append(out.Bots, x)
			delete(selected, x.ID)
		}
	}
	if len(selected) > 0 {
		return b, errors.New("selected Bot is not in this bundle")
	}
	chosen := map[string]bool{}
	for _, x := range out.Bots {
		chosen[x.ID] = true
	}
	out.Conversations = nil
	convs := map[string]bool{}
	for _, c := range b.Conversations {
		keep := true
		for _, id := range c.BotIDs {
			keep = keep && chosen[id]
		}
		if c.Kind == "dm" {
			keep = chosen[c.BotID]
		}
		if keep {
			out.Conversations = append(out.Conversations, c)
			convs[c.ID] = true
		} else if c.Kind == "group" {
			out.SkippedGroups++
		}
	}
	out.Messages = nil
	if cat["chats"] {
		for _, x := range b.Messages {
			if convs[x.ConversationID] {
				out.Messages = append(out.Messages, x)
			}
		}
	}
	out.Memories = nil
	if cat["memories"] {
		for _, x := range b.Memories {
			if convs[x.ConversationID] {
				out.Memories = append(out.Memories, x)
			}
		}
	}
	out.Schedules = nil
	if cat["schedules"] {
		for _, x := range b.Schedules {
			if convs[x.ConversationID] {
				out.Schedules = append(out.Schedules, x)
			}
		}
	}
	if !cat["settings"] {
		out.Settings = nil
	}
	selectPortableAttachments(&out, b, cat["attachments"], convs)
	if err := closePortableHistory(&out, b.Bots, b.Conversations); err != nil {
		return out, err
	}
	out.Counts = out.counts()
	return out, out.validate()
}

func portableDigest(b portableBundle) string {
	data, _ := json.Marshal(b)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func portableDestination(tx *sql.Tx) (string, []string, error) {
	rows, err := tx.Query(`SELECT id,name,dm_conversation_id FROM bots ORDER BY id`)
	if err != nil {
		return "", nil, err
	}
	var state []string
	names := []string{}
	for rows.Next() {
		var id, name, dm string
		if err = rows.Scan(&id, &name, &dm); err != nil {
			break
		}
		state = append(state, id, name, dm)
		names = append(names, name)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	for _, q := range []string{`SELECT COALESCE(timezone,'') FROM user_preferences WHERE id=1`, `SELECT model||':'||reasoning_effort FROM model_settings WHERE id=1`, `SELECT model FROM dictation_settings WHERE id=1`} {
		var v string
		err = tx.QueryRow(q).Scan(&v)
		if err != nil && err != sql.ErrNoRows {
			return "", nil, err
		}
		state = append(state, v)
	}
	data, _ := json.Marshal(state)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), names, nil
}
func (s *Store) previewPortable(ctx context.Context, b portableBundle) (portablePreview, error) {
	if err := b.validate(); err != nil {
		return portablePreview{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return portablePreview{}, err
	}
	defer tx.Rollback()
	dest, names, err := portableDestination(tx)
	if err != nil {
		return portablePreview{}, err
	}
	p := portablePreview{CanApply: true, ID: uuid.NewString(), Counts: b.counts(), Bots: b.Bots, Conflicts: []string{}, Excluded: b.Excluded, Warnings: []string{"Imported records are new copies. Existing records are never overwritten.", "All imported schedules are paused. Instructions and history are stored without execution.", "Content may contain secrets pasted into chats or instructions. Review before sharing.", "Guest disk, credentials, extensions, work items and execution history are excluded."}}
	data, _ := json.Marshal(b)
	p.SourceFormat, p.SourceVersion, p.EstimatedBytes = b.Format, b.Version, len(data)*3
	for _, asset := range b.Attachments {
		p.AttachmentBytes += asset.Size
		p.EstimatedBytes -= len(asset.Data) * 3
	}
	if len(b.Attachments) > 0 {
		p.Warnings = append(p.Warnings, "Attachment bytes can contain private information. Files stay on the destination account guest disk; its quota applies.")
		if s.portableBlobBackend() == nil {
			p.CanApply = false
			p.Warnings = append(p.Warnings, "Account file storage is unavailable. Deselect attachments to import the other data, or enable file storage and preview again.")
		}
	}
	p.Dependencies = []string{"Historical Bot references bring required configurations and empty DM structure, without restoring former group membership.", "Bot configuration and DM structure are required.", "Group history requires all member Bots; partial groups are skipped.", "Settings are preserved unless explicitly selected."}
	for _, x := range b.Bots {
		for _, name := range names {
			if strings.EqualFold(name, x.Name) {
				p.Conflicts = append(p.Conflicts, x.Name+": same name; import creates a new Bot")
				break
			}
		}
	}
	if b.Settings != nil {
		p.Conflicts = append(p.Conflicts, "Selected model, timezone and dictation settings replace the destination values.")
	}
	if b.AttachmentCount > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d attachment files were omitted; chat text is included without those files.", b.AttachmentCount))
	}
	for _, missing := range b.MissingAttachments {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Attachment omitted: %s (%s).", missing.Name, missing.Reason))
	}
	if b.SkippedGroups > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d groups omitted because not all member Bots were selected.", b.SkippedGroups))
	}
	expires := time.Now().Add(15 * time.Minute)
	p.ExpiresAt = expires.UTC().Format(time.RFC3339)
	// Bounded retained preview metadata. Applied journals remain as provenance/idempotency records.
	if _, err = tx.ExecContext(ctx, `DELETE FROM portability_imports WHERE status='preview' AND expires_at<?`, time.Now().Unix()); err != nil {
		return p, err
	}
	var pending int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM portability_imports WHERE status='preview'`).Scan(&pending); err != nil {
		return p, err
	}
	if pending >= 32 {
		return p, errors.New("too many pending import previews; wait for expiration")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO portability_imports(id,digest,destination,status,expires_at,created_at) VALUES(?,?,?,'preview',?,?)`, p.ID, portableDigest(b), dest, expires.Unix(), now())
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
