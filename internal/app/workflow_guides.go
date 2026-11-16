package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The workflow guides became built-in Skills (web-research, email, web-tasks,
// install-software). read_workflow_guide stays for one version as a deferred
// alias: a topic returns the body of its Skill, and commitments and team return
// the text that now also sits in the descriptions of the work-item, schedule and
// team tools.

const commitmentGuide = `Track genuine ongoing commitments with work-item tools: inspect existing items to avoid duplicates, preserve owner and status, mark done only after completion. Ordinary conversation needs no task. Recorded tasks do not execute themselves. Use scheduling tools for future work. To find existing schedules and their ids, call list_schedules (scope self covers every conversation of this Bot). To pause, resume or delete one, name pause_schedule, resume_schedule or delete_schedule in workspace_capabilities (query: schedule) and act on it by id. A one-time schedule already appears in the agenda, so create a task only for a separate broader commitment. A scheduled run is not complete until its run status is done. Follow the scheduled execution's own completion-receipt rules.`

const teamAssemblyGuide = `To assemble a team, call list_groups first. If a group has suitable members and purpose, continue the work in that group with send_group_message. Use invite_bot to add a missing member. Create a new group only when no group fits, and state its purpose. A first create_group call without list_groups is refused and lists the groups that hold the same members; continue in a fitting one, or repeat create_group when the purpose differs. Reuse suitable existing Bots. Create specialists only for missing skills. Use create_group, then send_group_message once to start work. Inside a group, assistant replies publish automatically. Use handoff for a concrete contribution from a listed member and end the turn after successful dispatch; its completed result resumes you. Do not duplicate dispatch, predict results, or send courtesy handoffs. Compare evidence, contribute distinct ideas and integrate a useful conclusion. A one-time message does not authorize changes to another Bot's durable instructions.`

// workflowGuideSkills maps an alias topic to its Skill.
var workflowGuideSkills = map[string]string{"research": "web-research", "mail": "email", "web_tasks": "web-tasks", "software": "install-software"}

func workflowGuideTool() Tool { return workflowGuideToolFor(true) }

// workflowGuideToolFor is the deferred alias. A Skill that requires a Bot
// computer is refused when the workspace has none, as read_skill refuses it.
func workflowGuideToolFor(computer bool) Tool {
	topics := []string{"research", "mail", "web_tasks", "software", "commitments", "team"}
	return Tool{Name: "read_workflow_guide", Deferred: true, Description: "Kept for one version: read a Skill with read_skill (web-research, email, web-tasks, install-software). Topics: research, mail, web_tasks, software, commitments, team.", Parameters: objectSchema(map[string]any{"topic": map[string]any{"type": "string", "enum": topics}}, []string{"topic"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
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
		case "commitments":
			return commitmentGuide, nil
		case "team":
			return teamAssemblyGuide, nil
		}
		name, ok := workflowGuideSkills[in.Topic]
		if !ok {
			return "", errors.New("unsupported workflow guide topic")
		}
		for _, skill := range extensions.BuiltinSkills() {
			if skill.Name != name {
				continue
			}
			if !computer && skill.NeedsComputer() {
				return "", tooloutcome.InvalidArguments("skill " + name + " needs a Bot computer, which this workspace has not set up; a Bot computer is added in Settings > Environments")
			}
			return skill.Body, nil
		}
		return "", errors.New("unsupported workflow guide topic")
	}}
}
