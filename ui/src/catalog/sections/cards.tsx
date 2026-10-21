// Cards in the timeline: the question card in all its forms, the approval card it draws with, handover cards and
// the "Always allow" surfaces. QuestionCard takes the same JSON the server sends, so these are its real states.
import { QuestionCard } from "../../QuestionCard";
import { ApprovalCard } from "../../ApprovalCard";
import { StandingAllowances } from "../../StandingAllowances";
import { BotAvatar } from "../../BotAvatar";
import { REMEMBERED_APPROVAL_ACTOR, STANDING_ALLOWANCE_ACTOR, type Question } from "../../questionTimeline";
import { json } from "../sandbox";
import { at, bot, isolated, pendingApproval } from "../world";
import type { CatalogSection, CatalogState } from "../types";

const changed = async () => {};
const noop = () => {};
const base = { conversation_id: "dm_mochi", bot_id: "bot_mochi", run_id: "run_q", type: "question" as const, status: "pending" as const, created_at: at(41), updated_at: at(41) };
const q = (id: string, extra: Partial<Question>): Question => ({ ...base, question_id: `q_${id}`, question_type: "yes_no", question: "Should I book the small room for Thursday at 15:00?", ...extra });
const card = (id: string, label: string, item: Question, extra: Partial<CatalogState> & { group?: boolean; archived?: boolean } = {}): CatalogState => {
  const { group, archived, ...state } = extra;
  return { id, label, kind: "inline", render: () => <QuestionCard item={item} bot={bot(item.bot_id)} group={group} archived={archived} onChanged={changed} />, ...state };
};
const answered = (answer: Question["answer"], by = "user"): Partial<Question> => ({ status: "answered", answer, answered_by: by, updated_at: at(42) });
const SKILL_FACTS = { kind: "skill_install" as const, name: "invoice-helper", change_id: "#a1b2c3d4e5f6", skill: { description: "Summarize invoices", excerpt: "Fetch invoices and summarize them.", allowed_tools: ["Bash", "Read"], files: ["SKILL.md (120 B)", "fetch.sh (40 B)"], has_scripts: true } };

/** Answers fail, so a click shows the card's error state. */
const refusing = isolated();
const failingServer: typeof refusing = (url, init) => url.pathname.endsWith("/answer") ? json({ error: { code: "unavailable", message: "Synthetic: the server is busy" } }, 503) : refusing(url, init);

export const questions: CatalogSection = {
  id: "questions", number: "13", title: "Question card (QuestionCard)", context: ["catalog-column"], server: failingServer,
  states: [
    card("yes-no", "Yes / no, waiting", q("yes_no", {})),
    card("single", "Single choice", q("single", { question_type: "single_choice", question: "Which room should I book?", options: [{ id: "small", label: "Small room" }, { id: "big", label: "Big room" }, { id: "online", label: "Online only" }] })),
    card("multi", "Multiple choice with \"Other\"", q("multi", { question_type: "multi_choice", question: "Who should get the summary?", allow_other: true, min_selections: 1, options: [{ id: "launch", label: "Launch list" }, { id: "board", label: "Board" }, { id: "press", label: "Press" }] })),
    card("text", "Free text", q("text", { question_type: "text", question: "What should the subject line say?" })),
    card("form", "Web form (with a password field)", q("form", { question_type: "form", question: "Sign in to the synthetic portal", source_url: "https://portal.example.test", fields: [{ id: "email", label: "Email", type: "email", required: true, placeholder: "you@example.test" }, { id: "password", label: "Password", type: "password", required: true }] })),
    card("approval", "Approval", pendingApproval()),
    card("approval-facts", "Approval with server-checked facts", pendingApproval("dm_mochi", { question_id: "q_facts", question: "Install a Skill?", approval: { action: "Install invoice-helper", target: "extension skill_install invoice-helper #a1b2c3d4e5f6", impact: "The Bot can run its scripts", facts: SKILL_FACTS } })),
    card("approval-standing", "Approval offering \"Always allow\"", pendingApproval("dm_mochi", { question_id: "q_standing", question: "Create the calendar event?", approval: { action: "Create a calendar event", target: "calendar.create_event", impact: "Adds one event", standing: { server: "calendar", tool: "create_event" } } })),
    card("reviewing", "Approval under automatic review", pendingApproval("dm_mochi", { question_id: "q_review", approval: { ...pendingApproval().approval!, review: { source: "auto-review", status: "reviewing", reason: "", model: "synthetic-model" } } })),
    card("error", "Answer failed", q("error", {}), { note: "the server refused the answer", drive: async step => { await step.click(".approval-accept"); await step.find(".approval-error"); } }),
    card("answered", "Answered by you", q("answered", answered(true))),
    card("answered-choice", "Answered, choice", q("answered_choice", { question_type: "single_choice", question: "Which room should I book?", options: [{ id: "small", label: "Small room" }, { id: "big", label: "Big room" }], ...answered("small") })),
    card("answered-auto", "Approved by automatic review", pendingApproval("dm_mochi", { question_id: "q_auto", ...answered(true, "auto-review"), approval: { ...pendingApproval().approval!, review: { source: "auto-review", status: "approved", reason: "Sending a summary to a known list is routine.", model: "synthetic-model", risk_level: "low" } } })),
    card("answered-remembered", "Approved by a remembered approval", pendingApproval("dm_mochi", { question_id: "q_remembered", ...answered(true, REMEMBERED_APPROVAL_ACTOR) })),
    card("answered-standing", "Approved by \"Always allow\"", pendingApproval("dm_mochi", { question_id: "q_standing_done", ...answered(true, STANDING_ALLOWANCE_ACTOR), approval: { action: "Create a calendar event", target: "calendar.create_event", impact: "Adds one event", standing: { server: "calendar", tool: "create_event" } } })),
    card("declined", "Declined", pendingApproval("dm_mochi", { question_id: "q_declined", ...answered(false) })),
    card("expired", "Expired", q("expired", { status: "expired" })),
    card("cancelled", "Cancelled", q("cancelled", { status: "cancelled" })),
    card("run-done", "Run already finished", q("run_done", { status: "run_done" })),
    card("archived", "In an archived chat", q("archived", {}), { archived: true }),
    card("group", "In a group", q("group", { bot_id: "bot_nori", conversation_id: "grp_launch" }), { group: true }),
  ],
};

const facts = [{ label: "Action", value: "Send an email to launch@example.test" }, { label: "Impact", value: "Twelve people receive the summary" }];
const approval = (id: string, label: string, props: Partial<Parameters<typeof ApprovalCard>[0]>, extra: Partial<CatalogState> = {}): CatalogState =>
  ({ id, label, kind: "inline", render: () => <ApprovalCard title="Send the weekly summary to the launch list?" avatar={<BotAvatar id="bot_mochi" mini />} time="12:41" facts={facts} payload={'{\n  "to": "launch@example.test"\n}'} onAnswer={noop} {...props} />, ...extra });

export const approvals: CatalogSection = {
  id: "approvals", number: "14", title: "Approval card (ApprovalCard)", context: ["catalog-column"],
  states: [
    approval("pending", "Waiting", {}),
    approval("verified", "With server-checked facts", { verified: [{ label: "Server", value: "calendar" }, { label: "Tool", value: "create_event" }], verifiedTitle: "Checked by Tofi", factsCaption: "Written by the Bot" }),
    approval("remember", "With \"Approve and remember\"", { onRemember: noop, rememberHint: "Later calls to the same address in this chat go ahead without asking." }),
    approval("always", "With \"Always allow\"", { onAlwaysAllow: noop, alwaysAllowHint: "Mochi may use calendar.create_event without asking, also when nobody is watching." }),
    approval("busy", "Working", { busy: "accept" }),
    approval("error", "Failed", { error: "Synthetic: the answer was not saved" }),
    approval("payload-open", "Arguments open", {}, { drive: async step => { await step.click(".approval-payload summary"); } }),
    approval("hover-accept", "Approve button, hover", {}, { force: [{ selector: ".approval-accept", state: "hover" }] }),
    approval("focus-accept", "Approve button, keyboard focus", {}, { force: [{ selector: ".approval-accept", state: "focus" }] }),
    approval("disabled", "Disabled", { disabled: true, note: "This chat is archived." }),
    approval("approved", "Approved", { resolution: { label: "Approved", accepted: true } }),
    approval("declined", "Declined", { resolution: { label: "Declined", accepted: false } }),
  ],
};

const handoverBase = { ...base, conversation_id: "dm_mochi" };
const takeover = (extra: Partial<Question> = {}): Question => ({ ...handoverBase, question_id: "q_take", question_type: "takeover", question: "Enter the verification code for the synthetic account.", source_url: "https://login.example.test", ...extra });
const connection = (extra: Partial<Question> = {}): Question => ({ ...handoverBase, question_id: "q_conn", question_type: "connection", question: "Connect Notes so I can file the summary.", target: "notes", ...extra });

export const handover: CatalogSection = {
  id: "handover", number: "15", title: "Takeover and connection cards (HandoverCards)", context: ["catalog-column"],
  server: isolated({ "/api/extensions/oauth-options": { vm_available: true, web_callback_origin: "", desktop_redirect_uri: "" }, "/api/extensions/mcp": { servers: [{ name: "notes", oauth: { connected: false } }] } }),
  states: [
    card("takeover", "Takeover, waiting", takeover()),
    card("takeover-done", "Takeover, done", takeover(answered("done"))),
    card("takeover-cannot", "Takeover, could not do it", takeover(answered("cannot"))),
    card("takeover-expired", "Takeover, expired", takeover({ status: "expired" })),
    card("takeover-group", "Takeover in a group", takeover({ bot_id: "bot_nori" }), { group: true }),
    card("connection", "Connection, waiting", connection()),
    card("connection-connected", "Connection, connected", connection(answered("connected"))),
    card("connection-declined", "Connection, declined", connection(answered("declined"))),
    card("connection-failed", "Connection, failed", connection(answered("failed"))),
    card("connection-archived", "Connection in an archived chat", connection(), { archived: true }),
  ],
  gaps: ["Connecting (a popup is open) and popup blocked: need a real OAuth popup."],
};

const LISTING = {
  allowances: [
    { id: "sa1", bot_id: "bot_mochi", bot_name: "Mochi", server: "calendar", tool: "create_event", state: "active", created_at: at(-3000), use_count: 12, last_used_at: at(-10) },
    { id: "sa2", bot_id: "bot_miso", bot_name: "Miso", server: "notes", tool: "append", state: "server_changed", created_at: at(-4000), use_count: 3 },
    { id: "sa3", bot_id: "bot_nori", bot_name: "Nori", server: "sheets", tool: "write_row", state: "tool_changed", created_at: at(-5000), use_count: 0 },
    { id: "sa4", bot_id: "bot_mochi", bot_name: "Mochi", server: "mail", tool: "send", state: "tier_changed", created_at: at(-6000), use_count: 1 },
  ],
  remembered: [{ id: "rm1", conversation_id: "dm_mochi", bot_id: "bot_mochi", bot_name: "Mochi", conversation_name: "Mochi", server: "mail", tool: "send", target_kind: "identifier", target_summary: "launch@example.test", expires_at: at(600), use_count: 2 }],
};

export const standing: CatalogSection = {
  id: "standing", number: "16", title: "\"Always allow\" (standing allowances)", context: ["settings-page-body"],
  server: isolated({ "/api/standing-allowances": LISTING }),
  states: [
    { id: "list", kind: "inline", label: "Settings list: active and voided grants, remembered approvals", render: () => <StandingAllowances /> },
    { id: "empty", kind: "overlay", label: "Settings list, empty", height: 360, server: isolated({ "/api/standing-allowances": { allowances: [], remembered: [] } }), render: () => <StandingAllowances /> },
    { id: "load-error", kind: "overlay", label: "Settings list, could not load", height: 360, server: (url, init) => url.pathname === "/api/standing-allowances" ? json({ error: { code: "unavailable", message: "Synthetic: the server is busy" } }, 503) : isolated()(url, init), render: () => <StandingAllowances /> },
    card("offer", "Card offering \"Always allow\"", pendingApproval("dm_mochi", { question_id: "q_offer", question: "Create the calendar event?", approval: { action: "Create a calendar event", target: "calendar.create_event", impact: "Adds one event", standing: { server: "calendar", tool: "create_event" } } }), { context: ["catalog-column"] }),
    card("decided", "Card decided by \"Always allow\"", pendingApproval("dm_mochi", { question_id: "q_decided", ...answered(true, STANDING_ALLOWANCE_ACTOR), approval: { action: "Create a calendar event", target: "calendar.create_event", impact: "Adds one event", standing: { server: "calendar", tool: "create_event" } } }), { context: ["catalog-column"] }),
  ],
};
