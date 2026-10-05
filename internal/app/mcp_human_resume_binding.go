package app

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// A human answer resumes a host-owned checkpoint before the model reproposes
// the call. Only that checkpoint's non-executing control records may differ
// from the reviewed snapshot. Every authorization/effect fact stays bound.
// This runs inside the same transaction as the ordinary execution claim.
func mcpHumanResumeContextMatches(ctx context.Context, db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval, q Question, snapshot, digest string, current mcpReviewContext) bool {
	if q.Status != questionAnswered || q.AnsweredBy == "" || q.AnsweredBy == autoReviewActor || string(q.Answer) != "true" || len(snapshot) == 0 || len(snapshot) > 64<<10 {
		return false
	}
	var saved mcpReviewContext
	if json.Unmarshal([]byte(snapshot), &saved) != nil || mcpReviewDigest(saved, call) != digest {
		return false
	}
	var raw string
	if db.QueryRow(`SELECT checkpoint_json FROM run_input_waits WHERE run_id=? AND question_id=? AND state='claimed'`, r.ID, q.ID).Scan(&raw) != nil {
		return false
	}
	var checkpoint struct {
		Version int                 `json:"version"`
		RunID   string              `json:"run_id"`
		BotID   string              `json:"bot_id"`
		Agent   *agent.Continuation `json:"agent"`
	}
	if json.Unmarshal([]byte(raw), &checkpoint) != nil || checkpoint.Version != 1 || checkpoint.RunID != r.ID || checkpoint.BotID != r.BotID || agent.ValidateContinuation(checkpoint.Agent) != nil || checkpoint.Agent.QuestionID != q.ID {
		return false
	}
	waitingID, activeID := checkpoint.Agent.WaitingToolCallID, runtime.ToolCallID(ctx)
	if activeID == "" || activeID == waitingID {
		return false
	}
	var waiting ToolActivity
	found := 0
	visitMCPReviewActivities(&saved, func(activities *[]ToolActivity) bool {
		for _, a := range *activities {
			if a.RunID == r.ID && a.CallID == waitingID {
				waiting, found = a, found+1
			}
		}
		return true
	})
	if found != 1 || waiting.ConversationID != c.ID || waiting.BotID != r.BotID || waiting.Name != checkpoint.Agent.WaitingToolName || waiting.Status != "running" || waiting.Truncated || waiting.Result != "" || waiting.Outcome != nil || !mcpResumeActivityMatchesCall(waiting, call) {
		return false
	}
	protocolMatch := false
	for _, message := range checkpoint.Agent.Messages {
		for _, tc := range message.ToolCalls {
			if tc.ID == waitingID && tc.Name == waiting.Name && tc.Arguments == waiting.Arguments {
				protocolMatch = true
			}
		}
	}
	if !protocolMatch {
		return false
	}
	control := mcpApprovalRecordedOutcome().JSON()
	resumed, proposed := 0, 0
	if !visitMCPReviewActivities(&current, func(activities *[]ToolActivity) bool {
		filtered := make([]ToolActivity, 0, len(*activities))
		for _, a := range *activities {
			switch {
			case a.RunID == r.ID && a.CallID == waitingID:
				if a.ConversationID != waiting.ConversationID || a.BotID != waiting.BotID || a.Name != waiting.Name || a.Arguments != waiting.Arguments || a.StartedAt != waiting.StartedAt || a.Status != "completed" || a.Truncated || a.Outcome == nil || a.Outcome.JSON() != control || a.Result != control {
					return false
				}
				filtered = append(filtered, waiting)
				resumed++
			case a.RunID == r.ID && a.CallID == activeID:
				if a.ConversationID != c.ID || a.BotID != r.BotID || a.Name != waiting.Name || a.Status != "running" || a.Truncated || a.Result != "" || a.Outcome != nil || !mcpResumeActivityMatchesCall(a, call) || !sameMCPResumeToolName(a, waiting) {
					return false
				}
				proposed++
			default:
				filtered = append(filtered, a)
			}
		}
		// Preserve nil versus empty slices in the original snapshot.
		if len(filtered) == 0 {
			filtered = nil
		}
		*activities = filtered
		return true
	}) || resumed != 1 || proposed != 1 {
		return false
	}
	if !filterMCPResumeProgress(db, c, r, &current, saved) {
		return false
	}
	if current.ScheduleLineage != nil {
		if saved.ScheduleLineage == nil || len(current.ScheduleLineage.Contexts) != len(saved.ScheduleLineage.Contexts) {
			return false
		}
		for i := range current.ScheduleLineage.Contexts {
			if !filterMCPResumeProgress(db, c, r, &current.ScheduleLineage.Contexts[i].Context, saved.ScheduleLineage.Contexts[i].Context) {
				return false
			}
		}
	}
	return mcpReviewDigest(current, call) == digest
}

func visitMCPReviewActivities(x *mcpReviewContext, visit func(*[]ToolActivity) bool) bool {
	if !visit(&x.ToolResults) {
		return false
	}
	if x.ScheduleLineage != nil {
		for i := range x.ScheduleLineage.Contexts {
			if !visit(&x.ScheduleLineage.Contexts[i].ToolResults) {
				return false
			}
		}
	}
	return true
}

func mcpResumeActivityMatchesCall(a ToolActivity, call extensions.MCPCallApproval) bool {
	raw := json.RawMessage(a.Arguments)
	if a.Name == "call_mcp_tool" {
		var envelope struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return false
		}
		raw = envelope.Arguments
	}
	var got, want any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal(call.Arguments, &want) != nil {
		return false
	}
	left, _ := json.Marshal(got)
	right, _ := json.Marshal(want)
	return bytes.Equal(left, right)
}

func sameMCPResumeToolName(a, waiting ToolActivity) bool {
	if a.Name != "call_mcp_tool" {
		return a.Name == waiting.Name
	}
	var left, right struct {
		Name string `json:"name"`
	}
	return json.Unmarshal([]byte(a.Arguments), &left) == nil && json.Unmarshal([]byte(waiting.Arguments), &right) == nil && left.Name != "" && left.Name == right.Name
}

// Native progress published after the snapshot is presentation/control text,
// never user authorization or a completed effect. Prior progress stays bound;
// imported, arbitrary assistant text and every user message stay in evidence.
func filterMCPResumeProgress(db reviewQuerier, c Conversation, r Run, current *mcpReviewContext, saved mcpReviewContext) bool {
	ids := make(map[string]bool, len(saved.Messages))
	var last int64
	for _, m := range saved.Messages {
		ids[m.ID] = true
		if m.Seq > last {
			last = m.Seq
		}
	}
	removed := map[string]bool{}
	messages := make([]Message, 0, len(current.Messages))
	for _, m := range current.Messages {
		if !ids[m.ID] && m.Seq > last && m.Role == "assistant" && m.Kind == "progress" && m.SenderBotID == r.BotID {
			var native bool
			if db.QueryRow(`SELECT EXISTS(SELECT 1 FROM stream_assistant_turns t JOIN messages m ON m.id=t.message_id WHERE t.message_id=? AND t.run_id=? AND m.run_id=t.run_id AND m.conversation_id=? AND m.sender_bot_id=? AND m.role='assistant' AND m.kind='progress' AND NOT EXISTS(SELECT 1 FROM portability_provenance p WHERE p.kind='message' AND p.target_id=m.id))`, m.ID, r.ID, c.ID, r.BotID).Scan(&native) != nil {
				return false
			}
			if native {
				removed[m.ID] = true
				continue
			}
		}
		messages = append(messages, m)
	}
	if len(messages) == 0 {
		messages = nil
	}
	current.Messages = messages
	provenance := make([]mcpMessageProvenance, 0, len(current.MessageProvenance))
	for _, p := range current.MessageProvenance {
		if !removed[p.MessageID] {
			provenance = append(provenance, p)
		}
	}
	if len(provenance) == 0 {
		provenance = nil
	}
	current.MessageProvenance = provenance
	return true
}
