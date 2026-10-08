package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
	Binding          mcpScheduleRunBinding `json:"binding"`
	Status           string                `json:"status"`
	Trigger          Message               `json:"untrusted_trigger"`
	TriggerTruncated bool                  `json:"trigger_truncated,omitempty"`
}

type mcpScheduleConversation struct {
	ConversationID string           `json:"conversation_id"`
	BotID          string           `json:"bot_id"`
	Context        mcpReviewContext `json:"untrusted_context"`
	ToolResults    []ToolActivity   `json:"untrusted_tool_results"`
}

// A compact, host-verified view of the occurrence's immutable authorization
// snapshot. The complete snapshot stays bound through SnapshotDigest.
type mcpScheduleOccurrenceEvidence struct {
	ScheduleID          string                        `json:"schedule_id"`
	RootRunID           string                        `json:"root_run_id"`
	TriggerMessageID    string                        `json:"trigger_message_id"`
	ScheduledForUTC     string                        `json:"scheduled_for_utc"`
	Revision            int64                         `json:"revision"`
	ExecutionSpec       scheduleExecutionSpec         `json:"execution_spec"`
	ExecutionSpecDigest string                        `json:"execution_spec_digest"`
	ContentTruncated    bool                          `json:"execution_content_truncated,omitempty"`
	Revisions           []mcpScheduleRevisionEvidence `json:"revisions"`
}

// One immutable creation/edit/revocation event with its original source.
type mcpScheduleRevisionEvidence struct {
	Revision         int64     `json:"revision"`
	EventKind        string    `json:"event_kind"`
	SourceKind       string    `json:"source_kind"`
	RequestID        string    `json:"request_id"`
	CreatedAt        string    `json:"created_at"`
	SourceDigest     string    `json:"source_digest"`
	ExecutionContent string    `json:"execution_content"`
	SubmittedFields  string    `json:"untrusted_submitted_fields,omitempty"`
	SourceRequest    string    `json:"source_request,omitempty"`
	SourceMessages   []Message `json:"source_conversation_context,omitempty"`
	Truncated        bool      `json:"truncated,omitempty"`
}

type mcpScheduleLineage struct {
	AccountID      string                        `json:"account_id"`
	TargetRunID    string                        `json:"target_run_id"`
	SnapshotDigest string                        `json:"snapshot_digest"`
	Occurrence     mcpScheduleOccurrenceEvidence `json:"occurrence"`
	Ancestry       []mcpScheduleAncestor         `json:"ancestry"`
	Contexts       []mcpScheduleConversation     `json:"contexts"`
	Authorization  []mcpAuthorizationEvidence    `json:"authorization_sources"`
}

// A delegated task whose chain starts at a host-ingress chat request. The
// top-level packet is that root request's evidence; these are the hops.
type mcpDelegationLineage struct {
	TargetRunID string                    `json:"target_run_id"`
	RootRunID   string                    `json:"root_run_id"`
	Ancestry    []mcpScheduleAncestor     `json:"ancestry"`
	Contexts    []mcpScheduleConversation `json:"contexts"`
}

// Direct and group chat runs use the chat reader. Every chain with a parent
// (scheduled or delegated) is admitted only through the account-aware chain
// resolver, including inside the claim transaction.
func (s *Server) readMCPReviewContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, string, error) {
	if r.Kind == runKindGroupChat || r.Kind != runKindSchedule && r.ParentRunID == "" {
		return readMCPReviewContext(db, c, r)
	}
	return s.readMCPChainReviewContext(db, c, r)
}

const mcpScheduleRunSQL = `SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`

func mcpScheduleMember(db reviewQuerier, conversation, bot string) error {
	var member bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversations c JOIN bots b ON b.id=? JOIN members m ON m.conversation_id=c.id AND m.bot_id=b.id WHERE c.id=? AND c.archived=0 AND b.archived=0 AND (c.kind='group' OR c.bot_id=b.id))`, bot, conversation).Scan(&member)
	if err != nil || !member {
		return mcpContextFail(mcpContextMembership)
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
	if err != nil {
		return m, mcpContextFail(mcpContextAncestryMessage)
	}
	if imported, e := scheduleImported(db, "message", id); e != nil || imported {
		return m, mcpContextFail(mcpContextAncestryImported)
	}
	return m, nil
}

func mcpScheduleLiveRun(db reviewQuerier, id string) (Run, error) {
	r, err := scanRun(db.QueryRow(mcpScheduleRunSQL, id))
	if err != nil {
		return r, mcpContextFail(mcpContextAncestryUnavailable)
	}
	if r.ID == "" || r.TriggerMessageID == "" || (r.Status != "running" && r.Status != "queued" && r.Status != runWaiting && r.Status != "done") || strings.TrimSpace(r.Error) != "" {
		return r, mcpContextFail(mcpContextAncestryEnded)
	}
	if imported, e := scheduleImported(db, "run", id); e != nil || imported {
		return r, mcpContextFail(mcpContextAncestryImported)
	}
	return r, mcpScheduleMember(db, r.ConversationID, r.BotID)
}

func mcpGroupRoundEdge(child, parent Run) bool {
	return child.Kind == runKindGroupChat && parent.Kind == runKindGroupChat && child.ParentRunID == parent.ID && child.ConversationID == parent.ConversationID && child.TriggerMessageID == parent.TriggerMessageID
}

func (s *Server) readMCPChainReviewContext(db reviewQuerier, c Conversation, requested Run) (mcpReviewContext, string, error) {
	var x mcpReviewContext
	account := s.reviewAccountID()
	if account == "" {
		return x, "", mcpContextFail(mcpContextAccountUnavailable)
	}
	if requested.ConversationID != c.ID {
		return x, "", mcpContextFail(mcpContextTargetBinding)
	}
	byID := make(map[string]Run)
	triggers := make(map[string]Message)
	var chain []Run
	id := requested.ID
	for id != "" && len(chain) < maxMCPAuthorizationAncestry {
		if _, seen := byID[id]; seen {
			return x, "", mcpContextFail(mcpContextAncestryCycle)
		}
		r, err := mcpScheduleLiveRun(db, id)
		if err != nil {
			return x, "", err
		}
		if len(chain) == 0 && (*mcpScheduleBinding(r) != *mcpScheduleBinding(requested) || r.Status != "running") {
			return x, "", mcpContextFail(mcpContextTargetBinding)
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
		return x, "", mcpContextFail(mcpContextAncestryUnavailable)
	}
	switch root := chain[len(chain)-1]; {
	case root.Kind == runKindSchedule:
		return s.readMCPScheduleChain(db, account, requested, chain, byID, triggers)
	case root.Kind == "" || root.Kind == "chat" || root.Kind == runKindGroupChat:
		return readMCPDelegatedChain(db, requested, chain, byID, triggers)
	}
	return x, "", mcpContextFail(mcpContextDelegationRoot)
}

// A delegated child of a human chat request is authorized only by that root's
// host-verified trigger. Every hop must be a recorded assignment or return.
func readMCPDelegatedChain(db reviewQuerier, requested Run, chain []Run, byID map[string]Run, triggers map[string]Message) (mcpReviewContext, string, error) {
	root := chain[len(chain)-1]
	for i := 0; i+1 < len(chain); i++ {
		if chain[i].TriggerMessageID == chain[i+1].TriggerMessageID && !mcpGroupRoundEdge(chain[i], chain[i+1]) {
			return mcpReviewContext{}, "", mcpContextFail(mcpContextRetryInheritance)
		}
	}
	if err := validateMCPScheduleEdges(db, chain, byID, triggers); err != nil {
		return mcpReviewContext{}, "", err
	}
	x, err := readMCPChatContext(db, Conversation{ID: root.ConversationID}, root)
	if err != nil {
		return mcpReviewContext{}, "", err
	}
	rootKey := root.ConversationID + "\x00" + root.BotID
	ancestry, contexts, rootRows, err := readMCPChainContexts(db, chain, triggers, false, rootKey)
	if err != nil {
		return mcpReviewContext{}, "", err
	}
	for _, a := range rootRows {
		if a.RunID != root.ID {
			x.ToolResults = append(x.ToolResults, a)
		}
	}
	x.Delegation = &mcpDelegationLineage{TargetRunID: requested.ID, RootRunID: root.ID, Ancestry: ancestry, Contexts: contexts}
	digest, err := fitMCPReviewContext(&x, mcpEvidenceBudget)
	if err != nil {
		return x, "", err
	}
	return x, digest, nil
}

func (s *Server) readMCPScheduleChain(db reviewQuerier, account string, requested Run, chain []Run, byID map[string]Run, triggers map[string]Message) (mcpReviewContext, string, error) {
	var x mcpReviewContext
	root := chain[len(chain)-1]
	for i, r := range chain {
		var scheduleID, scheduledFor string
		err := db.QueryRow(`SELECT schedule_id,scheduled_for_utc FROM schedule_occurrences WHERE run_id=?`, r.ID).Scan(&scheduleID, &scheduledFor)
		if i == len(chain)-1 {
			if err != nil {
				return x, "", mcpContextFail(mcpContextScheduleRoot)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return x, "", mcpContextFail(mcpContextScheduleRootAmbig)
		}
		if i+1 < len(chain) && r.TriggerMessageID == chain[i+1].TriggerMessageID {
			return x, "", mcpContextFail(mcpContextRetryInheritance)
		}
	}
	if err := validateMCPScheduleEdges(db, chain, byID, triggers); err != nil {
		return x, "", err
	}
	record, err := readScheduleOccurrenceAuthorization(db, root.ID)
	if err != nil || record.AccountID != account || record.TriggerMessageID != root.TriggerMessageID {
		return x, "", mcpContextFail(mcpContextOccurrenceAuth)
	}
	var actualSchedule, actualDue string
	if err := db.QueryRow(`SELECT schedule_id,scheduled_for_utc FROM schedule_occurrences WHERE run_id=?`, root.ID).Scan(&actualSchedule, &actualDue); err != nil || actualSchedule != record.ScheduleID || actualDue != record.ScheduledForUTC {
		return x, "", mcpContextFail(mcpContextOccurrenceAuth)
	}
	schedule, revision, spec, err := readBoundScheduleAuthorization(db, record.ScheduleID, account)
	if err != nil || revision != record.Revision || schedule.Status != scheduleActive || spec != record.Snapshot.ExecutionSpec || root.ConversationID != spec.ConversationID || root.BotID != spec.BotID {
		return x, "", mcpContextFail(mcpContextScheduleRevoked)
	}
	m := triggers[root.ID]
	if m.Role != "user" || m.Kind != "scheduled_task" || m.SenderBotID != "" || m.ConversationID != root.ConversationID || m.RunID != root.ID || m.Content != spec.Content {
		return x, "", mcpContextFail(mcpContextOccurrenceChanged)
	}
	snapshot := record.Snapshot
	occurrence := mcpScheduleOccurrenceEvidence{ScheduleID: snapshot.ScheduleID, RootRunID: snapshot.RootRunID, TriggerMessageID: snapshot.TriggerMessageID, ScheduledForUTC: snapshot.ScheduledForUTC, Revision: snapshot.Revision, ExecutionSpec: snapshot.ExecutionSpec, ExecutionSpecDigest: snapshot.ExecutionSpecDigest}
	occurrence.ExecutionSpec.Content, occurrence.ContentTruncated = mcpTruncate(occurrence.ExecutionSpec.Content, mcpEvidenceIntentRunes)
	lineage := &mcpScheduleLineage{AccountID: account, TargetRunID: requested.ID, SnapshotDigest: record.SnapshotDigest, Occurrence: occurrence}
	if err := verifyMCPScheduleSources(db, record, lineage, &x); err != nil {
		return x, "", err
	}
	if lineage.Ancestry, lineage.Contexts, _, err = readMCPChainContexts(db, chain, triggers, true, ""); err != nil {
		return x, "", err
	}
	// The target conversation's text lives once, in its lineage context.
	x.Instructions = lineage.Contexts[0].Context.Instructions
	x.ScheduleLineage = lineage
	bounds := &mcpEvidenceBounds{}
	boundMCPIntent(&x, bounds)
	x.Bounds = bounds.orNil()
	digest, err := fitMCPReviewContext(&x, mcpEvidenceBudget)
	if err != nil {
		return x, "", err
	}
	return x, digest, nil
}

// Each distinct (conversation, bot) contributes one bounded window ending at
// the nearest chain trigger in it; each chain run contributes its own records.
// Records of skipKey are returned separately for the caller's own context.
func readMCPChainContexts(db reviewQuerier, chain []Run, triggers map[string]Message, strictEffects bool, skipKey string) ([]mcpScheduleAncestor, []mcpScheduleConversation, []ToolActivity, error) {
	var ancestry []mcpScheduleAncestor
	var contexts []mcpScheduleConversation
	var skipped []ToolActivity
	index := map[string]int{}
	for _, r := range chain {
		trigger := triggers[r.ID]
		var cut bool
		trigger.Content, cut = mcpTruncate(trigger.Content, mcpEvidenceMessageRunes)
		ancestry = append(ancestry, mcpScheduleAncestor{Binding: *mcpScheduleBinding(r), Status: r.Status, Trigger: trigger, TriggerTruncated: cut})
		key := r.ConversationID + "\x00" + r.BotID
		if _, seen := index[key]; seen || key == skipKey {
			continue
		}
		evidence, err := readMCPScheduleConversation(db, r.ConversationID, r.BotID, triggers[r.ID])
		if err != nil {
			return nil, nil, nil, err
		}
		index[key] = len(contexts)
		contexts = append(contexts, mcpScheduleConversation{ConversationID: r.ConversationID, BotID: r.BotID, Context: evidence})
	}
	for _, r := range chain {
		key := r.ConversationID + "\x00" + r.BotID
		var bounds *mcpEvidenceBounds
		if key != skipKey {
			bounds = contexts[index[key]].Context.Bounds
			if bounds == nil {
				bounds = &mcpEvidenceBounds{}
			}
		} else {
			bounds = &mcpEvidenceBounds{}
		}
		activities, err := readMCPRunToolEvidence(db, r, strictEffects, bounds)
		if err != nil {
			return nil, nil, nil, err
		}
		if key == skipKey {
			skipped = append(skipped, activities...)
			continue
		}
		c := &contexts[index[key]]
		c.ToolResults = append(c.ToolResults, activities...)
		c.Context.Bounds = bounds.orNil()
	}
	return ancestry, contexts, skipped, nil
}

func (b *mcpEvidenceBounds) intent() *mcpTruncatedText {
	if b == nil {
		return nil
	}
	return b.Intent
}

func verifyMCPScheduleSources(db reviewQuerier, record scheduleOccurrenceAuthorization, lineage *mcpScheduleLineage, x *mcpReviewContext) error {
	chain := record.Snapshot.Revisions
	if record.Revision < 1 || record.Revision > maxScheduleAuthorizationRevisions || int64(len(chain)) != record.Revision {
		return mcpContextFail(mcpContextSourceChain)
	}
	initial := chain[0].ExecutionSpec
	for i, saved := range chain {
		current, err := readScheduleAuthorizationRevision(db, record.ScheduleID, int64(i)+1)
		if err != nil || !sameScheduleAuthorizationRevision(current, saved) || saved.PreviousRevision != int64(i) || saved.AccountID != record.AccountID || saved.ConversationID != initial.ConversationID || saved.BotID != initial.BotID || saved.RequestID == "" {
			return mcpContextFail(mcpContextSourceChain)
		}
		if saved.ExecutionSpec.AccountID != initial.AccountID || saved.ExecutionSpec.ConversationID != initial.ConversationID || saved.ExecutionSpec.BotID != initial.BotID || saved.ExecutionSpec.InitialAtUTC != initial.InitialAtUTC || saved.ExecutionSpec.Kind != initial.Kind || saved.ExecutionSpec.Timezone != initial.Timezone || saved.ExecutionSpec.IntervalSeconds != initial.IntervalSeconds || saved.ExecutionSpec.DailyTime != initial.DailyTime {
			return mcpContextFail(mcpContextSourceChain)
		}
		if i == 0 && saved.EventKind != "create" || i > 0 && saved.EventKind == "create" {
			return mcpContextFail(mcpContextSourceChain)
		}
		evidence := mcpScheduleRevisionEvidence{Revision: saved.Revision, EventKind: saved.EventKind, SourceKind: saved.SourceKind, RequestID: saved.RequestID, CreatedAt: saved.CreatedAt, SourceDigest: saved.SourceDigest}
		evidence.ExecutionContent, evidence.Truncated = mcpTruncate(saved.ExecutionSpec.Content, mcpEvidenceMessageRunes)
		switch saved.EventKind {
		case "pause", "delete", "archive":
			if i == len(chain)-1 {
				return mcpContextFail(mcpContextScheduleRevoked)
			}
			lineage.Occurrence.Revisions = append(lineage.Occurrence.Revisions, evidence)
			continue // Revocation removes authority and need not assert consent.
		case "create", "content_edit", "resume":
		default:
			return mcpContextFail(mcpContextSourceChain)
		}
		ref := &mcpScheduleSourceReference{ScheduleID: saved.ScheduleID, Revision: saved.Revision, RequestID: saved.RequestID, SourceKind: saved.SourceKind, SourceRunID: saved.SourceRunID, SourceDigest: saved.SourceDigest}
		switch saved.SourceKind {
		case scheduleSourceForm:
			var form scheduleFormAuthorizationSource
			if json.Unmarshal(saved.SourceContext, &form) != nil || form.Action != saved.EventKind || !json.Valid(form.SubmittedFields) || form.ExecutionSpec != saved.ExecutionSpec || saved.SourceRunID != "" || saved.SourceMessageID != "" {
				return mcpContextFail(mcpContextSourceProvenance)
			}
			lineage.Authorization = append(lineage.Authorization, mcpAuthorizationEvidence{Source: scheduleSourceForm, ScheduleSource: ref})
			var cut bool
			evidence.SubmittedFields, cut = mcpTruncate(string(form.SubmittedFields), mcpEvidenceMessageRunes)
			evidence.Truncated = evidence.Truncated || cut
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
				return mcpContextFail(mcpContextSourceProvenance)
			}
			r, err := scanRun(db.QueryRow(mcpScheduleRunSQL, saved.SourceRunID))
			if err != nil || *source.SourceRunBinding != *mcpScheduleBinding(r) || r.ConversationID != saved.ConversationID || r.ParentRunID != "" || (r.Kind != "" && r.Kind != "chat" && r.Kind != runKindGroupChat) || r.TriggerMessageID != saved.SourceMessageID {
				return mcpContextFail(mcpContextSourceProvenance)
			}
			if imported, err := scheduleImported(db, "run", r.ID); err != nil || imported {
				return mcpContextFail(mcpContextSourceProvenance)
			}
			if err := mcpScheduleMember(db, r.ConversationID, r.BotID); err != nil {
				return err
			}
			authorization := mcpAuthorizationSources(source)
			if len(authorization) == 0 {
				return mcpContextFail(mcpContextSourceProvenance)
			}
			captured := make(map[string]Message, len(source.Messages))
			for _, m := range source.Messages {
				if _, duplicate := captured[m.ID]; duplicate {
					return mcpContextFail(mcpContextSourceProvenance)
				}
				captured[m.ID] = m
			}
			triggerFound := false
			for _, e := range authorization {
				old, ok := captured[e.MessageID]
				if !ok || e.ScheduleSource != nil {
					return mcpContextFail(mcpContextSourceProvenance)
				}
				var content, role, conv, kind, sender, origin string
				err := db.QueryRow(`SELECT m.content,m.role,m.conversation_id,COALESCE(m.kind,''),COALESCE(m.sender_bot_id,''),`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.id=?`, e.MessageID).Scan(&content, &role, &conv, &kind, &sender, &origin)
				if err != nil || origin != mcpHostUserIngress || conv != r.ConversationID || role != old.Role || kind != old.Kind || sender != old.SenderBotID || !mcpCapturedTextMatches(content, old.Content, source.Bounds.truncatedMessage(e.MessageID)) {
					return mcpContextFail(mcpContextSourceProvenance)
				}
				if e.MessageID == saved.SourceMessageID {
					triggerFound = true
					if !mcpCapturedTextMatches(content, source.Intent, source.Bounds.intent()) {
						return mcpContextFail(mcpContextSourceProvenance)
					}
				}
				lineage.Authorization = append(lineage.Authorization, mcpAuthorizationEvidence{MessageID: e.MessageID, Source: mcpHostUserIngress, ScheduleSource: ref})
			}
			if !triggerFound {
				return mcpContextFail(mcpContextSourceProvenance)
			}
			var cut bool
			evidence.SourceRequest, cut = mcpTruncate(source.Intent, mcpEvidenceMessageRunes)
			evidence.Truncated = evidence.Truncated || cut
			messages := source.Messages
			if len(messages) > mcpEvidenceSourceMessages {
				messages, evidence.Truncated = messages[len(messages)-mcpEvidenceSourceMessages:], true
			}
			for _, m := range messages {
				m.Content, cut = mcpTruncate(m.Content, mcpEvidenceSourceRunes)
				m.Notice, m.RunID = nil, ""
				evidence.Truncated = evidence.Truncated || cut
				evidence.SourceMessages = append(evidence.SourceMessages, m)
			}
			if i == 0 {
				x.Intent, x.IntentMessageID = source.Intent, source.IntentMessageID
			}
		case scheduleSourceUnknown:
			return mcpContextFail(mcpContextSourceUnknown)
		default:
			return mcpContextFail(mcpContextSourceProvenance)
		}
		lineage.Occurrence.Revisions = append(lineage.Occurrence.Revisions, evidence)
	}
	return nil
}

// Resolve only source-backed chronological edges (scheduled or delegated). Unlike the runtime return
// resolver, this authorization walk never skips retries or failed ancestors.
func validateMCPScheduleEdges(db reviewQuerier, chain []Run, byID map[string]Run, triggers map[string]Message) error {
	assignment := func(child, parent Run) error {
		if child.BotID == parent.BotID || (parent.Status != "running" && parent.Status != "done" && parent.Status != runWaiting) {
			return mcpContextFail(mcpContextAssignmentBinding)
		}
		m := triggers[child.ID]
		n := m.Notice
		if m.ConversationID != child.ConversationID || m.Role != "assistant" || m.Kind != "notice" || m.SenderBotID != parent.BotID || n == nil || n.FromBotID != parent.BotID || n.TargetConversationID != child.ConversationID {
			return mcpContextFail(mcpContextAssignmentBinding)
		}
		expectedOrigin := parent.ConversationID
		if child.Kind == runKindTeam || child.ConversationID == parent.ConversationID {
			if parent.OriginConversationID != "" {
				expectedOrigin = parent.OriginConversationID
			}
		}
		if child.OriginConversationID != expectedOrigin {
			return mcpContextFail(mcpContextAssignmentBinding)
		}
		if child.ConversationID == parent.ConversationID {
			if child.Kind != runKindGroupTask || n.Type != "handoff" {
				return mcpContextFail(mcpContextAssignmentBinding)
			}
		} else {
			switch child.Kind {
			case "":
				if n.Type != "forward" {
					return mcpContextFail(mcpContextAssignmentBinding)
				}
			case runKindMessage:
				if n.Type != "message" {
					return mcpContextFail(mcpContextAssignmentBinding)
				}
			case runKindTeam:
				if n.Type != "handoff" {
					return mcpContextFail(mcpContextAssignmentBinding)
				}
			default:
				return mcpContextFail(mcpContextAssignmentBinding)
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
			return mcpContextFail(mcpContextAssignmentBinding)
		}
		// Fanout siblings share the first child's durable anchor. Its host notice
		// lists each target; parent-child trigger reuse is still rejected above.
		if child.Kind != runKindMessage || len(n.TargetRunIDs) < 2 || len(n.TargetRunIDs) > maxMCPAuthorizationAncestry || !contains(n.TargetRunIDs, child.ID) || !contains(n.TargetRunIDs, m.RunID) || !contains(n.TargetBotIDs, child.BotID) || !contains(n.ToBotIDs, child.BotID) {
			return mcpContextFail(mcpContextAssignmentBinding)
		}
		for _, id := range n.TargetRunIDs {
			sibling, err := scanRun(db.QueryRow(mcpScheduleRunSQL, id))
			if err != nil || sibling.Kind != runKindMessage || sibling.ParentRunID != parent.ID || sibling.TriggerMessageID != child.TriggerMessageID || sibling.ConversationID != child.ConversationID || !contains(n.TargetBotIDs, sibling.BotID) {
				return mcpContextFail(mcpContextAssignmentBinding)
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
			return mcpContextFail(mcpContextReturnBinding)
		}
		caller, err := requester(parent)
		if err != nil || caller.BotID != child.BotID || caller.ConversationID != child.ConversationID {
			return mcpContextFail(mcpContextReturnBinding)
		}
		return nil
	}
	original = func(r Run) (Run, error) {
		if visiting[r.ID] {
			return Run{}, mcpContextFail(mcpContextAncestryCycle)
		}
		visiting[r.ID] = true
		defer delete(visiting, r.ID)
		if r.Kind != runKindFollowup {
			return r, nil
		}
		parent, ok := byID[r.ParentRunID]
		if !ok {
			return Run{}, mcpContextFail(mcpContextAncestryUnavailable)
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
			return Run{}, mcpContextFail(mcpContextAncestryUnavailable)
		}
		if err := assignment(actual, caller); err != nil {
			return Run{}, err
		}
		return caller, nil
	}
	for i := 0; i+1 < len(chain); i++ {
		child, parent := chain[i], chain[i+1]
		if child.ParentRunID != parent.ID {
			return mcpContextFail(mcpContextAncestryUnavailable)
		}
		if mcpGroupRoundEdge(child, parent) {
			continue // An invited member shares its round's verified user trigger.
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

// One conversation of a chain: bounded text ending at the chain's trigger in
// it, its unread attachment manifest and human refusals. Earlier attachments
// are an explicit manifest, not a reason the occurrence cannot be reviewed.
func readMCPScheduleConversation(db reviewQuerier, conversation, bot string, trigger Message) (mcpReviewContext, error) {
	var x mcpReviewContext
	if err := mcpScheduleMember(db, conversation, bot); err != nil {
		return x, err
	}
	if err := db.QueryRow(`SELECT instructions FROM bots WHERE id=?`, bot).Scan(&x.Instructions); err != nil {
		return x, mcpContextFail(mcpContextInstructionsRead)
	}
	var err error
	if x.AttachmentBoundary, err = readMCPAttachmentBoundary(db, conversation, trigger.ID, trigger.Seq); err != nil {
		return x, err
	}
	bounds := &mcpEvidenceBounds{}
	boundMCPIntent(&x, bounds)
	if x.Messages, x.MessageProvenance, err = readMCPMessageWindow(db, conversation, trigger.Seq, true, bounds); err != nil {
		return x, err
	}
	if x.HumanRefusals, err = readMCPHumanRefusals(db, conversation, bounds); err != nil {
		return x, err
	}
	x.Bounds = bounds.orNil()
	return x, nil
}
