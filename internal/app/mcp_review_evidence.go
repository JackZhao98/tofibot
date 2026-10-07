package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The review packet is a bounded window, never the whole history. Authority
// comes from the trigger (or schedule lineage); older text, records and
// refusals are evidence that may be shortened or omitted with explicit flags.
// Size or count alone never makes a proposal unreviewable.
const (
	mcpEvidenceMessages         = 40
	mcpEvidenceMessageRunes     = 2000
	mcpEvidenceIntentRunes      = 6000
	mcpEvidenceInstructionRunes = 6000
	mcpEvidenceLaterUser        = 10
	mcpEvidenceRefusals         = 20
	mcpEvidenceRefusalRunes     = 1500
	mcpEvidenceToolRows         = 60
	mcpEvidenceToolRunes        = 1500
	mcpEvidenceAttachments      = 50
	mcpEvidenceSourceMessages   = 12
	mcpEvidenceSourceRunes      = 1000
	// Leaves room for 32 KiB arguments, schema and description within the
	// reviewer's 100 KiB input cap.
	mcpEvidenceBudget = 56 << 10
)

// A shortened text keeps its original size and digest so the full durable
// text stays bound even though the packet shows only a prefix.
type mcpTruncatedText struct {
	ID     string `json:"id,omitempty"`
	Runes  int    `json:"original_runes"`
	Digest string `json:"sha256"`
}

// Host facts about what this packet shortened or left out. They describe
// presentation bounds only; they are neither consent nor a context gap.
type mcpEvidenceBounds struct {
	Intent                   *mcpTruncatedText  `json:"user_intent_truncated,omitempty"`
	InstructionsTruncated    bool               `json:"bot_instructions_truncated,omitempty"`
	OlderMessagesOmitted     bool               `json:"older_messages_omitted,omitempty"`
	TruncatedMessages        []mcpTruncatedText `json:"truncated_messages,omitempty"`
	LaterUserMessagesOmitted bool               `json:"later_user_messages_omitted,omitempty"`
	OlderRefusalsOmitted     bool               `json:"older_refusals_omitted,omitempty"`
	RefusalPayloadsTruncated bool               `json:"refusal_payloads_truncated,omitempty"`
	OlderToolRecordsOmitted  bool               `json:"older_tool_records_omitted,omitempty"`
	ToolRecordsTruncated     bool               `json:"tool_records_truncated,omitempty"`
	OlderSourceTextOmitted   bool               `json:"older_schedule_source_text_omitted,omitempty"`
}

func (b *mcpEvidenceBounds) orNil() *mcpEvidenceBounds {
	if b == nil || b.Intent == nil && !b.InstructionsTruncated && !b.OlderMessagesOmitted && len(b.TruncatedMessages) == 0 && !b.LaterUserMessagesOmitted && !b.OlderRefusalsOmitted && !b.RefusalPayloadsTruncated && !b.OlderToolRecordsOmitted && !b.ToolRecordsTruncated && !b.OlderSourceTextOmitted {
		return nil
	}
	return b
}

// Messages after the authorizing trigger that a human typed. They can only
// add restrictions; they never become authorization evidence.
type mcpRestrictionMessage struct {
	Message
	Source string `json:"host_message_provenance"`
}

func mcpTruncate(s string, limit int) (string, bool) {
	if utf8.RuneCountInString(s) <= limit {
		return s, false
	}
	r := []rune(s)
	return string(r[:limit]) + "…[truncated]", true
}

func mcpTruncatedRecord(id, full string) mcpTruncatedText {
	return mcpTruncatedText{ID: id, Runes: utf8.RuneCountInString(full), Digest: digestBytes([]byte(full))}
}

// The durable text matches a captured (possibly shortened) copy only when the
// shown prefix and, for a shortened copy, the recorded full digest both agree.
func mcpCapturedTextMatches(full, captured string, record *mcpTruncatedText) bool {
	if record == nil {
		return full == captured
	}
	return record.Runes == utf8.RuneCountInString(full) && record.Digest == digestBytes([]byte(full)) && mcpTruncatedPrefix(full, captured)
}

func mcpTruncatedPrefix(full, captured string) bool {
	prefix := strings.TrimSuffix(captured, "…[truncated]")
	return prefix != captured && strings.HasPrefix(full, prefix)
}

func (b *mcpEvidenceBounds) truncatedMessage(id string) *mcpTruncatedText {
	if b == nil {
		return nil
	}
	for i := range b.TruncatedMessages {
		if b.TruncatedMessages[i].ID == id {
			return &b.TruncatedMessages[i]
		}
	}
	return nil
}

// Bound the authorizing request and bot instructions in place.
func boundMCPIntent(x *mcpReviewContext, bounds *mcpEvidenceBounds) {
	if shown, cut := mcpTruncate(x.Intent, mcpEvidenceIntentRunes); cut {
		record := mcpTruncatedRecord(x.IntentMessageID, x.Intent)
		bounds.Intent, x.Intent = &record, shown
	}
	if shown, cut := mcpTruncate(strings.ToValidUTF8(x.Instructions, "�"), mcpEvidenceInstructionRunes); cut || shown != x.Instructions {
		bounds.InstructionsTruncated, x.Instructions = true, shown
	}
}

// Newest-first window ending at the authorizing message. Later writes cannot
// enter it, so progress, summaries and other runs never change the digest.
func readMCPMessageWindow(db reviewQuerier, conversation string, throughSeq int64, notices bool, bounds *mcpEvidenceBounds) ([]Message, []mcpMessageProvenance, error) {
	rows, err := db.Query(`SELECT m.id,m.seq,m.role,COALESCE(m.kind,''),m.content,COALESCE(m.sender_bot_id,''),COALESCE(m.run_id,''),COALESCE(m.notice_data,''),`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.conversation_id=? AND m.seq<=? ORDER BY m.seq DESC LIMIT ?`, conversation, throughSeq, mcpEvidenceMessages+1)
	if err != nil {
		return nil, nil, mcpContextFail(mcpContextMessagesQuery)
	}
	var messages []Message
	var provenance []mcpMessageProvenance
	for rows.Next() {
		var m Message
		var notice, source string
		if err = rows.Scan(&m.ID, &m.Seq, &m.Role, &m.Kind, &m.Content, &m.SenderBotID, &m.RunID, &notice, &source); err != nil {
			err = mcpContextFail(mcpContextMessagesScan)
			break
		}
		if len(messages) == mcpEvidenceMessages {
			bounds.OlderMessagesOmitted = true
			break
		}
		if !notices {
			m.RunID = ""
		} else if notice != "" && utf8.ValidString(notice) && json.Unmarshal([]byte(notice), &m.Notice) != nil {
			m.Notice = nil
		}
		full := m.Content
		shown, cut := mcpTruncate(strings.ToValidUTF8(full, "�"), mcpEvidenceMessageRunes)
		if cut || shown != full {
			bounds.TruncatedMessages = append(bounds.TruncatedMessages, mcpTruncatedRecord(m.ID, full))
			m.Content = shown
		}
		messages = append(messages, m)
		provenance = append(provenance, mcpMessageProvenance{m.ID, source})
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if rowErr != nil {
		return nil, nil, mcpContextFail(mcpContextMessagesIteration)
	}
	reverseSlice(messages)
	reverseSlice(provenance)
	reverseSlice(bounds.TruncatedMessages)
	return messages, provenance, nil
}

// Human-typed messages after the trigger stay bound as restrictions.
func readMCPLaterUserMessages(db reviewQuerier, conversation string, afterSeq int64, bounds *mcpEvidenceBounds) ([]mcpRestrictionMessage, error) {
	rows, err := db.Query(`SELECT m.id,m.seq,m.role,COALESCE(m.kind,''),m.content,`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.conversation_id=? AND m.seq>? AND m.role='user' AND COALESCE(m.sender_bot_id,'')='' AND COALESCE(m.kind,'') IN ('','user_message') ORDER BY m.seq DESC LIMIT ?`, conversation, afterSeq, mcpEvidenceLaterUser+1)
	if err != nil {
		return nil, mcpContextFail(mcpContextMessagesQuery)
	}
	var out []mcpRestrictionMessage
	for rows.Next() {
		var m mcpRestrictionMessage
		if err = rows.Scan(&m.ID, &m.Seq, &m.Role, &m.Kind, &m.Content, &m.Source); err != nil {
			err = mcpContextFail(mcpContextMessagesScan)
			break
		}
		if len(out) == mcpEvidenceLaterUser {
			bounds.LaterUserMessagesOmitted = true
			break
		}
		m.Content, _ = mcpTruncate(strings.ToValidUTF8(m.Content, "�"), mcpEvidenceMessageRunes)
		out = append(out, m)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, mcpContextFail(mcpContextMessagesIteration)
	}
	reverseSlice(out)
	return out, nil
}

// Discovery output is schema/directory metadata already represented by the
// proposal itself; it is never an effect or authorization fact.
const mcpDiscoveryToolsSQL = `('search_mcp_tools','list_mcp_servers','search_mcp_catalog')`

// Observation-only records may appear between a human approval and the exact
// re-proposal without changing any authorization or effect fact.
func mcpObservationOnlyActivity(a ToolActivity) bool {
	switch a.Name {
	case "search_mcp_tools", "list_mcp_servers", "search_mcp_catalog":
		return true
	case "computer_browser", "computer_desktop":
		var in struct {
			Action       string `json:"action"`
			TargetAction string `json:"target_action"`
		}
		if json.Unmarshal([]byte(a.Arguments), &in) != nil {
			return false
		}
		return in.Action == "browser.snapshot" || in.Action == "browser.action" && in.TargetAction == "snapshot" || in.Action == "desktop.capture"
	}
	return false
}

// One run's own records, newest first within a count bound. Unknown effects
// are evidence for direct chat; scheduled ancestry keeps its stricter fence.
func readMCPRunToolEvidence(db reviewQuerier, r Run, strictEffects bool, bounds *mcpEvidenceBounds) ([]ToolActivity, error) {
	rows, err := db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? AND name NOT IN `+mcpDiscoveryToolsSQL+` ORDER BY started_at DESC,call_id DESC LIMIT ?`, r.ID, mcpEvidenceToolRows+1)
	if err != nil {
		return nil, mcpContextFail(mcpContextToolsQuery)
	}
	var out []ToolActivity
	for rows.Next() {
		var a ToolActivity
		var truncated int
		var outcome string
		if err = rows.Scan(&a.ConversationID, &a.BotID, &a.RunID, &a.CallID, &a.Name, &a.Arguments, &a.Result, &a.Status, &truncated, &a.StartedAt, &a.UpdatedAt, &outcome); err != nil {
			err = mcpContextFail(mcpContextToolsScan)
			break
		}
		if len(out) == mcpEvidenceToolRows {
			bounds.OlderToolRecordsOmitted = true
			break
		}
		a.Truncated, a.Outcome = truncated != 0, tooloutcome.Parse(outcome)
		if outcome != "" && a.Outcome == nil {
			err = mcpContextFail(mcpContextToolsOutcome)
			break
		}
		if !utf8.ValidString(a.Arguments) || !utf8.ValidString(a.Result) {
			err = mcpContextFail(mcpContextToolsUTF8)
			break
		}
		if a.ConversationID != r.ConversationID || a.BotID != r.BotID {
			err = mcpContextFail(mcpContextToolsBinding)
			break
		}
		if strictEffects && a.Outcome != nil && a.Outcome.Certainty == "unknown" {
			err = mcpContextFail(mcpContextToolsUncertain)
			break
		}
		var cutArgs, cutResult bool
		a.Arguments, cutArgs = mcpTruncate(a.Arguments, mcpEvidenceToolRunes)
		a.Result, cutResult = mcpTruncate(a.Result, mcpEvidenceToolRunes)
		if cutArgs || cutResult {
			a.Truncated, bounds.ToolRecordsTruncated = true, true
		}
		// Preserve incompleteness and uncertainty as untrusted evidence. Exact
		// replay claims remain the backend fence for a dispatched uncertain effect.
		out = append(out, a)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, mcpContextFail(mcpContextToolsIteration)
	}
	reverseSlice(out)
	return out, nil
}

// Genuine human refusals in this conversation, newest first. Malformed rows
// still fail: they are corrupted restrictions, not bounded evidence.
func readMCPHumanRefusals(db reviewQuerier, conversation string, bounds *mcpEvidenceBounds) ([]mcpHumanRefusal, error) {
	rows, err := db.Query(`SELECT q.id,q.run_id,a.action_hash,q.approval_json FROM questions q JOIN mcp_call_approvals a ON a.question_id=q.id WHERE q.conversation_id=? AND q.status='answered' AND q.answer_json='false' AND COALESCE(q.answered_by,'')<>'' AND q.answered_by<>? ORDER BY q.created_at DESC,q.id DESC LIMIT ?`, conversation, autoReviewActor, mcpEvidenceRefusals+1)
	if err != nil {
		return nil, mcpContextFail(mcpContextRefusalsQuery)
	}
	var out []mcpHumanRefusal
	for rows.Next() {
		var refusal mcpHumanRefusal
		var raw string
		if err = rows.Scan(&refusal.QuestionID, &refusal.RunID, &refusal.ActionHash, &raw); err != nil {
			err = mcpContextFail(mcpContextRefusalsScan)
			break
		}
		if len(out) == mcpEvidenceRefusals {
			bounds.OlderRefusalsOmitted = true
			break
		}
		if err = json.Unmarshal([]byte(raw), &refusal.Approval); err != nil || refusal.Approval == nil {
			err = mcpContextFail(mcpContextRefusalsInvalid)
			break
		}
		refusal.Approval.Review = nil // Prior model advice is not human provenance.
		var cut bool
		if refusal.Approval.Payload, cut = mcpTruncate(refusal.Approval.Payload, mcpEvidenceRefusalRunes); cut {
			bounds.RefusalPayloadsTruncated = true
		}
		out = append(out, refusal)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, mcpContextFail(mcpContextRefusalsIteration)
	}
	reverseSlice(out)
	return out, nil
}

func reverseSlice[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Drop the oldest evidence until the packet fits, flagging each omission.
// Authorization references, the trigger and the lineage are never dropped.
func fitMCPReviewContext(x *mcpReviewContext, budget int) (string, error) {
	for {
		raw, err := json.Marshal(x)
		if err != nil || !utf8.Valid(raw) {
			return "", mcpContextFail(mcpContextEncoding)
		}
		if len(raw) <= budget {
			return digestBytes(raw), nil
		}
		if !dropOldestMCPEvidence(x) {
			return "", mcpContextLimitFail(mcpContextBytesLimit, len(raw), budget, 1<<20, false)
		}
	}
}

func dropOldestMCPEvidence(x *mcpReviewContext) bool {
	bounds := func(b **mcpEvidenceBounds) *mcpEvidenceBounds {
		if *b == nil {
			*b = &mcpEvidenceBounds{}
		}
		return *b
	}
	contexts := []*mcpReviewContext{x}
	if x.ScheduleLineage != nil {
		for i := range x.ScheduleLineage.Contexts {
			contexts = append(contexts, &x.ScheduleLineage.Contexts[i].Context)
		}
	}
	if x.Delegation != nil {
		for i := range x.Delegation.Contexts {
			contexts = append(contexts, &x.Delegation.Contexts[i].Context)
		}
	}
	// 1. Older conversation text, keeping each window's newest message.
	for _, c := range contexts {
		if len(c.Messages) > 1 && c.Messages[0].ID != c.IntentMessageID {
			b := bounds(&c.Bounds)
			id := c.Messages[0].ID
			c.Messages, b.OlderMessagesOmitted = c.Messages[1:], true
			if len(c.MessageProvenance) > 0 && c.MessageProvenance[0].MessageID == id {
				c.MessageProvenance = c.MessageProvenance[1:]
			}
			return true
		}
	}
	// 2. Older tool records.
	toolLists := []*[]ToolActivity{&x.ToolResults, &x.SourceToolResults}
	if x.ScheduleLineage != nil {
		for i := range x.ScheduleLineage.Contexts {
			toolLists = append(toolLists, &x.ScheduleLineage.Contexts[i].ToolResults)
		}
	}
	if x.Delegation != nil {
		for i := range x.Delegation.Contexts {
			toolLists = append(toolLists, &x.Delegation.Contexts[i].ToolResults)
		}
	}
	for _, list := range toolLists {
		if len(*list) > 0 {
			*list = (*list)[1:]
			bounds(&x.Bounds).OlderToolRecordsOmitted = true
			return true
		}
	}
	// 3. Older schedule source conversation text.
	if x.ScheduleLineage != nil {
		for i := range x.ScheduleLineage.Occurrence.Revisions {
			if r := &x.ScheduleLineage.Occurrence.Revisions[i]; len(r.SourceMessages) > 0 {
				r.SourceMessages = r.SourceMessages[1:]
				bounds(&x.Bounds).OlderSourceTextOmitted = true
				return true
			}
		}
	}
	// 4. Unread attachment metadata, then the oldest human refusals.
	for _, c := range contexts {
		if a := c.AttachmentBoundary; a != nil && len(a.Omissions) > 0 {
			a.Omissions, a.OlderOmitted = a.Omissions[1:], true
			return true
		}
	}
	for _, c := range contexts {
		if len(c.HumanRefusals) > 0 {
			c.HumanRefusals = c.HumanRefusals[1:]
			bounds(&c.Bounds).OlderRefusalsOmitted = true
			return true
		}
	}
	// 5. Last resort for very long chains: shorten untrusted hop and revision
	// texts and bot instructions. Their digests stay bound by the lineage.
	const short = 200
	shorten := func(s *string) bool {
		if utf8.RuneCountInString(*s) <= short+len("…[truncated]") {
			return false
		}
		*s, _ = mcpTruncate(strings.TrimSuffix(*s, "…[truncated]"), short)
		return true
	}
	var ancestry []mcpScheduleAncestor
	if x.ScheduleLineage != nil {
		ancestry = x.ScheduleLineage.Ancestry
		for i := range x.ScheduleLineage.Occurrence.Revisions {
			r := &x.ScheduleLineage.Occurrence.Revisions[i]
			if shorten(&r.ExecutionContent) || shorten(&r.SubmittedFields) || shorten(&r.SourceRequest) {
				r.Truncated = true
				return true
			}
		}
	}
	if x.Delegation != nil {
		ancestry = x.Delegation.Ancestry
	}
	for i := range ancestry {
		if shorten(&ancestry[i].Trigger.Content) {
			ancestry[i].TriggerTruncated = true
			return true
		}
	}
	for _, c := range contexts {
		if shorten(&c.Instructions) {
			bounds(&c.Bounds).InstructionsTruncated = true
			return true
		}
	}
	return false
}
