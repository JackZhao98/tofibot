package app

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A human answer resumes a host-owned checkpoint before the model reproposes
// the call. Only that checkpoint's non-executing control records, the exact
// re-proposal and observation-only lookups made before it may differ from the
// reviewed snapshot. Every authorization/effect fact stays bound. Later
// progress text never enters the bounded window. This runs inside the same
// transaction as the ordinary execution claim.
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
	// The packet may show shortened arguments; the durable rows hold them whole.
	waitingRow, ok1 := mcpResumeActivity(db, r.ID, waitingID)
	activeRow, ok2 := mcpResumeActivity(db, r.ID, activeID)
	control := mcpApprovalRecordedOutcome().JSON()
	if !ok1 || !ok2 || waitingRow.ConversationID != c.ID || waitingRow.BotID != r.BotID || waitingRow.Name != checkpoint.Agent.WaitingToolName || waitingRow.Status != "completed" || waitingRow.Truncated || waitingRow.Outcome == nil || waitingRow.Outcome.JSON() != control || waitingRow.Result != control || !mcpResumeActivityMatchesCall(waitingRow, call) {
		return false
	}
	if activeRow.ConversationID != c.ID || activeRow.BotID != r.BotID || activeRow.Name != waitingRow.Name || activeRow.Status != "running" || activeRow.Truncated || activeRow.Result != "" || activeRow.Outcome != nil || !mcpResumeActivityMatchesCall(activeRow, call) || !sameMCPResumeToolName(activeRow, waitingRow) {
		return false
	}
	protocolMatch := false
	for _, message := range checkpoint.Agent.Messages {
		for _, tc := range message.ToolCalls {
			if tc.ID == waitingID && tc.Name == waitingRow.Name && tc.Arguments == waitingRow.Arguments {
				protocolMatch = true
			}
		}
	}
	if !protocolMatch {
		return false
	}
	var waiting ToolActivity
	found := 0
	known := map[string]bool{}
	visitMCPReviewActivities(&saved, func(activities *[]ToolActivity) bool {
		for _, a := range *activities {
			known[a.RunID+"\x00"+a.CallID] = true
			if a.RunID == r.ID && a.CallID == waitingID {
				waiting, found = a, found+1
			}
		}
		return true
	})
	shown, _ := mcpTruncate(waitingRow.Arguments, mcpEvidenceToolRunes)
	if found != 1 || waiting.ConversationID != c.ID || waiting.BotID != r.BotID || waiting.Name != waitingRow.Name || waiting.Status != "running" || waiting.Result != "" || waiting.Outcome != nil || waiting.Arguments != shown || waiting.StartedAt != waitingRow.StartedAt {
		return false
	}
	resumed, proposed := 0, 0
	if !visitMCPReviewActivities(&current, func(activities *[]ToolActivity) bool {
		filtered := make([]ToolActivity, 0, len(*activities))
		for _, a := range *activities {
			switch {
			case a.RunID == r.ID && a.CallID == waitingID:
				filtered = append(filtered, waiting)
				resumed++
			case a.RunID == r.ID && a.CallID == activeID:
				proposed++
			case a.RunID == r.ID && !known[a.RunID+"\x00"+a.CallID] && mcpObservationOnlyActivity(a):
				// A lookup before the exact re-proposal is not an effect or consent.
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
	return mcpReviewDigest(current, call) == digest
}

func mcpResumeActivity(db reviewQuerier, run, call string) (ToolActivity, bool) {
	var a ToolActivity
	var truncated int
	var outcome string
	if db.QueryRow(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=? AND call_id=?`, run, call).Scan(&a.ConversationID, &a.BotID, &a.RunID, &a.CallID, &a.Name, &a.Arguments, &a.Result, &a.Status, &truncated, &a.StartedAt, &a.UpdatedAt, &outcome) != nil {
		return a, false
	}
	a.Truncated, a.Outcome = truncated != 0, tooloutcome.Parse(outcome)
	return a, outcome == "" || a.Outcome != nil
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
	if x.Delegation != nil {
		for i := range x.Delegation.Contexts {
			if !visit(&x.Delegation.Contexts[i].ToolResults) {
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
