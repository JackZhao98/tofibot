package app

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

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
// transaction as the ordinary execution claim. The current packet is rebuilt
// by read from raw durable rows with those records normalized first, then
// bounded and fitted exactly as the review was, so a long history or run
// cannot make the comparison depend on which records the window kept.
func mcpHumanResumeContextMatches(ctx context.Context, db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval, q Question, snapshot, digest string, read func(reviewQuerier) (mcpReviewContext, error)) bool {
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
	waitingStarted, e := time.Parse(time.RFC3339Nano, waitingRow.StartedAt)
	if e != nil {
		return false
	}
	resumed, proposed := 0, 0
	current, err := read(&mcpResumeQuerier{db, func(a *ToolActivity) bool {
		if a.RunID != r.ID {
			return true
		}
		switch {
		case a.CallID == waitingID:
			// Restore the raw row as reviewed: the full durable arguments with the
			// reviewed lifecycle state, before bounding re-truncates them.
			resumed++
			a.Status, a.Result, a.Outcome, a.Truncated, a.UpdatedAt = waiting.Status, waiting.Result, nil, false, waiting.UpdatedAt
			return true
		case a.CallID == activeID:
			proposed++
			return false
		case !known[a.RunID+"\x00"+a.CallID] && mcpObservationOnlyActivity(*a):
			// A lookup after the approval, before the exact re-proposal, is not
			// an effect or consent. Older lookups stay as they were reviewed.
			started, e := time.Parse(time.RFC3339Nano, a.StartedAt)
			return e != nil || !started.After(waitingStarted)
		}
		return true
	}})
	return err == nil && resumed == 1 && proposed == 1 && mcpReviewDigest(current, call) == digest
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
