package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Built-in skills: task-specific procedures loaded only when a task matches,
// so the always-present policy stays short and the Bot's persona sets the tone.
const researchGuide = `Research and news:
Give the answer with the facts behind it; link the pages you used and say when things happened if timing matters. Open primary pages (the article, filing or official page) and cite those URLs: every link must be the specific page that states the fact, never a home, section, channel, tag or search page; a story you have not opened is not reported. Compute what was asked from data you already fetched (quotes, charts, tables). Distinguish event or publication time from retrieval time and current from delayed data; convert explicit timezones and keep stated dates. Word confirmed facts and guesses differently inside the sentences themselves; no separate caveat line, and none for plain opinions. For a roundup, one good listing page plus opening only the items you report is enough: about 3-5 page reads in total, no cross-checking every date. Prior findings are not fresh evidence. A scripted fetch is a quick first try; when a site refuses it, read the page in the real browser before calling it unreachable.`

const mailGuide = `Email in the browser (the signed-in Gmail in your computer):
Open searches by URL: https://mail.google.com/mail/u/0/#search/<query>, with Gmail operators such as from:, subject:, newer_than:7d, is:unread, category:promotions. Read the result list with browser.read; open a message with browser.click on its subject and read it in full once. Opening a mail to read it is fine under read-only requests; do not archive, label, mark, delete or reply unless asked. Tell the user what the relevant mails say and whether they need to do anything.`

const webTasksGuide = `Doing things on websites (forms, orders, bookings, messages, account or settings changes):
Fill fields with browser.click and desktop.type; select a field's existing text first (key ctrl+a) so typing replaces it, and label every click, type or key with its real effect (none, submit, purchase, send, delete, publish, account). Before an action with an outside effect that the user did not clearly ask for, ask with request_approval naming the exact button. Never enter payment, card, ID or password data unless the user provided it for this purpose. After submitting, read the confirmation page and say what happened.`

const softwareGuide = `Installing and removing software on your computer:
System directories are read-only (no sudo, no system apt). Install in user space: language package managers, or official release archives unpacked under /workspace/home/.local/opt and linked into /workspace/home/.local/bin (first on PATH, shared by all Bots). Inspect the existing command first and keep existing tools. To uninstall, remove exactly what you installed (the unpacked directory and its links), then verify with command -v and a version check. If a package must come from apt, apt-get download and dpkg-deb -x can extract it into user space with its dependencies and a wrapper. Do not expose credentials.`

const commitmentGuide = `Track genuine ongoing commitments with work-item tools: inspect existing items to avoid duplicates, preserve owner and status, mark done only after completion. Ordinary conversation needs no task. Recorded tasks do not execute themselves. Use scheduling tools for future work. To find or stop existing schedules, name list_schedules, pause_schedule and delete_schedule in workspace_capabilities (query: schedule) and call list_schedules with scope self to see every conversation of this Bot; pause or delete them by id. A one-time schedule already appears in the agenda, so create a task only for a separate broader commitment. A scheduled run is not complete until its run status is done. Follow the scheduled execution's own completion-receipt rules.`

const teamAssemblyGuide = `To assemble a team, call list_groups first. If a group has suitable members and purpose, continue the work in that group with send_group_message. Use invite_bot to add a missing member. Create a new group only when no group fits, and state its purpose. A first create_group call without list_groups is refused and lists the groups that hold the same members; continue in a fitting one, or repeat create_group when the purpose differs. Reuse suitable existing Bots. Create specialists only for missing skills. Use create_group, then send_group_message once to start work. Inside a group, assistant replies publish automatically. Use handoff for a concrete contribution from a listed member and end the turn after successful dispatch; its completed result resumes you. Do not duplicate dispatch, predict results, or send courtesy handoffs. Compare evidence, contribute distinct ideas and integrate a useful conclusion. A one-time message does not authorize changes to another Bot's durable instructions.`

// researchBrowserSentence is the one research-guide sentence that needs a computer.
const researchBrowserSentence = " A scripted fetch is a quick first try; when a site refuses it, read the page in the real browser before calling it unreachable."

func workflowGuideTool() Tool { return workflowGuideToolFor(true) }

// workflowGuideToolFor lists only the guides a workspace can act on: mail,
// web_tasks and software drive the Bot's computer, so a workspace with none
// offers research, commitments and team.
func workflowGuideToolFor(computer bool) Tool {
	description := "Read one built-in skill when the task matches it: research (looking things up, news, comparing sources), mail (finding or reading email), web_tasks (forms, orders, bookings, sending or changing things on websites), software (installing or removing software on your computer), commitments (ongoing work and schedules), team (assembling Bots). Chat and simple questions need none; reuse a skill already read. No side effects."
	topics := []string{"research", "mail", "web_tasks", "software", "commitments", "team"}
	if !computer {
		description = "Read one built-in skill when the task matches it: research (looking things up, news, comparing sources), commitments (ongoing work and schedules), team (assembling Bots). Chat and simple questions need none; reuse a skill already read. No side effects."
		topics = []string{"research", "commitments", "team"}
	}
	return Tool{Name: "read_workflow_guide", Description: description, Parameters: objectSchema(map[string]any{"topic": map[string]any{"type": "string", "enum": topics}}, []string{"topic"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Topic string `json:"topic"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", err
		}
		if !computer && in.Topic != "research" && in.Topic != "commitments" && in.Topic != "team" {
			return "", errors.New("unsupported workflow guide topic")
		}
		switch in.Topic {
		case "research":
			if !computer {
				return strings.TrimSuffix(researchGuide, researchBrowserSentence), nil
			}
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
