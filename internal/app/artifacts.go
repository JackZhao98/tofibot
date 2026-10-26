package app

// Artifact panels: one read-only listing of the files Bots produced (and, on
// request, the files the person uploaded), across every conversation, one Bot,
// or one conversation. Nothing here writes; attachment bytes are still served
// by GET /api/attachments/{id}.
//
// What counts: an attachment is judged by its FIRST binding inside its own
// conversation (the message it was created for). publish_file and
// generate_image files reach a message only through bindRunAttachmentsTx, so a
// file superseded or replaced before the final reply is never bound and never
// listed. A file bound later to a forward, a bot_result, or a delegation brief
// keeps its first message as its origin, so copies do not become new
// artifacts. Origin role 'assistant' with a sender Bot is a deliverable; role
// 'user' is an upload. Conversations that are not user_visible (hidden Bot
// traces) are left out, the same rule the conversation list applies, because
// the person cannot open them. Archived conversations stay, marked archived.
//
// Versions: within one conversation, one sender and one file name, the newest
// file is the current version and older ones are earlier versions (the same
// rule as filePreviewModel.ts, widened from one message to the conversation).
//
// Account isolation: every account has its own workspace database, so this
// query can only see the calling account's rows; the account gate in
// accounts.go routes /api/ requests to that workspace before we run.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

const (
	artifactDefaultLimit = 40
	artifactMaxLimit     = 100
	artifactMaxVersions  = 200
)

type ArtifactItem struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	MIME                 string `json:"mime"`
	Size                 int64  `json:"size"`
	CreatedAt            string `json:"created_at"`
	ConversationID       string `json:"conversation_id"`
	ConversationName     string `json:"conversation_name"`
	ConversationKind     string `json:"conversation_kind"`
	ConversationBotID    string `json:"conversation_bot_id,omitempty"`
	ConversationArchived bool   `json:"conversation_archived"`
	BotID                string `json:"bot_id,omitempty"`
	MessageID            string `json:"message_id"`
	MessageSeq           int64  `json:"message_seq"`
	Uploaded             bool   `json:"uploaded"`
	VersionGroup         string `json:"version_group"`
	VersionCount         int    `json:"version_count"`
}

type artifactQuery struct {
	scope          string // all | bot | conversation
	id             string
	includeUploads bool
	kind           string // "" | image | document
	search         string
	cursorAt       string
	cursorID       string
	limit          int
	allVersions    bool // keep every version instead of the newest per group
}

func artifactVersionGroup(conversation, sender, name string) string {
	sum := sha256.Sum256([]byte(conversation + "\x1f" + sender + "\x1f" + name))
	return hex.EncodeToString(sum[:8])
}

func (s *Store) listArtifacts(q artifactQuery) ([]ArtifactItem, bool, error) {
	if q.limit <= 0 {
		q.limit = artifactDefaultLimit
	}
	var args []any
	origin := `SELECT a.id,a.name,a.mime,a.size,a.created_at,a.conversation_id,c.name AS cname,c.kind AS ckind,COALESCE(c.bot_id,'') AS cbot,c.archived AS carch,
		m.id AS mid,m.seq AS mseq,m.role AS role,COALESCE(m.sender_bot_id,'') AS sender,
		ROW_NUMBER() OVER (PARTITION BY a.id ORDER BY m.created_at,m.seq,am.rowid) AS ord
		FROM attachments a
		JOIN attachment_messages am ON am.attachment_id=a.id
		JOIN messages m ON m.id=am.message_id AND m.conversation_id=a.conversation_id
		JOIN conversations c ON c.id=a.conversation_id
		WHERE c.user_visible=1`
	if q.scope == "conversation" {
		origin += ` AND a.conversation_id=?`
		args = append(args, q.id)
	}
	where := []string{`ord=1`}
	roles := `(role='assistant' AND sender<>'')`
	if q.includeUploads {
		roles = `(` + roles + ` OR role='user')`
	}
	where = append(where, roles)
	if q.scope == "bot" {
		// A Bot's deliverables are the files it sent. Its uploads view adds the
		// person's files in the conversations the Bot takes part in.
		cond := `(sender=?`
		args = append(args, q.id)
		if q.includeUploads {
			cond += ` OR (role='user' AND (cbot=? OR EXISTS(SELECT 1 FROM members mb WHERE mb.conversation_id=o.conversation_id AND mb.bot_id=?)))`
			args = append(args, q.id, q.id)
		}
		cond += `)`
		where = append(where, cond)
	}
	switch q.kind {
	case "image":
		where = append(where, `mime LIKE 'image/%'`)
	case "document":
		where = append(where, `mime NOT LIKE 'image/%'`)
	}
	if strings.TrimSpace(q.search) != "" {
		where = append(where, `instr(lower(name),lower(?))>0`)
		args = append(args, strings.TrimSpace(q.search))
	}
	query := `WITH origin AS (` + origin + `),
	scoped AS (SELECT * FROM origin o WHERE ` + strings.Join(where, " AND ") + `),
	ranked AS (SELECT *,ROW_NUMBER() OVER w AS rn,COUNT(*) OVER (PARTITION BY conversation_id,sender,name) AS cnt FROM scoped
		WINDOW w AS (PARTITION BY conversation_id,sender,name ORDER BY created_at DESC,id DESC))
	SELECT id,name,mime,size,created_at,conversation_id,cname,ckind,cbot,carch,mid,mseq,role,sender,cnt FROM ranked WHERE 1=1`
	if !q.allVersions {
		query += ` AND rn=1`
	}
	if q.cursorAt != "" {
		query += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, q.cursorAt, q.cursorAt, q.cursorID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, q.limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []ArtifactItem{}
	for rows.Next() {
		var it ArtifactItem
		var archived int
		var role string
		if err = rows.Scan(&it.ID, &it.Name, &it.MIME, &it.Size, &it.CreatedAt, &it.ConversationID, &it.ConversationName, &it.ConversationKind, &it.ConversationBotID, &archived, &it.MessageID, &it.MessageSeq, &role, &it.BotID, &it.VersionCount); err != nil {
			return nil, false, err
		}
		it.ConversationArchived = archived != 0
		it.Uploaded = role == "user"
		it.VersionGroup = artifactVersionGroup(it.ConversationID, it.BotID, it.Name)
		out = append(out, it)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > q.limit
	if more {
		out = out[:q.limit]
	}
	return out, more, nil
}

func (s *Server) routeArtifacts(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "artifacts" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	v := r.URL.Query()
	q := artifactQuery{scope: v.Get("scope"), id: v.Get("id"), includeUploads: v.Get("include_uploads") == "1", kind: v.Get("type"), search: v.Get("q")}
	if q.scope == "" {
		q.scope = "all"
	}
	if n, err := strconv.Atoi(v.Get("limit")); err == nil && n > 0 {
		q.limit = n
	}
	if q.limit > artifactMaxLimit {
		q.limit = artifactMaxLimit
	}
	if q.kind != "" && q.kind != "image" && q.kind != "document" && q.kind != "all" {
		writeErr(w, 400, "invalid_request", "type must be image, document or all")
		return true
	}
	if q.kind == "all" {
		q.kind = ""
	}
	if len(q.search) > 200 {
		q.search = q.search[:200]
	}
	if c := v.Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		at, id, ok := strings.Cut(string(raw), "|")
		if err != nil || !ok || at == "" || id == "" {
			writeErr(w, 400, "invalid_request", "invalid cursor")
			return true
		}
		q.cursorAt, q.cursorID = at, id
	}
	switch q.scope {
	case "all":
	case "bot":
		var n int
		if q.id == "" || s.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE id=?`, q.id).Scan(&n) != nil || n == 0 {
			writeErr(w, 404, "not_found", "bot not found")
			return true
		}
	case "conversation":
		c, err := s.store.GetConversation(q.id)
		if err != nil || !c.UserVisible {
			writeErr(w, 404, "not_found", "conversation not found")
			return true
		}
	case "versions":
		// Every version of the file group that attachment id belongs to.
		var conv string
		if q.id == "" || s.store.db.QueryRow(`SELECT conversation_id FROM attachments WHERE id=?`, q.id).Scan(&conv) != nil {
			writeErr(w, 404, "not_found", "attachment not found")
			return true
		}
		all, _, err := s.store.listArtifacts(artifactQuery{scope: "conversation", id: conv, includeUploads: true, limit: 1 << 20, allVersions: true})
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		var group string
		for _, it := range all {
			if it.ID == q.id {
				group = it.VersionGroup
			}
		}
		if group == "" {
			writeErr(w, 404, "not_found", "attachment is not an artifact")
			return true
		}
		versions := []ArtifactItem{}
		for _, it := range all {
			if it.VersionGroup == group && len(versions) < artifactMaxVersions {
				versions = append(versions, it)
			}
		}
		writeJSON(w, 200, map[string]any{"items": versions, "next_cursor": ""})
		return true
	default:
		writeErr(w, 400, "invalid_request", "scope must be all, bot, conversation or versions")
		return true
	}
	items, more, err := s.store.listArtifacts(q)
	if err != nil {
		writeErr(w, 500, "storage", err.Error())
		return true
	}
	next := ""
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt + "|" + last.ID))
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
	return true
}
