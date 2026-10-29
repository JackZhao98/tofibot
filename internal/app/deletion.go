package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrDeleteBusy = errors.New("queued or running work must finish or be cancelled before deletion")
var ErrDeleteType = errors.New("only groups can be deleted as conversations; delete a Bot to remove its direct message")

type DeleteResult struct {
	Deleted        bool   `json:"deleted"`
	BotID          string `json:"bot_id,omitempty"`
	ConversationID string `json:"conversation_id"`
}

func migrateDeletion(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS deleted_bot_identities(bot_id TEXT PRIMARY KEY,name TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS deleted_attachment_files(disk_name TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS deleted_bot_creations(client_creation_id TEXT PRIMARY KEY);`)
	return err
}

func (s *Store) DeleteBot(id string) (DeleteResult, error) {
	return s.deleteWorkspaceTarget(id, true, nil)
}

// DeleteBotReassigning deletes a Bot and, if it is the Primary Assistant,
// designates successor in the same transaction.
func (s *Store) DeleteBotReassigning(id, successor string) (DeleteResult, error) {
	return s.deleteWorkspaceTargetWith(id, true, nil, successor, true)
}
func (s *Store) DeleteGroup(id string) (DeleteResult, error) {
	return s.deleteWorkspaceTarget(id, false, nil)
}

// Metadata and invalidations commit together. Attachment unlinking uses a
// durable outbox after commit; a restart retries any unfinished file cleanup.
// VM files, browser profiles, and shared computer configuration are untouched.
func (s *Store) deleteWorkspaceTarget(id string, bot bool, actor *Run) (DeleteResult, error) {
	// A Bot-run delete is owner-facing too: it cannot name a successor, so it
	// can never remove the Primary Assistant while other Bots exist.
	return s.deleteWorkspaceTargetWith(id, bot, actor, "", actor != nil)
}

func (s *Store) deleteWorkspaceTargetWith(id string, bot bool, actor *Run, successor string, strictPrimary bool) (DeleteResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return DeleteResult{}, err
	}
	defer tx.Rollback()
	if actor != nil {
		var valid int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND bot_id=? AND conversation_id=? AND status='running'`, actor.ID, actor.BotID, actor.ConversationID).Scan(&valid); err != nil {
			return DeleteResult{}, err
		}
		if valid != 1 {
			return DeleteResult{}, errors.New("workspace management requires an active run")
		}
	}
	result := DeleteResult{Deleted: true, ConversationID: id}
	name := ""
	if bot {
		result.BotID = id
		if err = tx.QueryRow(`SELECT name,dm_conversation_id FROM bots WHERE id=?`, id).Scan(&name, &result.ConversationID); err != nil {
			return DeleteResult{}, err
		}
	} else {
		var kind string
		if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, id).Scan(&kind); err != nil {
			return DeleteResult{}, err
		}
		if kind != "group" {
			return DeleteResult{}, ErrDeleteType
		}
	}
	conv := result.ConversationID
	if bot {
		// The tasks the Bot gave and held, its waits, and the runs that worked on
		// those tasks end with it. This shares the transaction with the checks
		// below, so a refused deletion leaves them as they were.
		ended, cerr := cascadeEndBotTx(tx, id, "")
		if cerr != nil {
			return DeleteResult{}, cerr
		}
		if len(ended) > 0 {
			if _, cerr = cancelRunsBelowCollect(tx, `SELECT id FROM runs WHERE id IN (`+sqlMarks(len(ended))+`)`, anyArgs(ended)...); cerr != nil {
				return DeleteResult{}, cerr
			}
		}
	}
	// Include forwarded descendants and origin-bound work. Removing a member
	// also waits for its groups' work so an executing roster cannot go stale.
	seeds := `SELECT id FROM runs WHERE conversation_id=? OR origin_conversation_id=?`
	args := []any{conv, conv}
	if bot {
		seeds += ` OR bot_id=? OR conversation_id IN (SELECT conversation_id FROM members WHERE bot_id=?)`
		args = append(args, id, id)
	}
	seeds += ` OR id IN (SELECT run_id FROM team_operations WHERE json_valid(result) AND (json_extract(result,'$.id')=? OR json_extract(result,'$.group.id')=?))`
	args = append(args, id, id)
	family := `WITH RECURSIVE affected(id) AS (` + seeds + ` UNION SELECT r.id FROM runs r JOIN affected a ON r.parent_run_id=a.id) `
	var busy int
	if err = tx.QueryRow(family+`SELECT COUNT(*) FROM runs WHERE id IN (SELECT id FROM affected) AND status IN ('queued','running','waiting')`, args...).Scan(&busy); err != nil {
		return DeleteResult{}, err
	}
	if busy != 0 {
		return DeleteResult{}, ErrDeleteBusy
	}
	jobQuery := family + `SELECT COUNT(*) FROM computer_jobs WHERE status IN ('pending','running') AND (run_id IN (SELECT id FROM affected)`
	jobArgs := append([]any(nil), args...)
	if bot {
		jobQuery += ` OR bot_id=?`
		jobArgs = append(jobArgs, id)
	}
	jobQuery += `)`
	if err = tx.QueryRow(jobQuery, jobArgs...).Scan(&busy); err != nil {
		return DeleteResult{}, err
	}
	if busy != 0 {
		return DeleteResult{}, ErrDeleteBusy
	}
	if bot {
		// The Primary Assistant is never left behind silently: the owner names
		// its successor (a Bot-run delete cannot).
		if err = resolvePrimaryOnRemovalTx(tx, id, successor, strictPrimary); err != nil {
			return DeleteResult{}, err
		}
		if err = deleteFollowupsForBotTx(tx, id); err != nil {
			return DeleteResult{}, err
		}
		if err = revokeStandingAllowancesForBotTx(tx, id, "bot_deleted"); err != nil {
			return DeleteResult{}, err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO deleted_bot_creations(client_creation_id) SELECT client_creation_id FROM bot_onboarding WHERE bot_id=?`, id); err != nil {
			return DeleteResult{}, err
		}
		if _, err = tx.Exec(`INSERT INTO deleted_bot_identities(bot_id,name) VALUES(?,?)`, id, name); err != nil {
			return DeleteResult{}, err
		}
		// Replay of surviving shared messages must carry the same deleted identity.
		if _, err = tx.Exec(`UPDATE events SET data=json_set(data,'$.sender_bot_name',?) WHERE type='message' AND json_valid(data) AND json_extract(data,'$.sender_bot_id')=?`, name, id); err != nil {
			return DeleteResult{}, err
		}
		if _, err = tx.Exec(`UPDATE work_items SET parent_goal_id=NULL WHERE parent_goal_id IN (SELECT id FROM work_items WHERE bot_id=?)`, id); err != nil {
			return DeleteResult{}, err
		}
		for _, query := range []string{`DELETE FROM work_items WHERE bot_id=?`, `DELETE FROM waits WHERE bot_id=?`, `DELETE FROM command_waits WHERE bot_id=?`, `DELETE FROM bot_inbox WHERE bot_id=?`, `DELETE FROM schedules WHERE bot_id=?`, `DELETE FROM bot_connection_access WHERE bot_id=?`, `DELETE FROM memories WHERE bot_id=?`, `DELETE FROM computer_jobs WHERE bot_id=?`} {
			if _, err = tx.Exec(query, id); err != nil {
				return DeleteResult{}, err
			}
		}
	}
	// Only files owned by the deleted conversation are candidates. Shared
	// bindings to another conversation's attachment never transfer ownership.
	if _, err = tx.Exec(`INSERT OR IGNORE INTO deleted_attachment_files(disk_name) SELECT disk_name FROM attachments WHERE conversation_id=?`, conv); err != nil {
		return DeleteResult{}, err
	}
	// These tables intentionally lack a complete FK to their conversation/run.
	for _, query := range []string{
		`DELETE FROM computer_jobs WHERE run_id IN (SELECT id FROM runs WHERE conversation_id=?)`,
		`DELETE FROM team_operations WHERE run_id IN (SELECT id FROM runs WHERE conversation_id=?)`,
		`DELETE FROM tool_activities WHERE conversation_id=?`,
		`DELETE FROM summaries WHERE conversation_id=?`,
		`UPDATE work_items SET parent_goal_id=NULL WHERE parent_goal_id IN (SELECT id FROM work_items WHERE conversation_id=?)`,
		`DELETE FROM waits WHERE conversation_id=?`,
		`DELETE FROM bot_inbox WHERE conversation_id=?`,
		`DELETE FROM conversations WHERE id=?`,
	} {
		if _, err = tx.Exec(query, conv); err != nil {
			return DeleteResult{}, err
		}
	}
	if bot {
		if _, err = tx.Exec(`DELETE FROM bots WHERE id=?`, id); err != nil {
			return DeleteResult{}, err
		}
		if err = insertWorkspaceEventTx(tx, workspaceScopeBots, now()); err != nil {
			return DeleteResult{}, err
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeGroups, now()); err != nil {
		return DeleteResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return DeleteResult{}, err
	}
	if bot && s.portabilitySecrets != nil {
		// A deleted Bot's saved secrets go with it.
		s.portabilitySecrets.vault.purgeBotSecrets(id)
	}
	s.cleanupDeletedAttachments()
	return result, nil
}

func (s *Store) cleanupDeletedAttachments() {
	rows, err := s.db.Query(`SELECT disk_name FROM deleted_attachment_files`)
	if err != nil {
		return
	}
	var files []string
	for rows.Next() {
		var disk string
		if rows.Scan(&disk) == nil {
			files = append(files, disk)
		}
	}
	rows.Close()
	var local []string
	for _, disk := range files {
		if !strings.HasPrefix(disk, guestAttachmentPrefix) {
			local = append(local, disk)
			continue
		}
		id, e := guestAttachmentID(disk)
		if e != nil || s.legacyBlobs() == nil {
			continue
		}
		var owned int
		if e = s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE disk_name=?`, disk).Scan(&owned); e != nil || owned != 0 {
			continue
		}
		// A journaled guest object whose attachment still exists was copied to
		// the server store, not deleted. It goes only after the server file is
		// proven by its recorded sha256.
		if !s.verifiedServerCopy(id) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = s.legacyBlobs().DeleteBlob(ctx, id)
		cancel()
		if e == nil {
			s.db.Exec(`DELETE FROM deleted_attachment_files WHERE disk_name=?`, disk)
		}
	}
	files = local
	if len(files) == 0 {
		return
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer dir.Close()
	for _, disk := range files {
		// Never normalize hostile metadata into another attachment's basename.
		if disk == "" || disk == "." || disk == ".." || filepath.Base(disk) != disk || strings.ContainsAny(disk, "/\\") {
			continue
		}
		var owned int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE disk_name=?`, disk).Scan(&owned); err != nil || owned != 0 {
			continue
		}
		info, statErr := dir.Lstat(disk)
		if statErr == nil && info.IsDir() {
			continue
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			continue
		}
		if statErr == nil {
			if err = dir.Remove(disk); err != nil && !os.IsNotExist(err) {
				continue
			}
		}
		_, _ = s.db.Exec(`DELETE FROM deleted_attachment_files WHERE disk_name=?`, disk)
	}
}

func (s *Store) hydrateDeletedSenders(messages []Message) error {
	rows, err := s.db.Query(`SELECT bot_id,name FROM deleted_bot_identities`)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range messages {
		messages[i].SenderBotName = names[messages[i].SenderBotID]
	}
	return nil
}

func (s *Server) routeDeletion(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.Method != http.MethodDelete {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] == "" || (parts[0] != "bots" && parts[0] != "conversations") {
		return false
	}
	result, err := s.store.deleteWorkspaceTargetWith(parts[1], parts[0] == "bots", nil, r.URL.Query().Get("new_primary_id"), true)
	if err != nil {
		if writePrimaryError(w, err) {
			return true
		}
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeErr(w, 404, "not_found", "deletion target not found")
		case errors.Is(err, ErrDeleteBusy):
			writeErr(w, 409, "delete_busy", err.Error())
		case errors.Is(err, ErrDeleteType):
			writeErr(w, 400, "group_only", err.Error())
		default:
			writeErr(w, 500, "storage", "deletion could not be completed")
		}
		return true
	}
	// The deletion ended the tasks this Bot gave; stop the runs that worked on them.
	s.cancelInactiveRuns()
	writeJSON(w, 200, result)
	return true
}

func (s *Server) workspaceDeletionTools(c Conversation, r Run) []Tool {
	makeTool := func(name, key, description string, bot bool) Tool {
		return Tool{Name: name, Description: description, Parameters: objectSchema(map[string]any{key: map[string]any{"type": "string"}}, []string{key}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := requireWorkspaceToolRun(s, c, r); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var id string
			if bot {
				var input struct {
					BotID string `json:"bot_id"`
				}
				if err := decodeWorkspaceTool(raw, &input); err != nil {
					return "", err
				}
				id = input.BotID
			} else {
				var input struct {
					GroupID string `json:"group_id"`
				}
				if err := decodeWorkspaceTool(raw, &input); err != nil {
					return "", err
				}
				id = input.GroupID
			}
			if strings.TrimSpace(id) == "" {
				return "", errors.New("the target ID is required")
			}
			// A deletion cannot be undone: it is the settings change bot.delete or conversation.delete, applied only after the person
			// approves a card for exactly this target, and never in a run nobody attends.
			sr := settingRun{s: s, c: c, r: r}
			key, target := "conversation.delete", Target{ConversationID: strings.TrimSpace(id)}
			if bot {
				key, target = "bot.delete", Target{BotID: strings.TrimSpace(id)}
			}
			if _, err := sr.aliasSet(ctx, key, target, nil); err != nil {
				return "", err
			}
			result := DeleteResult{Deleted: true, ConversationID: target.ConversationID}
			if bot {
				result.BotID = target.BotID
			}
			data, err := json.Marshal(result)
			return string(data), err
		}}
	}
	return []Tool{
		makeTool("workspace_delete_bot", "bot_id", "Permanently delete a Bot only when the user explicitly requests its deletion. Use workspace_list to identify its exact ID. Removes its private conversation and records, memberships, owned goals/tasks, and schedules. Preserves its messages and sender identity in surviving groups and preserves shared computer files and browser profiles. Busy work blocks deletion; never claim deletion before this succeeds. The settings tool makes the same changes. A change to another Bot, a model, group members or a deletion is applied only after the person approves a card: when the call refuses with need_approval, ask with request_approval using the target it returns, then repeat the same call. Runs that no person attends change nothing.", true),
		makeTool("workspace_delete_group", "group_id", "Permanently delete a group only when the user explicitly requests its deletion. Use workspace_list to identify its exact ID. Removes this group's history, memories, files, goals/tasks, and schedules while retaining its member Bots and their private conversations. Busy work blocks deletion; never claim deletion before this succeeds. The settings tool makes the same changes. A change to another Bot, a model, group members or a deletion is applied only after the person approves a card: when the call refuses with need_approval, ask with request_approval using the target it returns, then repeat the same call. Runs that no person attends change nothing.", false),
	}
}
