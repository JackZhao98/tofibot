package app

import (
	"context"
	"encoding/json"
	"errors"
)

// Built-in skills: task-specific procedures loaded only when a task matches,
// so the always-present policy stays short and the Bot's persona sets the tone.
const researchGuide = `Research and news:
Give the answer with the facts behind it; link the pages you used and say when things happened if timing matters. Open primary pages (the article, filing or official page), not search-result pages, and cite those URLs. Compute what was asked from data you already fetched (quotes, charts, tables). Distinguish event or publication time from retrieval time and current from delayed data; convert explicit timezones and keep stated dates. Separate confirmed facts from speculation in one line. For a roundup, one good listing page plus opening only the items you report is enough: about 3-5 page reads in total, no cross-checking every date. Prior findings are not fresh evidence.`

const mailGuide = `Email in the browser (the signed-in Gmail in your computer):
Open searches by URL: https://mail.google.com/mail/u/0/#search/<query>, with Gmail operators such as from:, subject:, newer_than:7d, is:unread, category:promotions. Read the result list with browser.read; open a message with browser.click on its subject and read it in full once. Opening a mail to read it is fine under read-only requests; do not archive, label, mark, delete or reply unless asked. Tell the user what the relevant mails say and whether they need to do anything.`

const webTasksGuide = `Doing things on websites (forms, orders, bookings, messages, account or settings changes):
Fill fields with browser.click and desktop.type; select a field's existing text first (key ctrl+a) so typing replaces it, and label every click, type or key with its real effect (none, submit, purchase, send, delete, publish, account). Before an action with an outside effect that the user did not clearly ask for, ask with request_approval naming the exact button. Never enter payment, card, ID or password data unless the user provided it for this purpose. After submitting, read the confirmation page and say what happened.`

const softwareGuide = `Installing and removing software on your computer:
System directories are read-only (no sudo, no system apt). Install in user space: language package managers, or official release archives unpacked under /workspace/home/.local/opt and linked into /workspace/home/.local/bin (first on PATH, shared by all Bots). Inspect the existing command first and keep existing tools. To uninstall, remove exactly what you installed (the unpacked directory and its links), then verify with command -v and a version check. If a package must come from apt, apt-get download and dpkg-deb -x can extract it into user space with its dependencies and a wrapper. Do not expose credentials.`

const commitmentGuide = `Track genuine ongoing commitments with work-item tools: inspect existing items to avoid duplicates, preserve owner and status, mark done only after completion. Ordinary conversation needs no task. Recorded tasks do not execute themselves. Use scheduling tools for future work; a one-time schedule already appears in the agenda, so create a task only for a separate broader commitment. A scheduled run is not complete until its run status is done. Follow the scheduled execution's own completion-receipt rules.`

const teamAssemblyGuide = `To assemble a team, reuse suitable existing Bots or create specialists, create_group, then send_group_message once to start work in that new group. Inside a group, assistant replies publish automatically. Use handoff for a concrete contribution from a listed member and end the turn after successful dispatch; its completed result resumes you. Do not duplicate dispatch, predict results, or send courtesy handoffs. Compare evidence, contribute distinct ideas and integrate a useful conclusion. A one-time message does not authorize changes to another Bot's durable instructions.`

func workflowGuideTool() Tool {
	return Tool{Name: "read_workflow_guide", Description: "Read one built-in skill when the task matches it: research (looking things up, news, comparing sources), mail (finding or reading email), web_tasks (forms, orders, bookings, sending or changing things on websites), software (installing or removing software on your computer), commitments (ongoing work and schedules), team (assembling Bots). Chat and simple questions need none; reuse a skill already read. No side effects.", Parameters: objectSchema(map[string]any{"topic": map[string]any{"type": "string", "enum": []string{"research", "mail", "web_tasks", "software", "commitments", "team"}}}, []string{"topic"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
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
		case "mail":
			return mailGuide, nil
		case "web_tasks":
			return webTasksGuide, nil
		case "software":
			return softwareGuide, nil
		case "commitments":
			return commitmentGuide, nil
		case "team":
			return teamAssemblyGuide, nil
		default:
			return "", errors.New("unsupported workflow guide topic")
		}
	}}
}
