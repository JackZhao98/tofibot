package app

import (
	"context"
	"encoding/json"
	"errors"
)

const researchGuide = `Use fresh evidence for changing facts, private data and action outcomes. Honor requested sources/methods. Inspect plausible installed capabilities before generic browsing, unless the user chose a method. Read only relevant Skills and their supporting files. Reuse valid recent schemas; prior findings are not fresh evidence or authorization.
Cite actual sources. Distinguish event/publication time and current/delayed/historical data. Convert explicit timezones; otherwise retain the stated date and uncertainty. Retrieval time is not freshness. Apply material date limits; never invent precision, suppress supported findings or call unsearched categories fruitless. Disclose substitutions and gaps.
If a source is missing/disconnected, lacks credentials, is unavailable, errors or returns empty/stale/irrelevant data, try another available, permitted capability, including computer_browser when exposed; read computer_help(browser) for startup and visible search. One source's credentials do not block every method. Avoid repeated unchanged failures; if no permitted route works, report the precise blocker and evidence gap. Never bypass a denied target/action or browser prohibition. An alternate method needs independent authorization and its own approvals. Expired approval stops its action. Verify uncertain action outcomes before retrying or switching routes that could duplicate them.`

const commitmentGuide = `Track genuine ongoing commitments with work-item tools: inspect existing items to avoid duplicates, preserve owner and status, mark done only after completion. Ordinary conversation needs no task. Recorded tasks do not execute themselves. Use scheduling tools for future work; a one-time schedule already appears in the agenda, so create a task only for a separate broader commitment. Promise future work only after its schedule succeeds; never create an unbounded self-renewing loop. A scheduled run is not complete until its run status is done. Follow the scheduled execution's own completion-receipt rules.`

const teamAssemblyGuide = `To assemble a team, reuse suitable existing Bots or create specialists, create_group, then send_group_message once to start work in that new group. Inside a group, assistant replies publish automatically. Use handoff for a concrete contribution from a listed member and end the turn after successful dispatch; its completed result resumes you. Do not duplicate dispatch, predict results, or send courtesy handoffs. Compare evidence, contribute distinct ideas and integrate a useful conclusion. A one-time message does not authorize changes to another Bot's durable instructions.`

func workflowGuideTool() Tool {
	return Tool{Name: "read_workflow_guide", Description: "Read procedures when needed for complex/multi-source research, work planning or team assembly. Simple queries need no guide; reuse loaded topics. No side effects.", Parameters: objectSchema(map[string]any{"topic": map[string]any{"type": "string", "enum": []string{"research", "commitments", "team"}}}, []string{"topic"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Topic string `json:"topic"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", err
		}
		switch in.Topic {
		case "research":
			return researchGuide, nil
		case "commitments":
			return commitmentGuide, nil
		case "team":
			return teamAssemblyGuide, nil
		default:
			return "", errors.New("unsupported workflow guide topic")
		}
	}}
}
