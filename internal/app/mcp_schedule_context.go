package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const maxMCPAuthorizationAncestry = 32

// Authority-bearing source identity excludes mutable lifecycle and timestamps.
// Current ancestry status is checked separately and included in review evidence.
type mcpScheduleRunBinding struct {
	ID                   string `json:"id"`
	ConversationID       string `json:"conversation_id"`
	BotID                string `json:"bot_id"`
	ParentRunID          string `json:"parent_run_id"`
	Kind                 string `json:"kind"`
	OriginConversationID string `json:"origin_conversation_id"`
	TriggerMessageID     string `json:"trigger_message_id"`
}

func mcpScheduleBinding(r Run) *mcpScheduleRunBinding {
	return &mcpScheduleRunBinding{r.ID, r.ConversationID, r.BotID, r.ParentRunID, r.Kind, r.OriginConversationID, r.TriggerMessageID}
}

type mcpScheduleSourceReference struct {
	ScheduleID   string `json:"schedule_id"`
	Revision     int64  `json:"revision"`
	RequestID    string `json:"request_id"`
	SourceKind   string `json:"source_kind"`
	SourceRunID  string `json:"source_run_id,omitempty"`
	SourceDigest string `json:"source_digest"`
}

type mcpScheduleAncestor struct {
	Binding mcpScheduleRunBinding `json:"binding"`
	Status  string                `json:"status"`
	Trigger Message               `json:"untrusted_trigger"`
}

type mcpScheduleConversation struct {
	ConversationID string           `json:"conversation_id"`
	BotID          string           `json:"bot_id"`
	Context        mcpReviewContext `json:"untrusted_context"`
	ToolResults    []ToolActivity   `json:"untrusted_tool_results"`
}

type mcpScheduleLineage struct {
	AccountID      string                        `json:"account_id"`
	TargetRunID    string                        `json:"target_run_id"`
	SnapshotDigest string                        `json:"snapshot_digest"`
	Occurrence     scheduleAuthorizationSnapshot `json:"occurrence"`
	Ancestry       []mcpScheduleAncestor         `json:"ancestry"`
	Contexts       []mcpScheduleConversation     `json:"contexts"`
	Authorization  []mcpAuthorizationEvidence    `json:"authorization_sources"`
}

// Keep the cleared direct-chat reader unchanged. A scheduled parent chain is
// admitted only through this account-aware resolver, including inside claim tx.
func (s *Server) readMCPReviewContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, string, error) {
	if r.Kind != runKindSchedule && r.ParentRunID == "" {
		return readMCPReviewContext(db, c, r)
	}
	return s.readScheduledMCPReviewContext(db, c, r)
}

const mcpScheduleRunSQL = `SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`

func mcpScheduleMember(db reviewQuerier, conversation, bot string) error {
	var member bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversations c JOIN bots b ON b.id=? JOIN members m ON m.conversation_id=c.id AND m.bot_id=b.id WHERE c.id=? AND c.archived=0 AND b.archived=0 AND (c.kind='group' OR c.bot_id=b.id))`, bot, conversation).Scan(&member)
	if err != nil || !member {
		return errors.New("scheduled authorization membership unavailable")
	}
	return nil
}

func mcpScheduleMessage(db reviewQuerier, id string) (Message, error) {
	var m Message
	var kind, sender, run, notice sql.NullString
	err := db.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE id=?`, id).Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &kind, &sender, &run, &m.Content, &notice, &m.CreatedAt)
	m.Kind, m.SenderBotID, m.RunID = kind.String, sender.String, run.String
	if err == nil && notice.String != "" {
		err = json.Unmarshal([]byte(notice.String), &m.Notice)
	}
	if imported, e := scheduleImported(db, "message", id); err == nil && (e != nil || imported) {
		err = errors.New("scheduled context message import boundary")
	}
	return m, err
}

func mcpScheduleLiveRun(db reviewQuerier, id string) (Run, error) {
	r, err := scanRun(db.QueryRow(mcpScheduleRunSQL, id))
	if err != nil {
		return r, err
	}
	if r.ID == "" || r.TriggerMessageID == "" || (r.Status != "running" && r.Status != "queued" && r.Status != runWaiting && r.Status != "done") || strings.TrimSpace(r.Error) != "" {
		return r, errors.New("scheduled ancestry ended or has uncertain effects")
	}
	if imported, e := scheduleImported(db, "run", id); e != nil || imported {
		return r, errors.New("scheduled ancestry import boundary")
	}
	return r, mcpScheduleMember(db, r.ConversationID, r.BotID)
}

func (s *Server) readScheduledMCPReviewContext(db reviewQuerier, c Conversation, requested Run) (mcpReviewContext, string, error) {
	var x mcpReviewContext
	account := s.reviewAccountID()
	if account == "" || requested.ConversationID != c.ID {
		return x, "", errors.New("scheduled authorization account or target unavailable")
	}
	byID := make(map[string]Run)
	triggers := make(map[string]Message)
	var chain []Run
	id := requested.ID
	for id != "" && len(chain) < maxMCPAuthorizationAncestry {
		if _, seen := byID[id]; seen {
			return x, "", errors.New("scheduled authorization ancestry cycle")
		}
		r, err := mcpScheduleLiveRun(db, id)
		if err != nil {
			return x, "", err
		}
		if len(chain) == 0 && (*mcpScheduleBinding(r) != *mcpScheduleBinding(requested) || r.Status != "running") {
			return x, "", errors.New("scheduled authorization target binding changed")
		}
		m, err := mcpScheduleMessage(db, r.TriggerMessageID)
		if err != nil {
			return x, "", err
		}
		byID[id], triggers[id] = r, m
		chain = append(chain, r)
		id = r.ParentRunID
	}
	if id != "" || len(chain) == 0 {
		return x, "", errors.New("complete scheduled ancestry unavailable")
	}
	root := chain[len(chain)-1]
	if root.Kind != runKindSchedule || root.ParentRunID != "" {
		return x, "", errors.New("no unique actual schedule occurrence root")
	}
	for i, r := range chain {
		var scheduleID, scheduledFor string
		err := db.QueryRow(`SELECT schedule_id,scheduled_for_utc FROM schedule_occurrences WHERE run_id=?`, r.ID).Scan(&scheduleID, &scheduledFor)
		if i == len(chain)-1 {
			if err != nil {
				return x, "", errors.New("actual occurrence root unavailable")
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return x, "", errors.New("scheduled ancestry has ambiguous occurrence roots")
		}
		if i+1 < len(chain) && r.TriggerMessageID == chain[i+1].TriggerMessageID {
			return x, "", errors.New("retry inheritance lacks new authority and effect certainty")
		}
	}
	if err := validateMCPScheduleEdges(db, chain, byID, triggers); err != nil {
		return x, "", err
	}
	record, err := readScheduleOccurrenceAuthorization(db, root.ID)
	if err != nil || record.AccountID != account || record.TriggerMessageID != root.TriggerMessageID {
		return x, "", errors.New("complete occurrence authorization unavailable")
	}
	var actualSchedule, actualDue string
	if err := db.QueryRow(`SELECT schedule_id,scheduled_for_utc FROM schedule_occurrences WHERE run_id=?`, root.ID).Scan(&actualSchedule, &actualDue); err != nil || actualSchedule != record.ScheduleID || actualDue != record.ScheduledForUTC {
		return x, "", errors.New("occurrence authorization binding changed")
	}
	schedule, revision, spec, err := readBoundScheduleAuthorization(db, record.ScheduleID, account)
	if err != nil || revision != record.Revision || schedule.Status != scheduleActive || spec != record.Snapshot.ExecutionSpec || root.ConversationID != spec.ConversationID || root.BotID != spec.BotID {
		return x, "", errors.New("scheduled authorization was revised or revoked")
	}
	m := triggers[root.ID]
	if m.Role != "user" || m.Kind != "scheduled_task" || m.SenderBotID != "" || m.ConversationID != root.ConversationID || m.RunID != root.ID || m.Content != spec.Content {
		return x, "", errors.New("occurrence instruction binding changed")
	}
	lineage := &mcpScheduleLineage{AccountID: account, TargetRunID: requested.ID, SnapshotDigest: record.SnapshotDigest, Occurrence: record.Snapshot}
	if err := verifyMCPScheduleSources(db, record, lineage, &x); err != nil {
		return x, "", err
	}
	seenContext := make(map[string]bool)
	for _, r := range chain {
		lineage.Ancestry = append(lineage.Ancestry, mcpScheduleAncestor{Binding: *mcpScheduleBinding(r), Status: r.Status, Trigger: triggers[r.ID]})
		key := r.ConversationID + "\x00" + r.BotID
		if seenContext[key] {
			continue
		}
		seenContext[key] = true
		evidence, err := readMCPScheduleConversation(db, r.ConversationID, r.BotID)
		if err != nil {
			return x, "", err
		}
		lineage.Contexts = append(lineage.Contexts, mcpScheduleConversation{ConversationID: r.ConversationID, BotID: r.BotID, Context: evidence})
	}
	for _, r := range chain {
		rows, err := db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? ORDER BY started_at,call_id LIMIT 201`, r.ID)
		if err != nil {
			return x, "", err
		}
		var activities []ToolActivity
		for rows.Next() {
			var a ToolActivity
			var truncated int
			var outcome string
			err := rows.Scan(&a.ConversationID, &a.BotID, &a.RunID, &a.CallID, &a.Name, &a.Arguments, &a.Result, &a.Status, &truncated, &a.StartedAt, &a.UpdatedAt, &outcome)
			a.Truncated = truncated != 0
			a.Outcome = tooloutcome.Parse(outcome)
			if err != nil {
				rows.Close()
				return x, "", err
			}
			if a.Truncated || outcome != "" && a.Outcome == nil || a.Outcome != nil && a.Outcome.Certainty == "unknown" || !utf8.ValidString(a.Arguments) || !utf8.ValidString(a.Result) || a.ConversationID != r.ConversationID || a.BotID != r.BotID {
				rows.Close()
				return x, "", errors.New("complete tool-result or effect context unavailable")
			}
			activities = append(activities, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(activities) > 200 {
			return x, "", errors.New("complete tool-result context exceeds limit")
		}
		for i := range lineage.Contexts {
			if lineage.Contexts[i].ConversationID == r.ConversationID && lineage.Contexts[i].BotID == r.BotID {
				lineage.Contexts[i].ToolResults = append(lineage.Contexts[i].ToolResults, activities...)
				break
			}
		}
	}
	x.Instructions, x.Messages, x.MessageProvenance, x.Memories, x.Summary, x.SummaryVersion = lineage.Contexts[0].Context.Instructions, lineage.Contexts[0].Context.Messages, lineage.Contexts[0].Context.MessageProvenance, lineage.Contexts[0].Context.Memories, lineage.Contexts[0].Context.Summary, lineage.Contexts[0].Context.SummaryVersion
	x.ScheduleLineage = lineage
	raw, err := json.Marshal(x)
	if err != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return x, "", errors.New("complete scheduled authorization context exceeds limit")
	}
	return x, digestBytes(raw), nil
}

func verifyMCPScheduleSources(db reviewQuerier, record scheduleOccurrenceAuthorization, lineage *mcpScheduleLineage, x *mcpReviewContext) error {
	chain := record.Snapshot.Revisions
	if record.Revision < 1 || record.Revision > maxScheduleAuthorizationRevisions || int64(len(chain)) != record.Revision {
		return errors.New("complete original schedule source chain unavailable")
	}
	initial := chain[0].ExecutionSpec
	for i, saved := range chain {
		current, err := readScheduleAuthorizationRevision(db, record.ScheduleID, int64(i)+1)
		if err != nil || !sameScheduleAuthorizationRevision(current, saved) || saved.PreviousRevision != int64(i) || saved.AccountID != record.AccountID || saved.ConversationID != initial.ConversationID || saved.BotID != initial.BotID || saved.RequestID == "" {
			return errors.New("schedule source revision binding changed")
		}
		if saved.ExecutionSpec.AccountID != initial.AccountID || saved.ExecutionSpec.ConversationID != initial.ConversationID || saved.ExecutionSpec.BotID != initial.BotID || saved.ExecutionSpec.InitialAtUTC != initial.InitialAtUTC || saved.ExecutionSpec.Kind != initial.Kind || saved.ExecutionSpec.Timezone != initial.Timezone || saved.ExecutionSpec.IntervalSeconds != initial.IntervalSeconds || saved.ExecutionSpec.DailyTime != initial.DailyTime {
			return errors.New("schedule stable execution scope changed")
		}
		if i == 0 && saved.EventKind != "create" || i > 0 && saved.EventKind == "create" {
			return errors.New("legacy schedule lineage cannot be adopted implicitly")
		}
		switch saved.EventKind {
		case "pause", "delete", "archive":
			if i == len(chain)-1 {
				return errors.New("schedule authorization is revoked")
			}
			continue // Revocation removes authority and need not assert consent.
		case "create", "content_edit", "resume":
		default:
			return errors.New("unknown schedule authority event")
		}
		ref := &mcpScheduleSourceReference{ScheduleID: saved.ScheduleID, Revision: saved.Revision, RequestID: saved.RequestID, SourceKind: saved.SourceKind, SourceRunID: saved.SourceRunID, SourceDigest: saved.SourceDigest}
		switch saved.SourceKind {
		case scheduleSourceForm:
			var form scheduleFormAuthorizationSource
			if json.Unmarshal(saved.SourceContext, &form) != nil || form.Action != saved.EventKind || !json.Valid(form.SubmittedFields) || form.ExecutionSpec != saved.ExecutionSpec || saved.SourceRunID != "" || saved.SourceMessageID != "" {
				return errors.New("native schedule form binding changed")
			}
			lineage.Authorization = append(lineage.Authorization, mcpAuthorizationEvidence{Source: scheduleSourceForm, ScheduleSource: ref})
			if i == 0 {
				var fields struct {
					Content string `json:"content"`
				}
				if json.Unmarshal(form.SubmittedFields, &fields) == nil {
					x.Intent = fields.Content
				}
			}
		case scheduleSourceChat:
			var source mcpReviewContext
			if json.Unmarshal(saved.SourceContext, &source) != nil || source.ScheduleLineage != nil || source.SourceRunBinding == nil || source.IntentMessageID != saved.SourceMessageID || saved.SourceRunID == "" {
				return errors.New("native schedule chat source unavailable")
			}
			r, err := scanRun(db.QueryRow(mcpScheduleRunSQL, saved.SourceRunID))
			if err != nil || *source.SourceRunBinding != *mcpScheduleBinding(r) || r.ConversationID != saved.ConversationID || r.ParentRunID != "" || (r.Kind != "" && r.Kind != "chat" && r.Kind != runKindGroupChat) || r.TriggerMessageID != saved.SourceMessageID {
				return errors.New("native schedule source run binding changed")
			}
			if imported, err := scheduleImported(db, "run", r.ID); err != nil || imported {
				return errors.New("native schedule source run import boundary")
			}
			if err := mcpScheduleMember(db, r.ConversationID, r.BotID); err != nil {
				return err
			}
			authorization := mcpAuthorizationSources(source)
			if len(authorization) == 0 {
				return errors.New("native source consent provenance unavailable")
			}
			captured := make(map[string]Message, len(source.Messages))
			for _, m := range source.Messages {
				if _, duplicate := captured[m.ID]; duplicate {
					return errors.New("ambiguous source message identity")
				}
				captured[m.ID] = m
			}
			triggerFound := false
			for _, e := range authorization {
				old, ok := captured[e.MessageID]
				if !ok || e.ScheduleSource != nil {
					return errors.New("invalid native source message reference")
				}
				var content, role, conv, kind, sender, origin string
				err := db.QueryRow(`SELECT m.content,m.role,m.conversation_id,COALESCE(m.kind,''),COALESCE(m.sender_bot_id,''),`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.id=?`, e.MessageID).Scan(&content, &role, &conv, &kind, &sender, &origin)
				if err != nil || origin != mcpHostUserIngress || conv != r.ConversationID || role != old.Role || kind != old.Kind || sender != old.SenderBotID || content != old.Content {
					return errors.New("native original source text or provenance changed")
				}
				if e.MessageID == saved.SourceMessageID {
					triggerFound = true
					if content != source.Intent {
						return errors.New("original schedule intent changed")
					}
				}
				lineage.Authorization = append(lineage.Authorization, mcpAuthorizationEvidence{MessageID: e.MessageID, Source: mcpHostUserIngress, ScheduleSource: ref})
			}
			if !triggerFound {
				return errors.New("native source trigger lacks verified authorization")
			}
			if i == 0 {
				x.Intent, x.IntentMessageID = source.Intent, source.IntentMessageID
			}
		default:
			return errors.New("schedule source provenance is unknown")
		}
	}
	return nil
}

// Resolve only source-backed chronological edges. Unlike the runtime return
// resolver, this authorization walk never skips retries or failed ancestors.
func validateMCPScheduleEdges(db reviewQuerier, chain []Run, byID map[string]Run, triggers map[string]Message) error {
	assignment := func(child, parent Run) error {
		if child.BotID == parent.BotID || (parent.Status != "running" && parent.Status != "done" && parent.Status != runWaiting) {
			return errors.New("scheduled assignment requester unavailable")
		}
		m := triggers[child.ID]
		n := m.Notice
		if m.ConversationID != child.ConversationID || m.Role != "assistant" || m.Kind != "notice" || m.SenderBotID != parent.BotID || n == nil || n.FromBotID != parent.BotID || n.TargetConversationID != child.ConversationID {
			return errors.New("scheduled bot assignment binding changed")
		}
		expectedOrigin := parent.ConversationID
		if child.Kind == runKindTeam || child.ConversationID == parent.ConversationID {
			if parent.OriginConversationID != "" {
				expectedOrigin = parent.OriginConversationID
			}
		}
		if child.OriginConversationID != expectedOrigin {
			return errors.New("scheduled assignment origin changed")
		}
		if child.ConversationID == parent.ConversationID {
			if child.Kind != runKindGroupTask || n.Type != "handoff" {
				return errors.New("unsupported same-conversation continuation")
			}
		} else {
			switch child.Kind {
			case "":
				if n.Type != "forward" {
					return errors.New("invalid forward assignment")
				}
			case runKindMessage:
				if n.Type != "message" {
					return errors.New("invalid message assignment")
				}
			case runKindTeam:
				if n.Type != "handoff" {
					return errors.New("invalid team assignment")
				}
			default:
				return errors.New("unsupported cross-conversation continuation")
			}
		}
		contains := func(ids []string, id string) bool {
			for _, v := range ids {
				if v == id {
					return true
				}
			}
			return false
		}
		if n.TargetRunID == child.ID && n.ToBotID == child.BotID && m.RunID == child.ID {
			return nil
		}
		// The message tool uses a shared hidden trace even for one recipient.
		// Its sole child's trigger is the anchor; there is no TargetRunID list.
		if child.Kind == runKindMessage && n.TargetRunID == "" && len(n.TargetRunIDs) == 0 && m.RunID == child.ID && n.ToBotID == child.BotID && len(n.ToBotIDs) == 1 && n.ToBotIDs[0] == child.BotID && len(n.TargetBotIDs) == 1 && n.TargetBotIDs[0] == child.BotID {
			var siblings int
			if err := db.QueryRow(`SELECT COUNT(*) FROM runs WHERE parent_run_id=? AND trigger_message_id=? AND conversation_id=?`, parent.ID, child.TriggerMessageID, child.ConversationID).Scan(&siblings); err == nil && siblings == 1 {
				return nil
			}
			return errors.New("single-recipient message assignment is ambiguous")
		}
		// Fanout siblings share the first child's durable anchor. Its host notice
		// lists each target; parent-child trigger reuse is still rejected above.
		if child.Kind != runKindMessage || len(n.TargetRunIDs) < 2 || len(n.TargetRunIDs) > maxMCPAuthorizationAncestry || !contains(n.TargetRunIDs, child.ID) || !contains(n.TargetRunIDs, m.RunID) || !contains(n.TargetBotIDs, child.BotID) || !contains(n.ToBotIDs, child.BotID) {
			return errors.New("scheduled assignment target changed")
		}
		for _, id := range n.TargetRunIDs {
			sibling, err := scanRun(db.QueryRow(mcpScheduleRunSQL, id))
			if err != nil || sibling.Kind != runKindMessage || sibling.ParentRunID != parent.ID || sibling.TriggerMessageID != child.TriggerMessageID || sibling.ConversationID != child.ConversationID || !contains(n.TargetBotIDs, sibling.BotID) {
				return errors.New("fanout assignment membership changed")
			}
		}
		return nil
	}
	visiting := map[string]bool{}
	var original func(Run) (Run, error)
	var requester func(Run) (Run, error)
	var validateReturn func(Run, Run) error
	validateReturn = func(child, parent Run) error {
		m := triggers[child.ID]
		origin := child.ConversationID
		if child.ConversationID == parent.ConversationID {
			origin = parent.OriginConversationID
		}
		if parent.Status != "done" || m.ID == "" || m.ConversationID != child.ConversationID || m.RunID != parent.ID || m.Role != "assistant" || m.SenderBotID != parent.BotID || (m.Kind != "" && m.Kind != "forward_result" && m.Kind != messageKindBotResult) || child.OriginConversationID != origin {
			return errors.New("scheduled return result binding changed")
		}
		caller, err := requester(parent)
		if err != nil || caller.BotID != child.BotID || caller.ConversationID != child.ConversationID {
			return errors.New("scheduled return requester changed")
		}
		return nil
	}
	original = func(r Run) (Run, error) {
		if visiting[r.ID] {
			return Run{}, errors.New("scheduled logical return cycle")
		}
		visiting[r.ID] = true
		defer delete(visiting, r.ID)
		if r.Kind != runKindFollowup {
			return r, nil
		}
		parent, ok := byID[r.ParentRunID]
		if !ok {
			return Run{}, errors.New("scheduled return ancestry missing")
		}
		if err := validateReturn(r, parent); err != nil {
			return Run{}, err
		}
		caller, err := requester(parent)
		if err != nil {
			return Run{}, err
		}
		return original(caller)
	}
	requester = func(r Run) (Run, error) {
		actual, err := original(r)
		if err != nil {
			return Run{}, err
		}
		caller, ok := byID[actual.ParentRunID]
		if !ok {
			return Run{}, errors.New("scheduled logical requester missing")
		}
		if err := assignment(actual, caller); err != nil {
			return Run{}, err
		}
		return caller, nil
	}
	for i := 0; i+1 < len(chain); i++ {
		child, parent := chain[i], chain[i+1]
		if child.ParentRunID != parent.ID {
			return errors.New("scheduled ancestry link changed")
		}
		if child.Kind == runKindFollowup {
			if err := validateReturn(child, parent); err != nil {
				return err
			}
		} else if err := assignment(child, parent); err != nil {
			return err
		}
	}
	return nil
}

func readMCPScheduleConversation(db reviewQuerier, conversation, bot string) (mcpReviewContext, error) {
	var x mcpReviewContext
	if err := mcpScheduleMember(db, conversation, bot); err != nil {
		return x, err
	}
	var attachments int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE conversation_id=?`, conversation).Scan(&attachments); err != nil || attachments != 0 {
		return x, errors.New("complete non-text scheduled context unavailable")
	}
	if err := db.QueryRow(`SELECT instructions FROM bots WHERE id=?`, bot).Scan(&x.Instructions); err != nil {
		return x, err
	}
	if !utf8.ValidString(x.Instructions) {
		return x, errors.New("scheduled instructions cannot be represented completely")
	}
	rows, err := db.Query(`SELECT m.id,m.seq,m.role,COALESCE(m.kind,''),m.content,COALESCE(m.sender_bot_id,''),COALESCE(m.run_id,''),COALESCE(m.notice_data,''),`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.conversation_id=? ORDER BY m.seq LIMIT 201`, conversation)
	if err != nil {
		return x, err
	}
	for rows.Next() {
		var m Message
		var notice, source string
		if err = rows.Scan(&m.ID, &m.Seq, &m.Role, &m.Kind, &m.Content, &m.SenderBotID, &m.RunID, &notice, &source); err != nil {
			break
		}
		if !utf8.ValidString(m.Content) || !utf8.ValidString(notice) {
			err = errors.New("scheduled message cannot be represented completely")
			break
		}
		if notice != "" {
			if err = json.Unmarshal([]byte(notice), &m.Notice); err != nil {
				break
			}
		}
		x.Messages = append(x.Messages, m)
		x.MessageProvenance = append(x.MessageProvenance, mcpMessageProvenance{m.ID, source})
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.Messages) > 200 {
		return x, errors.New("complete scheduled conversation exceeds limit")
	}
	rows, err = db.Query(`SELECT id,content,revision FROM memories WHERE conversation_id=? AND (bot_id IS NULL OR bot_id=?) ORDER BY id LIMIT 201`, conversation, bot)
	if err != nil {
		return x, err
	}
	for rows.Next() {
		var m Memory
		if err = rows.Scan(&m.ID, &m.Content, &m.Revision); err != nil {
			break
		}
		if !utf8.ValidString(m.Content) {
			err = errors.New("scheduled memory cannot be represented completely")
			break
		}
		x.Memories = append(x.Memories, m)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.Memories) > 200 {
		return x, errors.New("complete scheduled memory exceeds limit")
	}
	err = db.QueryRow(`SELECT version,content FROM summaries WHERE conversation_id=? ORDER BY version DESC LIMIT 1`, conversation).Scan(&x.SummaryVersion, &x.Summary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return x, err
	}
	if !utf8.ValidString(x.Summary) {
		return x, errors.New("scheduled summary cannot be represented completely")
	}
	return x, nil
}
