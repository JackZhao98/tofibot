package app

// Team tools let a running bot assemble a bounded, durable discussion. They
// deliberately use the existing bots, conversations, runs and queue; a team
// is metadata around ordinary conversation execution rather than a new
// session type.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	maxTeamBotsPerRun = 4
	maxTeamGroups     = 4
	maxTeamMembers    = 8
	maxTeamDispatches = 8
	maxTeamNameRunes  = 200
	maxTeamTaskRunes  = 32000
)

// SQLite deferred transactions can otherwise race between the idempotency
// lookup and mutation. Team operations are infrequent and bounded, so one
// process-local critical section gives retries a stable exactly-once result.
var teamOperationMu sync.Mutex

// migrateTeams is intentionally separate so app startup can include it in
// its migration chain without changing the historical schema statement.
func migrateTeams(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS team_operations(
run_id TEXT NOT NULL, operation_key TEXT NOT NULL, result TEXT NOT NULL,
created_at TEXT NOT NULL, PRIMARY KEY(run_id,operation_key));
CREATE INDEX IF NOT EXISTS team_operations_run ON team_operations(run_id);`)
	return err
}

func teamKey(name string, value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return name + ":" + string(b), nil
}

// withTeamOperation makes the run check, idempotency lookup, mutation and
// result checkpoint one atomic SQLite transaction.
func (s *Store) withTeamOperation(runID, key string, fn func(*sql.Tx) (string, error)) (string, error) {
	teamOperationMu.Lock()
	defer teamOperationMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		return "", err
	}
	if status != "running" {
		return "", errors.New("run is no longer active")
	}
	var old string
	if err = tx.QueryRow(`SELECT result FROM team_operations WHERE run_id=? AND operation_key=?`, runID, key).Scan(&old); err == nil {
		return old, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	result, err := fn(tx)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO team_operations(run_id,operation_key,result,created_at) VALUES(?,?,?,?)`, runID, key, result, now()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func teamFamilyOperationCount(tx *sql.Tx, runID, operation string) (int, error) {
	const query = `WITH RECURSIVE
lineage(id,parent_run_id) AS (
 SELECT id,parent_run_id FROM runs WHERE id=?
 UNION ALL
 SELECT r.id,r.parent_run_id FROM runs r JOIN lineage l ON l.parent_run_id=r.id
),
root(id) AS (SELECT id FROM lineage WHERE parent_run_id IS NULL OR parent_run_id=''),
family(id) AS (
 SELECT id FROM root
 UNION ALL
 SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
)
SELECT COUNT(*) FROM team_operations
WHERE run_id IN (SELECT id FROM family)
AND substr(operation_key,1,?)=?`
	prefix := operation + ":"
	var count int
	err := tx.QueryRow(query, runID, len(prefix), prefix).Scan(&count)
	return count, err
}

func marshalTeamEvent(tx *sql.Tx, conversationID, typ string, value any, created string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conversationID, typ, string(b), created); err != nil {
		return err
	}
	if typ == "bot" {
		return insertWorkspaceEventTx(tx, workspaceScopeBots, created)
	}
	if typ == "conversation" {
		return insertWorkspaceEventTx(tx, workspaceScopeGroups, created)
	}
	return nil
}

func normalizeTeamText(value, field string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len([]rune(value)) > limit {
		return "", fmt.Errorf("%s is too long", field)
	}
	return value, nil
}

func (s *Server) teamTools(c Conversation, r Run) []Tool {
	tool := func(name, desc string, props map[string]any, req []string, fn func(context.Context, json.RawMessage) (string, error)) Tool {
		return Tool{Name: name, Description: desc, Parameters: objectSchema(props, req), Execute: fn}
	}
	return []Tool{
		tool("create_bot", "Create or reuse a specialist bot for this run (bounded).", map[string]any{"name": map[string]any{"type": "string"}, "instructions": map[string]any{"type": "string"}, "model": map[string]any{"type": "string", "description": "Optional exact configured model ID. Omit to use the workspace default; do not invent model identifiers."}}, []string{"name", "instructions"}, func(ctx context.Context, args json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var x struct {
				Name         string `json:"name"`
				Instructions string `json:"instructions"`
				Model        string `json:"model"`
			}
			if json.Unmarshal(args, &x) != nil {
				return "", errors.New("name and instructions required")
			}
			var err error
			if x.Name, err = normalizeTeamText(x.Name, "name", maxTeamNameRunes); err != nil {
				return "", err
			}
			if x.Instructions, err = normalizeTeamText(x.Instructions, "instructions", maxSystemRunes); err != nil {
				return "", err
			}
			x.Model = strings.TrimSpace(x.Model)
			switch strings.ToLower(x.Model) {
			case "default", "auto", "inherit":
				x.Model = ""
			}
			if x.Model != "" && x.Model != s.defaultModel {
				var configured int
				if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM bots WHERE model=?`, x.Model).Scan(&configured); err != nil {
					return "", err
				}
				if configured == 0 {
					return "", errors.New("model is not configured; omit model to inherit the workspace default")
				}
			}
			key, err := teamKey("create_bot", x)
			if err != nil {
				return "", err
			}
			result, e := s.store.withTeamOperation(r.ID, key, func(tx *sql.Tx) (string, error) {
				n, e := teamFamilyOperationCount(tx, r.ID, "create_bot")
				if e != nil {
					return "", e
				}
				if n >= maxTeamBotsPerRun {
					return "", errors.New("team bot creation limit reached")
				}
				var b Bot
				var archived int
				e = tx.QueryRow(`SELECT id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived FROM bots WHERE name=? AND instructions=? AND model=? AND archived=0`, x.Name, x.Instructions, x.Model).Scan(&b.ID, &b.Name, &b.Instructions, &b.Model, &b.ReasoningEffort, &b.DMConversationID, &b.CreatedAt, &archived)
				if e == sql.ErrNoRows {
					b = Bot{ID: uuid.NewString(), Name: x.Name, Instructions: x.Instructions, Model: x.Model, DMConversationID: uuid.NewString(), CreatedAt: now()}
					if _, e = tx.Exec(`INSERT INTO bots(id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at) VALUES(?,?,?,?,?,?,?)`, b.ID, b.Name, b.Instructions, b.Model, b.ReasoningEffort, b.DMConversationID, b.CreatedAt); e != nil {
						return "", e
					}
					if _, e = tx.Exec(`INSERT INTO conversations(id,kind,name,bot_id,updated_at) VALUES(?,?,?,?,?)`, b.DMConversationID, "dm", b.Name, b.ID, b.CreatedAt); e != nil {
						return "", e
					}
					if _, e = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, b.DMConversationID, b.ID); e != nil {
						return "", e
					}
					if e = marshalTeamEvent(tx, b.DMConversationID, "bot", b, b.CreatedAt); e != nil {
						return "", e
					}
				} else if e != nil {
					return "", e
				}
				out, e := json.Marshal(b)
				if e != nil {
					return "", e
				}
				return string(out), nil
			})
			return result, e
		}),
		tool("create_group", "Create a durable discussion group including the initiating bot.", map[string]any{"name": map[string]any{"type": "string"}, "bot_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, []string{"name", "bot_ids"}, func(ctx context.Context, args json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var x struct {
				Name   string   `json:"name"`
				BotIDs []string `json:"bot_ids"`
			}
			if json.Unmarshal(args, &x) != nil {
				return "", errors.New("name and bot_ids required")
			}
			var err error
			if x.Name, err = normalizeTeamText(x.Name, "name", maxTeamNameRunes); err != nil {
				return "", err
			}
			ids := append([]string{r.BotID}, x.BotIDs...)
			seen := map[string]bool{}
			uniq := make([]string, 0, len(ids))
			for _, id := range ids {
				id = strings.TrimSpace(id)
				if id != "" && !seen[id] {
					seen[id] = true
					uniq = append(uniq, id)
				}
			}
			if len(uniq) < 2 || len(uniq) > maxTeamMembers {
				return "", errors.New("group must have 2-8 distinct members")
			}
			sort.Strings(uniq)
			x.BotIDs = uniq
			key, err := teamKey("create_group", x)
			if err != nil {
				return "", err
			}
			return s.store.withTeamOperation(r.ID, key, func(tx *sql.Tx) (string, error) {
				count, e := teamFamilyOperationCount(tx, r.ID, "create_group")
				if e != nil {
					return "", e
				}
				if count >= maxTeamGroups {
					return "", errors.New("team group creation limit reached")
				}
				for _, member := range uniq {
					var found string
					var archived int
					if e = tx.QueryRow(`SELECT id,archived FROM bots WHERE id=?`, member).Scan(&found, &archived); e == sql.ErrNoRows {
						return "", fmt.Errorf("bot %q not found", member)
					} else if e != nil {
						return "", e
					}
					if archived != 0 {
						return "", fmt.Errorf("bot %q is archived and unavailable for new groups", member)
					}
				}
				id := uuid.NewString()
				t := now()
				if _, e = tx.Exec(`INSERT INTO conversations(id,kind,name,updated_at) VALUES(?,?,?,?)`, id, "group", x.Name, t); e != nil {
					return "", e
				}
				for _, member := range uniq {
					if _, e = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, id, member); e != nil {
						return "", e
					}
				}
				g := Conversation{ID: id, Kind: "group", Name: x.Name, UpdatedAt: t, BotIDs: uniq}
				if e = marshalTeamEvent(tx, id, "conversation", g, t); e != nil {
					return "", e
				}
				out, e := json.Marshal(g)
				if e != nil {
					return "", e
				}
				return string(out), nil
			})
		}),
		tool("invite_bot", "Add an existing bot to a discussion group.", map[string]any{"group_id": map[string]any{"type": "string"}, "bot_id": map[string]any{"type": "string"}}, []string{"group_id", "bot_id"}, func(ctx context.Context, args json.RawMessage) (string, error) { return s.teamInvite(ctx, r, args) }),
		tool("list_groups", "List discussion groups available to the initiating bot.", map[string]any{}, nil, func(ctx context.Context, args json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			gs, e := s.store.ListConversations()
			if e != nil {
				return "", e
			}
			out := make([]Conversation, 0)
			for _, g := range gs {
				if g.Kind == "group" {
					ok, e := s.store.IsMember(g.ID, r.BotID)
					if e != nil {
						return "", e
					}
					if ok {
						out = append(out, g)
					}
				}
			}
			b, e := json.Marshal(out)
			if e != nil {
				return "", e
			}
			return string(b), nil
		}),
		tool("send_group_message", "Start work in a different group by assigning one selected Bot. It rejects the current conversation: reply normally to publish here, and use handoff to delegate to another member of the current group.", map[string]any{"group_id": map[string]any{"type": "string"}, "bot_id": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, []string{"group_id", "bot_id", "message"}, func(ctx context.Context, args json.RawMessage) (string, error) { return s.teamSend(ctx, c, r, args) }),
	}
}

func (s *Server) teamInvite(ctx context.Context, r Run, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var x struct {
		GroupID string `json:"group_id"`
		BotID   string `json:"bot_id"`
	}
	if json.Unmarshal(args, &x) != nil {
		return "", errors.New("group_id and bot_id required")
	}
	x.GroupID, x.BotID = strings.TrimSpace(x.GroupID), strings.TrimSpace(x.BotID)
	if x.GroupID == "" || x.BotID == "" {
		return "", errors.New("group_id and bot_id required")
	}
	key, err := teamKey("invite_bot", x)
	if err != nil {
		return "", err
	}
	return s.store.withTeamOperation(r.ID, key, func(tx *sql.Tx) (string, error) {
		var kind string
		if e := tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, x.GroupID).Scan(&kind); e == sql.ErrNoRows || kind != "group" {
			return "", errors.New("group not found")
		} else if e != nil {
			return "", e
		}
		if e := requireConversationActiveTx(tx, x.GroupID); e != nil {
			return "", e
		}
		var n int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM members WHERE conversation_id=? AND bot_id=?`, x.GroupID, r.BotID).Scan(&n); e != nil {
			return "", e
		} else if n == 0 {
			return "", errors.New("initiator is not a member")
		}
		var archived int
		if e := tx.QueryRow(`SELECT archived FROM bots WHERE id=?`, x.BotID).Scan(&archived); e != nil {
			return "", e
		} else if archived != 0 {
			return "", ErrArchiveBlocked
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM members WHERE conversation_id=? AND bot_id=?`, x.GroupID, x.BotID).Scan(&n); e != nil {
			return "", e
		} else if n > 0 {
			return teamGroupResult(tx, x.GroupID)
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM members WHERE conversation_id=?`, x.GroupID).Scan(&n); e != nil {
			return "", e
		} else if n >= maxTeamMembers {
			return "", errors.New("group member limit reached")
		}
		if _, e := tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, x.GroupID, x.BotID); e != nil {
			return "", e
		}
		t := now()
		if _, e := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, x.GroupID); e != nil {
			return "", e
		}
		result, e := teamGroupResult(tx, x.GroupID)
		if e != nil {
			return "", e
		}
		var g Conversation
		if e = json.Unmarshal([]byte(result), &g); e != nil {
			return "", e
		}
		if e = marshalTeamEvent(tx, x.GroupID, "conversation", g, t); e != nil {
			return "", e
		}
		return result, nil
	})
}

func teamGroupResult(tx *sql.Tx, groupID string) (string, error) {
	g := Conversation{ID: groupID, Kind: "group"}
	var archived int
	if err := tx.QueryRow(`SELECT name,updated_at,archived FROM conversations WHERE id=? AND kind='group'`, groupID).Scan(&g.Name, &g.UpdatedAt, &archived); err != nil {
		return "", err
	}
	g.Archived = archived != 0
	rows, err := tx.Query(`SELECT bot_id FROM members WHERE conversation_id=? ORDER BY bot_id`, groupID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", err
		}
		g.BotIDs = append(g.BotIDs, id)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	out, err := json.Marshal(g)
	return string(out), err
}

func (s *Server) teamSend(ctx context.Context, c Conversation, r Run, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var x struct {
		GroupID string `json:"group_id"`
		BotID   string `json:"bot_id,omitempty"`
		Message string `json:"message"`
	}
	if json.Unmarshal(args, &x) != nil {
		return "", errors.New("group_id and message required")
	}
	x.GroupID, x.BotID = strings.TrimSpace(x.GroupID), strings.TrimSpace(x.BotID)
	var err error
	if x.Message, err = normalizeTeamText(x.Message, "message", maxTeamTaskRunes); err != nil || x.GroupID == "" {
		if err != nil {
			return "", err
		}
		return "", errors.New("group_id and message required")
	}
	if x.GroupID == c.ID {
		return "", errors.New("send_group_message cannot target the current conversation; reply normally to publish here, or use handoff to delegate to another current group member")
	}
	key, err := teamKey("send_group_message", x)
	if err != nil {
		return "", err
	}
	if err := s.yieldComputerOwner(r); err != nil {
		return "", fmt.Errorf("cannot release shared desktop for team handoff: %w", err)
	}
	var group Conversation
	result, e := s.store.withTeamOperation(r.ID, key, func(tx *sql.Tx) (string, error) {
		group = Conversation{ID: x.GroupID, Kind: "group"}
		var archived int
		if e := tx.QueryRow(`SELECT name,updated_at,archived FROM conversations WHERE id=? AND kind='group'`, x.GroupID).Scan(&group.Name, &group.UpdatedAt, &archived); e == sql.ErrNoRows {
			return "", errors.New("group not found")
		} else if e != nil {
			return "", e
		}
		group.Archived = archived != 0
		if group.Archived {
			return "", ErrArchiveBlocked
		}
		activeMembers, e := activeMemberCountTx(tx, x.GroupID)
		if e != nil {
			return "", e
		}
		if activeMembers == 0 {
			return "", ErrNoActiveMembers
		}
		rows, e := tx.Query(`SELECT bot_id FROM members WHERE conversation_id=? ORDER BY bot_id`, x.GroupID)
		if e != nil {
			return "", e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return "", e
			}
			group.BotIDs = append(group.BotIDs, id)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return "", e
		}
		rows.Close()
		initiatorMember := false
		for _, id := range group.BotIDs {
			initiatorMember = initiatorMember || id == r.BotID
		}
		if !initiatorMember {
			return "", errors.New("initiator is not a member")
		}
		dispatches, e := teamFamilyOperationCount(tx, r.ID, "send_group_message")
		if e != nil {
			return "", e
		}
		if dispatches >= maxTeamDispatches {
			return "", errors.New("team discussion limit reached")
		}
		member := x.BotID
		if member == "" && len(group.BotIDs) > 2 {
			return "", errors.New("multiple group members: specify the target bot_id; to post your own answer, reply normally without this tool")
		}
		if member == "" {
			for _, id := range group.BotIDs {
				if id != r.BotID {
					member = id
					break
				}
			}
		}
		if member == "" || member == r.BotID {
			return "", errors.New("target must be another group member")
		}
		targetMember := false
		for _, id := range group.BotIDs {
			if id == member {
				targetMember = true
				break
			}
		}
		if !targetMember {
			return "", errors.New("target is not a group member")
		}
		var model string
		if e := tx.QueryRow(`SELECT model,archived FROM bots WHERE id=?`, member).Scan(&model, &archived); e != nil {
			return "", e
		}
		if archived != 0 {
			return "", ErrArchiveBlocked
		}
		t := now()
		res, e := tx.Exec(`UPDATE runs SET handoff_count=handoff_count+1,updated_at=? WHERE id=? AND status='running' AND handoff_count=0`, t, r.ID)
		if e != nil {
			return "", e
		}
		if changed, e := res.RowsAffected(); e != nil {
			return "", e
		} else if changed != 1 {
			return "", errors.New("run handoff already used")
		}
		var seq int64
		if e := tx.QueryRow(nextMessageSeqSQL, group.ID, group.ID, streamDraftActive).Scan(&seq); e != nil {
			return "", e
		}
		childID := uuid.NewString()
		notice := &HandoffNotice{Type: "handoff", FromBotID: r.BotID, ToBotID: member, TargetConversationID: group.ID, TargetRunID: childID}
		noticeData, e := json.Marshal(notice)
		if e != nil {
			return "", e
		}
		m := Message{ID: uuid.NewString(), ConversationID: group.ID, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: r.BotID, RunID: childID, Content: x.Message, Notice: notice, CreatedAt: t}
		if _, e := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at,client_message_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, m.ID, group.ID, seq, m.Role, m.Kind, r.BotID, childID, m.Content, string(noticeData), t, "team:"+r.ID+":"+key); e != nil {
			return "", e
		}
		var q int64
		if e := tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, group.ID).Scan(&q); e != nil {
			return "", e
		}
		origin := r.OriginConversationID
		if origin == "" {
			origin = c.ID
		}
		child := Run{ID: childID, ConversationID: group.ID, BotID: member, Status: "queued", ParentRunID: r.ID, Model: model, Kind: runKindTeam, OriginConversationID: origin, TriggerMessageID: m.ID, QueueSeq: q, CreatedAt: t, UpdatedAt: t}
		if _, e := tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, child.ID, group.ID, member, "queued", r.ID, 0, model, runKindTeam, origin, m.ID, q, t, t); e != nil {
			return "", e
		}
		for _, event := range []struct {
			typ   string
			value any
		}{{"message", m}, {"run", child}} {
			if e := marshalTeamEvent(tx, group.ID, event.typ, event.value, t); e != nil {
				return "", e
			}
		}
		if _, e := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, group.ID); e != nil {
			return "", e
		}
		out, e := json.Marshal(map[string]any{"group": group, "message": m, "runs": []Run{child}, "origin_conversation_id": origin})
		if e != nil {
			return "", e
		}
		return string(out), nil
	})
	if e != nil {
		return "", e
	}
	var payload struct {
		Group Conversation `json:"group"`
		Runs  []Run        `json:"runs"`
	}
	if e = json.Unmarshal([]byte(result), &payload); e != nil {
		return "", e
	}
	for _, child := range payload.Runs {
		s.enqueue(payload.Group, child)
	}
	return result, nil
}
