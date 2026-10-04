package app

// Version 2 adds bounded inline bytes, never paths, URLs, archives or executable
// resources. Guest storage is the sole import destination, including fixtures.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const portableMaxAttachmentBytes = 4 << 20
const portableMaxAttachmentTotal = 8 << 20
const portableMaxAttachments = 256

type portableAttachment struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Name           string         `json:"name"`
	MIME           string         `json:"mime"`
	Size           int64          `json:"size"`
	SHA256         string         `json:"sha256"`
	Data           string         `json:"data_base64"`
	CreatedAt      string         `json:"created_at"`
	Origin         portableOrigin `json:"origin"`
}
type portableAttachmentBinding struct {
	AttachmentID string `json:"attachment_id"`
	MessageID    string `json:"message_id"`
}
type portableMissingAttachment struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Name           string         `json:"name"`
	Reason         string         `json:"reason"`
	Origin         portableOrigin `json:"origin"`
	MessageIDs     []string       `json:"message_ids,omitempty"`
}

func portableExclusions(attachments bool) []string {
	out := []string{}
	for _, x := range portableExcluded {
		if x != "attachments" || !attachments {
			out = append(out, x)
		}
	}
	return out
}
func portableFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\\x00\r\n")
}
func portableFileMIME(data []byte) string {
	typ := http.DetectContentType(data)
	if strings.HasPrefix(typ, "image/") {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || (format != "png" && format != "jpeg" && format != "gif") || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 40_000_000 || cfg.Height > 40_000_000/cfg.Width {
			return "application/octet-stream"
		}
	}
	return typ
}
func portableAttachmentData(x portableAttachment) ([]byte, error) {
	if x.Size < 0 || x.Size > portableMaxAttachmentBytes || len(x.Data) != base64.StdEncoding.EncodedLen(int(x.Size)) {
		return nil, errors.New("invalid attachment size")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(x.Data)
	sum := sha256.Sum256(data)
	if err != nil || int64(len(data)) != x.Size || len(x.SHA256) != 64 || hex.EncodeToString(sum[:]) != x.SHA256 || base64.StdEncoding.EncodeToString(data) != x.Data || x.MIME != portableFileMIME(data) {
		return nil, errors.New("attachment bytes, hash or type do not match")
	}
	return data, nil
}
func (b portableBundle) validatePortableAttachments(cat map[string]bool, ids map[string]bool, convs map[string]portableConversation, originOK func(portableOrigin) bool) error {
	bad := func() error { return errors.New("invalid attachment manifest, bytes or references") }
	if b.Version == 1 {
		if cat["attachments"] || len(b.Attachments)+len(b.AttachmentBindings)+len(b.MissingAttachments) != 0 {
			return bad()
		}
		return nil
	}
	if !reflect.DeepEqual(b.Excluded, portableExclusions(cat["attachments"])) || len(b.Attachments) > portableMaxAttachments {
		return bad()
	}
	if !cat["attachments"] && len(b.Attachments)+len(b.AttachmentBindings)+len(b.MissingAttachments) != 0 {
		return bad()
	}
	if cat["attachments"] && b.AttachmentCount != len(b.MissingAttachments) {
		return bad()
	}
	assets := map[string]bool{}
	total := int64(0)
	for _, x := range b.Attachments {
		if !portableID(x.ID) || ids[x.ID] || convs[x.ConversationID].ID == "" || !portableFileName(x.Name) || !portableTime(x.CreatedAt) || !originOK(x.Origin) {
			return bad()
		}
		ids[x.ID] = true
		assets[x.ID] = true
		total += x.Size
		if total > portableMaxAttachmentTotal {
			return errors.New("attachment total exceeds 8 MiB")
		}
		if _, err := portableAttachmentData(x); err != nil {
			return err
		}
	}
	for _, x := range b.MissingAttachments {
		if !portableID(x.ID) || ids[x.ID] || convs[x.ConversationID].ID == "" || !portableFileName(x.Name) || !originOK(x.Origin) {
			return bad()
		}
		switch x.Reason {
		case "missing", "unavailable", "unsafe_file", "too_large", "bundle_limit", "changed_metadata":
		default:
			return bad()
		}
		ids[x.ID] = true
	}
	messages := map[string]bool{}
	for _, x := range b.Messages {
		messages[x.ID] = true
	}
	bindings := map[string]bool{}
	perMessage := map[string]int{}
	for _, missing := range b.MissingAttachments {
		seen := map[string]bool{}
		for _, id := range missing.MessageIDs {
			perMessage[id]++
			if !messages[id] || seen[id] || perMessage[id] > 8 {
				return bad()
			}
			seen[id] = true
		}
	}
	for _, x := range b.AttachmentBindings {
		key := x.AttachmentID + ":" + x.MessageID
		perMessage[x.MessageID]++
		if !assets[x.AttachmentID] || !messages[x.MessageID] || bindings[key] || perMessage[x.MessageID] > 8 {
			return bad()
		}
		bindings[key] = true
	}
	return nil
}

func selectPortableAttachments(out *portableBundle, source portableBundle, enabled bool, convs map[string]bool) {
	if source.Version == 1 {
		return
	}
	out.Excluded = portableExclusions(enabled)
	out.Attachments, out.AttachmentBindings, out.MissingAttachments = nil, nil, nil
	out.AttachmentCount = 0
	messages := map[string]string{}
	for _, x := range out.Messages {
		messages[x.ID] = x.ConversationID
	}
	referenced := map[string]string{}
	for _, x := range source.AttachmentBindings {
		if conv := messages[x.MessageID]; conv != "" && referenced[x.AttachmentID] == "" {
			referenced[x.AttachmentID] = conv
		}
	}
	kept := map[string]bool{}
	for _, x := range source.Attachments {
		if !convs[x.ConversationID] {
			if conv := referenced[x.ID]; conv != "" {
				x.ConversationID = conv
			} else {
				continue
			}
		}
		if enabled {
			out.Attachments = append(out.Attachments, x)
			kept[x.ID] = true
		} else {
			out.AttachmentCount++
		}
	}
	for _, x := range source.AttachmentBindings {
		if enabled && kept[x.AttachmentID] && messages[x.MessageID] != "" {
			out.AttachmentBindings = append(out.AttachmentBindings, x)
		}
	}
	for _, x := range source.MissingAttachments {
		filtered := []string{}
		for _, id := range x.MessageIDs {
			if messages[id] != "" {
				filtered = append(filtered, id)
			}
		}
		x.MessageIDs = filtered
		if !convs[x.ConversationID] {
			if len(filtered) == 0 {
				continue
			}
			x.ConversationID = messages[filtered[0]]
		}
		out.AttachmentCount++
		if enabled {
			out.MissingAttachments = append(out.MissingAttachments, x)
		}
	}

	// Old category omissions have only a count, so preserve that limitation when
	// the source did not carry the attachment category at all.
	hasCategory := false
	for _, x := range source.Included {
		hasCategory = hasCategory || x == "attachments"
	}
	if !hasCategory {
		out.AttachmentCount = source.AttachmentCount
	}
}

func portablePlaceholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

type portableAttachmentSource struct {
	x    portableAttachment
	disk string
}
type portableAttachmentSnapshot struct {
	sources  []portableAttachmentSource
	bindings []portableAttachmentBinding
	missing  []portableMissingAttachment
	dbFile   string
}

// Only metadata is read under SQLite's single connection. All file reads use
// this immutable snapshot after the transaction has released the connection.
func snapshotPortableAttachments(ctx context.Context, tx *sql.Tx, b *portableBundle, origins map[string]portableOrigin) (snapshot portableAttachmentSnapshot, err error) {
	convs := map[string]bool{}
	args := []any{}
	for _, c := range b.Conversations {
		convs[c.ID] = true
		args = append(args, c.ID)
	}
	messages := map[string]string{}
	for _, m := range b.Messages {
		messages[m.ID] = m.ConversationID
		args = append(args, m.ID)
	}
	query := `SELECT id,conversation_id,name,size,created_at,disk_name FROM attachments WHERE conversation_id IN (` + portablePlaceholders(len(convs)) + `) OR id IN (SELECT attachment_id FROM attachment_messages WHERE message_id IN (` + portablePlaceholders(len(messages)) + `)) ORDER BY id LIMIT ?`
	args = append(args, portableMaxRecords+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return snapshot, err
	}
	sources := []portableAttachmentSource{}
	for rows.Next() {
		var a portableAttachmentSource
		if err = rows.Scan(&a.x.ID, &a.x.ConversationID, &a.x.Name, &a.x.Size, &a.x.CreatedAt, &a.disk); err != nil {
			break
		}
		sources = append(sources, a)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return snapshot, err
	}
	if len(sources) > portableMaxRecords {
		return snapshot, errors.New("attachment record limit exceeded; select fewer Bots")
	}
	bindings := []portableAttachmentBinding{}
	firstConv := map[string]string{}
	args = nil
	for _, m := range b.Messages {
		args = append(args, m.ID)
	}
	rows, err = tx.QueryContext(ctx, `SELECT attachment_id,message_id FROM attachment_messages WHERE message_id IN (`+portablePlaceholders(len(messages))+`) ORDER BY attachment_id,message_id LIMIT ?`, append(args, portableMaxRecords+1)...)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var x portableAttachmentBinding
		if err = rows.Scan(&x.AttachmentID, &x.MessageID); err != nil {
			break
		}
		bindings = append(bindings, x)
		if firstConv[x.AttachmentID] == "" {
			firstConv[x.AttachmentID] = messages[x.MessageID]
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return snapshot, err
	}
	if len(bindings) > portableMaxRecords {
		return snapshot, errors.New("attachment binding limit exceeded")
	}
	var dbFile string
	if err = tx.QueryRowContext(ctx, `PRAGMA database_list`).Scan(new(any), new(any), &dbFile); err != nil {
		return snapshot, err
	}
	for i, src := range sources {
		x := src.x
		originalConv := x.ConversationID
		if !convs[x.ConversationID] {
			x.ConversationID = firstConv[x.ID]
		}
		x.Origin = origins["attachment:"+x.ID]
		if x.Origin.RecordID == "" {
			x.Origin.RecordID = x.ID
		}
		if x.Origin.InstanceID == "" {
			x.Origin.InstanceID = b.SourceInstance
		}
		if x.Origin.ConversationID == "" {
			x.Origin.ConversationID = originalConv
		}
		if !portableID(x.ID) || !portableFileName(x.Name) || !portableTime(x.CreatedAt) {
			return snapshot, errors.New("invalid source attachment metadata")
		}
		sources[i].x = x
	}
	missingArgs := portableConversationArgs(b.Conversations)
	for _, m := range b.Messages {
		missingArgs = append(missingArgs, m.ID)
	}
	rows, err = tx.QueryContext(ctx, `SELECT metadata_json FROM portability_missing_assets WHERE conversation_id IN (`+portablePlaceholders(len(convs))+`) OR EXISTS(SELECT 1 FROM json_each(metadata_json,'$.message_ids') WHERE value IN (`+portablePlaceholders(len(messages))+`)) ORDER BY id LIMIT ?`, append(missingArgs, portableMaxRecords+1)...)

	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var missing portableMissingAttachment
		if err = portableJSON([]byte(raw), &missing); err != nil {
			break
		}
		filtered := []string{}
		for _, id := range missing.MessageIDs {
			if messages[id] != "" {
				filtered = append(filtered, id)
			}
		}
		missing.MessageIDs = filtered
		if !convs[missing.ConversationID] {
			if len(filtered) == 0 {
				continue
			}
			missing.ConversationID = messages[filtered[0]]
		}
		snapshot.missing = append(snapshot.missing, missing)
		if len(snapshot.missing) > portableMaxRecords {
			err = errors.New("omitted attachment record limit exceeded")
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return snapshot, err
	}
	snapshot.sources, snapshot.bindings, snapshot.dbFile = sources, bindings, dbFile
	return snapshot, nil
}

func (s *Store) exportPortableAttachmentBytes(ctx context.Context, b *portableBundle, snapshot portableAttachmentSnapshot) error {
	bindings, dbFile := snapshot.bindings, snapshot.dbFile
	fileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	total := int64(0)
	kept := map[string]bool{}
	b.MissingAttachments = append(b.MissingAttachments, snapshot.missing...)
	for _, src := range snapshot.sources {
		x := src.x
		reason := ""
		if x.Size < 0 || x.Size > portableMaxAttachmentBytes {
			reason = "too_large"
		} else if len(b.Attachments) >= portableMaxAttachments || total+x.Size > portableMaxAttachmentTotal {
			reason = "bundle_limit"
		}
		var data []byte
		if reason == "" {
			data, reason = s.readPortableAttachment(fileCtx, dbFile, x.ID, src.disk, x.Size)
		}
		if reason != "" {
			b.MissingAttachments = append(b.MissingAttachments, portableMissingAttachment{ID: x.ID, ConversationID: x.ConversationID, Name: x.Name, Reason: reason, Origin: x.Origin, MessageIDs: portableMissingBindings(x.ID, bindings)})
			continue
		}
		sum := sha256.Sum256(data)
		x.SHA256, x.Data, x.MIME = hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(data), portableFileMIME(data)
		b.Attachments = append(b.Attachments, x)
		kept[x.ID] = true
		total += x.Size
	}
	for _, x := range bindings {
		if kept[x.AttachmentID] {
			b.AttachmentBindings = append(b.AttachmentBindings, x)
		}
	}
	b.AttachmentCount = len(b.MissingAttachments)
	return nil
}
func (s *Store) readPortableAttachment(ctx context.Context, dbFile, id, disk string, size int64) ([]byte, string) {
	var data []byte
	if strings.HasPrefix(disk, guestAttachmentPrefix) {
		alias, err := guestAttachmentID(disk)
		if err != nil || alias != id {
			return nil, "unsafe_file"
		}
		if s.guestBlobs == nil {
			return nil, "unavailable"
		}
		data, err = s.guestBlobs.GetBlob(ctx, id)
		if err != nil {
			return nil, "missing"
		}
	} else {
		// Account exports never read a host fallback. Legacy local files must have
		// the exact managed UUID name and be regular, single-link, non-symlink files.
		if s.requireGuestAttachments || disk != id+".upload" {
			return nil, "unsafe_file"
		}
		folder := filepath.Join(filepath.Dir(dbFile), "attachments")
		info, err := os.Lstat(folder)
		if err != nil {
			return nil, "missing"
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, "unsafe_file"
		}
		root, err := os.OpenRoot(folder)
		if err != nil {
			return nil, "unavailable"
		}
		defer root.Close()
		f, err := openPortableLocalFile(root, disk)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, "missing"
			}
			return nil, "unsafe_file"
		}
		defer f.Close()
		info, err = f.Stat()
		if err != nil || !info.Mode().IsRegular() || !portableSingleLink(info) {
			return nil, "unsafe_file"
		}
		if info.Size() != size {
			return nil, "changed_metadata"
		}
		data, err = io.ReadAll(io.LimitReader(f, portableMaxAttachmentBytes+1))
		if err != nil {
			return nil, "unavailable"
		}
	}
	if int64(len(data)) != size {
		return nil, "changed_metadata"
	}
	return data, ""
}

// Errors intentionally disclose no host paths or guest transport details.
var errPortableStorage = fmt.Errorf("account file staging failed; no import was published")

func portableMissingBindings(id string, bindings []portableAttachmentBinding) []string {
	out := []string{}
	for _, x := range bindings {
		if x.AttachmentID == id {
			out = append(out, x.MessageID)
		}
	}
	return out
}
func portableConversationArgs(convs []portableConversation) []any {
	args := []any{}
	for _, x := range convs {
		args = append(args, x.ID)
	}
	return args
}
